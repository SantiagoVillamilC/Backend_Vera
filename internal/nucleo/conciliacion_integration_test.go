//go:build integration

package nucleo

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ventanaPrueba = time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)

func afirmarViolacionUnica(t *testing.T, err error, indice string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("error = %v, want *pgconn.PgError", err)
	}
	if pgErr.Code != pgerrcode.UniqueViolation {
		t.Fatalf("SQLSTATE = %s, want %s", pgErr.Code, pgerrcode.UniqueViolation)
	}
	if pgErr.ConstraintName != indice {
		t.Fatalf("constraint = %q, want %q", pgErr.ConstraintName, indice)
	}
	t.Logf("violación única literal: SQLSTATE %s, constraint %s", pgErr.Code, pgErr.ConstraintName)
}

func insertarDescuadreDePrueba(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tipo string, transferenciaID pgtype.UUID, referencia pgtype.Text) (repositorio.Descuadre, error) {
	t.Helper()
	return enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (repositorio.Descuadre, error) {
			return q.InsertarDescuadre(ctx, repositorio.InsertarDescuadreParams{
				Tipo:              tipo,
				TransferenciaID:   transferenciaID,
				ReferenciaExterna: referencia,
				MontoInterno:      pgtype.Int8{Int64: 100000, Valid: true},
				MontoExterno:      pgtype.Int8{Int64: 99999, Valid: true},
				VentanaDesde:      pgtype.Timestamptz{Time: ventanaPrueba, Valid: true},
				VentanaHasta:      pgtype.Timestamptz{Time: ventanaPrueba.Add(time.Hour), Valid: true},
			})
		})
}

func TestDescuadreSinResolverPorTransferenciaNoSeDuplica(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	transferencia := pgtype.UUID{Bytes: uuid.New(), Valid: true}

	primero, err := insertarDescuadreDePrueba(t, ctx, pool, "monto_distinto", transferencia, pgtype.Text{})
	if err != nil {
		t.Fatalf("primer descuadre: %v", err)
	}
	if _, err := insertarDescuadreDePrueba(t, ctx, pool, "monto_distinto", transferencia, pgtype.Text{}); err == nil {
		t.Fatal("el segundo descuadre sin resolver del mismo objeto no falló")
	} else {
		afirmarViolacionUnica(t, err, "descuadre_identidad_unico")
	}

	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (int64, error) {
			return q.ResolverDescuadre(ctx, repositorio.ResolverDescuadreParams{
				ID:         primero.ID,
				Resolucion: pgtype.Text{String: "mando el extracto", Valid: true},
			})
		}); err != nil {
		t.Fatalf("resolver el descuadre: %v", err)
	}

	segundo, err := insertarDescuadreDePrueba(t, ctx, pool, "monto_distinto", transferencia, pgtype.Text{})
	if err != nil {
		t.Fatalf("una diferencia ya resuelta debe admitir una nueva del mismo objeto: %v", err)
	}
	if segundo.ID == primero.ID {
		t.Errorf("el segundo descuadre reutilizó el id %d", primero.ID)
	}
}

func TestDescuadreSinResolverPorReferenciaExternaNoSeDuplica(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	referencia := pgtype.Text{String: "ext-" + uuid.NewString(), Valid: true}

	if _, err := insertarDescuadreDePrueba(t, ctx, pool, "extracto_sin_libro", pgtype.UUID{}, referencia); err != nil {
		t.Fatalf("primer descuadre: %v", err)
	}
	if _, err := insertarDescuadreDePrueba(t, ctx, pool, "extracto_sin_libro", pgtype.UUID{}, referencia); err == nil {
		t.Fatal("el segundo descuadre sin resolver de la misma referencia no falló")
	} else {
		afirmarViolacionUnica(t, err, "descuadre_identidad_unico")
	}
}

