//go:build integration

package inteligencia

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func transaccionDelPago(entryID int64) uuid.UUID {
	return transaccionDeGrupo(grupo{clave: "entry:" + strconv.FormatInt(entryID, 10)})
}

type filaDeLaCopia struct {
	Categoria         string
	Origen            string
	Contraparte       string
	ComercioNombre    string
	NombreNormalizado string
}

func leerFilaDeLaCopia(t *testing.T, pool *pgxpool.Pool, usuario string, transaccion uuid.UUID) filaDeLaCopia {
	t.Helper()
	var fila filaDeLaCopia
	_, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx, `
SELECT categoria, origen_categoria, contraparte,
       COALESCE(comercio_nombre, ''), COALESCE(contraparte_nombre_normalizado, '')
  FROM inteligencia.movimiento_enriquecido
 WHERE usuario_id = $1 AND transaccion_id = $2`,
				usuario, pgtype.UUID{Bytes: transaccion, Valid: true}).
				Scan(&fila.Categoria, &fila.Origen, &fila.Contraparte,
					&fila.ComercioNombre, &fila.NombreNormalizado)
		})
	if err != nil {
		t.Fatalf("leer fila de %q en %s: %v", usuario, transaccion, err)
	}
	return fila
}

func contarMemoria(t *testing.T, pool *pgxpool.Pool, usuario string) int {
	t.Helper()
	var total int
	_, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				"SELECT count(*) FROM inteligencia.memoria_correccion WHERE usuario_id = $1", usuario).Scan(&total)
		})
	if err != nil {
		t.Fatalf("contar memoria de %q: %v", usuario, err)
	}
	return total
}

func contarLedger(t *testing.T, pool *pgxpool.Pool) (int64, int64) {
	t.Helper()
	var asientos, lineas int64
	_, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
			if err := tx.QueryRow(ctx, "SELECT count(*) FROM nucleo.journal_entry").Scan(&asientos); err != nil {
				return struct{}{}, err
			}
			return struct{}{}, tx.QueryRow(ctx, "SELECT count(*) FROM nucleo.posting").Scan(&lineas)
		})
	if err != nil {
		t.Fatalf("contar el ledger: %v", err)
	}
	return asientos, lineas
}

func TestCorregirCategoriaRecalculaFilasPasadas(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	usuario := uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, usuario)
	base := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	ids := []int64{
		sembrarPagoExterno(t, pool, usuario, "Mercado Central", 1000, base),
		sembrarPagoExterno(t, pool, usuario, "Mercado Central", 2000, base.AddDate(0, 1, 0)),
		sembrarPagoExterno(t, pool, usuario, "Mercado Central", 3000, base.AddDate(0, 2, 0)),
	}

	servicio := NuevoServicio(pool)
	if _, err := servicio.Reconstruir(ctx, 1000); err != nil {
		t.Fatalf("Reconstruir: %v", err)
	}

	compras := CategoriaCompras
	resultado, err := servicio.CorregirMovimiento(ctx, usuario, transaccionDelPago(ids[2]),
		SolicitudDeCorreccion{Categoria: &compras})
	if err != nil {
		t.Fatalf("CorregirMovimiento: %v", err)
	}
	if resultado.FilasCambiadas != 3 {
		t.Errorf("filas cambiadas = %d, want 3", resultado.FilasCambiadas)
	}
	if resultado.Movimiento.Categoria != string(compras) || resultado.Movimiento.OrigenCategoria != "memoria" {
		t.Errorf("movimiento = %+v", resultado.Movimiento)
	}
	if resultado.Movimiento.CategoriaNombre != "Compras" {
		t.Errorf("nombre de la categoría = %q", resultado.Movimiento.CategoriaNombre)
	}
	for _, id := range ids {
		fila := leerFilaDeLaCopia(t, pool, usuario, transaccionDelPago(id))
		if fila.Categoria != string(compras) || fila.Origen != "memoria" {
			t.Errorf("fila %d = %+v, want compras por memoria", id, fila)
		}
	}

	repetido, err := servicio.CorregirMovimiento(ctx, usuario, transaccionDelPago(ids[2]),
		SolicitudDeCorreccion{Categoria: &compras})
	if err != nil {
		t.Fatalf("segunda corrección: %v", err)
	}
	if repetido.FilasCambiadas != 0 {
		t.Errorf("un reintento idéntico cambió %d filas, want 0", repetido.FilasCambiadas)
	}
	if total := contarMemoria(t, pool, usuario); total != 1 {
		t.Errorf("filas de memoria = %d, want 1 por la única clave de nombre", total)
	}
}

