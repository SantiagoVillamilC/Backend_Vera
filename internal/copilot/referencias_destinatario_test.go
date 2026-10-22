package copilot

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestReferenciaDestinatarioAislamientoCaducidadYManipulacion(t *testing.T) {
	r, err := NuevasReferenciasDestinatario(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	usuario, destino, conv := uuid.NewString(), uuid.NewString(), uuid.New()
	fecha := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	r.ahora = func() time.Time { return fecha }
	token, err := r.Emitir(usuario, conv, destino)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := base64.RawURLEncoding.DecodeString(token)
	idDestino := uuid.MustParse(destino)
	if bytes.Contains(b, idDestino[:]) || strings.Contains(token, destino) {
		t.Fatal("referencia revela el destino")
	}
	otro, err := r.Emitir(usuario, conv, destino)
	if err != nil || token == otro {
		t.Fatal("nonce reutilizado")
	}
	for nombre, caso := range map[string]struct {
		usuario string
		conv    uuid.UUID
		token   string
		tiempo  time.Time
		valido  bool
	}{
		"propio":            {usuario, conv, token, fecha, true},
		"antes de caducar":  {usuario, conv, token, fecha.Add(15*time.Minute - time.Second), true},
		"quince minutos":    {usuario, conv, token, fecha.Add(15 * time.Minute), false},
		"otra persona":      {uuid.NewString(), conv, token, fecha, false},
		"otra conversación": {usuario, uuid.New(), token, fecha, false},
		"futuro":            {usuario, conv, token, fecha.Add(-time.Second), false},
		"sin conversación":  {usuario, uuid.Nil, token, fecha, false},
		"truncada":          {usuario, conv, token[:len(token)-2], fecha, false},
		"excesiva":          {usuario, conv, strings.Repeat("a", 1024), fecha, false},
	} {
		t.Run(nombre, func(t *testing.T) {
			r.ahora = func() time.Time { return caso.tiempo }
			id, err := r.Resolver(caso.usuario, caso.conv, caso.token)
			if caso.valido {
				if err != nil || id != destino {
					t.Fatalf("%s %v", id, err)
				}
			} else if !errors.Is(err, ErrReferenciaDestinatario) {
				t.Fatalf("referencia inválida aceptada: %s %v", id, err)
			}
		})
	}
	r.ahora = func() time.Time { return fecha }
	for i := range b {
		b[i] ^= 1
		alterada := base64.RawURLEncoding.EncodeToString(b)
		if _, err := r.Resolver(usuario, conv, alterada); !errors.Is(err, ErrReferenciaDestinatario) {
			t.Fatalf("aceptó alteración byte %d", i)
		}
		b[i] ^= 1
	}
	replica, _ := NuevasReferenciasDestinatario(bytes.Repeat([]byte{7}, 32))
	replica.ahora = r.ahora
	if id, err := replica.Resolver(usuario, conv, token); err != nil || id != destino {
		t.Fatal("no funciona entre réplicas")
	}
	rotada, _ := NuevasReferenciasDestinatario(bytes.Repeat([]byte{8}, 32))
	rotada.ahora = r.ahora
	if _, err := rotada.Resolver(usuario, conv, token); !errors.Is(err, ErrReferenciaDestinatario) {
		t.Fatal("aceptó clave distinta")
	}
	if _, err := NuevasReferenciasDestinatario([]byte("corta")); err == nil {
		t.Fatal("aceptó clave débil")
	}
}
