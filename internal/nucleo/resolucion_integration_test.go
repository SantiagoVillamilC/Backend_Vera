//go:build integration

package nucleo

import (
	"context"
	"math/rand"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const ventanaDeResolucion = 5 * time.Minute

// secuenciaDeResolucion separa con una hora la ventana de cada prueba dentro de la misma corrida.
var secuenciaDeResolucion atomic.Int64

// epocaDeResolucion aparta las ventanas de esta corrida de las transferencias que dejaron las anteriores.
var epocaDeResolucion = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).
	Add(time.Duration(rand.Int63n(int64(60*365*24))) * time.Hour)

// baseUnicaDeResolucion da a cada prueba una ventana disjunta de las de cualquier otra corrida.
func baseUnicaDeResolucion() time.Time {
	return epocaDeResolucion.Add(time.Duration(secuenciaDeResolucion.Add(1)) * time.Hour)
}

// sembrarUsuarioYTransferencia crea las cuentas del emisor y su transferencia en el estado y momento pedidos.
func sembrarUsuarioYTransferencia(t *testing.T, ctx context.Context, pool *pgxpool.Pool, estado string, monto Centavos, actualizadaEn time.Time, conDespachoPendiente bool) (pgtype.UUID, string) {
	t.Helper()
	usuario := uuid.NewString()
	id := uuid.New()
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			if _, err := tx.Exec(ctx,
				`INSERT INTO cuenta (id, naturaleza, clase, titular_id) VALUES
				 ($1, 'pasivo', 'usuario', $3), ($2, 'pasivo', 'usuario', $3)`,
				NombreCuentaDisponible(usuario), NombreCuentaRetenido(usuario), usuario); err != nil {
				return struct{}{}, err
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO transferencia (id, usuario_id, cuenta_origen, llave_destino, monto, moneda, idempotency_key, estado, actualizada_en)
				 VALUES ($1, $2, $3, 'llave-resolucion', $4, 'COP', $5, $6, $7)`,
				id, usuario, NombreCuentaDisponible(usuario), monto.Int64(), uuid.NewString(), estado, actualizadaEn); err != nil {
				return struct{}{}, err
			}
			if !conDespachoPendiente {
				return struct{}{}, nil
			}
			// disponible_en lejos: el barrido del outbox de otras suites no debe tomar esta fila.
			_, err := tx.Exec(ctx,
				`INSERT INTO outbox (tipo, payload, disponible_en)
				 VALUES ('despacho_transferencia', jsonb_build_object('transferencia_id', $1::text), now() + interval '100 years')`,
				id.String())
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("sembrar transferencia: %v", err)
	}
	return pgtype.UUID{Bytes: id, Valid: true}, usuario
}

// sembrarDespacho escribe el asiento 3 de una transferencia para probar que la compensación lo deja intacto.
func sembrarDespacho(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id pgtype.UUID, ocurridoEn time.Time) {
	t.Helper()
	transferencia := leerTransferenciaDeResolucion(t, ctx, pool, id)
	lineas, err := lineasDeDespacho(transferencia)
	if err != nil {
		t.Fatalf("líneas del despacho: %v", err)
	}
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			return struct{}{}, insertarAsientoConLineas(ctx, q, TipoAsientoDespachoRiel, transferencia, ocurridoEn, pgtype.Text{}, lineas)
		}); err != nil {
		t.Fatalf("sembrar despacho: %v", err)
	}
}

// leerTransferenciaDeResolucion trae la fila cruda de la transferencia sembrada.
func leerTransferenciaDeResolucion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id pgtype.UUID) repositorio.Transferencia {
	t.Helper()
	fila, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (repositorio.Transferencia, error) {
			return q.ObtenerTransferencia(ctx, id)
		})
	if err != nil {
		t.Fatalf("leer transferencia: %v", err)
	}
	return fila
}

// saldoDeCustodiaHasta es el lado interno contra el que compara la diferencia del invariante 2.
func saldoDeCustodiaHasta(t *testing.T, ctx context.Context, pool *pgxpool.Pool, corte time.Time) Centavos {
	t.Helper()
	var saldo int64
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			s, err := q.SaldoDeCuentaHasta(ctx, repositorio.SaldoDeCuentaHastaParams{
				CuentaID: CuentaCustodiaBanco,
				CorteEn:  pgtype.Timestamptz{Time: corte, Valid: true},
			})
			saldo = s
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("saldo de custodia: %v", err)
	}
	return Centavos(saldo)
}

// custodioConDesfase devuelve el extracto con el desfase pedido sobre el saldo interno de custodia.
func custodioConDesfase(t *testing.T, ctx context.Context, pool *pgxpool.Pool, corte time.Time, desfase Centavos) LectorExtractoCustodio {
	t.Helper()
	saldo, err := saldoDeCustodiaHasta(t, ctx, pool, corte).Sumar(desfase)
	if err != nil {
		t.Fatalf("aplicar el desfase de custodia: %v", err)
	}
	return &custodioFalso{extracto: ExtractoCustodio{CorteEn: corte, Saldo: saldo, Origen: "prueba"}}
}

// custodioQueCuadra devuelve el extracto que hace cero la comparación del invariante 2.
func custodioQueCuadra(t *testing.T, ctx context.Context, pool *pgxpool.Pool, corte time.Time) LectorExtractoCustodio {
	t.Helper()
	return custodioConDesfase(t, ctx, pool, corte, 0)
}

// correrConciliacionDeResolucion fija el cursor y corre la corrida con el detector real.
func correrConciliacionDeResolucion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, desde, hasta time.Time, riel LectorExtractoRiel, custodio LectorExtractoCustodio, rastreador RastreadorErrores) int {
	t.Helper()
	fijarCursor(t, pool, desde)
	servicio := NewServicio(pool, nil, nil, custodio, rastreador, ConLectorExtractoRiel(riel))
	servicio.ahora = func() time.Time { return hasta.Add(ventanaDeResolucion) }
	n, err := servicio.Conciliar(ctx, ventanaDeResolucion)
	if err != nil {
		t.Fatalf("Conciliar: %v", err)
	}
	return n
}

// contarDescuadres cuenta las filas del tipo y la identidad pedidos.
func contarDescuadres(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tipo, identidad string) int64 {
	t.Helper()
	var filas int64
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				`SELECT count(*) FROM descuadre WHERE tipo = $1 AND COALESCE(transferencia_id::text, referencia_externa) = $2`,
				tipo, identidad).Scan(&filas)
		}); err != nil {
		t.Fatalf("contar descuadres: %v", err)
	}
	return filas
}

// contarDescuadresAbiertos cuenta las filas sin resolver del tipo y la identidad pedidos.
func contarDescuadresAbiertos(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tipo, identidad string) int64 {
	t.Helper()
	var filas int64
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				`SELECT count(*) FROM descuadre WHERE tipo = $1 AND COALESCE(transferencia_id::text, referencia_externa) = $2 AND resuelto_en IS NULL`,
				tipo, identidad).Scan(&filas)
		}); err != nil {
		t.Fatalf("contar descuadres abiertos: %v", err)
	}
	return filas
}

// leerDescuadre trae la última fila del tipo e identidad pedidos, resuelta o no.
func leerDescuadre(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tipo, identidad string) repositorio.Descuadre {
	t.Helper()
	fila, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (repositorio.Descuadre, error) {
			var d repositorio.Descuadre
			err := tx.QueryRow(ctx,
				`SELECT id, tipo, transferencia_id, referencia_externa, monto_interno, monto_externo,
				        ventana_desde, ventana_hasta, detectado_en, resuelto_en, resolucion
				   FROM descuadre
				  WHERE tipo = $1 AND COALESCE(transferencia_id::text, referencia_externa) = $2
				  ORDER BY id DESC LIMIT 1`, tipo, identidad).
				Scan(&d.ID, &d.Tipo, &d.TransferenciaID, &d.ReferenciaExterna, &d.MontoInterno, &d.MontoExterno,
					&d.VentanaDesde, &d.VentanaHasta, &d.DetectadoEn, &d.ResueltoEn, &d.Resolucion)
			return d, err
		})
	if err != nil {
		t.Fatalf("leer descuadre: %v", err)
	}
	return fila
}

// resolverDescuadresDeCustodiaAbiertos libera la identidad estable del invariante 2 entre corridas.
func resolverDescuadresDeCustodiaAbiertos(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			abiertos, err := q.BuscarDescuadresAbiertos(ctx, repositorio.BuscarDescuadresAbiertosParams{
				Tipo: TipoDescuadreCustodiaSaldoDistinto, Identidad: referenciaCustodia,
			})
			if err != nil {
				return struct{}{}, err
			}
			for _, fila := range abiertos {
				if _, err := q.ResolverDescuadre(ctx, repositorio.ResolverDescuadreParams{
					ID: fila.ID, Resolucion: pgtype.Text{String: "resuelta por la prueba", Valid: true},
				}); err != nil {
					return struct{}{}, err
				}
			}
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("resolver descuadres de custodia abiertos: %v", err)
	}
}

// asientosDeTransferencia trae los asientos ligados a una transferencia.
func asientosDeTransferencia(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id pgtype.UUID) []repositorio.JournalEntry {
	t.Helper()
	filas, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) ([]repositorio.JournalEntry, error) {
			return q.LeerAsientosDeTransferencia(ctx, id)
		})
	if err != nil {
		t.Fatalf("leer asientos: %v", err)
	}
	return filas
}

// tieneAsientoDeTipo reporta si entre los asientos hay uno del tipo pedido.
func tieneAsientoDeTipo(asientos []repositorio.JournalEntry, tipo string) bool {
	for _, asiento := range asientos {
		if asiento.Tipo == tipo {
			return true
		}
	}
	return false
}

// verificarCamposDeLaCaptura comprueba el canal y los campos identificadores que exige el criterio 24.
func verificarCamposDeLaCaptura(t *testing.T, captura capturaFalsa, quiero map[string]string) {
	t.Helper()

	if captura.contexto.Canal != CanalSoloLog {
		t.Errorf("canal = %q, quiero %q", captura.contexto.Canal, CanalSoloLog)
	}
	porClave := make(map[string]string, len(captura.contexto.Campos))
	for _, campo := range captura.contexto.Campos {
		porClave[campo.Clave] = campo.Valor
	}
	for clave, valor := range quiero {
		if porClave[clave] != valor {
			t.Errorf("campo %q = %q, quiero %q", clave, porClave[clave], valor)
		}
	}
	for _, clave := range []string{"descuadre_id", "detectado_en"} {
		if porClave[clave] == "" {
			t.Errorf("campo %q vacío: el log no ubica la diferencia", clave)
		}
	}
}

// HU-NF-08, criterio 5: lo que está en el libro y no en el extracto se compensa y deja el despacho intacto.
func TestHU_NF_08_Criterio5LibroSinExtractoCompensaYDejaElDespachoIntacto(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := baseUnicaDeResolucion()
	desde, hasta := base, base.Add(ventanaDeResolucion)
	monto := Centavos(150000)

	id, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaDespachada, monto, base.Add(time.Minute), false)
	sembrarDespacho(t, ctx, pool, id, base.Add(time.Minute))

	riel := &rielFalso{}
	rastreador := &rastreadorFalso{}
	n := correrConciliacionDeResolucion(t, ctx, pool, desde, hasta, riel, custodioQueCuadra(t, ctx, pool, hasta), rastreador)

	if n != 1 {
		t.Fatalf("diferencias = %d, quiero 1", n)
	}
	fila := leerDescuadre(t, ctx, pool, TipoDescuadreLibroSinExtracto, uuid.UUID(id.Bytes).String())
	if fila.ID == 0 || !fila.MontoInterno.Valid || fila.MontoInterno.Int64 != monto.Int64() {
		t.Fatalf("descuadre = %+v, quiero tipo libro_sin_extracto con el monto interno", fila)
	}
	transferencia := leerTransferenciaDeResolucion(t, ctx, pool, id)
	if transferencia.Estado != EstadoTransferenciaCompensada {
		t.Errorf("estado = %q, quiero compensada", transferencia.Estado)
	}
	asientos := asientosDeTransferencia(t, ctx, pool, id)
	if !tieneAsientoDeTipo(asientos, TipoAsientoCompensacion) {
		t.Errorf("asientos = %+v, quiero el 4b de compensación", asientos)
	}
	if !tieneAsientoDeTipo(asientos, TipoAsientoDespachoRiel) {
		t.Errorf("asientos = %+v, quiero el despacho original intacto", asientos)
	}
	if len(rastreador.capturas) != 0 {
		t.Errorf("capturas = %d, quiero 0: la compensación se resuelve sola", len(rastreador.capturas))
	}
}

// HU-NF-08, criterio 6: lo que está en el extracto y no en el libro se marca, se escala y no se asienta.
func TestHU_NF_08_Criterio6ExtractoSinLibroRegistraYEscalaSinAsiento(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := baseUnicaDeResolucion()
	desde, hasta := base, base.Add(ventanaDeResolucion)
	externa := uuid.New()

	riel := &rielFalso{extracto: ExtractoRiel{Operaciones: []OperacionDeExtractoRiel{{
		TransferenciaID: externa, Estado: estadoExtractoConfirmada, Monto: 50000, OcurridoEn: base.Add(time.Minute),
	}}}}
	rastreador := &rastreadorFalso{}
	n := correrConciliacionDeResolucion(t, ctx, pool, desde, hasta, riel, custodioQueCuadra(t, ctx, pool, hasta), rastreador)

	if n != 1 {
		t.Fatalf("diferencias = %d, quiero 1", n)
	}
	if contarDescuadres(t, ctx, pool, TipoDescuadreExtractoSinLibro, externa.String()) != 1 {
		t.Fatalf("no quedó la fila de extracto_sin_libro con la referencia externa")
	}
	if len(asientosDeTransferencia(t, ctx, pool, pgtype.UUID{Bytes: externa, Valid: true})) != 0 {
		t.Error("un extracto sin libro no debe producir ningún asiento")
	}
	if len(rastreador.capturas) != 1 {
		t.Fatalf("capturas = %d, quiero 1 por el defecto grave", len(rastreador.capturas))
	}
	if !strings.Contains(rastreador.capturas[0].err.Error(), "defecto grave") {
		t.Errorf("causa = %q, quiero el defecto grave", rastreador.capturas[0].err)
	}
	if !strings.Contains(rastreador.capturas[0].err.Error(), externa.String()) {
		t.Errorf("causa = %q, quiero que identifique la referencia externa", rastreador.capturas[0].err)
	}
}

// HU-NF-08, criterio 7: mismos en el libro y el extracto con montos distintos se escala sin automatizar.
func TestHU_NF_08_Criterio7MontoDistintoEscalaSinAutomatizar(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := baseUnicaDeResolucion()
	desde, hasta := base, base.Add(ventanaDeResolucion)

	id, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaDespachada, 100000, base.Add(time.Minute), false)
	riel := &rielFalso{extracto: ExtractoRiel{Operaciones: []OperacionDeExtractoRiel{{
		TransferenciaID: uuid.UUID(id.Bytes), Estado: estadoExtractoConfirmada, Monto: 99999, OcurridoEn: base.Add(time.Minute),
	}}}}
	rastreador := &rastreadorFalso{}
	n := correrConciliacionDeResolucion(t, ctx, pool, desde, hasta, riel, custodioQueCuadra(t, ctx, pool, hasta), rastreador)

	if n != 1 {
		t.Fatalf("diferencias = %d, quiero 1", n)
	}
	fila := leerDescuadre(t, ctx, pool, TipoDescuadreMontoDistinto, uuid.UUID(id.Bytes).String())
	if !fila.MontoInterno.Valid || fila.MontoInterno.Int64 != 100000 || !fila.MontoExterno.Valid || fila.MontoExterno.Int64 != 99999 {
		t.Fatalf("descuadre = %+v, quiero los dos montos en centavos", fila)
	}
	if transferencia := leerTransferenciaDeResolucion(t, ctx, pool, id); transferencia.Estado != EstadoTransferenciaDespachada {
		t.Errorf("estado = %q, quiero despachada sin automatizar", transferencia.Estado)
	}
	if len(asientosDeTransferencia(t, ctx, pool, id)) != 0 {
		t.Error("el monto distinto no debe producir asiento")
	}
	if len(rastreador.capturas) != 1 {
		t.Fatalf("capturas = %d, quiero 1", len(rastreador.capturas))
	}
	verificarCamposDeLaCaptura(t, rastreador.capturas[0], map[string]string{
		"tipo":             TipoDescuadreMontoDistinto,
		"transferencia_id": uuid.UUID(id.Bytes).String(),
		"monto_interno":    "100000",
		"monto_externo":    "99999",
		"ventana_desde":    desde.Format(time.RFC3339Nano),
		"ventana_hasta":    hasta.Format(time.RFC3339Nano),
	})
}

// HU-NF-08, criterio 8.1: el extracto confirma una transferencia sin resolver y manda el extracto.
func TestHU_NF_08_Criterio8_1ElExtractoConfirmaYSeProduceElAsientoDeCierre(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := baseUnicaDeResolucion()
	desde, hasta := base, base.Add(ventanaDeResolucion)
	monto := Centavos(200000)

	id, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaDespachada, monto, base.Add(time.Minute), false)
	sembrarDespacho(t, ctx, pool, id, base.Add(time.Minute))
	riel := &rielFalso{extracto: ExtractoRiel{Operaciones: []OperacionDeExtractoRiel{{
		TransferenciaID: uuid.UUID(id.Bytes), Estado: estadoExtractoConfirmada, Monto: monto, OcurridoEn: base.Add(2 * time.Minute),
	}}}}
	desfase, err := Centavos(0).Restar(monto)
	if err != nil {
		t.Fatalf("restar el efecto del 4a: %v", err)
	}
	n := correrConciliacionDeResolucion(t, ctx, pool, desde, hasta, riel, custodioConDesfase(t, ctx, pool, hasta, desfase), &rastreadorFalso{})

	if n != 1 {
		t.Fatalf("diferencias = %d, quiero 1", n)
	}
	if transferencia := leerTransferenciaDeResolucion(t, ctx, pool, id); transferencia.Estado != EstadoTransferenciaConfirmada {
		t.Errorf("estado = %q, quiero confirmada", transferencia.Estado)
	}
	if !tieneAsientoDeTipo(asientosDeTransferencia(t, ctx, pool, id), TipoAsientoLiquidacionConfirmada) {
		t.Error("falta el 4a que resuelve el dinero en vuelo contra la custodia")
	}
}

// HU-NF-08, criterio 8.2: el extracto rechaza una indeterminada y produce la compensación.
func TestHU_NF_08_Criterio8_2ElExtractoRechazaYSeProduceLaCompensacion(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := baseUnicaDeResolucion()
	desde, hasta := base, base.Add(ventanaDeResolucion)
	monto := Centavos(175000)

	id, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaIndeterminada, monto, base.Add(time.Minute), false)
	riel := &rielFalso{extracto: ExtractoRiel{Operaciones: []OperacionDeExtractoRiel{{
		TransferenciaID: uuid.UUID(id.Bytes), Estado: estadoExtractoRechazada, Monto: monto, OcurridoEn: base.Add(2 * time.Minute),
	}}}}
	n := correrConciliacionDeResolucion(t, ctx, pool, desde, hasta, riel, custodioQueCuadra(t, ctx, pool, hasta), &rastreadorFalso{})

	if n != 1 {
		t.Fatalf("diferencias = %d, quiero 1", n)
	}
	if transferencia := leerTransferenciaDeResolucion(t, ctx, pool, id); transferencia.Estado != EstadoTransferenciaCompensada {
		t.Errorf("estado = %q, quiero compensada", transferencia.Estado)
	}
	if !tieneAsientoDeTipo(asientosDeTransferencia(t, ctx, pool, id), TipoAsientoCompensacion) {
		t.Error("falta el 4b que devuelve el dinero al disponible del emisor")
	}
}

// HU-NF-08, criterio 8.3 y 22: un estado final contradicho se registra y se escala sin revertir el cierre.
func TestHU_NF_08_Criterio22EstadoFinalContradichoNoRevierteElCierre(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := baseUnicaDeResolucion()
	desde, hasta := base, base.Add(ventanaDeResolucion)
	monto := Centavos(210000)

	id, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaConfirmada, monto, base.Add(time.Minute), false)
	cierre := leerTransferenciaDeResolucion(t, ctx, pool, id)
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			return struct{}{}, escribirAsientoDeConfirmacion(ctx, q, cierre, base.Add(time.Minute))
		}); err != nil {
		t.Fatalf("sembrar el 4a: %v", err)
	}
	antes := len(asientosDeTransferencia(t, ctx, pool, id))

	riel := &rielFalso{extracto: ExtractoRiel{Operaciones: []OperacionDeExtractoRiel{{
		TransferenciaID: uuid.UUID(id.Bytes), Estado: estadoExtractoRechazada, Monto: monto, OcurridoEn: base.Add(2 * time.Minute),
	}}}}
	rastreador := &rastreadorFalso{}
	n := correrConciliacionDeResolucion(t, ctx, pool, desde, hasta, riel, custodioQueCuadra(t, ctx, pool, hasta), rastreador)

	if n != 1 {
		t.Fatalf("diferencias = %d, quiero 1", n)
	}
	if fila := leerDescuadre(t, ctx, pool, TipoDescuadreEstadoFinalContradicho, uuid.UUID(id.Bytes).String()); fila.ID == 0 {
		t.Fatal("no quedó la fila de estado_final_contradicho")
	}
	if transferencia := leerTransferenciaDeResolucion(t, ctx, pool, id); transferencia.Estado != EstadoTransferenciaConfirmada {
		t.Errorf("estado = %q, quiero confirmada sin revertir", transferencia.Estado)
	}
	if despues := len(asientosDeTransferencia(t, ctx, pool, id)); despues != antes {
		t.Errorf("asientos = %d, quiero %d: no se revierte el cierre ya escrito", despues, antes)
	}
	if len(rastreador.capturas) != 1 {
		t.Fatalf("capturas = %d, quiero 1", len(rastreador.capturas))
	}
}

// HU-NF-08, criterio 23: la diferencia de custodia se registra con los dos montos, se escala y no se asienta.
func TestHU_NF_08_Criterio23CustodiaRegistraLosDosMontosYNoAsienta(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := baseUnicaDeResolucion()
	desde, hasta := base, base.Add(ventanaDeResolucion)
	resolverDescuadresDeCustodiaAbiertos(t, ctx, pool)

	interno := saldoDeCustodiaHasta(t, ctx, pool, hasta)
	externo, err := interno.Sumar(70000)
	if err != nil {
		t.Fatalf("sumar el desfase: %v", err)
	}
	custodio := &custodioFalso{extracto: ExtractoCustodio{CorteEn: hasta, Saldo: externo, Origen: "prueba"}}
	rastreador := &rastreadorFalso{}
	n := correrConciliacionDeResolucion(t, ctx, pool, desde, hasta, &rielFalso{}, custodio, rastreador)

	if n != 1 {
		t.Fatalf("diferencias = %d, quiero 1", n)
	}
	fila := leerDescuadre(t, ctx, pool, TipoDescuadreCustodiaSaldoDistinto, referenciaCustodia)
	if !fila.MontoInterno.Valid || fila.MontoInterno.Int64 != interno.Int64() {
		t.Fatalf("monto interno = %+v, quiero %d", fila.MontoInterno, interno.Int64())
	}
	if !fila.MontoExterno.Valid || fila.MontoExterno.Int64 != externo.Int64() {
		t.Fatalf("monto externo = %+v, quiero %d", fila.MontoExterno, externo.Int64())
	}
	if len(rastreador.capturas) != 1 {
		t.Fatalf("capturas = %d, quiero 1", len(rastreador.capturas))
	}
	if contarDescuadresAbiertos(t, ctx, pool, TipoDescuadreCustodiaSaldoDistinto, referenciaCustodia) != 1 {
		t.Error("la diferencia de custodia no debe quedar duplicada sin resolver")
	}
}

// HU-NF-08, criterios 4 y 6: lo pendiente de despacho no se reporta como diferencia.
func TestHU_NF_08_Criterio4LoPendienteDeDespachoNoGeneraDiferencia(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := baseUnicaDeResolucion()
	desde, hasta := base, base.Add(ventanaDeResolucion)

	despachadaConOutbox, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaDespachada, 90000, base.Add(time.Minute), true)
	reservada, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaReservada, 90000, base.Add(2*time.Minute), false)
	rastreador := &rastreadorFalso{}
	n := correrConciliacionDeResolucion(t, ctx, pool, desde, hasta, &rielFalso{}, custodioQueCuadra(t, ctx, pool, hasta), rastreador)

	if n != 0 {
		t.Fatalf("diferencias = %d, quiero 0: la orden no salió al riel", n)
	}
	if transferencia := leerTransferenciaDeResolucion(t, ctx, pool, despachadaConOutbox); transferencia.Estado != EstadoTransferenciaDespachada {
		t.Errorf("la despachada con despacho pendiente cambió a %q", transferencia.Estado)
	}
	if transferencia := leerTransferenciaDeResolucion(t, ctx, pool, reservada); transferencia.Estado != EstadoTransferenciaReservada {
		t.Errorf("la reservada cambió a %q", transferencia.Estado)
	}
	if len(rastreador.capturas) != 0 {
		t.Errorf("capturas = %d, quiero 0", len(rastreador.capturas))
	}
}

// HU-NF-08, criterios 13 y 24: una diferencia sin resolver no se duplica ni se re-escala en cada corrida.
func TestHU_NF_08_Criterio13UnaDiferenciaSinResolverNoSeDuplicaNiSeReescala(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := baseUnicaDeResolucion()
	desde, hasta := base, base.Add(ventanaDeResolucion)

	id, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaDespachada, 100000, base.Add(time.Minute), false)
	riel := &rielFalso{extracto: ExtractoRiel{Operaciones: []OperacionDeExtractoRiel{{
		TransferenciaID: uuid.UUID(id.Bytes), Estado: estadoExtractoConfirmada, Monto: 99999, OcurridoEn: base.Add(time.Minute),
	}}}}
	custodio := custodioQueCuadra(t, ctx, pool, hasta)
	rastreador := &rastreadorFalso{}

	if n := correrConciliacionDeResolucion(t, ctx, pool, desde, hasta, riel, custodio, rastreador); n != 1 {
		t.Fatalf("primera corrida = %d diferencias, quiero 1", n)
	}
	if n := correrConciliacionDeResolucion(t, ctx, pool, desde, hasta, riel, custodio, rastreador); n != 0 {
		t.Fatalf("segunda corrida = %d diferencias, quiero 0", n)
	}
	if filas := contarDescuadres(t, ctx, pool, TipoDescuadreMontoDistinto, uuid.UUID(id.Bytes).String()); filas != 1 {
		t.Errorf("filas = %d, quiero 1: la diferencia sin resolver no se duplica", filas)
	}
	if len(rastreador.capturas) != 1 {
		t.Fatalf("capturas = %d, quiero 1: el escalamiento se emite una sola vez mientras siga sin resolver", len(rastreador.capturas))
	}
	fila := leerDescuadre(t, ctx, pool, TipoDescuadreMontoDistinto, uuid.UUID(id.Bytes).String())
	if fila.ResueltoEn.Valid || fila.Resolucion.Valid {
		t.Errorf("descuadre = %+v, quiero sin resolución para que siga escalado", fila)
	}
}
