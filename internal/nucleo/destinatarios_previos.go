package nucleo

import (
	"context"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
)

// DestinatariosPrevios pagina referencias de receptores de envíos propios liquidados.
// El cursor es la última referencia devuelta; ninguna llave telefónica se reinterpreta.
func (s *Servicio) DestinatariosPrevios(ctx context.Context, usuario, despues string) ([]string, error) {
	return enTransaccion(ctx, s.pool, func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) ([]string, error) {
		filas, err := q.DestinatariosPrevios(ctx, repositorio.DestinatariosPreviosParams{Usuario: usuario, Despues: despues})
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(filas))
		for _, f := range filas {
			if f.Valid {
				ids = append(ids, f.String)
			}
		}
		return ids, nil
	})
}
