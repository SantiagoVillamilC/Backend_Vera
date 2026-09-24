package auth

import "context"

// CanalOTP entrega un código de un solo uso al celular; un error significa que no se pudo enviar.
type CanalOTP interface {
	Enviar(ctx context.Context, celularE164 string, codigo string) (IDMensaje string, err error)
}
