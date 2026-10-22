package copilot

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
)

// ErrReferenciaDestinatario rechaza igualmente tokens alterados, ajenos y vencidos.
var ErrReferenciaDestinatario = errors.New("asistente: referencia de destinatario inválida")

// ReferenciasDestinatario cifra y autentica referencias válidas durante quince minutos.
// Es segura para uso concurrente y no conserva datos de contactos en memoria.
type ReferenciasDestinatario struct {
	cifrador cipher.AEAD
	ahora    func() time.Time
}

// NuevasReferenciasDestinatario deriva una clave con propósito separado del pepper existente.
// Todas las réplicas deben compartir el pepper; rotarlo invalida referencias anteriores.
func NuevasReferenciasDestinatario(pepper []byte) (*ReferenciasDestinatario, error) {
	if len(pepper) < 32 {
		return nil, errors.New("asistente: pepper de referencias debe tener al menos 32 bytes")
	}
	mac := hmac.New(sha256.New, pepper)
	mac.Write([]byte("vera:destinatario-ref:v1"))
	bloque, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(bloque)
	if err != nil {
		return nil, err
	}
	return &ReferenciasDestinatario{gcm, time.Now}, nil
}

func contextoReferencia(usuario string, conversacion uuid.UUID) []byte {
	return []byte("vera:destinatario-ref:v1:" + usuario + ":" + conversacion.String())
}

// Emitir sólo acepta un destino interno real; la salida no contiene IDs legibles.
func (r *ReferenciasDestinatario) Emitir(usuario string, conversacion uuid.UUID, destino string) (string, error) {
	id, err := uuid.Parse(destino)
	if err != nil || id == uuid.Nil || usuario == "" || conversacion == uuid.Nil {
		return "", ErrReferenciaDestinatario
	}
	payload := make([]byte, 24)
	copy(payload, id[:])
	emitida := r.ahora().Unix()
	if emitida < 0 {
		return "", ErrReferenciaDestinatario
	}
	binary.BigEndian.PutUint64(payload[16:], uint64(emitida))
	nonce := make([]byte, r.cifrador.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(r.cifrador.Seal(nonce, nonce, payload, contextoReferencia(usuario, conversacion))), nil
}

// Resolver se usa al preparar el envío; no sustituye la revalidación financiera.
func (r *ReferenciasDestinatario) Resolver(usuario string, conversacion uuid.UUID, token string) (string, error) {
	if usuario == "" || conversacion == uuid.Nil || len(token) > 256 {
		return "", ErrReferenciaDestinatario
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	n := r.cifrador.NonceSize()
	if err != nil || len(b) != n+24+r.cifrador.Overhead() {
		return "", ErrReferenciaDestinatario
	}
	p, err := r.cifrador.Open(nil, b[:n], b[n:], contextoReferencia(usuario, conversacion))
	if err != nil {
		return "", ErrReferenciaDestinatario
	}
	segundos := binary.BigEndian.Uint64(p[16:])
	if segundos > math.MaxInt64 {
		return "", ErrReferenciaDestinatario
	}
	emitida := int64(segundos)
	ahora := r.ahora().Unix()
	if emitida > ahora || ahora-emitida >= int64((15*time.Minute)/time.Second) {
		return "", ErrReferenciaDestinatario
	}
	id, err := uuid.FromBytes(p[:16])
	if err != nil || id == uuid.Nil {
		return "", ErrReferenciaDestinatario
	}
	return id.String(), nil
}