func TestDescuadreExigeUnaIdentidad(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	_, err := insertarDescuadreDePrueba(t, ctx, pool, "monto_distinto", pgtype.UUID{}, pgtype.Text{})
	if err == nil {
		t.Fatal("un descuadre sin transferencia ni referencia externa entró")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.CheckViolation {
		t.Fatalf("error = %v, want SQLSTATE %s", err, pgerrcode.CheckViolation)
	}
	if pgErr.ConstraintName != "descuadre_identidad_check" {
		t.Fatalf("constraint = %q, want %q", pgErr.ConstraintName, "descuadre_identidad_check")
	}
}

func TestIndicesDeLaConciliacionExisten(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	esperados := []string{
		"journal_entry_ocurrido_en",
		"journal_entry_transferencia_id",
		"posting_por_asiento",
		"transferencia_activas_por_actualizada",
		"descuadre_identidad_unico",
	}

	var encontrados []string
	if err := pool.QueryRow(ctx,
		`SELECT array_agg(indexname) FROM pg_indexes WHERE schemaname = 'nucleo' AND indexname = ANY($1)`,
		esperados).Scan(&encontrados); err != nil {
		t.Fatalf("leer pg_indexes: %v", err)
	}
	if len(encontrados) != len(esperados) {
		t.Fatalf("índices encontrados = %v, want los %d del diseño", encontrados, len(esperados))
	}
}

func TestCursorDeConciliacionGuardaElFinalDeLaVentana(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	primera := ventanaPrueba
	segunda := ventanaPrueba.Add(time.Minute)

	for _, hasta := range []time.Time{primera, segunda} {
		if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
			func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (repositorio.ConciliacionCorrida, error) {
				return q.InsertarCorridaConciliacion(ctx, pgtype.Timestamptz{Time: hasta, Valid: true})
			}); err != nil {
			t.Fatalf("insertar corrida: %v", err)
		}
	}

	var ultima pgtype.Timestamptz
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			leida, err := q.UltimaVentanaConciliada(ctx)
			ultima = leida
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("leer el cursor: %v", err)
	}
	if !ultima.Time.Equal(segunda) {
		t.Errorf("última ventana = %v, want %v", ultima.Time, segunda)
	}
}

func sembrarCuentasDeConciliacion(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	sufijo := uuid.NewString()
	activo := "activo:custodia:" + sufijo
	pasivo := "pasivo:usuario:" + sufijo + ":disponible"
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx,
				"INSERT INTO cuenta (id, naturaleza, clase, titular_id) VALUES ($1, 'activo', 'custodia', NULL), ($2, 'pasivo', 'usuario', $3)",
				activo, pasivo, sufijo)
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("sembrar cuentas: %v", err)
	}
	return activo, pasivo
}

func sembrarAsientoDeConciliacion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, debito, credito string, ocurridoEn time.Time) int64 {
	t.Helper()
	id, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (int64, error) {
			asiento, err := q.InsertarAsiento(ctx, repositorio.InsertarAsientoParams{
				Tipo:       "prueba_conciliacion",
				OcurridoEn: pgtype.Timestamptz{Time: ocurridoEn, Valid: true},
			})
			if err != nil {
				return 0, err
			}
			if _, err := q.InsertarLinea(ctx, repositorio.InsertarLineaParams{EntryID: asiento.ID, CuentaID: debito, Monto: 100, Moneda: "COP"}); err != nil {
				return 0, err
			}
			if _, err := q.InsertarLinea(ctx, repositorio.InsertarLineaParams{EntryID: asiento.ID, CuentaID: credito, Monto: -100, Moneda: "COP"}); err != nil {
				return 0, err
			}
			return asiento.ID, nil
		})
	if err != nil {
		t.Fatalf("sembrar asiento: %v", err)
	}
	return id
}

func TestLeerAsientosDeVentanaRespetaElBordeHasta(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	debito, credito := sembrarCuentasDeConciliacion(t, ctx, pool)

	base := time.Date(1999, 6, 1, 0, 0, 0, 0, time.UTC)
	dentro := sembrarAsientoDeConciliacion(t, ctx, pool, debito, credito, base)
	fuera := sembrarAsientoDeConciliacion(t, ctx, pool, debito, credito, base.Add(time.Hour))

	filas := leerVentanaDeAsientos(t, ctx, pool, base, base.Add(time.Hour))
	ids := map[int64]bool{}
	for _, fila := range filas {
		ids[fila.ID] = true
	}
	if !ids[dentro] {
		t.Errorf("el asiento en el borde desde (%d) no apareció en la ventana", dentro)
	}
	if ids[fuera] {
		t.Errorf("el asiento en el borde hasta (%d) apareció: el borde debe ser exclusivo", fuera)
	}
}

func leerVentanaDeAsientos(t *testing.T, ctx context.Context, pool *pgxpool.Pool, desde, hasta time.Time) []repositorio.JournalEntry {
	t.Helper()
	var filas []repositorio.JournalEntry
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			leidas, err := q.LeerAsientosDeVentana(ctx, repositorio.LeerAsientosDeVentanaParams{
				Desde: pgtype.Timestamptz{Time: desde, Valid: true},
				Hasta: pgtype.Timestamptz{Time: hasta, Valid: true},
			})
			filas = leidas
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("leer la ventana: %v", err)
	}
	return filas
}

