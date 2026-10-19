//go:build integration

package nucleo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func transferenciaDePrueba(usuarioID, cuentaOrigen, clave string) repositorio.InsertarTransferenciaParams {
	return repositorio.InsertarTransferenciaParams{
		ID:             pgtype.UUID{Bytes: uuid.New(), Valid: true},
		UsuarioID:      usuarioID,
		CuentaOrigen:   cuentaOrigen,
		LlaveDestino:   "llave-" + uuid.NewString(),
		Monto:          1000000,
		Moneda:         "COP",
		IdempotencyKey: clave,
		Estado:         EstadoTransferenciaCreada,
	}
}

func enTransaccionDeTransferencia(t *testing.T, pool *pgxpool.Pool, fn func(context.Context, *repositorio.Queries) error) error {
	t.Helper()

	_, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			return struct{}{}, fn(ctx, q)
		})
	return err
}

func subcuentaDisponibleDePrueba(t *testing.T, ctx context.Context, s *Servicio, usuarioID string) string {
	t.Helper()

	if _, err := s.AbrirSubcuentasDeUsuario(ctx, usuarioID); err != nil {
		t.Fatalf("abrir subcuentas de %s: %v", usuarioID, err)
	}
	return NombreCuentaDisponible(usuarioID)
}

func contarTransferenciasDeUsuario(t *testing.T, pool *pgxpool.Pool, usuarioID string) int {
	t.Helper()

	var filas int
	if _, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				"SELECT count(*) FROM nucleo.transferencia WHERE usuario_id = $1", usuarioID).Scan(&filas)
		}); err != nil {
		t.Fatalf("contar transferencias de %s: %v", usuarioID, err)
	}
	return filas
}

// Capítulo 9: la restricción única sobre (usuario_id, idempotency_key) es la que decide, no un SELECT previo.
func TestClaveDeIdempotenciaDeTransferenciaUnicaPorUsuario(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	s := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	cuenta := subcuentaDisponibleDePrueba(t, ctx, s, usuario)
	otroUno := uuid.NewString()
	otraCuentaUno := subcuentaDisponibleDePrueba(t, ctx, s, otroUno)
	otroDos := uuid.NewString()
	otraCuentaDos := subcuentaDisponibleDePrueba(t, ctx, s, otroDos)
	clave := "k-" + uuid.NewString()

	err := enTransaccionDeTransferencia(t, pool, func(ctx context.Context, q *repositorio.Queries) error {
		if _, err := q.InsertarTransferencia(ctx, transferenciaDePrueba(usuario, cuenta, clave)); err != nil {
			return err
		}
		_, err := q.InsertarTransferencia(ctx, transferenciaDePrueba(usuario, cuenta, clave))
		return err
	})
	requerirUnicidad(t, err, "transferencia_idempotencia_unica")

	if err := enTransaccionDeTransferencia(t, pool, func(ctx context.Context, q *repositorio.Queries) error {
		if _, err := q.InsertarTransferencia(ctx, transferenciaDePrueba(otroUno, otraCuentaUno, clave)); err != nil {
			return err
		}
		_, err := q.InsertarTransferencia(ctx, transferenciaDePrueba(otroDos, otraCuentaDos, clave))
		return err
	}); err != nil {
		t.Fatalf("dos usuarios distintos con la misma clave: %v", err)
	}
}

// Capítulo 9: el reintento del mismo usuario con la misma llave devuelve la transferencia original, sin insertar otra.
func TestCrearTransferenciaDevuelveLaGuardadaAlReintentar(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	s := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	cuenta := subcuentaDisponibleDePrueba(t, ctx, s, usuario)
	clave := "k-" + uuid.NewString()

	primera, creada, err := s.CrearTransferencia(ctx, usuario, cuenta, "llave-beto", Centavos(1000000), "COP", clave)
	if err != nil || !creada {
		t.Fatalf("primer intento: creada = %v, err = %v; quiero true, nil", creada, err)
	}

	segunda, creada, err := s.CrearTransferencia(ctx, usuario, cuenta, "llave-beto", Centavos(1000000), "COP", clave)
	if err != nil {
		t.Fatalf("segundo intento: %v", err)
	}
	if creada {
		t.Error("segundo intento: creada = true, quiero false")
	}
	if segunda.ID != primera.ID {
		t.Errorf("segundo intento devolvió %s, quiero la original %s", segunda.ID, primera.ID)
	}
	if segunda.Estado != EstadoTransferenciaCreada {
		t.Errorf("estado devuelto = %q, quiero %q", segunda.Estado, EstadoTransferenciaCreada)
	}

	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			var filas int
			if err := tx.QueryRow(ctx, "SELECT count(*) FROM nucleo.transferencia WHERE usuario_id = $1", usuario).Scan(&filas); err != nil {
				return struct{}{}, err
			}
			if filas != 1 {
				t.Errorf("filas de %s = %d, quiero 1 sin duplicado", usuario, filas)
			}
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("contar transferencias: %v", err)
	}
	t.Logf("reintento con la misma llave devolvió %s; una sola fila", primera.ID)
}

