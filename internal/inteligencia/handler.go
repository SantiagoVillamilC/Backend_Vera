package inteligencia

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
)

// ErrServicioDeLecturaNulo reporta la construcción de un Handler sin el servicio que necesita.
var ErrServicioDeLecturaNulo = errors.New("inteligencia: NuevoHandler necesita un Servicio de lectura no nulo")

// ServicioDeLectura es lo que el handler necesita del servicio de inteligencia.
type ServicioDeLectura interface {
	Movimientos(ctx context.Context, usuarioID string, ancla *CursorDeMovimiento, limite int) (PaginaDeMovimientos, error)
	Resumen(ctx context.Context, usuarioID, mes string) (ResumenDelMes, error)
}

// Handler sirve la vista enriquecida y el resumen de consumo del usuario autenticado.
type Handler struct {
	servicio ServicioDeLectura
}

// NuevoHandler construye el handler sobre el servicio de lectura y falla si el servicio es nulo.
func NuevoHandler(servicio ServicioDeLectura) *Handler {
	if servicio == nil {
		panic(ErrServicioDeLecturaNulo)
	}
	return &Handler{servicio: servicio}
}

// ListarMovimientosInteligencia responde una página de la vista enriquecida, paginada por cursor opaco.
func (h *Handler) ListarMovimientosInteligencia(w http.ResponseWriter, r *http.Request,
	params api.ListarMovimientosInteligenciaParams) {
	usuarioID, ok := usuarioDeLaPeticion(w, r)
	if !ok {
		return
	}
	limite, err := limiteDeLaPagina(params.Limit)
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "ValidationError", "limit must be a positive integer", nil)
		return
	}
	var ancla *CursorDeMovimiento
	if params.Cursor != nil && *params.Cursor != "" {
		decodificado, err := DecodificarCursor(*params.Cursor)
		if err != nil {
			api.WriteError(w, http.StatusBadRequest, "ValidationError",
				"cursor must be a value returned in meta.next_cursor", nil)
			return
		}
		ancla = &decodificado
	}

	pagina, err := h.servicio.Movimientos(r.Context(), usuarioID, ancla, limite)
	if err != nil {
		api.WriteAPIError(w, r, err)
		return
	}
	movimientos := make([]api.InteligenciaMovimiento, 0, len(pagina.Movimientos))
	for _, movimiento := range pagina.Movimientos {
		movimientos = append(movimientos, respuestaDeMovimiento(movimiento))
	}
	api.WriteDataConCursor(w, http.StatusOK, movimientos, &api.MetaCursor{
		Limit:      limite,
		NextCursor: pagina.Siguiente,
		HasMore:    pagina.HayMas,
	})
}

// ResumenInteligencia responde el gasto del mes por categoría y la referencia de los meses completos.
func (h *Handler) ResumenInteligencia(w http.ResponseWriter, r *http.Request, params api.ResumenInteligenciaParams) {
	usuarioID, ok := usuarioDeLaPeticion(w, r)
	if !ok {
		return
	}
	mes := ""
	if params.Mes != nil {
		mes = *params.Mes
	}
	resumen, err := h.servicio.Resumen(r.Context(), usuarioID, mes)
	if errors.Is(err, ErrMesInvalido) {
		api.WriteError(w, http.StatusBadRequest, "ValidationError", "mes must use the YYYY-MM format", nil)
		return
	}
	if err != nil {
		api.WriteAPIError(w, r, err)
		return
	}
	api.WriteData(w, http.StatusOK, respuestaDeResumen(resumen), nil)
}

func usuarioDeLaPeticion(w http.ResponseWriter, r *http.Request) (string, bool) {
	usuarioID, ok := identidad.UsuarioIDDeContexto(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
		return "", false
	}
	return usuarioID, true
}

func limiteDeLaPagina(valor *string) (int, error) {
	if valor == nil || *valor == "" {
		return LimiteMovimientosPorDefecto, nil
	}
	limite, err := strconv.Atoi(*valor)
	if err != nil || limite < 1 {
		return 0, errors.New("limit must be a positive integer")
	}
	if limite > LimiteMovimientosMaximo {
		limite = LimiteMovimientosMaximo
	}
	return limite, nil
}

func respuestaDeMovimiento(movimiento MovimientoDeLectura) api.InteligenciaMovimiento {
	respuesta := api.InteligenciaMovimiento{
		TransaccionId:       movimiento.TransaccionID,
		OcurridoEn:          movimiento.OcurridoEn,
		Moneda:              api.InteligenciaMovimientoMoneda(movimiento.Moneda),
		Direccion:           api.InteligenciaMovimientoDireccion(movimiento.Direccion),
		MontoCentavos:       strconv.FormatInt(movimiento.MontoCentavos, 10),
		Contraparte:         movimiento.Contraparte,
		ContraparteResuelta: movimiento.ContraparteResuelta,
		Categoria:           movimiento.Categoria,
		CategoriaNombre:     movimiento.CategoriaNombre,
		OrigenCategoria:     api.InteligenciaMovimientoOrigenCategoria(movimiento.OrigenCategoria),
	}
	if movimiento.Descripcion != "" {
		descripcion := movimiento.Descripcion
		respuesta.Descripcion = &descripcion
	}
	return respuesta
}

func respuestaDeResumen(resumen ResumenDelMes) api.InteligenciaResumen {
	categorias := make([]api.InteligenciaResumenCategoria, 0, len(resumen.Categorias))
	for _, categoria := range resumen.Categorias {
		respuesta := api.InteligenciaResumenCategoria{
			Codigo:        categoria.Codigo,
			Nombre:        categoria.Nombre,
			TotalCentavos: strconv.FormatInt(categoria.TotalCentavos, 10),
			MesesConGasto: categoria.MesesConGasto,
		}
		if categoria.MedianaCentavos != nil {
			mediana := strconv.FormatInt(*categoria.MedianaCentavos, 10)
			respuesta.MedianaCentavos = &mediana
		}
		categorias = append(categorias, respuesta)
	}
	return api.InteligenciaResumen{
		Mes:             resumen.Mes,
		Desde:           resumen.Desde,
		Hasta:           resumen.Hasta,
		Alcance:         api.InteligenciaResumenAlcance(resumen.Alcance),
		Moneda:          api.InteligenciaResumenMoneda(resumen.Moneda),
		Evaluable:       resumen.Evaluable,
		MesesCompletos:  resumen.MesesCompletos,
		MesesRequeridos: resumen.MesesRequeridos,
		TotalCentavos:   strconv.FormatInt(resumen.TotalCentavos, 10),
		CupoAvisos:      resumen.CupoAvisos,
		AvisosEmitidos:  resumen.AvisosEmitidos,
		Categorias:      categorias,
	}
}