func TestLeerTransferenciasActivasDeVentanaSoloDevuelveLasActivas(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	debito, _ := sembrarCuentasDeConciliacion(t, ctx, pool)
	base := time.Date(1999, 7, 1, 0, 0, 0, 0, time.UTC)

	activa := sembrarTransferenciaDeConciliacion(t, ctx, pool, debito, "despachada", base)
	confirmada := sembrarTransferenciaDeConciliacion(t, ctx, pool, debito, "confirmada", base.Add(time.Minute))
	fuera := sembrarTransferenciaDeConciliacion(t, ctx, pool, debito, "despachada", base.Add(time.Hour))

	filas := leerTransferenciasActivas(t, ctx, pool, base, base.Add(time.Hour))
	encontradas := map[[16]byte]bool{}
	for _, fila := range filas {
		encontradas[fila.ID.Bytes] = true
	}
	if !encontradas[activa.Bytes] {
		t.Errorf("la transferencia despachada en la ventana (%s) no apareció", activa)
	}
	if encontradas[confirmada.Bytes] {
		t.Errorf("la transferencia confirmada (%s) apareció: no es estado activo", confirmada)
	}
	if encontradas[fuera.Bytes] {
		t.Errorf("la transferencia fuera de la ventana (%s) apareció", fuera)
	}
}

func leerTransferenciasActivas(t *testing.T, ctx context.Context, pool *pgxpool.Pool, desde, hasta time.Time) []repositorio.LeerTransferenciasActivasDeVentanaRow {
	t.Helper()
	var filas []repositorio.LeerTransferenciasActivasDeVentanaRow
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			leidas, err := q.LeerTransferenciasActivasDeVentana(ctx, repositorio.LeerTransferenciasActivasDeVentanaParams{
				Desde: pgtype.Timestamptz{Time: desde, Valid: true},
				Hasta: pgtype.Timestamptz{Time: hasta, Valid: true},
			})
			filas = leidas
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("leer transferencias activas: %v", err)
	}
	return filas
}

func sembrarTransferenciaDeConciliacion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cuentaOrigen, estado string, actualizadaEn time.Time) pgtype.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx,
				`INSERT INTO transferencia (id, usuario_id, cuenta_origen, llave_destino, monto, moneda, idempotency_key, estado, actualizada_en)
				 VALUES ($1, $2, $3, 'llave-prueba', 1000, 'COP', $4, $5, $6)`,
				id, uuid.NewString(), cuentaOrigen, uuid.NewString(), estado, actualizadaEn)
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("sembrar transferencia: %v", err)
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}

// abridorDeConciliacion cuenta los Begin para probar el orden de las lecturas externas.
type abridorDeConciliacion struct {
	interno abridorDeTransacciones
	begins  int
}

func (a *abridorDeConciliacion) Begin(ctx context.Context) (transaccionDeNucleo, error) {
	a.begins++
	return a.interno.Begin(ctx)
}

// rielFalso es el extracto del riel controlado por la prueba.
type rielFalso struct {
	extracto       ExtractoRiel
	err            error
	llamadas       int
	desde, hasta   time.Time
	abridor        *abridorDeConciliacion
	beginsAlLlamar int
}

func (r *rielFalso) Extracto(_ context.Context, desde, hasta time.Time) (ExtractoRiel, error) {
	r.llamadas++
	r.desde, r.hasta = desde, hasta
	if r.abridor != nil {
		r.beginsAlLlamar = r.abridor.begins
	}
	return r.extracto, r.err
}

// custodioFalso es el extracto del custodio controlado por la prueba.
type custodioFalso struct {
	extracto       ExtractoCustodio
	err            error
	llamadas       int
	corte          time.Time
	abridor        *abridorDeConciliacion
	beginsAlLlamar int
}

func (c *custodioFalso) Extracto(_ context.Context, corteEn time.Time) (ExtractoCustodio, error) {
	c.llamadas++
	c.corte = corteEn
	if c.abridor != nil {
		c.beginsAlLlamar = c.abridor.begins
	}
	return c.extracto, c.err
}

// detectorEspia captura la ventana que el enganche recibe y devuelve lo pedido.
type detectorEspia struct {
	llamadas int
	ventana  ventanaConciliacion
	devuelve int
	err      error
}