// NF-A2-01: la cuenta origen tiene que ser del usuario; la de otro titular se rechaza sin insertar.
func TestCrearTransferenciaRechazaCuentaOrigenAjena(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	s := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	subcuentaDisponibleDePrueba(t, ctx, s, usuario)
	otro := uuid.NewString()
	cuentaAjena := subcuentaDisponibleDePrueba(t, ctx, s, otro)

	_, _, err := s.CrearTransferencia(ctx, usuario, cuentaAjena, "llave-beto", Centavos(1000000), "COP", "k-"+uuid.NewString())
	if !errors.Is(err, ErrCuentaOrigenAjena) {
		t.Fatalf("cuenta ajena: err = %v, quiero ErrCuentaOrigenAjena", err)
	}
	if filas := contarTransferenciasDeUsuario(t, pool, usuario); filas != 0 {
		t.Errorf("filas de %s tras la cuenta ajena = %d, quiero 0", usuario, filas)
	}

	propia := subcuentaDisponibleDePrueba(t, ctx, s, usuario)
	if _, creada, err := s.CrearTransferencia(ctx, usuario, propia, "llave-beto", Centavos(1000000), "COP", "k-"+uuid.NewString()); err != nil || !creada {
		t.Fatalf("cuenta propia: creada = %v, err = %v; quiero true, nil", creada, err)
	}
	if filas := contarTransferenciasDeUsuario(t, pool, usuario); filas != 1 {
		t.Errorf("filas de %s = %d tras la cuenta propia, quiero 1", usuario, filas)
	}
	t.Logf("cuenta ajena rechazada con %v y cero filas; la cuenta propia insertó una fila", ErrCuentaOrigenAjena)
}

// NF-A2-01: la misma llave con otro cuerpo se rechaza; con el mismo cuerpo devuelve la guardada.
func TestCrearTransferenciaConLlaveReutilizadaYOtroCuerpo(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	s := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	cuenta := subcuentaDisponibleDePrueba(t, ctx, s, usuario)
	clave := "k-" + uuid.NewString()

	original, creada, err := s.CrearTransferencia(ctx, usuario, cuenta, "llave-beto", Centavos(1000000), "COP", clave)
	if err != nil || !creada {
		t.Fatalf("primer intento: creada = %v, err = %v; quiero true, nil", creada, err)
	}

	misma, creada, err := s.CrearTransferencia(ctx, usuario, cuenta, "llave-beto", Centavos(1000000), "COP", clave)
	if err != nil || creada {
		t.Fatalf("mismo cuerpo: creada = %v, err = %v; quiero false, nil", creada, err)
	}
	if misma.ID != original.ID {
		t.Errorf("mismo cuerpo devolvió %s, quiero la original %s", misma.ID, original.ID)
	}
	if filas := contarTransferenciasDeUsuario(t, pool, usuario); filas != 1 {
		t.Errorf("filas tras el mismo cuerpo = %d, quiero 1", filas)
	}

	if _, _, err := s.CrearTransferencia(ctx, usuario, cuenta, "llave-beto", Centavos(2000000), "COP", clave); !errors.Is(err, ErrIdempotenciaEnConflicto) {
		t.Fatalf("otro monto: err = %v, quiero ErrIdempotenciaEnConflicto", err)
	}

	actual, err := s.ConsultarTransferencia(ctx, usuario, original.ID)
	if err != nil {
		t.Fatalf("consultar la original: %v", err)
	}
	if actual.ID != original.ID || actual.Monto != Centavos(1000000) {
		t.Errorf("la fila cambió: id = %s monto = %d, quiero %s y 1000000", actual.ID, actual.Monto, original.ID)
	}
	if filas := contarTransferenciasDeUsuario(t, pool, usuario); filas != 1 {
		t.Errorf("filas tras el conflicto = %d, quiero 1", filas)
	}
	t.Logf("mismo cuerpo devolvió %s; otro monto devolvió %v sin alterar la fila", original.ID, ErrIdempotenciaEnConflicto)
}