func TestCorregirNombreNoCategorizaNiCambiaLaClave(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	usuario := uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, usuario)
	base := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	ids := []int64{
		sembrarPagoExterno(t, pool, usuario, "Mercado Central", 1000, base),
		sembrarPagoExterno(t, pool, usuario, "Mercado Central", 2000, base.AddDate(0, 1, 0)),
	}

	servicio := NuevoServicio(pool)
	if _, err := servicio.Reconstruir(ctx, 1000); err != nil {
		t.Fatalf("Reconstruir: %v", err)
	}

	nombre := "Mercado Central Nuevo"
	resultado, err := servicio.CorregirMovimiento(ctx, usuario, transaccionDelPago(ids[1]),
		SolicitudDeCorreccion{NombreComercio: &nombre})
	if err != nil {
		t.Fatalf("corregir nombre: %v", err)
	}
	if resultado.FilasCambiadas != 2 {
		t.Errorf("filas cambiadas = %d, want 2", resultado.FilasCambiadas)
	}
	for _, id := range ids {
		fila := leerFilaDeLaCopia(t, pool, usuario, transaccionDelPago(id))
		if fila.Categoria != string(SinCategoria) || fila.Origen != "sin_categoria" {
			t.Errorf("el nombre categorizó la fila %d: %+v", id, fila)
		}
		if fila.Contraparte != nombre || fila.ComercioNombre != nombre {
			t.Errorf("nombre de la fila %d = %q / %q", id, fila.Contraparte, fila.ComercioNombre)
		}
		if fila.NombreNormalizado != "MERCADO-CENTRAL" {
			t.Errorf("la clave de nombre cambió en la fila %d: %q", id, fila.NombreNormalizado)
		}
	}

	compras := CategoriaCompras
	segunda, err := servicio.CorregirMovimiento(ctx, usuario, transaccionDelPago(ids[1]),
		SolicitudDeCorreccion{Categoria: &compras})
	if err != nil {
		t.Fatalf("corregir categoría: %v", err)
	}
	if segunda.FilasCambiadas != 2 {
		t.Errorf("la categoría no alcanzó las dos filas: %d", segunda.FilasCambiadas)
	}
	for _, id := range ids {
		fila := leerFilaDeLaCopia(t, pool, usuario, transaccionDelPago(id))
		if fila.Categoria != string(compras) || fila.Origen != "memoria" {
			t.Errorf("fila %d = %+v, want compras", id, fila)
		}
		if fila.Contraparte != nombre {
			t.Errorf("la categoría renombró la fila %d: %q", id, fila.Contraparte)
		}
	}
}

func TestCorregirCategoriaAislaUsuariosYPersonas(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	pagador, receptora := uuid.NewString(), uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, pagador)
	sembrarCuentaDeUsuario(t, pool, receptora)
	sembrarPersona(t, pool, receptora, "Persona Receptora")
	base := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	primera := sembrarTransferencia(t, pool, pagador, receptora, 1000, base)
	sembrarTransferencia(t, pool, pagador, receptora, 2000, base.AddDate(0, 0, 1))

	servicio := NuevoServicio(pool)
	if _, err := servicio.Reconstruir(ctx, 1000); err != nil {
		t.Fatalf("Reconstruir: %v", err)
	}

	compras := CategoriaCompras
	resultado, err := servicio.CorregirMovimiento(ctx, pagador, primera, SolicitudDeCorreccion{Categoria: &compras})
	if err != nil {
		t.Fatalf("CorregirMovimiento: %v", err)
	}
	if resultado.FilasCambiadas != 2 {
		t.Errorf("filas del pagador = %d, want 2", resultado.FilasCambiadas)
	}
	if resultado.Movimiento.Contraparte != "Persona Receptora" {
		t.Errorf("contraparte = %q", resultado.Movimiento.Contraparte)
	}
	receptoraFila := leerFilaDeLaCopia(t, pool, receptora, primera)
	if receptoraFila.Categoria != string(SinCategoria) {
		t.Errorf("la corrección alcanzó a la receptora: %+v", receptoraFila)
	}
}

func TestCorregirMovimientoAjenoOInexistenteEsNoEncontrado(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	duenio, atacante := uuid.NewString(), uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, duenio)
	sembrarCuentaDeUsuario(t, pool, atacante)
	base := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	id := sembrarPagoExterno(t, pool, duenio, "Mercado Central", 1000, base)
	transaccion := transaccionDelPago(id)

	servicio := NuevoServicio(pool)
	if _, err := servicio.Reconstruir(ctx, 1000); err != nil {
		t.Fatalf("Reconstruir: %v", err)
	}

	compras := CategoriaCompras
	if _, err := servicio.CorregirMovimiento(ctx, atacante, transaccion, SolicitudDeCorreccion{Categoria: &compras}); !errors.Is(err, ErrMovimientoNoEncontrado) {
		t.Errorf("transacción ajena: error = %v, want ErrMovimientoNoEncontrado", err)
	}
	if _, err := servicio.CorregirMovimiento(ctx, duenio, uuid.New(), SolicitudDeCorreccion{Categoria: &compras}); !errors.Is(err, ErrMovimientoNoEncontrado) {
		t.Errorf("transacción inexistente: error = %v, want ErrMovimientoNoEncontrado", err)
	}
	if total := contarMemoria(t, pool, atacante); total != 0 {
		t.Errorf("el atacante escribió %d filas de memoria", total)
	}
	if fila := leerFilaDeLaCopia(t, pool, duenio, transaccion); fila.Categoria != string(SinCategoria) {
		t.Errorf("la víctima cambió: %+v", fila)
	}
}

