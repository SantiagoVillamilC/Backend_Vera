package inteligencia

import (
	"context"
	"fmt"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// FilaCatalogoMarca es una fila del archivo curado ya validada. El nombre va en la forma en que la
// cascada lo busca, que es ClaveDeCatalogo del nombre original.
type FilaCatalogoMarca struct {
	Nombre    string
	Categoria Categoria
	Llave     string
}

// ConteosCatalogoMarca es lo que dejó escrito una carga del catálogo curado.
type ConteosCatalogoMarca struct {
	Insertadas   int64
	Actualizadas int64
	Borradas     int64
}

// CategoriasSembradas lee los códigos que admite el catálogo curado. La regla es la misma que
// aplica el disparador de la tabla, así que el cargador rechaza el archivo por la misma razón por la
// que la base rechazaría la fila.
func CategoriasSembradas(ctx context.Context, pool *pgxpool.Pool) (map[string]struct{}, error) {
	codigos, err := enTransaccion(ctx, poolDeInteligencia{pool: pool},
		func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) ([]string, error) {
			return q.ListarCodigosDeCategoria(ctx)
		})
	if err != nil {
		return nil, fmt.Errorf("inteligencia: listar las categorías sembradas: %w", err)
	}
	return conjuntoVisible(codigos)
}

// conjuntoVisible arma el conjunto de códigos que el cargador acepta. Una lista vacía es un error:
// sin categorías visibles no hay contra qué validar el archivo.
func conjuntoVisible(codigos []string) (map[string]struct{}, error) {
	if len(codigos) == 0 {
		return nil, fmt.Errorf("inteligencia: inteligencia.categoria no tiene categorías visibles")
	}
	validas := make(map[string]struct{}, len(codigos))
	for _, codigo := range codigos {
		validas[codigo] = struct{}{}
	}
	return validas, nil
}

// GuardarCatalogoMarca escribe el catálogo curado en una sola transacción: el upsert es idempotente
// y el borrado solo toca las filas del mismo origen que ya no están en el archivo. La versión se
// escribe, pero no filtra el borrado: una fila retirada conserva la versión con la que entró, así
// que filtrar por ella la dejaría en la tabla para siempre.
func GuardarCatalogoMarca(ctx context.Context, pool *pgxpool.Pool, filas []FilaCatalogoMarca,
	origen, version string) (ConteosCatalogoMarca, error) {
	conteos, err := enTransaccion(ctx, poolDeInteligencia{pool: pool},
		func(ctx context.Context, _ transaccionDeInteligencia,
			q *repositorio.Queries) (ConteosCatalogoMarca, error) {
			var cuenta ConteosCatalogoMarca
			nombres := make([]string, 0, len(filas))
			for _, fila := range filas {
				var llave pgtype.Text
				if fila.Llave != "" {
					llave = pgtype.Text{String: fila.Llave, Valid: true}
				}
				insertada, err := q.UpsertCatalogoMarca(ctx, repositorio.UpsertCatalogoMarcaParams{
					NombreNormalizado: fila.Nombre,
					Categoria:         string(fila.Categoria),
					Llave:             llave,
					Origen:            origen,
					Version:           version,
				})
				if err != nil {
					return ConteosCatalogoMarca{}, fmt.Errorf("inteligencia: escribir %q en el catálogo: %w",
						fila.Nombre, err)
				}
				if insertada {
					cuenta.Insertadas++
				} else {
					cuenta.Actualizadas++
				}
				nombres = append(nombres, fila.Nombre)
			}
			borradas, err := q.BorrarCatalogoMarcaFueraDe(ctx, repositorio.BorrarCatalogoMarcaFueraDeParams{
				Origen:  origen,
				Nombres: nombres,
			})
			if err != nil {
				return ConteosCatalogoMarca{}, fmt.Errorf("inteligencia: borrar el catálogo fuera del archivo: %w",
					err)
			}
			cuenta.Borradas = borradas
			return cuenta, nil
		})
	if err != nil {
		return ConteosCatalogoMarca{}, err
	}
	return conteos, nil
}
