// Package rielsim es el cliente HTTP del simulador del riel.
package rielsim

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/google/uuid"
)

// topeCuerpoRiel es el tamaño máximo del cuerpo que el lector acepta del simulador.
const topeCuerpoRiel = 64 * 1024

// tiempoEsperaRiel es el plazo por defecto del cliente, igual al plazo de entrega del outbox (Criterio 16).
const tiempoEsperaRiel = 20 * time.Second

// Riel habla con el simulador del riel por HTTP.
type Riel struct {
	base    string
	cliente *http.Client
}

// NuevoRiel arma el cliente con la URL base del simulador y un cliente; sin cliente usa uno con plazo.
func NuevoRiel(base string, cliente *http.Client) *Riel {
	if cliente == nil {
		cliente = &http.Client{Timeout: tiempoEsperaRiel}
	}
	return &Riel{base: strings.TrimRight(base, "/"), cliente: cliente}
}

// Enviar entrega la orden al simulador y traduce su respuesta al desenlace.
func (r *Riel) Enviar(ctx context.Context, orden nucleo.OrdenRiel) (nucleo.RespuestaRiel, error) {
	datos, err := json.Marshal(orden)
	if err != nil {
		return nucleo.RespuestaRiel{}, fmt.Errorf("rielsim: serializar la orden: %w", err)
	}
	return r.pedir(ctx, http.MethodPost, r.base+"/transferencias", datos, orden.TransferenciaID)
}

// ConsultarEstado devuelve el desenlace que el simulador registró para esa transferencia.
func (r *Riel) ConsultarEstado(ctx context.Context, transferenciaID uuid.UUID) (nucleo.RespuestaRiel, error) {
	return r.pedir(ctx, http.MethodGet, r.base+"/transferencias/"+transferenciaID.String(), nil, transferenciaID)
}

// Extracto pide al simulador las operaciones ocurridas en la ventana [desde, hasta).
func (r *Riel) Extracto(ctx context.Context, desde, hasta time.Time) (nucleo.ExtractoRiel, error) {
	destino, err := urlDeExtracto(r.base+"/extracto", desde, hasta)
	if err != nil {
		return nucleo.ExtractoRiel{}, fmt.Errorf("rielsim: armar la URL del extracto: %w", err)
	}
	peticion, err := http.NewRequestWithContext(ctx, http.MethodGet, destino, nil)
	if err != nil {
		return nucleo.ExtractoRiel{}, fmt.Errorf("rielsim: armar la petición del extracto: %w", err)
	}
	respuesta, err := r.cliente.Do(peticion)
	if err != nil {
		return nucleo.ExtractoRiel{}, fmt.Errorf("rielsim: pedir el extracto: %w", causaSinURL(err))
	}
	defer func() { _ = respuesta.Body.Close() }()
	if respuesta.StatusCode < 200 || respuesta.StatusCode >= 300 {
		return nucleo.ExtractoRiel{}, fmt.Errorf("rielsim: el extracto respondió %s", respuesta.Status)
	}
	return leerExtracto(respuesta.Body)
}

// pedir hace la petición, exige un 2xx y traduce el 404 a ErrRielSinRegistro.
func (r *Riel) pedir(ctx context.Context, metodo, destino string, cuerpo []byte, esperado uuid.UUID) (nucleo.RespuestaRiel, error) {
	peticion, err := http.NewRequestWithContext(ctx, metodo, destino, bytes.NewReader(cuerpo))
	if err != nil {
		return nucleo.RespuestaRiel{}, fmt.Errorf("rielsim: armar la petición: %w", causaSinURL(err))
	}
	if cuerpo != nil {
		peticion.Header.Set("Content-Type", "application/json")
	}
	respuesta, err := r.cliente.Do(peticion)
	if err != nil {
		return nucleo.RespuestaRiel{}, fmt.Errorf("rielsim: hablar con el riel: %w", causaSinURL(err))
	}
	defer func() { _ = respuesta.Body.Close() }()
	if respuesta.StatusCode == http.StatusNotFound {
		return nucleo.RespuestaRiel{}, nucleo.ErrRielSinRegistro
	}
	if respuesta.StatusCode < 200 || respuesta.StatusCode >= 300 {
		return nucleo.RespuestaRiel{}, fmt.Errorf("rielsim: el riel respondió %s", respuesta.Status)
	}
	return leerRespuesta(respuesta.Body, esperado)
}

