// Package custodiosim es el cliente HTTP del extracto que publica el simulador de custodia.
package custodiosim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
)

// tiempoEsperaExtracto es el plazo máximo que el núcleo espera por el corte del simulador.
const tiempoEsperaExtracto = 10 * time.Second

// topeCuerpoExtracto es el tamaño máximo del cuerpo que el lector acepta del simulador.
const topeCuerpoExtracto = 64 * 1024

// LectorExtracto consulta GET /extracto del simulador de custodia.
type LectorExtracto struct {
	url     string
	cliente *http.Client
}

// NuevoLectorExtracto arma el lector con la URL del endpoint y un cliente HTTP; sin cliente usa uno con plazo.
func NuevoLectorExtracto(url string, cliente *http.Client) *LectorExtracto {
	if cliente == nil {
		cliente = &http.Client{Timeout: tiempoEsperaExtracto}
	}
	return &LectorExtracto{url: url, cliente: cliente}
}

// corteHTTP es la forma en el cable del corte: los centavos viajan como cadena, nunca como número.
type corteHTTP struct {
	CorteEn       time.Time        `json:"corte_en"`
	SaldoCentavos *nucleo.Centavos `json:"saldo_centavos"`
	Origen        string           `json:"origen"`
}

// Extracto pide el corte completo al instante pedido y rechaza una respuesta sin status 2xx o sin el contrato.
func (l *LectorExtracto) Extracto(ctx context.Context, corteEn time.Time) (nucleo.ExtractoCustodio, error) {
	destino, err := urlDeCorte(l.url, corteEn)
	if err != nil {
		return nucleo.ExtractoCustodio{}, fmt.Errorf("custodiosim: armar la URL del extracto: %w", err)
	}
	peticion, err := http.NewRequestWithContext(ctx, http.MethodGet, destino, nil)
	if err != nil {
		return nucleo.ExtractoCustodio{}, fmt.Errorf("custodiosim: armar la petición del extracto: %w", err)
	}
	respuesta, err := l.cliente.Do(peticion)
	if err != nil {
		return nucleo.ExtractoCustodio{}, fmt.Errorf("custodiosim: pedir el extracto: %w", causaSinURL(err))
	}
	defer func() { _ = respuesta.Body.Close() }()
	if respuesta.StatusCode < 200 || respuesta.StatusCode >= 300 {
		return nucleo.ExtractoCustodio{}, fmt.Errorf("custodiosim: el extracto respondió %s", respuesta.Status)
	}

	datos, err := io.ReadAll(io.LimitReader(respuesta.Body, topeCuerpoExtracto+1))
	if err != nil {
		return nucleo.ExtractoCustodio{}, fmt.Errorf("custodiosim: leer el extracto: %w", err)
	}
	if len(datos) > topeCuerpoExtracto {
		return nucleo.ExtractoCustodio{}, errors.New("custodiosim: el extracto superó el tamaño máximo")
	}

	var corte corteHTTP
	if err := json.Unmarshal(datos, &corte); err != nil {
		return nucleo.ExtractoCustodio{}, fmt.Errorf("custodiosim: leer el extracto: %w", err)
	}
	if corte.CorteEn.IsZero() {
		return nucleo.ExtractoCustodio{}, errors.New("custodiosim: el extracto llegó sin corte_en")
	}
	if corte.SaldoCentavos == nil {
		return nucleo.ExtractoCustodio{}, errors.New("custodiosim: el extracto llegó sin saldo_centavos")
	}
	if corte.Origen == "" {
		return nucleo.ExtractoCustodio{}, errors.New("custodiosim: el extracto llegó sin origen")
	}
	return nucleo.ExtractoCustodio{
		CorteEn: corte.CorteEn,
		Saldo:   *corte.SaldoCentavos,
		Origen:  corte.Origen,
	}, nil
}

// urlDeCorte agrega el corte pedido como query del endpoint, sin tocar el resto de la URL.
func urlDeCorte(base string, corteEn time.Time) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("corte_en", corteEn.UTC().Format(time.RFC3339Nano))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// causaSinURL quita de la causa el *url.Error que arrastraría el userinfo de la URL.
func causaSinURL(err error) error {
	var conURL *url.Error
	if errors.As(err, &conURL) {
		return conURL.Err
	}
	return err
}

var _ nucleo.LectorExtractoCustodio = (*LectorExtracto)(nil)
