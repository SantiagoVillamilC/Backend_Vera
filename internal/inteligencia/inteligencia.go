package inteligencia

import (
	"context"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Servicio expone el catálogo y el cursor del lote de enriquecimiento.
type Servicio struct {
	pool         abridorDeTransacciones
	clasificador *Modelo
}

// OpcionServicio ajusta el servicio al construirlo.
type OpcionServicio func(*Servicio)

// ConClasificador enciende el escalón 6 con el modelo cargado.
func ConClasificador(modelo *Modelo) OpcionServicio {
	return func(s *Servicio) { s.clasificador = modelo }
}

// NuevoServicio arma el servicio sobre el pool de la aplicación.
func NuevoServicio(pool *pgxpool.Pool, opciones ...OpcionServicio) *Servicio {
	servicio := &Servicio{pool: poolDeInteligencia{pool: pool}}
	for _, opcion := range opciones {
		opcion(servicio)
	}
	return servicio
}

// Categorias devuelve el catálogo sembrado, en el orden firmado.
func (s *Servicio) Categorias(ctx context.Context) ([]EntradaCatalogo, error) {
	filas, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) ([]repositorio.Categoria, error) {
			return q.ListarCategorias(ctx)
		})
	if err != nil {
		return nil, fmt.Errorf("inteligencia: listar categorías: %w", err)
	}
	categorias := make([]EntradaCatalogo, 0, len(filas))
	for _, fila := range filas {
		categorias = append(categorias, EntradaCatalogo{
			Codigo: Categoria(fila.Codigo),
			Nombre: fila.Nombre,
			Grupo:  fila.Grupo,
			Orden:  fila.Orden,
		})
	}
	return categorias, nil
}

// EnriquecerAsientos procesa un lote y devuelve cuántos asientos miró, para el worker de River.
func (s *Servicio) EnriquecerAsientos(ctx context.Context, limite int) (int, error) {
	resultado, err := s.Enriquecer(ctx, limite)
	if err != nil {
		return 0, err
	}
	return resultado.Asientos, nil
}

// EstadoDelCursor es la marca de agua del lote de enriquecimiento.
type EstadoDelCursor struct {
	UltimoEntryID int64
	ActualizadoEn time.Time
}

// Cursor lee la marca de agua vigente.
func (s *Servicio) Cursor(ctx context.Context) (EstadoDelCursor, error) {
	fila, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) (repositorio.LeerCursorRow, error) {
			return q.LeerCursor(ctx)
		})
	if err != nil {
		return EstadoDelCursor{}, fmt.Errorf("inteligencia: leer cursor: %w", err)
	}
	return EstadoDelCursor{UltimoEntryID: fila.UltimoEntryID, ActualizadoEn: fila.ActualizadoEn.Time}, nil
}

// AvanzarCursor mueve la marca de agua hacia adelante. Un valor menor o igual no la retrocede.
func (s *Servicio) AvanzarCursor(ctx context.Context, ultimoEntryID int64) error {
	_, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) (struct{}, error) {
			return struct{}{}, q.AvanzarCursor(ctx, ultimoEntryID)
		})
	if err != nil {
		return fmt.Errorf("inteligencia: avanzar cursor a %d: %w", ultimoEntryID, err)
	}
	return nil
}
