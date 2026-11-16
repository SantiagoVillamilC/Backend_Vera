package control

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Controlador es lo que la API de control necesita del servicio.
type Controlador interface {
	Encender() (Vista, bool, error)
	Apagar() Vista
	Vista() Vista
}

type errorSobre struct {
	Codigo  string `json:"code"`
	Mensaje string `json:"message"`
}

type sobre struct {
	Exito bool        `json:"success"`
	Datos any         `json:"data"`
	Error *errorSobre `json:"error"`
	Meta  any         `json:"meta"`
}

// ManejadorControl sirve POST /encender, POST /apagar y GET /estado con token Bearer propio. Debe
// enlazarse solo a loopback (la configuración lo valida).
func ManejadorControl(c Controlador, token string, log *slog.Logger) http.Handler {
	digestoEsperado := sha256.Sum256([]byte(token))
	mux := http.NewServeMux()
	mux.HandleFunc("/encender", solo(http.MethodPost, func(w http.ResponseWriter, _ *http.Request) { encender(w, c, log) }))
	mux.HandleFunc("/apagar", solo(http.MethodPost, func(w http.ResponseWriter, _ *http.Request) {
		v := c.Apagar()
		codigo := http.StatusAccepted
		if v.Estado == "apagado" {
			codigo = http.StatusOK
		}
		responder(w, codigo, sobre{Exito: true, Datos: v})
	}))
	mux.HandleFunc("/estado", solo(http.MethodGet, func(w http.ResponseWriter, _ *http.Request) {
		responder(w, http.StatusOK, sobre{Exito: true, Datos: c.Vista()})
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !tokenValido(r, digestoEsperado) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="modelos-control"`)
			responder(w, http.StatusUnauthorized, sobre{Error: &errorSobre{"no_autorizado", "token ausente o invalido"}})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
		mux.ServeHTTP(w, r)
	})
}

// tokenValido compara los digests SHA-256 en tiempo constante, así ni el contenido ni la longitud del
// token esperado se pueden deducir por tiempo de respuesta.
func tokenValido(r *http.Request, esperado [sha256.Size]byte) bool {
	cabecera := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(cabecera, "Bearer ")
	recibido := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(recibido[:], esperado[:]) == 1 && ok
}

func solo(metodo string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != metodo {
			w.Header().Set("Allow", metodo)
			responder(w, http.StatusMethodNotAllowed, sobre{Error: &errorSobre{"metodo_no_permitido", "usa " + metodo}})
			return
		}
		h(w, r)
	}
}

func encender(w http.ResponseWriter, c Controlador, log *slog.Logger) {
	v, iniciado, err := c.Encender()
	switch {
	case errors.Is(err, ErrApagando), errors.Is(err, ErrRequiereApagar):
		responder(w, http.StatusConflict, sobre{Error: &errorSobre{"conflicto", err.Error()}})
	case errors.Is(err, ErrConfiguracion):
		responder(w, http.StatusPreconditionFailed, sobre{Error: &errorSobre{"configuracion", err.Error()}})
	case err != nil:
		log.Error("fallo al encender", "error", err.Error())
		responder(w, http.StatusInternalServerError, sobre{Error: &errorSobre{"interno", "no se pudo iniciar el encendido"}})
	case iniciado:
		responder(w, http.StatusAccepted, sobre{Exito: true, Datos: v})
	default:
		responder(w, http.StatusOK, sobre{Exito: true, Datos: v})
	}
}

func responder(w http.ResponseWriter, codigo int, s sobre) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(codigo)
	_ = json.NewEncoder(w).Encode(s)
}

// ServidorControl construye el http.Server de la API de control con tiempos de espera acotados.
func ServidorControl(dir string, h http.Handler) *http.Server {
	return &http.Server{Addr: dir, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: time.Minute}
}
