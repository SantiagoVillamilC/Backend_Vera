//go:build integration

package nucleo

import (
	"context"
	"errors"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
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
