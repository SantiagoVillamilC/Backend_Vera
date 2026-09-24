package auth

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// capturadorDeLogs acumula el texto de cada registro de slog para inspeccionarlo.
type capturadorDeLogs struct {
	mu    sync.Mutex
	texto []byte
}

func (h *capturadorDeLogs) Enabled(context.Context, slog.Level) bool { return true }

func (h *capturadorDeLogs) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.texto = append(h.texto, r.Message...)
	h.texto = append(h.texto, '\n')
	r.Attrs(func(a slog.Attr) bool {
		h.texto = append(h.texto, a.Value.String()...)
		h.texto = append(h.texto, '\n')
		return true
	})
	return nil
}

func (h *capturadorDeLogs) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *capturadorDeLogs) WithGroup(string) slog.Handler { return h }

func (h *capturadorDeLogs) contenido() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return bytes.Clone(h.texto)
}

// canalOTPLogueador imita al simulador: registra el envío y loguea solo el id, nunca el código.
type canalOTPLogueador struct {
	canalOTPFalso
}

func (c *canalOTPLogueador) Enviar(ctx context.Context, celular, codigo string) (string, error) {
	slog.InfoContext(ctx, "otp simulado enviado", "id_mensaje", "sim-"+celular)
	return c.canalOTPFalso.Enviar(ctx, celular, codigo)
}

// canalConFijo impone un código determinista como el simulador configurado.
type canalConFijo struct {
	canalOTPFalso
	codigo string
}

func (c *canalConFijo) CodigoFijo() (string, bool) { return c.codigo, c.codigo != "" }

func TestCodigoAleatorioTieneSeisDigitos(t *testing.T) {
	t.Parallel()

	for range 1000 {
		codigo, err := codigoAleatorio()
		if err != nil {
			t.Fatalf("codigoAleatorio: %v", err)
		}
		if len(codigo) != longitudCodigo {
			t.Fatalf("código %q tiene %d dígitos, quiero %d", codigo, len(codigo), longitudCodigo)
		}
		for _, r := range codigo {
			if r < '0' || r > '9' {
				t.Fatalf("código %q contiene un carácter no numérico", codigo)
			}
		}
	}
}

func TestCodigoAleatorioSinSesgoEvidente(t *testing.T) {
	t.Parallel()

	const muestras = 1000
	frecuencias := map[rune]int{}
	for range muestras {
		codigo, err := codigoAleatorio()
		if err != nil {
			t.Fatalf("codigoAleatorio: %v", err)
		}
		for _, r := range codigo {
			frecuencias[r]++
		}
	}
	esperado := muestras * longitudCodigo / 10
	for digito := '0'; digito <= '9'; digito++ {
		conteo := frecuencias[digito]
		if conteo < 450 || conteo > 750 {
			t.Errorf("el dígito %q salió %d veces de %d, fuera de la banda [450,750] alrededor de %d", digito, conteo, muestras*longitudCodigo, esperado)
		}
	}
}

func TestHMACDeCodigoDependeDelPepperYDelCodigo(t *testing.T) {
	t.Parallel()

	pepperA := bytes.Repeat([]byte{0x11}, 32)
	pepperB := bytes.Repeat([]byte{0x22}, 32)

	primero := hmacDeCodigo(pepperA, "123456")
	if len(primero) != 32 {
		t.Fatalf("HMAC de %d bytes, quiero 32", len(primero))
	}
	if !bytes.Equal(primero, hmacDeCodigo(pepperA, "123456")) {
		t.Error("el mismo código con el mismo pepper dio HMAC distinto")
	}
	if bytes.Equal(primero, hmacDeCodigo(pepperB, "123456")) {
		t.Error("dos peppers distintos dieron el mismo HMAC para el mismo código")
	}
	if bytes.Equal(primero, hmacDeCodigo(pepperA, "654321")) {
		t.Error("dos códigos distintos dieron el mismo HMAC")
	}
}

func TestCodigoDelDesafioUsaElFijoDelCanal(t *testing.T) {
	t.Parallel()

	fijo, err := codigoDelDesafio(&canalConFijo{codigo: "123456"})
	if err != nil {
		t.Fatalf("codigoDelDesafio con fijo: %v", err)
	}
	if fijo != "123456" {
		t.Errorf("código = %q, quiero %q", fijo, "123456")
	}

	aleatorio, err := codigoDelDesafio(&canalConFijo{})
	if err != nil {
		t.Fatalf("codigoDelDesafio sin fijo: %v", err)
	}
	if len(aleatorio) != longitudCodigo {
		t.Errorf("código sin fijo = %q, quiero %d dígitos", aleatorio, longitudCodigo)
	}
}

func TestGeneracionYEnvioNoLogueaElCodigo(t *testing.T) {
	capturador := &capturadorDeLogs{}
	previo := slog.Default()
	slog.SetDefault(slog.New(capturador))
	t.Cleanup(func() { slog.SetDefault(previo) })

	canal := &canalOTPLogueador{}
	codigo, err := codigoDelDesafio(canal)
	if err != nil {
		t.Fatalf("codigoDelDesafio: %v", err)
	}
	if _, err := canal.Enviar(t.Context(), "+573001112233", codigo); err != nil {
		t.Fatalf("Enviar: %v", err)
	}

	registros := capturador.contenido()
	if len(registros) == 0 {
		t.Fatal("no se emitió ningún registro: la prueba de fuga sería vacía")
	}
	if strings.Contains(string(registros), codigo) {
		t.Error("la generación o el envío loguearon el código OTP")
	}
}

func TestNuevoGestorDesafiosRechazaPepperCorto(t *testing.T) {
	t.Parallel()

	if _, err := NuevoGestorDesafios(nil, &canalOTPFalso{}, nil, nil, bytes.Repeat([]byte{0x01}, 31)); err == nil {
		t.Error("un OTP_PEPPER de 31 bytes debía fallar")
	}
	gestor, err := NuevoGestorDesafios(nil, &canalOTPFalso{}, nil, nil, bytes.Repeat([]byte{0x01}, 32))
	if err != nil {
		t.Fatalf("32 bytes debían ser válidos: %v", err)
	}
	if gestor.optIn == nil || gestor.eventos == nil {
		t.Error("el gestor debía rellenar opt-in y eventos con sus stubs")
	}
}

func TestSolicitudValidaElCatalogoDePropositos(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		solicitud Solicitud
		wantErr   bool
	}{
		"login con destino":     {solicitud: Solicitud{Proposito: PropositoLogin, Celular: "+57300"}},
		"propósito desconocido": {solicitud: Solicitud{Proposito: "otro", Celular: "+57300"}, wantErr: true},
		"destino vacío":         {solicitud: Solicitud{Proposito: PropositoRegistro}, wantErr: true},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			if err := caso.solicitud.valida(); (err != nil) != caso.wantErr {
				t.Errorf("valida() = %v, wantErr %v", err, caso.wantErr)
			}
		})
	}
}