func TestCorregirCategoriaNoVisibleNoEscribeNada(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	usuario := uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, usuario)
	id := sembrarPagoExterno(t, pool, usuario, "Mercado Central", 1000, time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC))
	servicio := NuevoServicio(pool)
	if _, err := servicio.Reconstruir(ctx, 1000); err != nil {
		t.Fatalf("Reconstruir: %v", err)
	}

	invisible := SinCategoria
	inventada := Categoria("inventada")
	for nombre, categoria := range map[string]Categoria{"sin_categoria": invisible, "inventada": inventada} {
		t.Run(nombre, func(t *testing.T) {
			if _, err := servicio.CorregirMovimiento(ctx, usuario, transaccionDelPago(id),
				SolicitudDeCorreccion{Categoria: &categoria}); !errors.Is(err, ErrCategoriaNoVisible) {
				t.Fatalf("error = %v, want ErrCategoriaNoVisible", err)
			}
		})
	}
	if total := contarMemoria(t, pool, usuario); total != 0 {
		t.Errorf("memoria = %d, want 0", total)
	}
	if fila := leerFilaDeLaCopia(t, pool, usuario, transaccionDelPago(id)); fila.Categoria != string(SinCategoria) {
		t.Errorf("la fila cambió: %+v", fila)
	}
}

func TestCorregirNombreEnPersonaYEnDestinoGenericoSeRechaza(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	pagador, receptora := uuid.NewString(), uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, pagador)
	sembrarCuentaDeUsuario(t, pool, receptora)
	sembrarPersona(t, pool, receptora, "Persona Receptora")
	transferencia := sembrarTransferencia(t, pool, pagador, receptora, 1000,
		time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC))
	generico := sembrarPagoExterno(t, pool, pagador, textoComercioExterno, 2000,
		time.Date(2026, time.June, 2, 12, 0, 0, 0, time.UTC))

	servicio := NuevoServicio(pool)
	if _, err := servicio.Reconstruir(ctx, 1000); err != nil {
		t.Fatalf("Reconstruir: %v", err)
	}

	nombre := "Nombre Corregido"
	if _, err := servicio.CorregirMovimiento(ctx, pagador, transferencia, SolicitudDeCorreccion{NombreComercio: &nombre}); !errors.Is(err, ErrNombreNoAplica) {
		t.Errorf("persona: error = %v, want ErrNombreNoAplica", err)
	}
	compras := CategoriaCompras
	if _, err := servicio.CorregirMovimiento(ctx, pagador, transaccionDelPago(generico), SolicitudDeCorreccion{Categoria: &compras}); !errors.Is(err, ErrDestinoGenerico) {
		t.Errorf("genérico por categoría: error = %v, want ErrDestinoGenerico", err)
	}
	if _, err := servicio.CorregirMovimiento(ctx, pagador, transaccionDelPago(generico), SolicitudDeCorreccion{NombreComercio: &nombre}); !errors.Is(err, ErrDestinoGenerico) {
		t.Errorf("genérico por nombre: error = %v, want ErrDestinoGenerico", err)
	}
	if total := contarMemoria(t, pool, pagador); total != 0 {
		t.Errorf("memoria = %d, want 0", total)
	}
}