// detectarFn ata el espía al tipo del punto de enganche.
func (d *detectorEspia) detectarFn() detectorDeVentana {
	return func(_ context.Context, _ *repositorio.Queries, ventana ventanaConciliacion) (int, error) {
		d.llamadas++
		d.ventana = ventana
		return d.devuelve, d.err
	}
}

// servicioDeConciliacion arma el Servicio con fakes y el reloj pedido.
func servicioDeConciliacion(pool *pgxpool.Pool, reloj func() time.Time, riel LectorExtractoRiel, custodio LectorExtractoCustodio, espia *detectorEspia) *Servicio {
	return &Servicio{
		pool:       poolDeNucleo{pool: pool},
		custodio:   custodio,
		lectorRiel: riel,
		detectar:   espia.detectarFn(),
		ahora:      reloj,
	}
}

// limpiarCursor borra el cursor con el rol dueño para probar la primera corrida.
func limpiarCursor(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL no definida: limpiar el cursor exige el rol dueño")
	}
	dueno, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("abrir el pool dueño: %v", err)
	}
	t.Cleanup(dueno.Close)
	if _, err := dueno.Exec(t.Context(), "DELETE FROM nucleo.conciliacion_corrida"); err != nil {
		t.Fatalf("limpiar el cursor: %v", err)
	}
}

// fijarCursor deja el cursor de la tabla en el final de ventana pedido.
func fijarCursor(t *testing.T, pool *pgxpool.Pool, hasta time.Time) {
	t.Helper()

	_, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, "INSERT INTO conciliacion_corrida (ventana_hasta) VALUES ($1)", hasta)
			return struct{}{}, err
		})
	if err != nil {
		t.Fatalf("fijar el cursor: %v", err)
	}
}

// ultimaVentana lee el ventana_hasta de la última corrida.
func ultimaVentana(t *testing.T, pool *pgxpool.Pool) time.Time {
	t.Helper()

	var hasta time.Time
	_, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			v, err := q.UltimaVentanaConciliada(ctx)
			if err != nil {
				return struct{}{}, err
			}
			hasta = v.Time.UTC()
			return struct{}{}, nil
		})
	if err != nil {
		t.Fatalf("leer el cursor: %v", err)
	}
	return hasta
}

// contarCorridas cuenta las filas del cursor.
func contarCorridas(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()

	var filas int64
	_, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx, "SELECT count(*) FROM conciliacion_corrida").Scan(&filas)
		})
	if err != nil {
		t.Fatalf("contar corridas: %v", err)
	}
	return filas
}

// idsDeAsientos devuelve el conjunto de identificadores de la ventana leída.
func idsDeAsientos(asientos []repositorio.JournalEntry) map[int64]bool {
	ids := make(map[int64]bool, len(asientos))
	for _, asiento := range asientos {
		ids[asiento.ID] = true
	}
	return ids
}

// HU-NF-08, criterios 1 y 2.1: la ventana toma lo de tiempo de evento en [desde, hasta) y deja fuera lo reciente.
func TestConciliarSeleccionaPorTiempoDeEventoYDejaFueraLoReciente(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	debito, credito := sembrarCuentasDeConciliacion(t, ctx, pool)

	desde := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	hasta := desde.Add(5 * time.Minute)
	fijarCursor(t, pool, desde)

	dentro := sembrarAsientoDeConciliacion(t, ctx, pool, debito, credito, desde.Add(time.Minute))
	enBorde := sembrarAsientoDeConciliacion(t, ctx, pool, debito, credito, hasta)
	antes := sembrarAsientoDeConciliacion(t, ctx, pool, debito, credito, desde.Add(-time.Second))
	despues := sembrarAsientoDeConciliacion(t, ctx, pool, debito, credito, hasta.Add(time.Second))

	espia := &detectorEspia{}
	servicio := servicioDeConciliacion(pool, func() time.Time { return hasta.Add(5 * time.Minute) },
		&rielFalso{}, &custodioFalso{}, espia)

	if _, err := servicio.Conciliar(ctx, 5*time.Minute); err != nil {
		t.Fatalf("Conciliar: %v", err)
	}

	ids := idsDeAsientos(espia.ventana.asientos)
	if !ids[dentro] {
		t.Error("el asiento dentro de la ventana no fue seleccionado")
	}
	if ids[enBorde] {
		t.Error("el asiento en el borde hasta entró: el borde es exclusivo")
	}
	if ids[antes] {
		t.Error("el asiento anterior al cursor entró en la ventana")
	}
	if ids[despues] {
		t.Error("el asiento más reciente que N entró en la ventana")
	}
}

