package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/go-chi/httprate"
)

// Contadores acumula intentos por clave con incremento y TTL atomicos.
type Contadores interface {
	Incrementar(ctx context.Context, clave string, ventana time.Duration) (valor int64, err error)
	Bloquear(ctx context.Context, clave string, hasta time.Duration) error
	Bloqueada(ctx context.Context, clave string) (bool, error)
}

// Prefijos por proposito para que A2-06.3 y A2-07.1 compongan la clave sin inventarla.
const (
	PrefijoOTPCuenta    = "otp:cuenta:"
	PrefijoOTPOrigen    = "otp:origen:"
	PrefijoLoginCuenta  = "login:cuenta:"
	PrefijoLoginOrigen  = "login:origen:"
	PrefijoFallosCuenta = "fallos:cuenta:"
)

// Claves deriva las claves de los contadores desde CONTADORES_PEPPER.
type Claves struct {
	pepper []byte
}

// NuevasClaves exige un pepper de al menos 32 bytes para el HMAC de los correos.
func NuevasClaves(pepper []byte) (*Claves, error) {
	if len(pepper) < 32 {
		return nil, fmt.Errorf("auth: CONTADORES_PEPPER debe tener al menos 32 bytes, tiene %d", len(pepper))
	}
	return &Claves{pepper: pepper}, nil
}

// ClaveDeCorreo devuelve el HMAC-SHA256 en hex del correo normalizado, exista o no la cuenta.
func (c *Claves) ClaveDeCorreo(correoNormalizado string) string {
	mac := hmac.New(sha256.New, c.pepper)
	mac.Write([]byte(correoNormalizado))
	return hex.EncodeToString(mac.Sum(nil))
}

// ClaveDeCelular devuelve el HMAC-SHA256 en hex del celular, exista o no la cuenta.
func (c *Claves) ClaveDeCelular(celular string) string {
	mac := hmac.New(sha256.New, c.pepper)
	mac.Write([]byte(celular))
	return hex.EncodeToString(mac.Sum(nil))
}

// ClaveDeOrigen devuelve la IP canonicalizada, con prefijo /64 en IPv6.
func (c *Claves) ClaveDeOrigen(ip string) string {
	return httprate.CanonicalizeIP(ip)
}
