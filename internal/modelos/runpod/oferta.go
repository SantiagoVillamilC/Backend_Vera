package runpod

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Oferta es lo que el catálogo dice de una GPU en la nube SECURE, solo lectura.
type Oferta struct {
	GPU            string
	Encontrada     bool
	Secure         bool
	Disponibilidad string // NONE, LOW, MEDIUM o HIGH. Vacío si el catálogo no la informó.
	TarifaSecure   float64
	TarifaLegible  bool
}

// HayExistencias es falso con NONE o con una disponibilidad desconocida (falla cerrado).
func (o Oferta) HayExistencias() bool {
	switch o.Disponibilidad {
	case "LOW", "MEDIUM", "HIGH":
		return true
	}
	return false
}

// Oferta consulta GET /v2/catalog/gpus?include=AVAILABILITY&product=POD&cloud=SECURE.
func (c *Cliente) Oferta(ctx context.Context, gpu string) (Oferta, error) {
	consulta := url.Values{"include": {"AVAILABILITY"}, "product": {"POD"}, "cloud": {"SECURE"}}
	r, err := c.hacer(ctx, http.MethodGet, "/catalog/gpus", consulta, nil)
	if err != nil {
		return Oferta{}, err
	}
	var catalogo struct {
		GPUs []struct {
			ID     string `json:"id"`
			Secure bool   `json:"secure"`
			Precio struct {
				Secure *float64 `json:"secure"`
			} `json:"price"`
			Disponibilidad string `json:"availability"`
		} `json:"gpus"`
	}
	if err := json.Unmarshal(r.cuerpo, &catalogo); err != nil {
		return Oferta{}, fmt.Errorf("%w: catalogo de GPU", ErrRespuestaInvalida)
	}
	for _, g := range catalogo.GPUs {
		if g.ID != gpu {
			continue
		}
		o := Oferta{GPU: gpu, Encontrada: true, Secure: g.Secure, Disponibilidad: g.Disponibilidad}
		if g.Precio.Secure != nil && *g.Precio.Secure > 0 {
			o.TarifaSecure, o.TarifaLegible = *g.Precio.Secure, true
		}
		return o, nil
	}
	return Oferta{GPU: gpu}, nil
}
