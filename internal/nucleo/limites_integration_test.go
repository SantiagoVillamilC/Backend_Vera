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
	"github.com/jackc/pgx/v5/pgxpool"
)

// abrirPoolDuenoDePrueba abre el pool del rol dueño, el único que puede sembrar configuración en regla_limite y valor_uvt.
func abrirPoolDuenoDePrueba(tb testing.TB) *pgxpool.Pool {
	tb.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		tb.Skip("DATABASE_URL (rol dueño) no definida: la prueba necesita sembrar la UVT de otro año")
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		tb.Fatalf("parsear DSN del dueño: %v", err)
	}
	cfg.MaxConns = 1
	cfg.MinIdleConns = 0

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		tb.Fatalf("abrir pool del dueño: %v", err)
	}
	tb.Cleanup(pool.Close)
	return pool
}

// sembrarValorUVT siembra la UVT de un año y la borra al terminar; el rol del núcleo solo lee esa tabla.
func sembrarValorUVT(t *testing.T, pool *pgxpool.Pool, anio int, valorCentavos int64) {
	t.Helper()

	if _, err := pool.Exec(t.Context(), `INSERT INTO nucleo.valor_uvt (anio, valor_centavos, fuente)
		VALUES ($1, $2, $3)
		ON CONFLICT (anio) DO UPDATE SET valor_centavos = EXCLUDED.valor_centavos, fuente = EXCLUDED.fuente`,
		anio, valorCentavos, "prueba de integración"); err != nil {
		t.Fatalf("sembrar la UVT de %d: %v", anio, err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM nucleo.valor_uvt WHERE anio = $1`, anio); err != nil {
			t.Errorf("borrar la UVT de %d: %v", anio, err)
		}
	})
}

// insertarTransferenciaDePrueba fija creada_en y estado para armar la ventana mensual, cosa que InsertarTransferencia no permite.
func insertarTransferenciaDePrueba(t *testing.T, pool *pgxpool.Pool, usuarioID, cuentaOrigen, llave string, monto Centavos, estado string, creadaEn time.Time) {
	t.Helper()

	_, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, `INSERT INTO nucleo.transferencia
				(id, usuario_id, cuenta_origen, llave_destino, monto, moneda, idempotency_key, estado, creada_en)
				VALUES ($1, $2, $3, $4, $5, 'COP', $6, $7, $8)`,
				uuid.New(), usuarioID, cuentaOrigen, llave, monto.Int64(), "k-"+uuid.NewString(), estado, creadaEn)
			return struct{}{}, err
		})
	if err != nil {
		t.Fatalf("insertar transferencia de %s: %v", usuarioID, err)
	}
}

// ejecutarVerificarLimite evalúa el motor dentro de una transacción como lo hará el flujo del envío.
func ejecutarVerificarLimite(ctx context.Context, pool *pgxpool.Pool, usuarioID string, monto Centavos, momento time.Time) error {
	_, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			return struct{}{}, verificarLimite(ctx, q, usuarioID, NivelKYCDepositoBajoMonto, monto, momento)
		})
	return err
}

// TestVerificarLimiteSumaLaVentanaMensualDelMismoEmisor cubre el criterio 11: intraentidad y por llave del mismo emisor suman; otros usuarios, otros meses, compensadas y no reservadas no (HU-NF-06).
func TestVerificarLimiteSumaLaVentanaMensualDelMismoEmisor(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	s := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	cuenta := subcuentaDisponibleDePrueba(t, ctx, s, usuario)
	otro := uuid.NewString()
	cuentaOtro := subcuentaDisponibleDePrueba(t, ctx, s, otro)

	momento := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	mesAnterior := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

	insertarTransferenciaDePrueba(t, pool, usuario, cuenta, "intra:"+uuid.NewString(), topeCanonico2026-1500, EstadoTransferenciaReservada, momento)
	insertarTransferenciaDePrueba(t, pool, usuario, cuenta, "bre-b:"+uuid.NewString(), 1000, EstadoTransferenciaReservada, momento)
	insertarTransferenciaDePrueba(t, pool, otro, cuentaOtro, "intra:"+uuid.NewString(), topeCanonico2026, EstadoTransferenciaReservada, momento)
	insertarTransferenciaDePrueba(t, pool, usuario, cuenta, "intra:"+uuid.NewString(), topeCanonico2026, EstadoTransferenciaReservada, mesAnterior)
	insertarTransferenciaDePrueba(t, pool, usuario, cuenta, "intra:"+uuid.NewString(), topeCanonico2026, EstadoTransferenciaCompensada, momento)
	insertarTransferenciaDePrueba(t, pool, usuario, cuenta, "intra:"+uuid.NewString(), topeCanonico2026, EstadoTransferenciaCreada, momento)

	if err := ejecutarVerificarLimite(ctx, pool, usuario, 500, momento); err != nil {
		t.Fatalf("monto que cabe en el residuo del mes: %v, quiero nil", err)
	}
	if err := ejecutarVerificarLimite(ctx, pool, usuario, 501, momento); !errors.Is(err, ErrCupoMensualExcedido) {
		t.Fatalf("monto que supera el cupo del mes: %v, quiero ErrCupoMensualExcedido", err)
	}
	t.Logf("la ventana sumó solo los envíos reservados del mismo emisor en el mes")
}

// TestVerificarLimiteUsaLaUVTDelAnioDelEnvio cubre el DoD: el valor es el de la fecha del envío, no el actual (HU-NF-06).
func TestVerificarLimiteUsaLaUVTDelAnioDelEnvio(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	poolDueno := abrirPoolDuenoDePrueba(t)
	sembrarValorUVT(t, poolDueno, 2099, 100)

	ctx := t.Context()
	s := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	subcuentaDisponibleDePrueba(t, ctx, s, usuario)

	momento := time.Date(2099, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := ejecutarVerificarLimite(ctx, pool, usuario, 21050, momento); err != nil {
		t.Fatalf("exactamente el tope con la UVT de 2099: %v, quiero nil", err)
	}
	if err := ejecutarVerificarLimite(ctx, pool, usuario, 21051, momento); !errors.Is(err, ErrTopeOperacionExcedido) {
		t.Fatalf("un centavo más con la UVT de 2099: %v, quiero ErrTopeOperacionExcedido", err)
	}
	t.Logf("con la UVT de prueba de 2099 (1,00 COP) el tope quedó en 21050 centavos, no en el de 2026")
}

// TestVerificarLimiteBordeDelTopeDeOperacionConLaUVTDe2026 cubre el criterio 10 con el valor real sembrado (HU-NF-06).
func TestVerificarLimiteBordeDelTopeDeOperacionConLaUVTDe2026(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	s := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	subcuentaDisponibleDePrueba(t, ctx, s, usuario)
	momento := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	if err := ejecutarVerificarLimite(ctx, pool, usuario, topeCanonico2026, momento); err != nil {
		t.Fatalf("exactamente 210,50 UVT: %v, quiero nil", err)
	}
	if err := ejecutarVerificarLimite(ctx, pool, usuario, topeCanonico2026+1, momento); !errors.Is(err, ErrTopeOperacionExcedido) {
		t.Fatalf("un centavo más que 210,50 UVT: %v, quiero ErrTopeOperacionExcedido", err)
	}
	t.Logf("210,50 UVT de 2026 = %d centavos; el borde pasa y un centavo más se rechaza", topeCanonico2026)
}

// TestVerificarLimiteRechazaSinValorDeUVTDelAnio cubre la recomendación de rechazar cuando falta la configuración (HU-NF-06, Preguntas abiertas).
func TestVerificarLimiteRechazaSinValorDeUVTDelAnio(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	s := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	subcuentaDisponibleDePrueba(t, ctx, s, usuario)
	momento := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	if err := ejecutarVerificarLimite(ctx, pool, usuario, 1000, momento); !errors.Is(err, ErrValorUVTNoVigente) {
		t.Fatalf("año sin UVT: %v, quiero ErrValorUVTNoVigente", err)
	}
	t.Logf("un envío de 2025, sin UVT sembrada, se rechaza en vez de pasar sin tope")
}

// TestVerificarLimiteRechazaDosEnviosSimultaneosQueSuperanElCupo cubre el criterio 12.1, pendiente del candado de NF-A2-04 (HU-NF-06).
func TestVerificarLimiteRechazaDosEnviosSimultaneosQueSuperanElCupo(t *testing.T) {
	t.Skip("pendiente del candado ordenado de NF-A2-04: sin candado, dos transacciones concurrentes leen la misma suma y las dos pasan")

	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	s := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	cuenta := subcuentaDisponibleDePrueba(t, ctx, s, usuario)
	momento := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	insertarTransferenciaDePrueba(t, pool, usuario, cuenta, "intra:"+uuid.NewString(), topeCanonico2026-1000, EstadoTransferenciaReservada, momento)

	resultados := make(chan error, 2)
	for intento := 0; intento < 2; intento++ {
		go func() { resultados <- ejecutarVerificarLimite(ctx, pool, usuario, 600, momento) }()
	}
	aprobados := 0
	for intento := 0; intento < 2; intento++ {
		if err := <-resultados; err == nil {
			aprobados++
		}
	}
	if aprobados > 1 {
		t.Errorf("aprobaron %d envíos simultáneos, quiero a lo sumo 1", aprobados)
	}
}
