package whatsapp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	tokenDePrueba     = "token-secreto-de-whatsapp"
	phoneIDDePrueba   = "1234567890"
	plantillaDePrueba = "otp_login"
	destinoDePrueba   = "+573001234567"
	codigoDePrueba    = "135790"
)

func TestEnviarArmaLaPeticionYLeeElID(t *testing.T) {
	t.Parallel()

	var cuerpo cuerpoEnvio
	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("método = %s, quiero POST", r.Method)
		}
		if r.URL.Path != "/"+phoneIDDePrueba+"/messages" {
			t.Errorf("ruta = %s, quiero /%s/messages", r.URL.Path, phoneIDDePrueba)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+tokenDePrueba {
			t.Errorf("Authorization = %q, quiero el token con Bearer", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, quiero application/json", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&cuerpo); err != nil {
			t.Errorf("el cuerpo no es JSON: %v", err)
		}
		_, _ = io.WriteString(w, `{"messages":[{"id":"wamid.ABC"}]}`)
	}))
	t.Cleanup(servidor.Close)

	cliente := Nuevo(servidor.URL, tokenDePrueba, phoneIDDePrueba, plantillaDePrueba, nil)
	id, err := cliente.Enviar(t.Context(), destinoDePrueba, codigoDePrueba)
	if err != nil {
		t.Fatalf("Enviar: %v", err)
	}
	if id != "wamid.ABC" {
		t.Errorf("id = %q, quiero wamid.ABC", id)
	}
	if cuerpo.MessagingProduct != "whatsapp" || cuerpo.Type != "template" {
		t.Errorf("cuerpo = %+v, quiero messaging_product whatsapp y type template", cuerpo)
	}
	if cuerpo.To != "573001234567" {
		t.Errorf("to = %q, quiero 573001234567 sin el +", cuerpo.To)
	}
	if cuerpo.Template.Name != plantillaDePrueba {
		t.Errorf("plantilla = %q, quiero %q", cuerpo.Template.Name, plantillaDePrueba)
	}
	if cuerpo.Template.Language.Code != idiomaPlantilla {
		t.Errorf("idioma = %q, quiero %q", cuerpo.Template.Language.Code, idiomaPlantilla)
	}
	if len(cuerpo.Template.Components) != 2 {
		t.Fatalf("componentes = %d, quiero body y button", len(cuerpo.Template.Components))
	}
	body := cuerpo.Template.Components[0]
	if body.Type != "body" || len(body.Parameters) != 1 || body.Parameters[0].Text != codigoDePrueba {
		t.Errorf("componente body = %+v, quiero el código como parámetro", body)
	}
	boton := cuerpo.Template.Components[1]
	if boton.Type != "button" || boton.SubType != "url" || len(boton.Parameters) != 1 || boton.Parameters[0].Text != codigoDePrueba {
		t.Errorf("componente button = %+v, quiero el código como parámetro", boton)
	}
}

func TestEnviarMapeaEl4xxASinFiltrarElTokenNiElCuerpo(t *testing.T) {
	t.Parallel()

	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "token invalido en el proveedor", http.StatusUnauthorized)
	}))
	t.Cleanup(servidor.Close)

	cliente := Nuevo(servidor.URL, tokenDePrueba, phoneIDDePrueba, plantillaDePrueba, nil)
	_, err := cliente.Enviar(t.Context(), destinoDePrueba, codigoDePrueba)
	if err == nil {
		t.Fatal("Enviar con 401 devolvió nil, quiero error del puerto")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %v, quiero que mencione el status 401", err)
	}
	if strings.Contains(err.Error(), tokenDePrueba) {
		t.Errorf("el error filtró el token: %v", err)
	}
	if strings.Contains(err.Error(), codigoDePrueba) {
		t.Errorf("el error filtró el código: %v", err)
	}
	if strings.Contains(err.Error(), "token invalido en el proveedor") {
		t.Errorf("el error incluyó el cuerpo de la respuesta: %v", err)
	}
}

func TestEnviarMapeaEl5xxAError(t *testing.T) {
	t.Parallel()

	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(servidor.Close)

	cliente := Nuevo(servidor.URL, tokenDePrueba, phoneIDDePrueba, plantillaDePrueba, nil)
	_, err := cliente.Enviar(t.Context(), destinoDePrueba, codigoDePrueba)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("error = %v, quiero error del puerto con el status 500", err)
	}
}

func TestEnviarMapeaElTimeoutAError(t *testing.T) {
	t.Parallel()

	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = io.WriteString(w, `{"messages":[{"id":"tarde"}]}`)
	}))
	t.Cleanup(servidor.Close)

	cliente := Nuevo(servidor.URL, tokenDePrueba, phoneIDDePrueba, plantillaDePrueba, &http.Client{Timeout: 20 * time.Millisecond})
	if _, err := cliente.Enviar(t.Context(), destinoDePrueba, codigoDePrueba); err == nil {
		t.Fatal("Enviar con timeout devolvió nil, quiero error del puerto")
	}
}

func TestEnviarRechazaLaRespuestaSinID(t *testing.T) {
	t.Parallel()

	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"messages":[]}`)
	}))
	t.Cleanup(servidor.Close)

	cliente := Nuevo(servidor.URL, tokenDePrueba, phoneIDDePrueba, plantillaDePrueba, nil)
	if _, err := cliente.Enviar(t.Context(), destinoDePrueba, codigoDePrueba); err == nil {
		t.Fatal("Enviar con 200 sin id devolvió nil, quiero error")
	}
}

func TestNuevoUsaClienteConPlazoPorDefecto(t *testing.T) {
	t.Parallel()

	cliente := Nuevo(BaseGraphAPI, tokenDePrueba, phoneIDDePrueba, plantillaDePrueba, nil)
	if cliente.cliente.Timeout != tiempoEsperaWhatsApp {
		t.Errorf("plazo por defecto = %v, quiero %v", cliente.cliente.Timeout, tiempoEsperaWhatsApp)
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

func (h *handlerMemoria) contenido() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Clone(string(h.lineas))
}

func TestEnviarNoLogueaElTokenNiElCodigo(t *testing.T) {
	destino := &handlerMemoria{}
	previo := slog.Default()
	slog.SetDefault(slog.New(destino))
	t.Cleanup(func() { slog.SetDefault(previo) })

	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"messages":[{"id":"wamid.ABC"}]}`)
	}))
	t.Cleanup(servidor.Close)

	cliente := Nuevo(servidor.URL, tokenDePrueba, phoneIDDePrueba, plantillaDePrueba, nil)
	if _, err := cliente.Enviar(t.Context(), destinoDePrueba, codigoDePrueba); err != nil {
		t.Fatalf("Enviar: %v", err)
	}

	registros := destino.contenido()
	if !strings.Contains(registros, "wamid.ABC") || !strings.Contains(registros, "200") {
		t.Errorf("los registros = %q, quiero el id del mensaje y el status", registros)
	}
	if strings.Contains(registros, tokenDePrueba) {
		t.Error("el adaptador logueó el token")
	}
	if strings.Contains(registros, codigoDePrueba) {
		t.Error("el adaptador logueó el código")
	}
}
