package inteligencia

import (
	"context"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/avisos"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Servicio expone el catálogo y el cursor del lote de enriquecimiento.
type Servicio struct {
	pool            abridorDeTransacciones
	clasificador    *Modelo
	avisosCfg       avisos.Configuracion
	metricas        Metricas
	lexico          *Lexico
	catalogoNombres map[string]Categoria
	catalogoLlaves  map[string]Categoria
}

// OpcionServicio ajusta el servicio al construirlo.
type OpcionServicio func(*Servicio)

// ConClasificador enciende el escalón 6 con el modelo cargado.
func ConClasificador(modelo *Modelo) OpcionServicio {
	return func(s *Servicio) { s.clasificador = modelo }
}

// ConCatalogo enciende el escalón 4 sobre los mapas que CargarCatalogo llena.
func ConCatalogo(nombres, llaves map[string]Categoria) OpcionServicio {
	return func(s *Servicio) {
		s.catalogoNombres = nombres
		s.catalogoLlaves = llaves
	}
}

// ConMetricas inyecta el registro de métricas del contexto. Sin él no se mide nada.
func ConMetricas(metricas Metricas) OpcionServicio {
	return func(s *Servicio) {
		if metricas != nil {
			s.metricas = metricas
		}
	}
}

// NuevoServicio arma el servicio sobre el pool de la aplicación.
func NuevoServicio(pool *pgxpool.Pool, opciones ...OpcionServicio) *Servicio {
	servicio := &Servicio{
		pool:      poolDeInteligencia{pool: pool},
		avisosCfg: avisos.ConfiguracionPorDefecto(),
		metricas:  SinMetricas(),
	}
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

// CargarCatalogo lee la tabla curada y llena los mapas que recibió ConCatalogo. Vacía los dos mapas
// antes de llenarlos, así una segunda llamada no deja filas viejas. Tampoco deja el escalón
// encendido si la tabla está vacía: un catálogo sin filas decide lo mismo que no tenerlo. No vuelve
// a filtrar por categoría: la tabla solo admite las visibles, porque la base lo exige.
func (s *Servicio) CargarCatalogo(ctx context.Context) error {
	filas, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeInteligencia,
			q *repositorio.Queries) ([]repositorio.ListarCatalogoMarcaRow, error) {
			return q.ListarCatalogoMarca(ctx)
		})
	if err != nil {
		return fmt.Errorf("inteligencia: listar el catálogo curado: %w", err)
	}
	if s.catalogoNombres == nil {
		s.catalogoNombres = make(map[string]Categoria, len(filas))
	}
	if s.catalogoLlaves == nil {
		s.catalogoLlaves = make(map[string]Categoria, len(filas))
	}
	clear(s.catalogoNombres)
	clear(s.catalogoLlaves)
	for _, fila := range filas {
		categoria := Categoria(fila.Categoria)
		s.catalogoNombres[fila.NombreNormalizado] = categoria
		if fila.Llave.Valid && fila.Llave.String != "" {
			s.catalogoLlaves[fila.Llave.String] = categoria
		}
	}
	return nil
}

// catalogo arma los mapas que la cascada consulta en el escalón 4.
func (s *Servicio) catalogo() CatalogoCurado {
	return CatalogoCurado{Nombres: s.catalogoNombres, Llaves: s.catalogoLlaves}
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
