package copilot

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
)

// ConfigPreparacion fija la exigencia de código y la disponibilidad de Bre-B.
type ConfigPreparacion struct {
	UmbralCodigo      nucleo.Centavos
	LlavesHabilitadas bool
}

// ValidadorPreparacion sólo expone lecturas del núcleo, jamás Transferir.
type ValidadorPreparacion interface {
	PrevalidarTransferencia(context.Context, string, string, nucleo.Centavos) (nucleo.PrevalidacionTransferencia, error)
	PrevalidarTransferenciaPorLlave(context.Context, string, nucleo.Llave, nucleo.Centavos) (nucleo.PrevalidacionTransferencia, error)
}

// DestinosPreparacion separa la revalidación de referencias del directorio por celular.
type DestinosPreparacion interface {
	DestinatarioPorCelularIA(context.Context, string) (auth.DestinatarioIA, bool, error)
	DestinatarioPorReferenciaIA(context.Context, string) (auth.DestinatarioIA, bool, error)
}

// PropuestaTransferencia mantiene los datos de servidor antes de persistirlos.
type PropuestaTransferencia struct {
	Tipo, Destino, TipoLlave, Nombre, Celular, Entidad, UsuarioFinanciero string
	Monto                                                                 nucleo.Centavos
	RequiereCodigo                                                        bool
}

// GuardadorPropuestas reemplaza la propuesta propia en una transacción aislada del ledger.
type GuardadorPropuestas interface {
	GuardarPropuesta(context.Context, PropuestaTransferencia) (AccionHerramienta, error)
}

// PreparadorTransferencias crea propuestas; sus puertos no permiten ejecutar dinero.
type PreparadorTransferencias struct {
	nucleo         ValidadorPreparacion
	destinos       DestinosPreparacion
	guardador      GuardadorPropuestas
	referencias    *ReferenciasDestinatario
	limiteDestinos *auth.LimiteDestinatarios
	contadores     auth.Contadores
	claves         *auth.Claves
	config         ConfigPreparacion
}

// NuevoPreparadorTransferencias exige contador compartido y un umbral positivo en centavos.
func NuevoPreparadorTransferencias(n ValidadorPreparacion, d DestinosPreparacion, g GuardadorPropuestas, r *ReferenciasDestinatario, l *auth.LimiteDestinatarios, c auth.Contadores, k *auth.Claves, cfg ConfigPreparacion) (*PreparadorTransferencias, error) {
	if n == nil || d == nil || g == nil || r == nil || l == nil || c == nil || k == nil || cfg.UmbralCodigo <= 0 {
		return nil, errors.New("asistente: configuración de preparación incompleta")
	}
	return &PreparadorTransferencias{n, d, g, r, l, c, k, cfg}, nil
}

