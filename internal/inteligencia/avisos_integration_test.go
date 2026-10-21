//go:build integration

package inteligencia

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/avisos"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Las pruebas de avisos siembran la copia directamente: el lote de enriquecimiento ya tiene su propia
// prueba y lo que aquí se mide es la detección, el cupo, la deduplicación y el descarte.

var ahoraDeAvisos = time.Date(2026, 7, 15, 15, 0, 0, 0, time.UTC)

var mesesDeAvisos = []time.Month{
	time.January, time.February, time.March, time.April, time.May, time.June,
}

type filaDeCopia struct {
	usuario     string
	ocurrido    time.Time
	monto       int64
	direccion   string
	categoria   string
	contraparte string
	llave       string
	nombre      string
	tipoDestino string
	transaccion uuid.UUID
}

func sembrarCopia(t *testing.T, pool *pgxpool.Pool, fila filaDeCopia) {
	t.Helper()
	tipoDestino := fila.tipoDestino
	if tipoDestino == "" {
		tipoDestino = "comercio"
	}
	_, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, `
INSERT INTO inteligencia.movimiento_enriquecido (
    usuario_id, transaccion_id, entry_ancla, ocurrido_en, moneda, direccion, monto_centavos,
    contraparte, contraparte_resuelta, descripcion, categoria, origen_categoria,
    comercio_mid, comercio_nombre, mcc, version_modelo, tipo_destino,
    contraparte_llave, contraparte_nombre_normalizado
) VALUES ($1, $2, $3, $4, 'COP', $5, $6, $7, false, NULL, $8, 'sin_categoria',
          NULL, $9, NULL, 'cascada-1:primer-corte', $10, $11, $12)`,
				fila.usuario,
				pgtype.UUID{Bytes: fila.transaccion, Valid: true},
				fila.ocurrido.UnixNano(),
				pgtype.Timestamptz{Time: fila.ocurrido, Valid: true},
				fila.direccion,
				fila.monto,
				fila.contraparte,
				fila.categoria,
				pgtype.Text{String: fila.nombre, Valid: fila.nombre != ""},
				tipoDestino,
				pgtype.Text{String: fila.llave, Valid: fila.llave != ""},
				pgtype.Text{String: fila.nombre, Valid: fila.nombre != ""})
			return struct{}{}, err
		})
	if err != nil {
		t.Fatalf("sembrar movimiento de %q: %v", fila.usuario, err)
	}
}

// sembrarDeposito asienta un depósito con el signo del ledger: la subcuenta del usuario recibe el
// monto en negativo y el activo en positivo.
func sembrarDeposito(t *testing.T, pool *pgxpool.Pool, usuario string, monto int64, cuando time.Time) {
	t.Helper()
	sembrarAsiento(t, pool, "deposito", nil, "fondeo", cuando, []lineaDePrueba{
		{"activo:custodia:banco", monto},
		{"pasivo:usuario:" + usuario + ":disponible", -monto},
	})
}

func gastoMensual(t *testing.T, pool *pgxpool.Pool, usuario string, dia int, monto int64,
	categoria, contraparte, llave string) {
	t.Helper()
	for _, mes := range mesesDeAvisos {
		sembrarCopia(t, pool, filaDeCopia{
			usuario: usuario, ocurrido: time.Date(2026, mes, dia, 15, 0, 0, 0, time.UTC),
			monto: monto, direccion: "salida", categoria: categoria, contraparte: contraparte,
			llave: llave, nombre: "NOMBRE-" + llave, transaccion: uuid.New(),
		})
	}
}

func usuarioDeAvisos(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	usuario := uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, usuario)
	return usuario
}

func tiposDeAvisos(t *testing.T, servicio *Servicio, usuario string) map[avisos.Tipo]AvisoEmitido {
	t.Helper()
	delMes, err := servicio.AvisosDelUsuario(t.Context(), usuario, "2026-07", ahoraDeAvisos)
	if err != nil {
		t.Fatalf("listar avisos de %q: %v", usuario, err)
	}
	porTipo := map[avisos.Tipo]AvisoEmitido{}
	for _, aviso := range delMes {
		porTipo[aviso.Tipo] = aviso
	}
	return porTipo
}

