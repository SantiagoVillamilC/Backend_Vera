package inteligencia

import (
	"context"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/avisos"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
)

// SerieConsultable es una serie recurrente del usuario, con la regla que ya usa el motor de avisos.
// Los montos van en centavos enteros, como todo el dominio.
type SerieConsultable struct {
	IdentidadTipo  string
	IdentidadValor string
	Estado         string
	Cadencia       string
	CadenciaDias   int
	UltimoPago     time.Time
	ProximoFecha   *time.Time
	ProximoMonto   *int64
	Categoria      string
}

// SeriesDelUsuario arma el contexto de recurrencia con la historia disponible y devuelve las series
// ordenadas. Reutiliza `avisos.NuevoContexto`, que ya calcula las series con la regla viva, y no
// reimplementa detección, tolerancias ni cadencia.
func (s *Servicio) SeriesDelUsuario(ctx context.Context, usuarioID string, ahora time.Time) ([]SerieConsultable, error) {
	var series []SerieConsultable
	_, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) (struct{}, error) {
			contexto, err := s.contextoDeAvisos(ctx, q, usuarioID, ahora)
			if err != nil {
				return struct{}{}, err
			}
			series = seriesConsultables(contexto)
			return struct{}{}, nil
		})
	if err != nil {
		return nil, fmt.Errorf("inteligencia: series de %q: %w", usuarioID, err)
	}
	return series, nil
}

// seriesConsultables proyecta el contexto a la forma que publica el contrato, sin exponer identidad
// de terceros: la identidad es la llave o el nombre normalizado del destino del propio usuario.
func seriesConsultables(contexto avisos.Contexto) []SerieConsultable {
	categorias := categoriasDeLasSeries(contexto.Pagos)
	series := make([]SerieConsultable, 0, len(contexto.Series))
	for _, serie := range contexto.Series {
		consultable := SerieConsultable{
			IdentidadTipo:  serie.Identidad.Tipo,
			IdentidadValor: serie.Identidad.Valor,
			Estado:         string(serie.Estado),
			Cadencia:       serie.Cadencia,
			CadenciaDias:   serie.Indice.CadenciaDias,
			UltimoPago:     serie.UltimoPago,
			Categoria:      categorias[serie.Identidad],
		}
		if serie.Proximo != nil {
			fecha := serie.Proximo.Centro
			monto := serie.Proximo.MontoCentro
			consultable.ProximoFecha = &fecha
			consultable.ProximoMonto = &monto
		}
		series = append(series, consultable)
	}
	return series
}

// categoriasDeLasSeries toma la última categoría conocida de cada identidad. El pago más reciente
// manda: una serie puede haber cambiado de categoría entre meses y el endpoint publica la vigente.
func categoriasDeLasSeries(pagos []avisos.Pago) map[avisos.Identidad]string {
	categorias := map[avisos.Identidad]string{}
	instantes := map[avisos.Identidad]time.Time{}
	for _, pago := range pagos {
		if pago.Categoria == "" {
			continue
		}
		anterior, visto := instantes[pago.Identidad]
		if visto && !pago.OcurridoEn.After(anterior) {
			continue
		}
		instantes[pago.Identidad] = pago.OcurridoEn
		categorias[pago.Identidad] = pago.Categoria
	}
	return categorias
}
