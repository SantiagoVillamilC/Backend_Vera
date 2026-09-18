//go:build integration

package nucleo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// sembrarTopeOperacion reescribe el tope de la regla de operación del nivel y lo restaura al terminar (HU-NF-06, criterio 10).
func sembrarTopeOperacion(t *testing.T, pool *pgxpool.Pool, topeCentesimas int64) {
	t.Helper()

	var original int64
	if err := pool.QueryRow(t.Context(), `SELECT tope_uvt_centesimas FROM nucleo.regla_limite WHERE id = 'deposito_bajo_monto_operacion'`).Scan(&original); err != nil {
		t.Fatalf("leer el tope de operación vigente: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE nucleo.regla_limite SET tope_uvt_centesimas = $1 WHERE id = 'deposito_bajo_monto_operacion'`, topeCentesimas); err != nil {
		t.Fatalf("sembrar el tope de operación: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `UPDATE nucleo.regla_limite SET tope_uvt_centesimas = $1 WHERE id = 'deposito_bajo_monto_operacion'`, original); err != nil {
			t.Errorf("restaurar el tope de operación: %v", err)
		}
	})
}

// escenarioMontoMaximo siembra un caso del criterio 14 y el rechazo que debe dar un centavo más.
type escenarioMontoMaximo struct {
	nombre            string
	saldo             Centavos
	acumulado         Centavos
	quererMaximo      Centavos
	errorUnCentavoMas error
}

// TestHU_NF_06_Criterio14_EnviarElMontoMaximoNoDaSaldoInsuficiente cubre los tres casos y el cupo contra Postgres real (HU-NF-06, criterio 14).
func TestHU_NF_06_Criterio14_EnviarElMontoMaximoNoDaSaldoInsuficiente(t *testing.T) {
	casos := []escenarioMontoMaximo{
		{
			nombre:            "sin cruzar el tope exento",
			saldo:             100000000,
			acumulado:         0,
			quererMaximo:      100000000,
			errorUnCentavoMas: ErrSaldoInsuficiente,
		},
		{
			nombre:            "con el tope exento agotado",
			saldo:             100000000,
			acumulado:         topeExentoGMF2026,
			quererMaximo:      99601594,
			errorUnCentavoMas: ErrSaldoInsuficiente,
		},
		{
			nombre:            "con el envío cruzando el tope",
			saldo:             200000000,
			acumulado:         topeExentoGMF2026 - 100000000,
			quererMaximo:      199601594,
			errorUnCentavoMas: ErrSaldoInsuficiente,
		},
		{
			nombre:            "con el cupo mensual recortando",
			saldo:             5000000000,
			acumulado:         topeCanonico2026 - 1000,
			quererMaximo:      1000,
			errorUnCentavoMas: ErrCupoMensualExcedido,
		},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			pool := abrirPoolDePrueba(t)
			ctx := t.Context()
			momento := time.Now().UTC()
			base := NewServicio(pool, LectorNivelKYCFijo{}, nil, nil, nil)
			emisor := uuid.NewString()
			cuentaEmisor := subcuentaDisponibleDePrueba(t, ctx, base, emisor)
			fondearDisponibleDePrueba(t, pool, cuentaEmisor, caso.saldo)
			receptor := uuid.NewString()
			subcuentaDisponibleDePrueba(t, ctx, base, receptor)
			if caso.acumulado > 0 {
				insertarTransferenciaDePrueba(t, pool, emisor, cuentaEmisor, "intra:"+uuid.NewString(),
					caso.acumulado, EstadoTransferenciaReservada, momento)
			}

			s := NewServicio(pool, LectorNivelKYCFijo{}, nil, nil, nil, ConResolutorDestino(resolutorDePrueba{"receptor": receptor}))

			maximo, err := s.MontoMaximoEnviable(ctx, emisor, momento)
			if err != nil {
				t.Fatalf("monto máximo: %v, quiero nil", err)
			}
			if maximo != caso.quererMaximo {
				t.Fatalf("monto máximo = %d, quiero %d", maximo, caso.quererMaximo)
			}

			if _, _, err := s.Transferir(ctx, solicitudDeMontoMaximo(emisor, cuentaEmisor, maximo+1)); !errors.Is(err, caso.errorUnCentavoMas) {
				t.Fatalf("un centavo más: err = %v, quiero %v", err, caso.errorUnCentavoMas)
			}

			transferencia, creada, err := s.Transferir(ctx, solicitudDeMontoMaximo(emisor, cuentaEmisor, maximo))
			if err != nil || !creada {
				t.Fatalf("enviar el máximo: creada = %v, err = %v; quiero true, nil", creada, err)
			}
			if transferencia.Estado != EstadoTransferenciaConfirmada {
				t.Errorf("estado = %q, quiero %q", transferencia.Estado, EstadoTransferenciaConfirmada)
			}
			if got := saldoDeCuentaDePrueba(t, base, NombreCuentaDisponible(receptor)); got != maximo {
				t.Errorf("disponible del receptor = %d, quiero %d", got, maximo)
			}
			t.Logf("enviar exactamente %d no dio saldo insuficiente; un centavo más dio %v", maximo, caso.errorUnCentavoMas)
		})
	}
}