func TestEmitirAvisosGuardaLasSieteTarjetas(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	servicio := NuevoServicio(pool)

	presupuesto := usuarioDeAvisos(t, pool)
	gastoMensual(t, pool, presupuesto, 5, 90_000_000, "alimentos_mercado", "MERCADO", "L-MERCADO")
	sembrarCopia(t, pool, filaDeCopia{usuario: presupuesto,
		ocurrido: time.Date(2026, 7, 3, 15, 0, 0, 0, time.UTC), monto: 150_000_000,
		direccion: "salida", categoria: "alimentos_mercado", contraparte: "MERCADO",
		llave: "L-MERCADO", nombre: "MERCADO", transaccion: uuid.New()})

	categoria := usuarioDeAvisos(t, pool)
	gastoMensual(t, pool, categoria, 5, 60_000_000, "alimentos_mercado", "MERCADO", "L-MERCADO")
	valores := []int64{30_000_000, 40_000_000, 35_000_000, 45_000_000, 40_000_000, 40_000_000}
	for i, mes := range mesesDeAvisos {
		sembrarCopia(t, pool, filaDeCopia{usuario: categoria,
			ocurrido: time.Date(2026, mes, 6, 15, 0, 0, 0, time.UTC), monto: valores[i],
			direccion: "salida", categoria: "transporte", contraparte: "TRANSPORTE",
			llave: "L-TRANSPORTE", nombre: "TRANSPORTE", transaccion: uuid.New()})
	}
	sembrarCopia(t, pool, filaDeCopia{usuario: categoria,
		ocurrido: time.Date(2026, 7, 4, 15, 0, 0, 0, time.UTC), monto: 95_000_000,
		direccion: "salida", categoria: "transporte", contraparte: "TRANSPORTE",
		llave: "L-TRANSPORTE", nombre: "TRANSPORTE", transaccion: uuid.New()})
	sembrarCopia(t, pool, filaDeCopia{usuario: categoria,
		ocurrido: time.Date(2026, 7, 5, 15, 0, 0, 0, time.UTC), monto: 60_000_000,
		direccion: "salida", categoria: "alimentos_mercado", contraparte: "MERCADO",
		llave: "L-MERCADO", nombre: "MERCADO", transaccion: uuid.New()})

	caida := usuarioDeAvisos(t, pool)
	gastoMensual(t, pool, caida, 5, 30_000_000, "alimentos_mercado", "MERCADO", "L-MERCADO")
	montosIngreso := []int64{200_000_000, 200_000_000, 200_000_000, 200_000_000, 200_000_000, 100_000_000}
	for i, mes := range mesesDeAvisos {
		sembrarDeposito(t, pool, caida, montosIngreso[i], time.Date(2026, mes, 1, 14, 0, 0, 0, time.UTC))
	}

	pago := usuarioDeAvisos(t, pool)
	gastoMensual(t, pool, pago, 17, 80_000_000, "vivienda_servicios", "ARRIENDO", "L-ARRIENDO")
	gastoMensual(t, pool, pago, 10, 35_000_000, "alimentos_mercado", "MERCADO", "L-MERCADO")
	sembrarDeposito(t, pool, pago, 30_000_000, time.Date(2026, 1, 1, 14, 0, 0, 0, time.UTC))

	cobro := usuarioDeAvisos(t, pool)
	gastoMensual(t, pool, cobro, 6, 80_000_000, "alimentos_mercado", "MERCADO", "L-MERCADO")
	for _, momento := range []time.Time{
		time.Date(2026, 6, 20, 15, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 10, 15, 0, 0, 0, time.UTC),
	} {
		monto := int64(15_000_000)
		if momento.Month() == time.July {
			monto = 15_200_000
		}
		sembrarCopia(t, pool, filaDeCopia{usuario: cobro, ocurrido: momento, monto: monto,
			direccion: "salida", categoria: "entretenimiento", contraparte: "NUEVA",
			llave: "L-NUEVA", nombre: "NUEVA", transaccion: uuid.New()})
	}

	insertados, err := servicio.EmitirAvisos(t.Context(), ahoraDeAvisos)
	if err != nil {
		t.Fatalf("EmitirAvisos: %v", err)
	}
	if insertados != 7 {
		t.Fatalf("insertados = %d, want 7", insertados)
	}

	dePresupuesto := tiposDeAvisos(t, servicio, presupuesto)
	if len(dePresupuesto) != 2 {
		t.Fatalf("presupuesto: %d avisos, want 2", len(dePresupuesto))
	}
	if aviso, existe := dePresupuesto[avisos.PresupuestoAnticipado]; !existe ||
		aviso.Texto != "En julio de 2026 llevas $1.500.000 en gastos. Lo habitual en tus meses completos es $900.000." {
		t.Fatalf("aviso de presupuesto = %+v", dePresupuesto)
	}
	if aviso := dePresupuesto[avisos.CategoriaSobreMedia]; aviso.Prioridad != 5 || aviso.Titulo != "Gastas más en Alimentos y mercado" {
		t.Fatalf("aviso de categoría = %+v", aviso)
	}
	if aviso := dePresupuesto[avisos.PresupuestoAnticipado]; aviso.Prioridad != 4 || aviso.SeveridadCentavos != 60_000_000 {
		t.Fatalf("prioridad o severidad del presupuesto = %+v", aviso)
	}
	if _, existe := tiposDeAvisos(t, servicio, categoria)[avisos.PresupuestoAnticipado]; !existe {
		t.Fatal("el escenario de categoría también dispara presupuesto")
	}
	if _, existe := tiposDeAvisos(t, servicio, caida)[avisos.CaidaIngreso]; !existe {
		t.Fatal("falta la caída de ingreso")
	}
	if aviso, existe := tiposDeAvisos(t, servicio, pago)[avisos.PagoNoAlcanza]; !existe ||
		aviso.Texto != "Tu saldo es $300.000 y los pagos recurrentes que vencen desde 2026-07-17 suman $800.000." {
		t.Fatalf("aviso de pago = %+v", aviso)
	}
	if aviso, existe := tiposDeAvisos(t, servicio, cobro)[avisos.CobroNuevo]; !existe ||
		aviso.Texto != "Apareció un cobro de $151.000 el 2026-06-20 y ya se repite o está declarado como recurrente." {
		t.Fatalf("aviso de cobro = %+v", aviso)
	}
}

