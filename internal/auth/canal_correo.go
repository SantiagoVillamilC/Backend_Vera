package auth

import "context"

// CanalCorreo entrega un aviso por correo; un error significa que no se pudo enviar.
type CanalCorreo interface {
	Enviar(ctx context.Context, correo, asunto, cuerpo string) (IDMensaje string, err error)
}