// TestHU_NF_06_Criterio14_TopeDeOperacionMenorQueElMensual cubre la ida y vuelta con el tope por operación recortando (HU-NF-06, criterio 14).
func TestHU_NF_06_Criterio14_TopeDeOperacionMenorQueElMensual(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	sembrarTopeOperacion(t, abrirPoolDuenoDePrueba(t), 1000)

	ctx := t.Context()
	momento := time.Now().UTC()
	base := NewServicio(pool, LectorNivelKYCFijo{}, nil, nil, nil)
	emisor := uuid.NewString()
	cuentaEmisor := subcuentaDisponibleDePrueba(t, ctx, base, emisor)
	fondearDisponibleDePrueba(t, pool, cuentaEmisor, 5000000000)
	receptor := uuid.NewString()
	subcuentaDisponibleDePrueba(t, ctx, base, receptor)

	s := NewServicio(pool, LectorNivelKYCFijo{}, nil, nil, nil, ConResolutorDestino(resolutorDePrueba{"receptor": receptor}))

	topeOperacion, err := centavosDesdeTopeUVT(1000, uvts2026)
	if err != nil {
		t.Fatalf("tope de operación: %v", err)
	}
	maximo, err := s.MontoMaximoEnviable(ctx, emisor, momento)
	if err != nil {
		t.Fatalf("monto máximo: %v, quiero nil", err)
	}
	if maximo != topeOperacion {
		t.Fatalf("monto máximo = %d, quiero el tope por operación %d", maximo, topeOperacion)
	}

	if _, _, err := s.Transferir(ctx, solicitudDeMontoMaximo(emisor, cuentaEmisor, maximo+1)); !errors.Is(err, ErrTopeOperacionExcedido) {
		t.Fatalf("un centavo más: err = %v, quiero ErrTopeOperacionExcedido", err)
	}

	transferencia, creada, err := s.Transferir(ctx, solicitudDeMontoMaximo(emisor, cuentaEmisor, maximo))
	if err != nil || !creada {
		t.Fatalf("enviar el máximo: creada = %v, err = %v; quiero true, nil", creada, err)
	}
	if transferencia.Estado != EstadoTransferenciaConfirmada {
		t.Errorf("estado = %q, quiero %q", transferencia.Estado, EstadoTransferenciaConfirmada)
	}
	if got := saldoDeCuentaDePrueba(t, base, NombreCuentaDisponible(receptor)); got != maximo {
		t.Errorf("disponible del receptor = %d, quiero %d", got, maximo)
	}
	t.Logf("con el tope por operación %d, el máximo fue %d; un centavo más dio %v", topeOperacion, maximo, ErrTopeOperacionExcedido)
}

// solicitudDeMontoMaximo arma un envío al receptor de prueba con una llave nueva en cada intento (HU-NF-06, criterio 14).
func solicitudDeMontoMaximo(usuarioID, cuentaOrigen string, monto Centavos) SolicitudTransferencia {
	return SolicitudTransferencia{
		UsuarioID:      usuarioID,
		CuentaOrigen:   cuentaOrigen,
		LlaveDestino:   "receptor",
		Monto:          monto,
		Moneda:         MonedaCOP,
		IdempotencyKey: "k-" + uuid.NewString(),
	}
}