func TestEmitirAvisosEsIdempotenteYConservaElTexto(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	servicio := NuevoServicio(pool)
	usuario := usuarioDeAvisos(t, pool)
	gastoMensual(t, pool, usuario, 5, 10_000_000, "alimentos_mercado", "MERCADO", "L-MERCADO")
	sembrarCopia(t, pool, filaDeCopia{usuario: usuario,
		ocurrido: time.Date(2026, 7, 3, 15, 0, 0, 0, time.UTC), monto: 90_000_000,
		direccion: "salida", categoria: "alimentos_mercado", contraparte: "MERCADO",
		llave: "L-MERCADO", nombre: "MERCADO", transaccion: uuid.New()})

	if _, err := servicio.EmitirAvisos(t.Context(), ahoraDeAvisos); err != nil {
		t.Fatalf("primera corrida: %v", err)
	}
	primera := tiposDeAvisos(t, servicio, usuario)
	insertados, err := servicio.EmitirAvisos(t.Context(), ahoraDeAvisos.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("segunda corrida: %v", err)
	}
	if insertados != 0 {
		t.Fatalf("la segunda corrida insertó %d avisos", insertados)
	}
	segunda := tiposDeAvisos(t, servicio, usuario)
	if len(segunda) != len(primera) {
		t.Fatalf("avisos = %d, want %d", len(segunda), len(primera))
	}
	for tipo, aviso := range primera {
		if segunda[tipo].ID != aviso.ID || segunda[tipo].Texto != aviso.Texto {
			t.Fatalf("el aviso %s cambió: %+v contra %+v", tipo, segunda[tipo], aviso)
		}
	}
}

func TestEmitirAvisosRespetaElCupoMensual(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	servicio := NuevoServicio(pool)
	usuario := usuarioDeAvisos(t, pool)
	gastoMensual(t, pool, usuario, 5, 10_000_000, "alimentos_mercado", "MERCADO", "L-MERCADO")
	sembrarCopia(t, pool, filaDeCopia{usuario: usuario,
		ocurrido: time.Date(2026, 7, 3, 15, 0, 0, 0, time.UTC), monto: 90_000_000,
		direccion: "salida", categoria: "alimentos_mercado", contraparte: "MERCADO",
		llave: "L-MERCADO", nombre: "MERCADO", transaccion: uuid.New()})
	sembrarAvisosPrevios(t, pool, usuario, 2)
	insertados, err := servicio.EmitirAvisos(t.Context(), ahoraDeAvisos)
	if err != nil {
		t.Fatalf("EmitirAvisos: %v", err)
	}
	if insertados != 1 {
		t.Fatalf("con dos avisos previos insertados = %d, want 1", insertados)
	}
}

