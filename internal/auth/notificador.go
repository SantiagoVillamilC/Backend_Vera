package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
)

// Tipos de aviso que viajan por el outbox de auth.
const (
	TipoAvisoContrasenaCambiada     = "contrasena_cambiada"
	TipoAvisoCambioNumero           = "cambio_numero"
	TipoAvisoRecuperacionSolicitada = "recuperacion_solicitada"
	TipoAvisoAbrirSubcuentas        = "abrir_subcuentas"
)

// Destinos que cubre el puerto Notificador: correo y whatsapp.
const (
	DestinoCorreo   = "correo"
	DestinoWhatsApp = "whatsapp"
)

// Aviso es una notificación a encolar en el outbox; su payload nunca lleva un secreto.
type Aviso struct {
	Tipo      string
	Destino   string
	Correo    string
	Celular   string
	Plantilla string
}

// payloadAviso es la forma persistida del aviso: destino, dirección y plantilla.
type payloadAviso struct {
	Destino   string `json:"destino"`
	Correo    string `json:"correo,omitempty"`
	Celular   string `json:"celular,omitempty"`
	Plantilla string `json:"plantilla"`
}

// Notificador entrega un aviso por el canal que su destino indique.
type Notificador interface {
	Enviar(ctx context.Context, aviso Aviso) (IDMensaje string, err error)
}

// NotificadorDeAvisos entrega los avisos por correo o por WhatsApp según su destino.
type NotificadorDeAvisos struct {
	correo   CanalCorreo
	whatsapp CanalOTP
}

// NuevoNotificadorDeAvisos construye el notificador con sus dos canales.
func NuevoNotificadorDeAvisos(correo CanalCorreo, whatsapp CanalOTP) *NotificadorDeAvisos {
	return &NotificadorDeAvisos{correo: correo, whatsapp: whatsapp}
}

// Enviar escoge el canal por el destino del aviso y devuelve el id del proveedor.
func (n *NotificadorDeAvisos) Enviar(ctx context.Context, aviso Aviso) (string, error) {
	var (
		id  string
		err error
	)
	switch aviso.Destino {
	case DestinoCorreo:
		id, err = n.correo.Enviar(ctx, aviso.Correo, asuntoDeAviso(aviso), cuerpoDeAviso(aviso))
	case DestinoWhatsApp:
		id, err = n.whatsapp.Enviar(ctx, aviso.Celular, cuerpoDeAviso(aviso))
	default:
		return "", fmt.Errorf("auth: destino de aviso desconocido %q", aviso.Destino)
	}
	if err != nil {
		return "", fmt.Errorf("auth: entregar el aviso %q por %s: %w", aviso.Tipo, aviso.Destino, err)
	}
	return id, nil
}

// EncolarAviso inserta el aviso en auth.outbox dentro de la transacción del caso de uso.
func EncolarAviso(ctx context.Context, q *repositorio.Queries, aviso Aviso) error {
	if err := aviso.valido(); err != nil {
		return err
	}
	payload, err := json.Marshal(payloadAviso{
		Destino:   aviso.Destino,
		Correo:    aviso.Correo,
		Celular:   aviso.Celular,
		Plantilla: aviso.Plantilla,
	})
	if err != nil {
		return fmt.Errorf("auth: serializar el payload del aviso %q: %w", aviso.Tipo, err)
	}
	if _, err := q.InsertarAviso(ctx, repositorio.InsertarAvisoParams{Tipo: aviso.Tipo, Payload: payload}); err != nil {
		return fmt.Errorf("auth: encolar el aviso %q: %w", aviso.Tipo, err)
	}
	return nil
}

// valido exige tipo y, cuando el aviso notifica, la dirección de su destino y la plantilla.
func (a Aviso) valido() error {
	if a.Tipo == "" {
		return errors.New("auth: el aviso necesita tipo")
	}
	switch a.Destino {
	case "":
		return nil
	case DestinoCorreo:
		if a.Correo == "" || a.Plantilla == "" {
			return errors.New("auth: el aviso por correo necesita correo y plantilla")
		}
	case DestinoWhatsApp:
		if a.Celular == "" || a.Plantilla == "" {
			return errors.New("auth: el aviso por whatsapp necesita celular y plantilla")
		}
	default:
		return fmt.Errorf("auth: destino de aviso desconocido %q", a.Destino)
	}
	return nil
}

// asuntoDeAviso arma el asunto del correo; el texto definitivo está pendiente de fijar.
func asuntoDeAviso(aviso Aviso) string {
	return fmt.Sprintf("[PLANTILLA PENDIENTE] Aviso %s", aviso.Tipo)
}

// cuerpoDeAviso arma el cuerpo sin datos de la cuenta; el texto definitivo está pendiente de fijar.
func cuerpoDeAviso(aviso Aviso) string {
	return fmt.Sprintf("[PLANTILLA PENDIENTE] Plantilla %q: hubo un cambio. Si no fuiste tú, escribe a soporte.", aviso.Plantilla)
}
