package otpsim

import (
	"bytes"
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestEnviarGuardaElCodigoYUltimoCodigoLoDevuelve(t *testing.T) {
	t.Parallel()

	sim, err := Nuevo("")
	if err != nil {
		t.Fatalf("Nuevo: %v", err)
	}

	id, err := sim.Enviar(t.Context(), "+573001234567", "135790")
	if err != nil {
		t.Fatalf("Enviar: %v", err)
	}
	if id == "" {
		t.Error("Enviar devolvió un id vacío")
	}
	codigo, ok := sim.UltimoCodigo("+573001234567")
	if !ok || codigo != "135790" {
		t.Errorf("UltimoCodigo = (%q, %v), quiero (\"135790\", true)", codigo, ok)
	}
}

func TestUltimoCodigoSinRegistroDevuelveFalso(t *testing.T) {
	t.Parallel()

	sim, err := Nuevo("")
	if err != nil {
		t.Fatalf("Nuevo: %v", err)
	}
	if codigo, ok := sim.UltimoCodigo("+573009999999"); ok || codigo != "" {
		t.Errorf("UltimoCodigo = (%q, %v), quiero (\"\", false)", codigo, ok)
	}
}

func TestCodigoFijo(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		entrada    string
		wantCodigo string
		wantHay    bool
	}{
		"presente": {entrada: "123456", wantCodigo: "123456", wantHay: true},
		"ausente":  {entrada: "", wantCodigo: "", wantHay: false},
	}
	for name, tc := range casos {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			sim, err := Nuevo(tc.entrada)
			if err != nil {
				t.Fatalf("Nuevo(%q): %v", tc.entrada, err)
			}
			codigo, hay := sim.CodigoFijo()
			if codigo != tc.wantCodigo || hay != tc.wantHay {
				t.Errorf("CodigoFijo = (%q, %v), quiero (%q, %v)", codigo, hay, tc.wantCodigo, tc.wantHay)
			}
		})
	}
}

func TestNuevoRechazaUnCodigoFijoInvalido(t *testing.T) {
	t.Parallel()

	casos := map[string]string{
		"cinco dígitos": "12345",
		"siete dígitos": "1234567",
		"no numérico":   "abcdef",
		"con espacio":   "12 456",
		"con signo":     "+12345",
	}
	for name, entrada := range casos {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := Nuevo(entrada); err == nil {
				t.Errorf("Nuevo(%q) aceptó un código fijo inválido", entrada)
			}
		})
	}
}

// handlerMemoria acumula el mensaje y los atributos de cada registro para inspeccionarlos.
type handlerMemoria struct {
	mu     sync.Mutex
	lineas []byte
}

func (h *handlerMemoria) Enabled(context.Context, slog.Level) bool { return true }

func (h *handlerMemoria) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lineas = append(h.lineas, r.Message...)
	h.lineas = append(h.lineas, '\n')
	r.Attrs(func(a slog.Attr) bool {
		h.lineas = append(h.lineas, a.Value.String()...)
		h.lineas = append(h.lineas, '\n')
		return true
	})
	return nil
}

func (h *handlerMemoria) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *handlerMemoria) WithGroup(string) slog.Handler { return h }

func (h *handlerMemoria) contenido() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return bytes.Clone(h.lineas)
}

func TestEnviarNoLogueaElCodigo(t *testing.T) {
	const codigo = "135790"

	destino := &handlerMemoria{}
	previo := slog.Default()
	slog.SetDefault(slog.New(destino))
	t.Cleanup(func() { slog.SetDefault(previo) })

	sim, err := Nuevo("")
	if err != nil {
		t.Fatalf("Nuevo: %v", err)
	}
	if _, err := sim.Enviar(t.Context(), "+573001234567", codigo); err != nil {
		t.Fatalf("Enviar: %v", err)
	}

	registros := destino.contenido()
	if len(registros) == 0 {
		t.Fatal("el simulador no emitió ningún registro: la prueba de fuga sería vacía")
	}
	if strings.Contains(string(registros), codigo) {
		t.Error("el simulador logueó el código OTP")
	}
}

func TestEnviarSoportaUsoConcurrente(t *testing.T) {
	t.Parallel()

	sim, err := Nuevo("")
	if err != nil {
		t.Fatalf("Nuevo: %v", err)
	}

	const total = 32
	var wg sync.WaitGroup
	for i := range total {
		celular := "+5730012" + strconv.Itoa(30000+i)
		wg.Go(func() {
			if _, err := sim.Enviar(t.Context(), celular, "111111"); err != nil {
				t.Errorf("Enviar(%q): %v", celular, err)
			}
		})
	}
	wg.Wait()
}