// Manejar recibe argumentos ya validados y la identidad autorizada por el catálogo.
func (p *PreparadorTransferencias) Manejar(ctx context.Context, id identidad.Identidad, a ArgumentosHerramienta) (DatosHerramienta, error) {
	conv, ok := ConversacionDelContexto(ctx)
	if !ok || id.UsuarioID == "" {
		return nil, ErrHerramientaValidacion
	}
	parte, err := parteDelAsistente(ctx)
	if err != nil {
		return nil, ErrHerramientaValidacion
	}
	monto, err := nucleo.ParsearPesosEnteros(a.MontoPesos)
	if err != nil {
		return nil, ErrHerramientaValidacion
	}
	n, err := p.contadores.Incrementar(ctx, "vera:preparaciones:"+p.claves.ClaveDeParte(parte.String()), time.Hour)
	if err != nil || n <= 0 {
		return nil, ErrHerramientaNoDisponible
	}
	if n > 10 {
		return nil, ErrHerramientaLimite
	}
	propuesta := PropuestaTransferencia{Tipo: "transferencia_interna", UsuarioFinanciero: id.UsuarioID, Monto: monto, RequiereCodigo: monto > p.config.UmbralCodigo}
	if a.LlaveBreB != "" {
		if !p.config.LlavesHabilitadas {
			return nil, ErrHerramientaNoDisponible
		}
		llave, err := llaveParaPreparacion(a.LlaveBreB)
		if err != nil {
			return nil, ErrHerramientaValidacion
		}
		r, err := p.nucleo.PrevalidarTransferenciaPorLlave(ctx, id.UsuarioID, llave, monto)
		if err != nil {
			return nil, errorPreparacion(err)
		}
		propuesta.Tipo = "transferencia_llave"
		propuesta.Destino = llave.Valor
		propuesta.TipoLlave = llave.Tipo
		propuesta.Entidad = r.Entidad
		propuesta.Nombre = "Destinatario por llave"
		if llave.Tipo == nucleo.TipoLlaveCelular {
			propuesta.Celular = celularParaModelo(llave.Valor)
		}
	} else {
		var d auth.DestinatarioIA
		var existe bool
		if a.DestinatarioRef != "" {
			destino, err := p.referencias.Resolver(id.UsuarioID, conv, a.DestinatarioRef)
			if err != nil {
				return nil, ErrHerramientaValidacion
			}
			d, existe, err = p.destinos.DestinatarioPorReferenciaIA(ctx, destino)
			if err != nil {
				return nil, err
			}
		} else {
			if err := p.limiteDestinos.Exigir(ctx, id.UsuarioID); err != nil {
				if errors.Is(err, auth.ErrConsultaDestinatariosLimitada) {
					return nil, ErrHerramientaLimite
				}
				return nil, ErrHerramientaNoDisponible
			}
			d, existe, err = p.destinos.DestinatarioPorCelularIA(ctx, a.Celular)
			if err != nil {
				return nil, err
			}
		}
		if !existe {
			return nil, ErrHerramientaDestinatarioNoEncontrado
		}
		if d.ID == id.UsuarioID {
			return nil, ErrHerramientaDestinoPropio
		}
		r, err := p.nucleo.PrevalidarTransferencia(ctx, id.UsuarioID, d.ID, monto)
		if err != nil {
			return nil, errorPreparacion(err)
		}
		if r.DestinoID != d.ID {
			return nil, ErrHerramientaNoDisponible
		}
		propuesta.Destino = r.DestinoID
		propuesta.Nombre = nombreParaModelo(d.Nombre)
		propuesta.Celular = celularParaModelo(d.Celular)
	}
	return p.guardador.GuardarPropuesta(ctx, propuesta)
}

// Las formas inequívocas se infieren. Documento y comercio admiten prefijo explícito
// para que una secuencia numérica no se interprete como otra clase de llave.
func llaveParaPreparacion(valor string) (nucleo.Llave, error) {
	valor = strings.TrimSpace(valor)
	if tipo, llave, ok := strings.Cut(valor, ":"); ok {
		return nucleo.ParsearLlave(tipo, llave)
	}
	switch {
	case strings.HasPrefix(valor, "@"):
		return nucleo.ParsearLlave(nucleo.TipoLlaveAlfanumerico, valor)
	case strings.Contains(valor, "@"):
		return nucleo.ParsearLlave(nucleo.TipoLlaveCorreo, valor)
	case strings.HasPrefix(valor, "+57"):
		return nucleo.ParsearLlave(nucleo.TipoLlaveCelular, strings.TrimPrefix(valor, "+57"))
	default:
		return nucleo.Llave{}, nucleo.ErrLlaveInvalida
	}
}

func errorPreparacion(err error) error {
	for _, caso := range []struct {
		causa  error
		codigo ErrorHerramienta
	}{
		{nucleo.ErrMontoInvalido, ErrHerramientaValidacion}, {nucleo.ErrLlaveInvalida, ErrHerramientaValidacion},
		{nucleo.ErrDestinoNoExiste, ErrHerramientaDestinatarioNoEncontrado}, {nucleo.ErrLlaveNoEncontrada, ErrHerramientaDestinatarioNoEncontrado}, {nucleo.ErrDestinoPropio, ErrHerramientaDestinoPropio},
		{nucleo.ErrSaldoInsuficiente, "InsufficientFunds"}, {nucleo.ErrTopeOperacionExcedido, "OperationLimitExceeded"}, {nucleo.ErrCupoMensualExcedido, "MonthlyQuotaExceeded"},
		{nucleo.ErrDirectorioSinRespuesta, "DirectoryUnavailable"}, {nucleo.ErrDirectorioNoDisponible, "DirectoryUnavailable"},
	} {
		if errors.Is(err, caso.causa) {
			return caso.codigo
		}
	}
	return err
}
