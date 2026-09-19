// Package otpsim es el canal de OTP simulado, determinista y sin red, para pruebas y demo.
package otpsim

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
)

// Simulador guarda en memoria el último código entregado a cada celular.
type Simulador struct {
	mu         sync.Mutex
	ultimos    map[string]string
	codigoFijo string
}

// Nuevo arma el simulador con un código fijo opcional de exactamente seis dígitos.
func Nuevo(codigoFijo string) (*Simulador, error) {
	if codigoFijo != "" && !esCodigoDeSeisDigitos(codigoFijo) {
		return nil, fmt.Errorf("otpsim: OTP_SIMULADOR_CODIGO_FIJO debe tener 6 dígitos numéricos")
	}
	return &Simulador{ultimos: map[string]string{}, codigoFijo: codigoFijo}, nil
}

// Enviar registra el código del celular y devuelve un id de mensaje determinista.
func (s *Simulador) Enviar(ctx context.Context, celularE164, codigo string) (string, error) {
	s.mu.Lock()
	s.ultimos[celularE164] = codigo
	s.mu.Unlock()
	id := "sim-" + strings.TrimPrefix(celularE164, "+")
	slog.InfoContext(ctx, "otp simulado enviado", "id_mensaje", id)
	return id, nil
}

// UltimoCodigo devuelve el último código guardado para el celular y si existe.
func (s *Simulador) UltimoCodigo(celularE164 string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	codigo, ok := s.ultimos[celularE164]
	return codigo, ok
}

// CodigoFijo devuelve el código que debe generar el servicio, si se configuró uno.
func (s *Simulador) CodigoFijo() (string, bool) {
	return s.codigoFijo, s.codigoFijo != ""
}

// esCodigoDeSeisDigitos reporta si el valor son seis dígitos ASCII exactos.
func esCodigoDeSeisDigitos(valor string) bool {
	if len(valor) != 6 {
		return false
	}
	for _, r := range valor {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

var _ auth.CanalOTP = (*Simulador)(nil)
