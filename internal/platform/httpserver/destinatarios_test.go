package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
)

type consultadorDestinoFalso struct {
	nombre         string
	existe, propio bool
	llamadas       int
}

func (f *consultadorDestinoFalso) NombreDeDestinoPorCelular(_ context.Context, _, _ string) (string, bool, bool, error) {
	f.llamadas++
	return f.nombre, f.existe, f.propio, nil
}

type contadorDestinoFalso struct {
	valores map[string]int64
	err     error
	claves  []string
}

func (f *contadorDestinoFalso) Incrementar(_ context.Context, clave string, _ time.Duration) (int64, error) {
	f.claves = append(f.claves, clave)
	if f.valores == nil {
		f.valores = make(map[string]int64)
	}
	f.valores[clave]++
	return f.valores[clave], f.err
}
func (*contadorDestinoFalso) Bloquear(context.Context, string, time.Duration) error { return nil }
func (*contadorDestinoFalso) Bloqueada(context.Context, string) (bool, error)       { return false, nil }

func pedirDestino(t *testing.T, h http.Handler, celular, usuario string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/destinatarios?celular="+celular, nil)
	if usuario != "" {
		req = req.WithContext(identidad.ConUsuarioID(req.Context(), usuario))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// HU-NF-06 C15.2 y C15.3: los errores de inexistente y destino propio conservan los codigos del envio.
func TestHU_NF_06_C15_2_C15_3_ErroresDeDestinatario(t *testing.T) {
	claves, _ := auth.NuevasClaves([]byte("01234567890123456789012345678901"))
	for _, caso := range []struct {
		nombre         string
		existe, propio bool
		status         int
		codigo         string
	}{
		{"inexistente", false, false, 404, "RecipientNotFound"},
		{"propio", true, true, 422, "SelfTransferNotAllowed"},
	} {
		t.Run(caso.nombre, func(t *testing.T) {
			h := NuevoDestinatariosHandler(&consultadorDestinoFalso{existe: caso.existe, propio: caso.propio}, &contadorDestinoFalso{}, claves, 2, time.Hour)
			rec := pedirDestino(t, http.HandlerFunc(h.ConsultarDestinatario), "3001234567", "usuario-1")
			if rec.Code != caso.status || !contains(rec.Body.String(), caso.codigo) {
				t.Fatalf("status=%d cuerpo=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// HU-NF-06 C15.4: el limite es por usuario, cuenta fallos y rechaza si el contador no responde.
func TestHU_NF_06_C15_4_LimitePorUsuario(t *testing.T) {
	claves, _ := auth.NuevasClaves([]byte("01234567890123456789012345678901"))
	consultador := &consultadorDestinoFalso{nombre: "Laura R.", existe: true}
	contador := &contadorDestinoFalso{}
	h := NuevoDestinatariosHandler(consultador, contador, claves, 2, time.Hour)
	for _, status := range []int{200, 200, 429} {
		if rec := pedirDestino(t, http.HandlerFunc(h.ConsultarDestinatario), "3001234567", "usuario-1"); rec.Code != status {
			t.Fatalf("status=%d, quiero %d", rec.Code, status)
		}
	}
	if consultador.llamadas != 2 || len(contador.claves) != 3 {
		t.Fatalf("consultas=%d conteos=%d", consultador.llamadas, len(contador.claves))
	}
	if rec := pedirDestino(t, http.HandlerFunc(h.ConsultarDestinatario), "3001234567", "usuario-2"); rec.Code != 200 {
		t.Fatalf("otro usuario status=%d", rec.Code)
	}
	if contador.claves[0] == contador.claves[3] {
		t.Fatal("usuarios distintos comparten la misma clave de contador")
	}
	contador.err = errors.New("redis caido")
	if rec := pedirDestino(t, http.HandlerFunc(h.ConsultarDestinatario), "3001234567", "usuario-3"); rec.Code != 503 {
		t.Fatalf("status con contador caido=%d", rec.Code)
	}
}

// HU-NF-06 C15.1: la ruta generada exige auth y celular, y responde solo con el nombre en data.
func TestHU_NF_06_C15_1_RutaDeConsulta(t *testing.T) {
	claves, _ := auth.NuevasClaves([]byte("01234567890123456789012345678901"))
	h := NuevoDestinatariosHandler(&consultadorDestinoFalso{nombre: "Laura R.", existe: true}, &contadorDestinoFalso{}, claves, 2, time.Hour)
	router := NewRouter(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{Identidad: auth.UsuarioDePrueba{}, Destinatarios: h})
	for _, caso := range []struct {
		ruta   string
		token  bool
		status int
		codigo string
	}{
		{"/v1/destinatarios?celular=3001234567", false, 401, "Unauthorized"},
		{"/v1/destinatarios", true, 400, "BadRequest"},
		{"/v1/destinatarios?celular=3001234567", true, 200, "Laura R."},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, caso.ruta, nil)
		if caso.token {
			req.Header.Set(auth.CabeceraUsuarioDePrueba, "1")
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != caso.status || !strings.Contains(rec.Body.String(), caso.codigo) {
			t.Fatalf("%s: status=%d cuerpo=%s", caso.ruta, rec.Code, rec.Body.String())
		}
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestDestinatariosPresupuestoCompartidoConAsistente(t *testing.T) {
	claves, _ := auth.NuevasClaves([]byte("01234567890123456789012345678901"))
	contador := &contadorDestinoFalso{}
	consulta := &consultadorDestinoFalso{nombre: "Laura R.", existe: true}
	h := NuevoDestinatariosHandler(consulta, contador, claves, 2, time.Hour)
	limiteIA := auth.NuevoLimiteDestinatarios(contador, claves, 2, time.Hour)
	if err := limiteIA.Exigir(t.Context(), "usuario-1"); err != nil {
		t.Fatal(err)
	}
	if rec := pedirDestino(t, http.HandlerFunc(h.ConsultarDestinatario), "3001234567", "usuario-1"); rec.Code != 200 {
		t.Fatalf("HTTP: %d", rec.Code)
	}
	if err := limiteIA.Exigir(t.Context(), "usuario-1"); !errors.Is(err, auth.ErrConsultaDestinatariosLimitada) {
		t.Fatalf("IA ignoró HTTP: %v", err)
	}
	if rec := pedirDestino(t, http.HandlerFunc(h.ConsultarDestinatario), "3001234567", "usuario-1"); rec.Code != 429 {
		t.Fatalf("HTTP ignoró IA: %d", rec.Code)
	}
	if consulta.llamadas != 1 {
		t.Fatal("consultó después de agotar el presupuesto")
	}
}