func TestEmitirAvisosConElCupoAgotadoNoInserta(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	servicio := NuevoServicio(pool)
	usuario := usuarioDeAvisos(t, pool)
	gastoMensual(t, pool, usuario, 5, 10_000_000, "alimentos_mercado", "MERCADO", "L-MERCADO")
	sembrarCopia(t, pool, filaDeCopia{usuario: usuario,
		ocurrido: time.Date(2026, 7, 3, 15, 0, 0, 0, time.UTC), monto: 90_000_000,
		direccion: "salida", categoria: "alimentos_mercado", contraparte: "MERCADO",
		llave: "L-MERCADO", nombre: "MERCADO", transaccion: uuid.New()})
	sembrarAvisosPrevios(t, pool, usuario, 3)
	insertados, err := servicio.EmitirAvisos(t.Context(), ahoraDeAvisos)
	if err != nil {
		t.Fatalf("EmitirAvisos: %v", err)
	}
	if insertados != 0 {
		t.Fatalf("con el cupo agotado insertados = %d, want 0", insertados)
	}
}

func sembrarAvisosPrevios(t *testing.T, pool *pgxpool.Pool, usuario string, cantidad int) {
	t.Helper()
	for i := 0; i < cantidad; i++ {
		_, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
			func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
				_, err := tx.Exec(ctx, `
INSERT INTO inteligencia.aviso (id, usuario_id, tipo, clave, periodo, severidad_centavos, prioridad,
    texto, texto_verificado, creado_en)
VALUES ($1, $2, 'cobro_nuevo', $3, '2026-07-01', 1000, 3, 'aviso previo', true, $4)`,
					pgtype.UUID{Bytes: uuid.New(), Valid: true}, usuario, fmt.Sprintf("previo-%d", i),
					pgtype.Timestamptz{Time: ahoraDeAvisos.Add(-time.Duration(i) * time.Hour), Valid: true})
				return struct{}{}, err
			})
		if err != nil {
			t.Fatalf("sembrar aviso previo: %v", err)
		}
	}
}

func TestDescartarAvisoLoQuitaDeLaBandejaYConservaElInstante(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	servicio := NuevoServicio(pool)
	usuario := usuarioDeAvisos(t, pool)
	otro := usuarioDeAvisos(t, pool)
	gastoMensual(t, pool, usuario, 5, 10_000_000, "alimentos_mercado", "MERCADO", "L-MERCADO")
	sembrarCopia(t, pool, filaDeCopia{usuario: usuario,
		ocurrido: time.Date(2026, 7, 3, 15, 0, 0, 0, time.UTC), monto: 90_000_000,
		direccion: "salida", categoria: "alimentos_mercado", contraparte: "MERCADO",
		llave: "L-MERCADO", nombre: "MERCADO", transaccion: uuid.New()})
	if _, err := servicio.EmitirAvisos(t.Context(), ahoraDeAvisos); err != nil {
		t.Fatalf("EmitirAvisos: %v", err)
	}
	delMes, err := servicio.AvisosDelUsuario(t.Context(), usuario, "2026-07", ahoraDeAvisos)
	if err != nil || len(delMes) == 0 {
		t.Fatalf("avisos = %d, err = %v", len(delMes), err)
	}
	aviso := delMes[0]

	primero, err := servicio.DescartarAviso(t.Context(), usuario, aviso.ID, ahoraDeAvisos)
	if err != nil {
		t.Fatalf("descartar: %v", err)
	}
	segundo, err := servicio.DescartarAviso(t.Context(), usuario, aviso.ID, ahoraDeAvisos.Add(time.Hour))
	if err != nil {
		t.Fatalf("descartar dos veces: %v", err)
	}
	if !primero.DescartadoEn.Equal(segundo.DescartadoEn) {
		t.Fatalf("el descarte se reescribió: %v contra %v", primero.DescartadoEn, segundo.DescartadoEn)
	}
	restantes, err := servicio.AvisosDelUsuario(t.Context(), usuario, "2026-07", ahoraDeAvisos)
	if err != nil {
		t.Fatalf("listar después de descartar: %v", err)
	}
	for _, restante := range restantes {
		if restante.ID == aviso.ID {
			t.Fatal("el aviso descartado sigue en la bandeja")
		}
	}
	if _, err := servicio.DescartarAviso(t.Context(), otro, aviso.ID, ahoraDeAvisos); !errors.Is(err, ErrAvisoNoEncontrado) {
		t.Fatalf("descartar un aviso ajeno: err = %v", err)
	}
	if _, err := servicio.DescartarAviso(t.Context(), usuario, uuid.New(), ahoraDeAvisos); !errors.Is(err, ErrAvisoNoEncontrado) {
		t.Fatalf("descartar un aviso inexistente: err = %v", err)
	}
}

func TestElRolDeInteligenciaNoEscribeEnElLedger(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	usuario := uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, usuario)
	_, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx,
				"UPDATE nucleo.posting SET monto = monto + 1 WHERE cuenta_id LIKE 'pasivo:usuario:%'")
			return struct{}{}, err
		})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("err = %v, want permiso denegado 42501", err)
	}
}
