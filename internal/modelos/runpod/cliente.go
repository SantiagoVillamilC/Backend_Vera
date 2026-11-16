// Package runpod es un cliente mínimo de la API REST v2 de RunPod (https://api.runpod.io/v2).
// Solo implementa lo que modelos-control necesita y nunca expone la clave en errores ni registros.
package runpod

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/secreto"
)

const (
	agente          = "vera-modelos-control/1.0" // Cloudflare corta con 403 el User-Agent por defecto
	maxCuerpo       = 4 << 20
	tiempoPeticion  = 30 * time.Second
	maxDetalleError = 200
)

// Errores centinela.
var (
	ErrNoExiste          = errors.New("runpod: recurso inexistente")
	ErrRespuestaInvalida = errors.New("runpod: respuesta invalida")
	ErrEncolado          = errors.New("runpod: la creacion quedo en cola")
)

// ErrorAPI es una respuesta de error de RunPod, ya redactada y recortada.
type ErrorAPI struct {
	Metodo, Ruta string
	Estado       int
	Titulo       string
	Detalle      string
	ReintentarEn time.Duration
}

func (e *ErrorAPI) Error() string {
	return fmt.Sprintf("runpod: %s %s respondio %d %s: %s", e.Metodo, e.Ruta, e.Estado, e.Titulo, e.Detalle)
}

// Is permite errors.Is(err, ErrNoExiste) para un 404.
func (e *ErrorAPI) Is(destino error) bool {
	return destino == ErrNoExiste && e.Estado == http.StatusNotFound
}

// Reintentable dice si repetir la misma petición puede tener éxito (límite de tasa o fallo del servidor).
func (e *ErrorAPI) Reintentable() bool {
	return e.Estado == http.StatusTooManyRequests || e.Estado >= 500
}

// Config reúne lo necesario para crear el cliente.
type Config struct {
	BaseURL string
	Clave   string
	HTTP    *http.Client
}

// Cliente habla con la API v2. Es seguro para uso concurrente.
type Cliente struct {
	base     *url.URL
	clave    string
	http     *http.Client
	redactor secreto.Redactor
}

// Nuevo valida la configuración. La base debe ser https, o http solo hacia loopback (pruebas).
func Nuevo(cfg Config) (*Cliente, error) {
	base, err := url.Parse(strings.TrimRight(cfg.BaseURL, "/"))
	if err != nil || base.User != nil || base.Host == "" {
		return nil, errors.New("runpod: URL base invalida")
	}
	loopback := base.Hostname() == "127.0.0.1" || base.Hostname() == "localhost" || base.Hostname() == "::1"
	if base.Scheme != "https" && (base.Scheme != "http" || !loopback) {
		return nil, errors.New("runpod: la URL base debe ser https")
	}
	if strings.TrimSpace(cfg.Clave) == "" {
		return nil, errors.New("runpod: falta la clave")
	}
	h := cfg.HTTP
	if h == nil {
		h = &http.Client{Timeout: tiempoPeticion}
	}
	copia := *h
	copia.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Cliente{base: base, clave: cfg.Clave, http: &copia, redactor: secreto.Nuevo(cfg.Clave)}, nil
}

type respuesta struct {
	estado int
	cuerpo []byte
	cab    http.Header
}

// hacer envía una petición y devuelve la respuesta cruda. Un estado >= 400 se convierte en *ErrorAPI.
func (c *Cliente) hacer(ctx context.Context, metodo, ruta string, consulta url.Values, cuerpo any) (respuesta, error) {
	destino := *c.base
	destino.Path += ruta
	destino.RawQuery = consulta.Encode()
	var lector io.Reader
	if cuerpo != nil {
		datos, err := json.Marshal(cuerpo)
		if err != nil {
			return respuesta{}, fmt.Errorf("runpod: serializar cuerpo: %w", err)
		}
		lector = bytes.NewReader(datos)
	}
	req, err := http.NewRequestWithContext(ctx, metodo, destino.String(), lector)
	if err != nil {
		return respuesta{}, fmt.Errorf("runpod: crear peticion: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.clave)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", agente)
	if cuerpo != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return respuesta{}, fmt.Errorf("runpod: %s %s: %s", metodo, ruta, c.redactor.Limpiar(errorSinURL(err)))
	}
	defer func() { _ = res.Body.Close() }()
	datos, err := io.ReadAll(io.LimitReader(res.Body, maxCuerpo))
	if err != nil {
		return respuesta{}, fmt.Errorf("runpod: leer respuesta de %s %s: %w", metodo, ruta, err)
	}
	r := respuesta{estado: res.StatusCode, cuerpo: datos, cab: res.Header}
	if res.StatusCode >= 400 {
		return r, c.errorAPI(metodo, ruta, r)
	}
	return r, nil
}

// errorSinURL devuelve el motivo de un fallo de transporte sin repetir la URL.
func errorSinURL(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

func (c *Cliente) errorAPI(metodo, ruta string, r respuesta) *ErrorAPI {
	var p struct{ Title, Detail string }
	_ = json.Unmarshal(r.cuerpo, &p) // un cuerpo ilegible no cambia el estado HTTP
	e := &ErrorAPI{Metodo: metodo, Ruta: ruta, Estado: r.estado,
		Titulo:  secreto.Recortar(c.redactor.Limpiar(p.Title), 60),
		Detalle: secreto.Recortar(c.redactor.Limpiar(p.Detail), maxDetalleError)}
	if seg, err := strconv.Atoi(r.cab.Get("Retry-After")); err == nil && seg >= 0 {
		e.ReintentarEn = time.Duration(seg) * time.Second
	}
	return e
}