// HU-NF-08, criterio 2.2: las ventanas son contiguas y sin huecos aunque se salte una corrida.
func TestConciliarEsContiguoYNoDejaHuecosAunqueSeSalteUnaCorrida(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	limpiarCursor(t, pool)

	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	const antiguedad = 5 * time.Minute
	reloj := base
	riel := &rielFalso{}
	espia := &detectorEspia{}
	servicio := servicioDeConciliacion(pool, func() time.Time { return reloj }, riel, &custodioFalso{}, espia)

	// Primera corrida: sin cursor, la ventana es vacía y solo siembra el cursor.
	if n, err := servicio.Conciliar(ctx, antiguedad); err != nil || n != 0 {
		t.Fatalf("primera corrida = (%d, %v), quiero (0, nil)", n, err)
	}
	if espia.llamadas != 0 || riel.llamadas != 0 {
		t.Fatal("la primera corrida no debe leer extractos ni detectar diferencias")
	}
	if got := ultimaVentana(t, pool); !got.Equal(base.Add(-antiguedad)) {
		t.Errorf("cursor sembrado = %v, quiero %v", got, base.Add(-antiguedad))
	}

	// Segunda corrida: arranca en el final de la anterior y avanza el cursor.
	reloj = base.Add(60 * time.Second)
	if _, err := servicio.Conciliar(ctx, antiguedad); err != nil {
		t.Fatalf("segunda corrida: %v", err)
	}
	if espia.llamadas != 1 || riel.llamadas != 1 {
		t.Fatalf("la segunda corrida debe leer extractos y detectar una vez (riel=%d, detector=%d)", riel.llamadas, espia.llamadas)
	}
	if !espia.ventana.desde.Equal(base.Add(-antiguedad)) {
		t.Errorf("desde de la segunda = %v, quiero %v", espia.ventana.desde, base.Add(-antiguedad))
	}
	cursorSegundo := ultimaVentana(t, pool)
	if !cursorSegundo.Equal(espia.ventana.hasta) {
		t.Errorf("cursor = %v, quiero el ventana_hasta %v", cursorSegundo, espia.ventana.hasta)
	}

	// Tercera corrida: se salta una; la siguiente arranca en el mismo cursor, sin hueco ni solape.
	reloj = base.Add(180 * time.Second)
	if _, err := servicio.Conciliar(ctx, antiguedad); err != nil {
		t.Fatalf("tercera corrida: %v", err)
	}
	if !espia.ventana.desde.Equal(cursorSegundo) {
		t.Errorf("desde tras saltar una corrida = %v, quiero %v (sin hueco)", espia.ventana.desde, cursorSegundo)
	}
	if !espia.ventana.hasta.Equal(base.Add(180 * time.Second).Add(-antiguedad)) {
		t.Errorf("hasta de la tercera = %v, quiero %v", espia.ventana.hasta, base.Add(180*time.Second).Add(-antiguedad))
	}
}

// HU-NF-08, criterio 3: una confirmación atrasada se ubica por su tiempo de evento y no por el de llegada.
func TestConciliarUbicaLaConfirmacionAtrasadaEnSuVentana(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	debito, credito := sembrarCuentasDeConciliacion(t, ctx, pool)

	desde := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	hasta := desde.Add(5 * time.Minute)
	fijarCursor(t, pool, desde)

	tardia := sembrarAsientoDeConciliacion(t, ctx, pool, debito, credito, desde.Add(time.Minute))
	siguiente := sembrarAsientoDeConciliacion(t, ctx, pool, debito, credito, hasta.Add(time.Minute))

	espia := &detectorEspia{}
	servicio := servicioDeConciliacion(pool, func() time.Time { return hasta.Add(5 * time.Minute) },
		&rielFalso{}, &custodioFalso{}, espia)

	if _, err := servicio.Conciliar(ctx, 5*time.Minute); err != nil {
		t.Fatalf("Conciliar: %v", err)
	}

	ids := idsDeAsientos(espia.ventana.asientos)
	if !ids[tardia] {
		t.Error("la confirmación atrasada no quedó en su ventana de ocurrencia")
	}
	if ids[siguiente] {
		t.Error("un asiento de la ventana siguiente entró en la ventana")
	}
	for _, asiento := range espia.ventana.asientos {
		if asiento.ID != tardia {
			continue
		}
		if !asiento.OcurridoEn.Time.UTC().Before(hasta) {
			t.Error("ocurrido_en debía quedar dentro de la ventana")
		}
		if !asiento.RegistradoEn.Time.UTC().After(hasta) {
			t.Error("registrado_en debía ser posterior a la ventana: la prueba no demuestra selección por evento")
		}
	}
}