// leerRespuesta decodifica y valida el contrato de la respuesta del riel.
func leerRespuesta(cuerpo io.Reader, esperado uuid.UUID) (nucleo.RespuestaRiel, error) {
	datos, err := io.ReadAll(io.LimitReader(cuerpo, topeCuerpoRiel+1))
	if err != nil {
		return nucleo.RespuestaRiel{}, fmt.Errorf("rielsim: leer la respuesta: %w", err)
	}
	if len(datos) > topeCuerpoRiel {
		return nucleo.RespuestaRiel{}, errors.New("rielsim: la respuesta del riel superó el tamaño máximo")
	}
	var respuesta nucleo.RespuestaRiel
	if err := json.Unmarshal(datos, &respuesta); err != nil {
		return nucleo.RespuestaRiel{}, fmt.Errorf("rielsim: leer la respuesta: %w", err)
	}
	if respuesta.TransferenciaID == uuid.Nil {
		return nucleo.RespuestaRiel{}, errors.New("rielsim: la respuesta llegó sin transferencia_id")
	}
	if respuesta.TransferenciaID != esperado {
		return nucleo.RespuestaRiel{}, fmt.Errorf("rielsim: la respuesta trae transferencia_id %s, se esperaba %s", respuesta.TransferenciaID, esperado)
	}
	if respuesta.Resultado != nucleo.ResultadoConfirmado && respuesta.Resultado != nucleo.ResultadoRechazado {
		return nucleo.RespuestaRiel{}, fmt.Errorf("rielsim: desenlace desconocido %q", respuesta.Resultado)
	}
	if respuesta.OcurridoEn.IsZero() {
		return nucleo.RespuestaRiel{}, errors.New("rielsim: la respuesta llegó sin ocurrido_en")
	}
	return respuesta, nil
}

// causaSinURL quita de la causa el *url.Error que arrastraría el userinfo de la URL.
func causaSinURL(err error) error {
	var conURL *url.Error
	if errors.As(err, &conURL) {
		return conURL.Err
	}
	return err
}

// urlDeExtracto agrega la ventana pedida como query del endpoint, en RFC 3339.
func urlDeExtracto(base string, desde, hasta time.Time) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("desde", desde.UTC().Format(time.RFC3339Nano))
	q.Set("hasta", hasta.UTC().Format(time.RFC3339Nano))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// extractoRielHTTP es la forma en el cable del extracto del riel.
type extractoRielHTTP struct {
	Corte       time.Time            `json:"corte"`
	Operaciones *[]operacionRielHTTP `json:"operaciones"`
}

// operacionRielHTTP es una fila del extracto, con el monto como cadena y el id global.
type operacionRielHTTP struct {
	TransferenciaID *uuid.UUID       `json:"transferencia_id"`
	Estado          string           `json:"estado"`
	MontoCentavos   *nucleo.Centavos `json:"monto_centavos"`
	OcurridoEn      time.Time        `json:"ocurrido_en"`
}

// leerExtracto decodifica y valida el contrato del extracto del riel.
func leerExtracto(cuerpo io.Reader) (nucleo.ExtractoRiel, error) {
	datos, err := io.ReadAll(io.LimitReader(cuerpo, topeCuerpoRiel+1))
	if err != nil {
		return nucleo.ExtractoRiel{}, fmt.Errorf("rielsim: leer el extracto: %w", err)
	}
	if len(datos) > topeCuerpoRiel {
		return nucleo.ExtractoRiel{}, errors.New("rielsim: el extracto superó el tamaño máximo")
	}
	var crudo extractoRielHTTP
	if err := json.Unmarshal(datos, &crudo); err != nil {
		return nucleo.ExtractoRiel{}, fmt.Errorf("rielsim: leer el extracto: %w", err)
	}
	if crudo.Corte.IsZero() {
		return nucleo.ExtractoRiel{}, errors.New("rielsim: el extracto llegó sin corte")
	}
	if crudo.Operaciones == nil {
		return nucleo.ExtractoRiel{}, errors.New("rielsim: el extracto llegó sin operaciones")
	}
	operaciones := make([]nucleo.OperacionDeExtractoRiel, 0, len(*crudo.Operaciones))
	for _, o := range *crudo.Operaciones {
		if o.TransferenciaID == nil || o.MontoCentavos == nil || o.Estado == "" || o.OcurridoEn.IsZero() {
			return nucleo.ExtractoRiel{}, errors.New("rielsim: el extracto trae una operación incompleta")
		}
		operaciones = append(operaciones, nucleo.OperacionDeExtractoRiel{
			TransferenciaID: *o.TransferenciaID,
			Estado:          o.Estado,
			Monto:           *o.MontoCentavos,
			OcurridoEn:      o.OcurridoEn.UTC(),
		})
	}
	return nucleo.ExtractoRiel{Corte: crudo.Corte.UTC(), Operaciones: operaciones}, nil
}

var _ nucleo.Riel = (*Riel)(nil)

var _ nucleo.LectorExtractoRiel = (*Riel)(nil)
