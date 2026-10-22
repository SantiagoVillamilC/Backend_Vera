package nucleo

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PlanTransferencia contiene una resolución del núcleo obtenida antes del candado
// de confirmación. Sus campos privados impiden sustituir datos después de autorizar.
type PlanTransferencia struct {
	servicio       *Servicio
	interna        SolicitudTransferencia
	llave          *SolicitudEnvioPorLlave
	destino, nivel string
	resolucion     ResolucionDirectorio
}

// PlanificarTransferencia resuelve destino y KYC sin una transacción financiera abierta.
// Saldo, límites y asientos siguen verificándose al ejecutar el plan.
func (s *Servicio) PlanificarTransferencia(ctx context.Context, sol SolicitudTransferencia) (*PlanTransferencia, error) {
	if sol.Moneda == "" {
		sol.Moneda = MonedaCOP
	}
	destino, nivel, err := s.resolverTransferenciaInterna(ctx, sol)
	if err != nil {
		return nil, err
	}
	return &PlanTransferencia{servicio: s, interna: sol, destino: destino, nivel: nivel}, nil
}

// PlanificarTransferenciaPorLlave consulta el directorio antes de bloquear una acción.
func (s *Servicio) PlanificarTransferenciaPorLlave(ctx context.Context, sol SolicitudEnvioPorLlave) (*PlanTransferencia, error) {
	if sol.Moneda == "" {
		sol.Moneda = MonedaCOP
	}
	if sol.Monto <= 0 {
		return nil, ErrMontoInvalido
	}
	if sol.Moneda != MonedaCOP {
		return nil, ErrMonedaInvalida
	}
	r, err := s.ResolverLlave(ctx, sol.TipoLlave, sol.Llave)
	if err != nil {
		return nil, err
	}
	if s.kyc == nil {
		return nil, ErrKYCNoDisponible
	}
	nivel, err := s.kyc.NivelKYC(ctx, sol.UsuarioID)
	if err != nil {
		return nil, err
	}
	return &PlanTransferencia{servicio: s, llave: &sol, resolucion: r, nivel: nivel}, nil
}

type poolConfirmacion struct{ tx pgx.Tx }

func (p poolConfirmacion) Begin(ctx context.Context) (transaccionDeNucleo, error) {
	return p.tx.Begin(ctx)
}

// EjecutarEn reutiliza las mismas reservas, límites, ledger, outbox e idempotencia
// de Transferir/TransferirPorLlave dentro de savepoints de la transacción del llamador.
// No confirma el tx exterior. El llamador debe volver a su rol antes de usar su módulo.
func (p *PlanTransferencia) EjecutarEn(ctx context.Context, tx pgx.Tx) (Transferencia, error) {
	if p == nil || p.servicio == nil || tx == nil {
		return Transferencia{}, ErrResolutorDestinoNoDisponible
	}
	s := *p.servicio
	s.pool = poolConfirmacion{tx}
	if p.llave != nil {
		t, err := s.reservarYEncolar(ctx, *p.llave, p.resolucion, p.nivel, time.Now().UTC())
		if err == nil {
			return t, validarLedgerConfirmacion(ctx, tx)
		}
		t, _, err = s.reintentoDeEnvioPorLlave(ctx, *p.llave, err)
		return t, err
	}
	t, err := s.reservarYTransferir(ctx, p.interna, p.destino, p.nivel, time.Now().UTC())
	if err == nil {
		return t, validarLedgerConfirmacion(ctx, tx)
	}
	t, _, err = s.reintentoDeTransferencia(ctx, p.interna, err)
	return t, err
}

// Los triggers diferidos del ledger consultan sus tablas con el rol actual.
// Se validan bajo rol_nucleo antes de devolver el control al asistente.
func validarLedgerConfirmacion(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, "SET CONSTRAINTS nucleo.asiento_cuadra, nucleo.asiento_minimo_dos_lineas IMMEDIATE"); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "SET CONSTRAINTS nucleo.asiento_cuadra, nucleo.asiento_minimo_dos_lineas DEFERRED")
	return err
}

// ConsultarTransferenciaEn recupera un resultado en el mismo tx, sin abrir otra conexión.
func (s *Servicio) ConsultarTransferenciaEn(ctx context.Context, tx pgx.Tx, usuario string, id uuid.UUID) (Transferencia, error) {
	copia := *s
	copia.pool = poolConfirmacion{tx}
	return copia.ConsultarTransferencia(ctx, usuario, id)
}
