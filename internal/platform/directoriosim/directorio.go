// Package directoriosim es el cliente HTTP del directorio de llaves simulado.
package directoriosim

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
)

// tiempoEsperaDirectorio es el plazo máximo que el núcleo espera por la resolución de una llave.
const tiempoEsperaDirectorio = 5 * time.Second

// topeCuerpoDirectorio es el tamaño máximo del cuerpo que el lector acepta del simulador.
const topeCuerpoDirectorio = 16 * 1024

// Directorio resuelve llaves contra el endpoint /resolver del directorio simulado.
type Directorio struct {
	base    string
	cliente *http.Client
}

// NuevoDirectorio arma el cliente con la URL base del simulador y un cliente; sin cliente usa uno con plazo.
func NuevoDirectorio(base string, cliente *http.Client) *Directorio {
	if cliente == nil {
		cliente = &http.Client{Timeout: tiempoEsperaDirectorio}
	}
	return &Directorio{base: strings.TrimRight(base, "/"), cliente: cliente}
}

// resolucionHTTP es la forma en el cable: entidad y producto, sin ningún dato de la cuenta.
type resolucionHTTP struct {
	Entidad  string `json:"entidad"`
	Producto string `json:"producto"`
}

// Resolver consulta la entidad y el producto de la llave y traduce el 404 a ErrLlaveNoEncontrada.
func (d *Directorio) Resolver(ctx context.Context, llave nucleo.Llave) (nucleo.ResolucionDirectorio, error) {
	peticion, err := http.NewRequestWithContext(ctx, http.MethodGet, urlDeResolucion(d.base, llave), nil)
	if err != nil {
		return nucleo.ResolucionDirectorio{}, fmt.Errorf("directoriosim: armar la petición: %w", causaSinURL(err))
	}
	respuesta, err := d.cliente.Do(peticion)
	if err != nil {
		return nucleo.ResolucionDirectorio{}, fmt.Errorf("%w: hablar con el directorio: %w", nucleo.ErrDirectorioSinRespuesta, causaSinURL(err))
	}
	defer func() { _ = respuesta.Body.Close() }()
	if respuesta.StatusCode == http.StatusNotFound {
		return nucleo.ResolucionDirectorio{}, nucleo.ErrLlaveNoEncontrada
	}
	if respuesta.StatusCode < 200 || respuesta.StatusCode >= 300 {
		return nucleo.ResolucionDirectorio{}, fmt.Errorf("%w: el directorio respondió %s", nucleo.ErrDirectorioSinRespuesta, respuesta.Status)
	}
	return leerResolucion(respuesta.Body)
}

// leerResolucion decodifica el contrato y rechaza cualquier campo ajeno, en particular una cuenta.
func leerResolucion(cuerpo io.Reader) (nucleo.ResolucionDirectorio, error) {
	datos, err := io.ReadAll(io.LimitReader(cuerpo, topeCuerpoDirectorio+1))
	if err != nil {
		return nucleo.ResolucionDirectorio{}, fmt.Errorf("%w: leer la respuesta: %w", nucleo.ErrDirectorioSinRespuesta, err)
	}
	if len(datos) > topeCuerpoDirectorio {
		return nucleo.ResolucionDirectorio{}, fmt.Errorf("%w: la respuesta superó el tamaño máximo", nucleo.ErrDirectorioSinRespuesta)
	}
	var crudo resolucionHTTP
	decodificador := json.NewDecoder(bytes.NewReader(datos))
	decodificador.DisallowUnknownFields()
	if err := decodificador.Decode(&crudo); err != nil {
		return nucleo.ResolucionDirectorio{}, fmt.Errorf("%w: leer la resolución: %w", nucleo.ErrDirectorioSinRespuesta, err)
	}
	if strings.TrimSpace(crudo.Entidad) == "" || strings.TrimSpace(crudo.Producto) == "" {
		return nucleo.ResolucionDirectorio{}, fmt.Errorf("%w: la resolución llegó incompleta", nucleo.ErrDirectorioSinRespuesta)
	}
	return nucleo.ResolucionDirectorio{Entidad: crudo.Entidad, Producto: crudo.Producto}, nil
}

// urlDeResolucion arma la URL con tipo y valor como query, sin tocar el resto de la URL.
func urlDeResolucion(base string, llave nucleo.Llave) string {
	q := url.Values{}
	q.Set("tipo", llave.Tipo)
	q.Set("valor", llave.Valor)
	return base + "/resolver?" + q.Encode()
}

// causaSinURL quita de la causa el *url.Error que arrastraría el userinfo de la URL.
func causaSinURL(err error) error {
	var conURL *url.Error
	if errors.As(err, &conURL) {
		return conURL.Err
	}
	return err
}

var _ nucleo.DirectorioLlaves = (*Directorio)(nil)
