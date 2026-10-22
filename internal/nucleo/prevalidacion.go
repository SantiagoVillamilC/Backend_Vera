package nucleo

import (
	"context"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
)

// PrevalidacionTransferencia contiene una resolución informativa, nunca una reserva.
type PrevalidacionTransferencia struct {
	DestinoID string
	Gravamen  Centavos
	Entidad   string
}

// PrevalidarTransferencia aplica las mismas reglas que Transferir sin crear transferencias ni postings.
func (s *Servicio) PrevalidarTransferencia(ctx context.Context, usuario, destino string, monto Centavos) (PrevalidacionTransferencia, error) {
	solicitud := SolicitudTransferencia{UsuarioID: usuario, CuentaOrigen: NombreCuentaDisponible(usuario), LlaveDestino: destino, Monto: monto, Moneda: MonedaCOP}
	destinoID, nivel, err := s.resolverTransferenciaInterna(ctx, solicitud)
	if err != nil {
		return PrevalidacionTransferencia{}, err
	}
	g, err := s.prevalidarFondos(ctx, usuario, nivel, monto, []string{solicitud.CuentaOrigen, NombreCuentaDisponible(destinoID)})
	if err != nil {
		return PrevalidacionTransferencia{}, err
	}
	return PrevalidacionTransferencia{DestinoID: destinoID, Gravamen: g}, nil
}

// PrevalidarTransferenciaPorLlave comparte resolución, límites y saldo con TransferirPorLlave.
func (s *Servicio) PrevalidarTransferenciaPorLlave(ctx context.Context, usuario string, llave Llave, monto Centavos) (PrevalidacionTransferencia, error) {
	if monto <= 0 {
		return PrevalidacionTransferencia{}, ErrMontoInvalido
	}
	r, err := s.ResolverLlave(ctx, llave.Tipo, llave.Valor)
	if err != nil {
		return PrevalidacionTransferencia{}, err
	}
	if s.kyc == nil {
		return PrevalidacionTransferencia{}, ErrKYCNoDisponible
	}
	nivel, err := s.kyc.NivelKYC(ctx, usuario)
	if err != nil {
		return PrevalidacionTransferencia{}, err
	}
	g, err := s.prevalidarFondos(ctx, usuario, nivel, monto, []string{NombreCuentaDisponible(usuario)})
	if err != nil {
		return PrevalidacionTransferencia{}, err
	}
	return PrevalidacionTransferencia{Entidad: r.Entidad, Gravamen: g}, nil
}

func (s *Servicio) prevalidarFondos(ctx context.Context, usuario, nivel string, monto Centavos, cuentas []string) (Centavos, error) {
	return enTransaccion(ctx, s.pool, func(ctx context.Context, tx transaccionDeNucleo, q *repositorio.Queries) (Centavos, error) {
		if _, err := tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY"); err != nil {
			return 0, err
		}
		filas, err := q.CuentasParaPrevalidacion(ctx, cuentas)
		if err != nil {
			return 0, err
		}
		if len(filas) != len(cuentasUnicasOrdenadas(cuentas)) {
			return 0, ErrCuentaDelCandadoAusente
		}
		g, err := verificarLimite(ctx, q, usuario, nivel, monto, time.Now().UTC())
		if err != nil {
			return 0, err
		}
		if err := verificarSaldoDeReserva(ctx, q, NombreCuentaDisponible(usuario), monto, g); err != nil {
			return 0, err
		}
		return g, nil
	})
}