// Capítulo 8: las transiciones válidas avanzan por compare-and-set y las inválidas no mueven ninguna fila.
func TestCompareAndSetDeLaMaquinaDeEstadosDeTransferencia(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	s := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	cuenta := subcuentaDisponibleDePrueba(t, ctx, s, usuario)

	crear := func() Transferencia {
		t.Helper()
		tr, creada, err := s.CrearTransferencia(ctx, usuario, cuenta, "llave-"+uuid.NewString(),
			Centavos(1000000), "COP", "k-"+uuid.NewString())
		if err != nil || !creada {
			t.Fatalf("crear transferencia: creada = %v, err = %v", creada, err)
		}
		return tr
	}
	avanzar := func(id uuid.UUID, esperado, nuevo string) {
		t.Helper()
		tr, err := s.avanzarEstadoTransferencia(ctx, id, esperado, nuevo)
		if err != nil {
			t.Fatalf("avanzar %s de %q a %q: %v", id, esperado, nuevo, err)
		}
		if tr.Estado != nuevo {
			t.Errorf("estado = %q, quiero %q", tr.Estado, nuevo)
		}
	}
	rechazar := func(id uuid.UUID, esperado, nuevo string) {
		t.Helper()
		if _, err := s.avanzarEstadoTransferencia(ctx, id, esperado, nuevo); !errors.Is(err, ErrTransicionNoAplicada) {
			t.Errorf("%q -> %q: err = %v, quiero ErrTransicionNoAplicada", esperado, nuevo, err)
		}
	}

	confirmada := crear()
	avanzar(confirmada.ID, EstadoTransferenciaCreada, EstadoTransferenciaReservada)
	avanzar(confirmada.ID, EstadoTransferenciaReservada, EstadoTransferenciaDespachada)
	avanzar(confirmada.ID, EstadoTransferenciaDespachada, EstadoTransferenciaConfirmada)

	fallida := crear()
	avanzar(fallida.ID, EstadoTransferenciaCreada, EstadoTransferenciaReservada)
	avanzar(fallida.ID, EstadoTransferenciaReservada, EstadoTransferenciaDespachada)
	avanzar(fallida.ID, EstadoTransferenciaDespachada, EstadoTransferenciaFallida)
	avanzar(fallida.ID, EstadoTransferenciaFallida, EstadoTransferenciaCompensada)

	indeterminadaConfirmada := crear()
	avanzar(indeterminadaConfirmada.ID, EstadoTransferenciaCreada, EstadoTransferenciaReservada)
	avanzar(indeterminadaConfirmada.ID, EstadoTransferenciaReservada, EstadoTransferenciaDespachada)
	avanzar(indeterminadaConfirmada.ID, EstadoTransferenciaDespachada, EstadoTransferenciaIndeterminada)
	avanzar(indeterminadaConfirmada.ID, EstadoTransferenciaIndeterminada, EstadoTransferenciaConfirmada)

	indeterminadaCompensada := crear()
	avanzar(indeterminadaCompensada.ID, EstadoTransferenciaCreada, EstadoTransferenciaReservada)
	avanzar(indeterminadaCompensada.ID, EstadoTransferenciaReservada, EstadoTransferenciaDespachada)
	avanzar(indeterminadaCompensada.ID, EstadoTransferenciaDespachada, EstadoTransferenciaIndeterminada)
	avanzar(indeterminadaCompensada.ID, EstadoTransferenciaIndeterminada, EstadoTransferenciaCompensada)

	rechazar(confirmada.ID, EstadoTransferenciaConfirmada, EstadoTransferenciaReservada)

	sinReservar := crear()
	rechazar(sinReservar.ID, EstadoTransferenciaCreada, EstadoTransferenciaDespachada)
	rechazar(sinReservar.ID, EstadoTransferenciaDespachada, EstadoTransferenciaConfirmada)

	actual, err := s.ConsultarTransferencia(ctx, usuario, confirmada.ID)
	if err != nil {
		t.Fatalf("consultar la confirmada: %v", err)
	}
	if actual.Estado != EstadoTransferenciaConfirmada {
		t.Errorf("estado tras las transiciones inválidas = %q, quiero %q", actual.Estado, EstadoTransferenciaConfirmada)
	}
	t.Logf("transiciones válidas avanzaron; las inválidas no movieron filas")
}