// HU-NF-08, criterio 17: sin alguno de los extractos la ventana no se concilia y el cursor no avanza.
func TestConciliarNoAvanzaElCursorSiFaltaUnExtracto(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	desde := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	hasta := desde.Add(5 * time.Minute)
	fijarCursor(t, pool, desde)
	antes := contarCorridas(t, pool)

	casos := map[string]struct{ rielErr, custodioErr error }{
		"sin extracto del riel":     {rielErr: errors.New("riel caído")},
		"sin extracto del custodio": {custodioErr: errors.New("custodio caído")},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			espia := &detectorEspia{}
			servicio := servicioDeConciliacion(pool, func() time.Time { return hasta.Add(5 * time.Minute) },
				&rielFalso{err: caso.rielErr}, &custodioFalso{err: caso.custodioErr}, espia)

			if _, err := servicio.Conciliar(ctx, 5*time.Minute); err == nil {
				t.Fatal("Conciliar con un extracto caído = nil, quiero error para reintentar")
			}
			if espia.llamadas != 0 {
				t.Error("no debe detectar diferencias sin extractos")
			}
			if despues := contarCorridas(t, pool); despues != antes {
				t.Errorf("corridas = %d, quiero %d: la ventana quedó sin conciliar", despues, antes)
			}
		})
	}
}

// HU-NF-08, criterio 1: los dos extractos se leen antes de abrir la transacción de escritura.
func TestConciliarLeeLosExtractosAntesDeLaTransaccionDeEscritura(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	desde := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	hasta := desde.Add(5 * time.Minute)
	fijarCursor(t, pool, desde)

	abridor := &abridorDeConciliacion{interno: poolDeNucleo{pool: pool}}
	riel := &rielFalso{abridor: abridor}
	custodio := &custodioFalso{abridor: abridor}
	espia := &detectorEspia{}
	servicio := &Servicio{
		pool:       abridor,
		custodio:   custodio,
		lectorRiel: riel,
		detectar:   espia.detectarFn(),
		ahora:      func() time.Time { return hasta.Add(5 * time.Minute) },
	}

	if _, err := servicio.Conciliar(ctx, 5*time.Minute); err != nil {
		t.Fatalf("Conciliar: %v", err)
	}
	// El cursor se leyó en el primer Begin; los extractos deben llegar antes del segundo.
	if riel.beginsAlLlamar != 1 {
		t.Errorf("el extracto del riel se leyó tras %d Begin, quiero 1 (solo el cursor)", riel.beginsAlLlamar)
	}
	if custodio.beginsAlLlamar != 1 {
		t.Errorf("el extracto del custodio se leyó tras %d Begin, quiero 1 (solo el cursor)", custodio.beginsAlLlamar)
	}
	if abridor.begins != 2 {
		t.Errorf("Begin = %d, quiero 2 (lectura del cursor y escritura de la corrida)", abridor.begins)
	}
	if espia.llamadas != 1 {
		t.Errorf("el enganche se llamó %d veces, quiero 1", espia.llamadas)
	}
}

// HU-NF-08, criterio 1: las diferencias que el enganche registre quedan selladas con la ventana de la corrida.
func TestConciliarEntregaLaVentanaAlEngancheYDevuelveLasDiferencias(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	desde := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	hasta := desde.Add(5 * time.Minute)
	fijarCursor(t, pool, desde)

	espia := &detectorEspia{devuelve: 3}
	servicio := servicioDeConciliacion(pool, func() time.Time { return hasta.Add(5 * time.Minute) },
		&rielFalso{}, &custodioFalso{}, espia)

	n, err := servicio.Conciliar(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("Conciliar: %v", err)
	}
	if n != 3 {
		t.Errorf("diferencias = %d, quiero las 3 del enganche", n)
	}
	if !espia.ventana.desde.Equal(desde) || !espia.ventana.hasta.Equal(hasta) {
		t.Errorf("ventana del enganche = [%v, %v), quiero [%v, %v)", espia.ventana.desde, espia.ventana.hasta, desde, hasta)
	}
}