func TestReconstruirConservaCategoriaYNombreCorregidos(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	usuario := uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, usuario)
	base := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	ids := []int64{
		sembrarPagoExterno(t, pool, usuario, "Mercado Central", 1000, base),
		sembrarPagoExterno(t, pool, usuario, "Mercado Central", 2000, base.AddDate(0, 1, 0)),
	}

	servicio := NuevoServicio(pool)
	if _, err := servicio.Reconstruir(ctx, 1000); err != nil {
		t.Fatalf("Reconstruir: %v", err)
	}

	nombre := "Mercado Central Nuevo"
	if _, err := servicio.CorregirMovimiento(ctx, usuario, transaccionDelPago(ids[0]),
		SolicitudDeCorreccion{NombreComercio: &nombre}); err != nil {
		t.Fatalf("corregir nombre: %v", err)
	}
	compras := CategoriaCompras
	if _, err := servicio.CorregirMovimiento(ctx, usuario, transaccionDelPago(ids[0]),
		SolicitudDeCorreccion{Categoria: &compras}); err != nil {
		t.Fatalf("corregir categoría: %v", err)
	}

	antes, err := servicio.Huella(ctx)
	if err != nil {
		t.Fatalf("Huella antes: %v", err)
	}
	despues, err := servicio.Reconstruir(ctx, 1000)
	if err != nil {
		t.Fatalf("Reconstruir con correcciones: %v", err)
	}
	if antes != despues {
		t.Errorf("la huella cambió: %s y %s", antes, despues)
	}
	for _, id := range ids {
		fila := leerFilaDeLaCopia(t, pool, usuario, transaccionDelPago(id))
		if fila.Categoria != string(compras) || fila.Contraparte != nombre || fila.ComercioNombre != nombre {
			t.Errorf("la reconstrucción perdió la corrección en %d: %+v", id, fila)
		}
		if fila.NombreNormalizado != "MERCADO-CENTRAL" {
			t.Errorf("la clave de nombre cambió en %d: %q", id, fila.NombreNormalizado)
		}
	}
}

func TestCorregirNoEscribeEnElLedger(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	usuario := uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, usuario)
	id := sembrarPagoExterno(t, pool, usuario, "Mercado Central", 1000, time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC))
	servicio := NuevoServicio(pool)
	if _, err := servicio.Reconstruir(ctx, 1000); err != nil {
		t.Fatalf("Reconstruir: %v", err)
	}

	asientosAntes, lineasAntes := contarLedger(t, pool)
	compras := CategoriaCompras
	if _, err := servicio.CorregirMovimiento(ctx, usuario, transaccionDelPago(id), SolicitudDeCorreccion{Categoria: &compras}); err != nil {
		t.Fatalf("CorregirMovimiento: %v", err)
	}
	asientosDespues, lineasDespues := contarLedger(t, pool)
	if asientosAntes != asientosDespues || lineasAntes != lineasDespues {
		t.Errorf("el ledger cambió: asientos %d a %d, líneas %d a %d",
			asientosAntes, asientosDespues, lineasAntes, lineasDespues)
	}
}

func TestCorregirFilaSinDestinoPideReconstruir(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	usuario := uuid.NewString()
	transaccion := uuid.New()
	_, err := enTransaccion(ctx, poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, `
INSERT INTO inteligencia.movimiento_enriquecido (
    usuario_id, transaccion_id, entry_ancla, ocurrido_en, moneda, direccion, monto_centavos,
    contraparte, contraparte_resuelta, categoria, origen_categoria, version_modelo
) VALUES ($1, $2, 1, now(), 'COP', 'salida', 100, 'Vieja', false, 'sin_categoria', 'sin_categoria', 'prueba')`,
				usuario, pgtype.UUID{Bytes: transaccion, Valid: true})
			return struct{}{}, err
		})
	if err != nil {
		t.Fatalf("insertar fila sin destino: %v", err)
	}

	compras := CategoriaCompras
	_, err = NuevoServicio(pool).CorregirMovimiento(ctx, usuario, transaccion, SolicitudDeCorreccion{Categoria: &compras})
	if !errors.Is(err, ErrCopiaSinDestino) {
		t.Fatalf("error = %v, want ErrCopiaSinDestino", err)
	}
}

func TestCorregirCategoriaYNombreCuentaCadaFilaUnaVez(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	usuario := uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, usuario)
	base := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	ids := []int64{
		sembrarPagoExterno(t, pool, usuario, "Mercado Central", 1000, base),
		sembrarPagoExterno(t, pool, usuario, "Mercado Central", 2000, base.AddDate(0, 1, 0)),
	}

	servicio := NuevoServicio(pool)
	if _, err := servicio.Reconstruir(ctx, 1000); err != nil {
		t.Fatalf("Reconstruir: %v", err)
	}

	compras := CategoriaCompras
	nombre := "Mercado Central Nuevo"
	resultado, err := servicio.CorregirMovimiento(ctx, usuario, transaccionDelPago(ids[0]),
		SolicitudDeCorreccion{Categoria: &compras, NombreComercio: &nombre})
	if err != nil {
		t.Fatalf("CorregirMovimiento: %v", err)
	}
	if resultado.FilasCambiadas != 2 {
		t.Errorf("filas cambiadas = %d, want 2 y no 4", resultado.FilasCambiadas)
	}
	for _, id := range ids {
		fila := leerFilaDeLaCopia(t, pool, usuario, transaccionDelPago(id))
		if fila.Categoria != string(compras) || fila.Contraparte != nombre || fila.ComercioNombre != nombre {
			t.Errorf("fila %d = %+v", id, fila)
		}
	}
}