func TestMontoDeTransferenciaEsBigint(t *testing.T) {
	pool := abrirPoolDePrueba(t)

	if _, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			var tipo string
			if err := tx.QueryRow(ctx, `SELECT data_type FROM information_schema.columns
				WHERE table_schema = 'nucleo' AND table_name = 'transferencia' AND column_name = 'monto'`).Scan(&tipo); err != nil {
				return struct{}{}, err
			}
			if tipo != "bigint" {
				t.Errorf("transferencia.monto data_type = %q, quiero bigint", tipo)
			}

			var flotantes int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
				WHERE table_schema = 'nucleo' AND table_name = 'transferencia'
				  AND data_type IN ('numeric', 'double precision', 'real')`).Scan(&flotantes); err != nil {
				return struct{}{}, err
			}
			if flotantes != 0 {
				t.Errorf("transferencia tiene %d columnas numeric o flotantes, quiero 0", flotantes)
			}
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("information_schema: %v", err)
	}
}

func TestRolNucleoNoPuedeReescribirElMontoDeLaTransferencia(t *testing.T) {
	pool := abrirPoolDePrueba(t)

	_, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, "UPDATE transferencia SET monto = 1")
			return struct{}{}, err
		})
	if err == nil {
		t.Fatal("rol_nucleo pudo reescribir transferencia.monto")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.InsufficientPrivilege {
		t.Fatalf("error = %v, quiero SQLSTATE %s", err, pgerrcode.InsufficientPrivilege)
	}
	t.Logf("UPDATE monto rechazado: %v", err)
}

// resolutorDePrueba resuelve la llave de destino contra un mapa en memoria.
type resolutorDePrueba map[string]string

func (r resolutorDePrueba) UsuarioIDDeDestino(_ context.Context, llave string) (string, bool, error) {
	id, ok := r[llave]
	return id, ok, nil
}

// errFallaInyectada marca la línea de la transferencia interna que la prueba obliga a fallar.
var errFallaInyectada = errors.New("prueba: falla inyectada entre los dos asientos")

// abridorConFalla envuelve el pool y devuelve transacciones que fallan al escribir en una cuenta concreta.
type abridorConFalla struct {
	real           abridorDeTransacciones
	cuentaQueFalla string
}

func (a abridorConFalla) Begin(ctx context.Context) (transaccionDeNucleo, error) {
	tx, err := a.real.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return transaccionConFalla{transaccionDeNucleo: tx, cuentaQueFalla: a.cuentaQueFalla}, nil
}

// transaccionConFalla inyecta el error solo en el INSERT de una línea de posting de la cuenta elegida.
type transaccionConFalla struct {
	transaccionDeNucleo
	cuentaQueFalla string
}

func (t transaccionConFalla) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "INSERT INTO nucleo.posting") && len(args) >= 2 {
		if cuenta, ok := args[1].(string); ok && cuenta == t.cuentaQueFalla {
			return filaSinResultado{err: errFallaInyectada}
		}
	}
	return t.transaccionDeNucleo.QueryRow(ctx, sql, args...)
}

// saldoDeCuentaDePrueba devuelve el saldo a favor de una cuenta con el signo del usuario.
func saldoDeCuentaDePrueba(t *testing.T, s *Servicio, cuenta string) Centavos {
	t.Helper()

	saldo, err := s.Saldo(t.Context(), cuenta)
	if err != nil {
		t.Fatalf("saldo de %s: %v", cuenta, err)
	}
	return saldo
}

// asientoConLineas resume un asiento del diario con su suma y su número de líneas.
type asientoConLineas struct {
	ID         int64
	Tipo       string
	OcurridoEn time.Time
	Suma       int64
	Lineas     int64
}

// asientosDeLaTransferencia lee los asientos ligados a una transferencia con su suma y su conteo de líneas.
func asientosDeLaTransferencia(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) []asientoConLineas {
	t.Helper()

	var asientos []asientoConLineas
	_, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			rows, err := tx.Query(ctx, `
				SELECT je.id, je.tipo, je.ocurrido_en, COALESCE(SUM(p.monto), 0)::bigint, count(p.id)
				  FROM nucleo.journal_entry je
				  LEFT JOIN nucleo.posting p ON p.entry_id = je.id
				 WHERE je.transferencia_id = $1
				 GROUP BY je.id, je.tipo, je.ocurrido_en
				 ORDER BY je.id`, pgtype.UUID{Bytes: id, Valid: true})
			if err != nil {
				return struct{}{}, err
			}
			defer rows.Close()
			for rows.Next() {
				var asiento asientoConLineas
				var ocurrido pgtype.Timestamptz
				if err := rows.Scan(&asiento.ID, &asiento.Tipo, &ocurrido, &asiento.Suma, &asiento.Lineas); err != nil {
					return struct{}{}, err
				}
				asiento.OcurridoEn = ocurrido.Time.UTC()
				asientos = append(asientos, asiento)
			}
			return struct{}{}, rows.Err()
		})
	if err != nil {
		t.Fatalf("leer los asientos de %s: %v", id, err)
	}
	return asientos
}

// contarAsientosYPosting cuenta las filas del diario para probar que una transacción fallida no deja rastro.
func contarAsientosYPosting(t *testing.T, pool *pgxpool.Pool) (int64, int64) {
	t.Helper()

	var asientos, posting int64
	_, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				`SELECT (SELECT count(*) FROM nucleo.journal_entry), (SELECT count(*) FROM nucleo.posting)`).
				Scan(&asientos, &posting)
		})
	if err != nil {
		t.Fatalf("contar el diario: %v", err)
	}
	return asientos, posting
}

// verificarApendiceC corre las verificaciones del Apéndice C acotadas a los asientos del envío y a sus cuentas.
func verificarApendiceC(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, cuentas []string) {
	t.Helper()

	_, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			idParam := pgtype.UUID{Bytes: id, Valid: true}
			var descuadrados, cortos, activos int64
			if err := tx.QueryRow(ctx, `
				SELECT count(*) FROM (
					SELECT entry_id FROM nucleo.posting
					 WHERE entry_id IN (SELECT id FROM nucleo.journal_entry WHERE transferencia_id = $1)
					 GROUP BY entry_id HAVING SUM(monto) <> 0) x`, idParam).Scan(&descuadrados); err != nil {
				return struct{}{}, err
			}
			if err := tx.QueryRow(ctx, `
				SELECT count(*) FROM (
					SELECT entry_id FROM nucleo.posting
					 WHERE entry_id IN (SELECT id FROM nucleo.journal_entry WHERE transferencia_id = $1)
					 GROUP BY entry_id HAVING count(*) < 2) x`, idParam).Scan(&cortos); err != nil {
				return struct{}{}, err
			}
			if err := tx.QueryRow(ctx, `
				SELECT count(*) FROM nucleo.posting p
				  JOIN nucleo.cuenta c ON c.id = p.cuenta_id
				 WHERE p.entry_id IN (SELECT id FROM nucleo.journal_entry WHERE transferencia_id = $1)
				   AND c.naturaleza = 'activo'`, idParam).Scan(&activos); err != nil {
				return struct{}{}, err
			}

			var deudores int64
			for _, cuenta := range cuentas {
				var saldo int64
				if err := tx.QueryRow(ctx,
					`SELECT COALESCE(SUM(monto), 0)::bigint FROM nucleo.posting WHERE cuenta_id = $1`,
					cuenta).Scan(&saldo); err != nil {
					return struct{}{}, err
				}
				if saldo > 0 {
					deudores++
				}
			}
			if descuadrados != 0 {
				t.Errorf("Apéndice C: %d asiento(s) descuadrado(s) en el envío, quiero 0", descuadrados)
			}
			if cortos != 0 {
				t.Errorf("Apéndice C: %d asiento(s) con menos de dos líneas, quiero 0", cortos)
			}
			if activos != 0 {
				t.Errorf("Apéndice C: el envío tocó %d cuenta(s) de activo, quiero 0", activos)
			}
			if deudores != 0 {
				t.Errorf("Apéndice C: %d subcuenta(s) de usuario quedaron en saldo deudor, quiero 0", deudores)
			}
			return struct{}{}, nil
		})
	if err != nil {
		t.Fatalf("Apéndice C: %v", err)
	}
}

// HU-NF-06 Criterios 7 y 9: el envío feliz deja los dos asientos con el mismo transferencia_id y ocurrido_en, suma cero y sin tocar el activo.
func TestHU_NF_06_Criterio7_EnvioFelizAsientaRetencionYTransferenciaInterna(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := NewServicio(pool, LectorNivelKYCFijo{}, nil, nil, nil)
	emisor := uuid.NewString()
	cuentaEmisor := subcuentaDisponibleDePrueba(t, ctx, base, emisor)
	fondearDisponibleDePrueba(t, pool, cuentaEmisor, 1000000)
	receptor := uuid.NewString()
	subcuentaDisponibleDePrueba(t, ctx, base, receptor)

	s := NewServicio(pool, LectorNivelKYCFijo{}, nil, nil, nil, ConResolutorDestino(resolutorDePrueba{"receptor": receptor}))
	transferencia, creada, err := s.Transferir(ctx, SolicitudTransferencia{
		UsuarioID:      emisor,
		CuentaOrigen:   cuentaEmisor,
		LlaveDestino:   "receptor",
		Monto:          Centavos(250000),
		Moneda:         "COP",
		IdempotencyKey: "k-" + uuid.NewString(),
	})
	if err != nil || !creada {
		t.Fatalf("envío: creada = %v, err = %v; quiero true, nil", creada, err)
	}
	if transferencia.Estado != EstadoTransferenciaConfirmada {
		t.Errorf("estado = %q, quiero %q", transferencia.Estado, EstadoTransferenciaConfirmada)
	}
	if transferencia.ActualizadaEn.Before(transferencia.CreadaEn) {
		t.Errorf("actualizada_en = %s, quiero posterior o igual a creada_en %s", transferencia.ActualizadaEn, transferencia.CreadaEn)
	}

	asientos := asientosDeLaTransferencia(t, pool, transferencia.ID)
	if len(asientos) != 2 {
		t.Fatalf("asientos = %d, quiero 2 (retención y transferencia interna)", len(asientos))
	}
	if asientos[0].Tipo != TipoAsientoRetencion || asientos[1].Tipo != TipoAsientoTransferenciaInterna {
		t.Errorf("tipos = %q, %q; quiero %q y %q", asientos[0].Tipo, asientos[1].Tipo, TipoAsientoRetencion, TipoAsientoTransferenciaInterna)
	}
	if asientos[0].Suma != 0 || asientos[1].Suma != 0 || asientos[0].Lineas < 2 || asientos[1].Lineas < 2 {
		t.Errorf("sumas y líneas = (%d,%d) y (%d,%d), quiero suma cero y al menos dos líneas en cada asiento",
			asientos[0].Suma, asientos[0].Lineas, asientos[1].Suma, asientos[1].Lineas)
	}
	if !asientos[0].OcurridoEn.Equal(asientos[1].OcurridoEn) {
		t.Errorf("ocurrido_en difiere: %s vs %s", asientos[0].OcurridoEn, asientos[1].OcurridoEn)
	}

	if got := saldoDeCuentaDePrueba(t, base, cuentaEmisor); got != 750000 {
		t.Errorf("disponible del emisor = %d, quiero 750000", got)
	}
	if got := saldoDeCuentaDePrueba(t, base, NombreCuentaRetenido(emisor)); got != 0 {
		t.Errorf("retenido del emisor = %d, quiero 0", got)
	}
	if got := saldoDeCuentaDePrueba(t, base, NombreCuentaDisponible(receptor)); got != 250000 {
		t.Errorf("disponible del receptor = %d, quiero 250000", got)
	}

	verificarApendiceC(t, pool, transferencia.ID, []string{cuentaEmisor, NombreCuentaRetenido(emisor), NombreCuentaDisponible(receptor)})
}

// HU-NF-06 Criterio 7: una falla entre los dos asientos no deja ninguno y el saldo del emisor queda intacto.
func TestHU_NF_06_Criterio7_EnvioAtomicoConFallaEntreLosDosAsientos(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := NewServicio(pool, LectorNivelKYCFijo{}, nil, nil, nil)
	emisor := uuid.NewString()
	cuentaEmisor := subcuentaDisponibleDePrueba(t, ctx, base, emisor)
	fondearDisponibleDePrueba(t, pool, cuentaEmisor, 1000000)
	receptor := uuid.NewString()
	subcuentaDisponibleDePrueba(t, ctx, base, receptor)

	asientosAntes, postingAntes := contarAsientosYPosting(t, pool)

	s := NewServicio(pool, LectorNivelKYCFijo{}, nil, nil, nil, ConResolutorDestino(resolutorDePrueba{"receptor": receptor}))
	s.pool = abridorConFalla{real: s.pool, cuentaQueFalla: NombreCuentaDisponible(receptor)}

	_, _, err := s.Transferir(ctx, SolicitudTransferencia{
		UsuarioID:      emisor,
		CuentaOrigen:   cuentaEmisor,
		LlaveDestino:   "receptor",
		Monto:          Centavos(250000),
		Moneda:         "COP",
		IdempotencyKey: "k-" + uuid.NewString(),
	})
	if !errors.Is(err, errFallaInyectada) {
		t.Fatalf("err = %v, quiero la falla inyectada", err)
	}

	if got := saldoDeCuentaDePrueba(t, base, cuentaEmisor); got != 1000000 {
		t.Errorf("disponible del emisor = %d, quiero 1000000 intacto", got)
	}
	if got := saldoDeCuentaDePrueba(t, base, NombreCuentaDisponible(receptor)); got != 0 {
		t.Errorf("disponible del receptor = %d, quiero 0", got)
	}
	asientosDespues, postingDespues := contarAsientosYPosting(t, pool)
	if asientosAntes != asientosDespues || postingAntes != postingDespues {
		t.Errorf("el diario cambió: journal_entry %d->%d, posting %d->%d", asientosAntes, asientosDespues, postingAntes, postingDespues)
	}
	if filas := contarTransferenciasDeUsuario(t, pool, emisor); filas != 0 {
		t.Errorf("transferencias del emisor = %d, quiero 0", filas)
	}
}

// HU-NF-06 Criterio 6 (aportado por NF-A2-01): el reintento con la misma llave devuelve la primera transferencia y no asienta de nuevo.
func TestHU_NF_06_ReintentoNoDuplicaElEnvio(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := NewServicio(pool, LectorNivelKYCFijo{}, nil, nil, nil)
	emisor := uuid.NewString()
	cuentaEmisor := subcuentaDisponibleDePrueba(t, ctx, base, emisor)
	fondearDisponibleDePrueba(t, pool, cuentaEmisor, 1000000)
	receptor := uuid.NewString()
	subcuentaDisponibleDePrueba(t, ctx, base, receptor)

	s := NewServicio(pool, LectorNivelKYCFijo{}, nil, nil, nil, ConResolutorDestino(resolutorDePrueba{"receptor": receptor}))
	solicitud := SolicitudTransferencia{
		UsuarioID:      emisor,
		CuentaOrigen:   cuentaEmisor,
		LlaveDestino:   "receptor",
		Monto:          Centavos(250000),
		Moneda:         "COP",
		IdempotencyKey: "k-" + uuid.NewString(),
	}

	primera, creada, err := s.Transferir(ctx, solicitud)
	if err != nil || !creada {
		t.Fatalf("primer envío: creada = %v, err = %v; quiero true, nil", creada, err)
	}
	segunda, creada, err := s.Transferir(ctx, solicitud)
	if err != nil {
		t.Fatalf("reintento: %v", err)
	}
	if creada {
		t.Error("el reintento no debe crear una segunda transferencia")
	}
	if segunda.ID != primera.ID {
		t.Errorf("reintento devolvió %s, quiero la original %s", segunda.ID, primera.ID)
	}
	if got := saldoDeCuentaDePrueba(t, base, cuentaEmisor); got != 750000 {
		t.Errorf("disponible del emisor = %d, quiero 750000 sin doble débito", got)
	}
	if got := saldoDeCuentaDePrueba(t, base, NombreCuentaDisponible(receptor)); got != 250000 {
		t.Errorf("disponible del receptor = %d, quiero 250000 sin doble crédito", got)
	}
	if filas := contarTransferenciasDeUsuario(t, pool, emisor); filas != 1 {
		t.Errorf("transferencias del emisor = %d, quiero 1", filas)
	}
	if asientos := asientosDeLaTransferencia(t, pool, primera.ID); len(asientos) != 2 {
		t.Errorf("asientos = %d, quiero 2", len(asientos))
	}
}

// HU-NF-06 Criterio 6: el reintento con la misma llave y el destino ya borrado devuelve la primera transferencia.
func TestHU_NF_06_ReintentoConDestinoInexistenteDevuelveLaOriginal(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := NewServicio(pool, LectorNivelKYCFijo{}, nil, nil, nil)
	emisor := uuid.NewString()
	cuentaEmisor := subcuentaDisponibleDePrueba(t, ctx, base, emisor)
	fondearDisponibleDePrueba(t, pool, cuentaEmisor, 1000000)
	receptor := uuid.NewString()
	subcuentaDisponibleDePrueba(t, ctx, base, receptor)

	destinos := resolutorDePrueba{"receptor": receptor}
	s := NewServicio(pool, LectorNivelKYCFijo{}, nil, nil, nil, ConResolutorDestino(destinos))
	solicitud := SolicitudTransferencia{
		UsuarioID:      emisor,
		CuentaOrigen:   cuentaEmisor,
		LlaveDestino:   "receptor",
		Monto:          Centavos(250000),
		Moneda:         "COP",
		IdempotencyKey: "k-" + uuid.NewString(),
	}

	primera, creada, err := s.Transferir(ctx, solicitud)
	if err != nil || !creada {
		t.Fatalf("primer envío: creada = %v, err = %v; quiero true, nil", creada, err)
	}

	delete(destinos, "receptor")

	segunda, creada, err := s.Transferir(ctx, solicitud)
	if err != nil {
		t.Fatalf("reintento con el destino ya inexistente: %v", err)
	}
	if creada {
		t.Error("el reintento no debe crear una segunda transferencia")
	}
	if segunda.ID != primera.ID {
		t.Errorf("reintento devolvió %s, quiero la original %s", segunda.ID, primera.ID)
	}
	if segunda.Estado != EstadoTransferenciaConfirmada {
		t.Errorf("estado devuelto = %q, quiero %q", segunda.Estado, EstadoTransferenciaConfirmada)
	}
	if got := saldoDeCuentaDePrueba(t, base, cuentaEmisor); got != 750000 {
		t.Errorf("disponible del emisor = %d, quiero 750000 sin doble débito", got)
	}
	if filas := contarTransferenciasDeUsuario(t, pool, emisor); filas != 1 {
		t.Errorf("transferencias del emisor = %d, quiero 1", filas)
	}
	if asientos := asientosDeLaTransferencia(t, pool, primera.ID); len(asientos) != 2 {
		t.Errorf("asientos = %d, quiero 2", len(asientos))
	}
	t.Logf("reintento con el destino borrado devolvió %s sin rechazo", primera.ID)
}

// HU-NF-06 Criterios 4 y 5: el destino inexistente y el propio se rechazan sin dejar transferencia.
func TestHU_NF_06_Criterios4y5_DestinoInexistenteYPropio(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := NewServicio(pool, LectorNivelKYCFijo{}, nil, nil, nil)
	emisor := uuid.NewString()
	cuentaEmisor := subcuentaDisponibleDePrueba(t, ctx, base, emisor)
	fondearDisponibleDePrueba(t, pool, cuentaEmisor, 1000000)

	casos := map[string]struct {
		resolutor ResolutorDestino
		esperado  error
	}{
		"destino inexistente": {resolutorDePrueba{}, ErrDestinoNoExiste},
		"destino propio":      {resolutorDePrueba{"yo": emisor}, ErrDestinoPropio},
	}
	for nombre, tc := range casos {
		t.Run(nombre, func(t *testing.T) {
			s := NewServicio(pool, LectorNivelKYCFijo{}, nil, nil, nil, ConResolutorDestino(tc.resolutor))
			_, _, err := s.Transferir(ctx, SolicitudTransferencia{
				UsuarioID:      emisor,
				CuentaOrigen:   cuentaEmisor,
				LlaveDestino:   "yo",
				Monto:          Centavos(1000),
				Moneda:         "COP",
				IdempotencyKey: "k-" + uuid.NewString(),
			})
			if !errors.Is(err, tc.esperado) {
				t.Fatalf("err = %v, quiero %v", err, tc.esperado)
			}
		})
	}
	if filas := contarTransferenciasDeUsuario(t, pool, emisor); filas != 0 {
		t.Errorf("transferencias del emisor = %d, quiero 0", filas)
	}
}

// TestHU_NF_06_Criterio10_TransferirSinRespuestaDeKYCNoDejaReserva cubre los tres fallos del lector de KYC: timeout,
// tipo de parte desconocido, y un nivel sin regla vigente que evaluarLimite ya rechaza. Ninguno deja fila de
// transferencia ni retiene dinero (HU-NF-06, criterio 10).
func TestHU_NF_06_Criterio10_TransferirSinRespuestaDeKYCNoDejaReserva(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := NewServicio(pool, LectorNivelKYCFijo{}, nil, nil, nil)
	emisor := uuid.NewString()
	cuentaEmisor := subcuentaDisponibleDePrueba(t, ctx, base, emisor)
	fondearDisponibleDePrueba(t, pool, cuentaEmisor, 1000000)
	receptor := uuid.NewString()
	subcuentaDisponibleDePrueba(t, ctx, base, receptor)

	casos := map[string]struct {
		nivel    string
		causa    error
		esperado error
	}{
		"timeout del lector":               {causa: context.DeadlineExceeded, esperado: context.DeadlineExceeded},
		"tipo de parte desconocido en kyc": {causa: errTipoDeParteDesconocidoSimulado, esperado: errTipoDeParteDesconocidoSimulado},
		"nivel sin regla vigente":          {nivel: "nivel_inventado_sin_regla", esperado: ErrReglaLimiteNoVigente},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			kyc := lectorNivelKYCFalso{fn: func(context.Context, string) (string, error) {
				return caso.nivel, caso.causa
			}}
			s := NewServicio(pool, kyc, nil, nil, nil, ConResolutorDestino(resolutorDePrueba{"receptor": receptor}))

			_, _, err := s.Transferir(ctx, SolicitudTransferencia{
				UsuarioID:      emisor,
				CuentaOrigen:   cuentaEmisor,
				LlaveDestino:   "receptor",
				Monto:          Centavos(1000),
				Moneda:         "COP",
				IdempotencyKey: "k-" + uuid.NewString(),
			})
			if !errors.Is(err, caso.esperado) {
				t.Fatalf("err = %v, quiero %v", err, caso.esperado)
			}
		})
	}
	if filas := contarTransferenciasDeUsuario(t, pool, emisor); filas != 0 {
		t.Errorf("transferencias del emisor = %d, quiero 0", filas)
	}
	if got := saldoDeCuentaDePrueba(t, base, cuentaEmisor); got != 1000000 {
		t.Errorf("disponible del emisor = %d, quiero 1000000 intacto", got)
	}
	if got := saldoDeCuentaDePrueba(t, base, NombreCuentaRetenido(emisor)); got != 0 {
		t.Errorf("retenido del emisor = %d, quiero 0", got)
	}
}
