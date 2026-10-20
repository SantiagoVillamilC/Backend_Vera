package inteligencia

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

// ErrServicioDeLecturaNulo reporta la construcción de un Handler sin el servicio que necesita.
var ErrServicioDeLecturaNulo = errors.New("inteligencia: NuevoHandler necesita un Servicio de lectura no nulo")

// ServicioDeLectura es lo que el handler necesita del servicio de inteligencia.
type ServicioDeLectura interface {
	Movimientos(ctx context.Context, usuarioID string, ancla *CursorDeMovimiento, limite int) (PaginaDeMovimientos, error)
	CorregirMovimiento(ctx context.Context, usuarioID string, transaccionID uuid.UUID, solicitud SolicitudDeCorreccion) (CorreccionAplicada, error)
	Resumen(ctx context.Context, usuarioID, mes string) (ResumenDelMes, error)
	AvisosDelUsuario(ctx context.Context, usuarioID, mes string, ahora time.Time) ([]AvisoEmitido, error)
	DescartarAviso(ctx context.Context, usuarioID string, avisoID uuid.UUID, ahora time.Time) (DescarteDeAviso, error)
}

// Handler sirve la vista enriquecida y el resumen de consumo del usuario autenticado.
type Handler struct {
	servicio ServicioDeLectura
	reloj    func() time.Time
}

// NuevoHandler construye el handler sobre el servicio de lectura y falla si el servicio es nulo.
func NuevoHandler(servicio ServicioDeLectura) *Handler {
	return NuevoHandlerConReloj(servicio, time.Now)
}

// NuevoHandlerConReloj construye el handler con un reloj inyectable para las pruebas.
func NuevoHandlerConReloj(servicio ServicioDeLectura, reloj func() time.Time) *Handler {
	if servicio == nil {
		panic(ErrServicioDeLecturaNulo)
	}
	if reloj == nil {
		reloj = time.Now
	}
	return &Handler{servicio: servicio, reloj: reloj}
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

// CorregirMovimientoInteligencia aplica una corrección de categoría o de nombre y devuelve el movimiento.
func (h *Handler) CorregirMovimientoInteligencia(w http.ResponseWriter, r *http.Request, transaccionID string) {
	usuarioID, ok := usuarioDeLaPeticion(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(transaccionID)
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "transaccion_id", Rule: "uuid"}})
		return
	}
	var cuerpo api.CorregirMovimientoInteligenciaJSONRequestBody
	if !api.DecodeAndValidate(w, r, &cuerpo) {
		return
	}
	solicitud, err := solicitudDeCorreccion(cuerpo)
	if err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "body", Rule: "at_least_one"}})
		return
	}
	resultado, err := h.servicio.CorregirMovimiento(r.Context(), usuarioID, id, solicitud)
	if err != nil {
		escribirErrorDeLaCorreccion(w, r, err)
		return
	}
	api.WriteData(w, http.StatusOK, respuestaDeCorreccion(resultado), nil)
}

func solicitudDeCorreccion(cuerpo api.InteligenciaCorreccionRequest) (SolicitudDeCorreccion, error) {
	solicitud := SolicitudDeCorreccion{}
	if cuerpo.Categoria != nil {
		categoria := Categoria(strings.TrimSpace(*cuerpo.Categoria))
		solicitud.Categoria = &categoria
	}
	if cuerpo.NombreComercio != nil {
		nombre := strings.TrimSpace(*cuerpo.NombreComercio)
		solicitud.NombreComercio = &nombre
	}
	if solicitud.Categoria == nil && solicitud.NombreComercio == nil {
		return SolicitudDeCorreccion{}, ErrCorreccionVacia
	}
	return solicitud, nil
}

func escribirErrorDeLaCorreccion(w http.ResponseWriter, r *http.Request, err error) {
	parametro := strconv.Itoa(LargoNombreComercio)
	switch {
	case errors.Is(err, ErrMovimientoNoEncontrado):
		api.WriteError(w, http.StatusNotFound, "NotFound", "Movement not found.", nil)
	case errors.Is(err, ErrCategoriaNoVisible):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "categoria", Rule: "categoria_visible"}})
	case errors.Is(err, ErrNombreNoAplica):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "nombre_comercio", Rule: "solo_comercio"}})
	case errors.Is(err, ErrNombreVacio):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "nombre_comercio", Rule: "min"}})
	case errors.Is(err, ErrNombreLargo):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "nombre_comercio", Rule: "max", Param: &parametro}})
	case errors.Is(err, ErrDestinoGenerico):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "transaccion_id", Rule: "destino_generico"}})
	case errors.Is(err, ErrCopiaSinDestino):
		api.WriteError(w, http.StatusConflict, "CopiaSinReconstruir",
			"The enriched copy must be rebuilt before correcting this movement.", nil)
	case errors.Is(err, ErrCorreccionOcupada):
		api.WriteError(w, http.StatusConflict, "CorreccionEnCurso",
			"Another enrichment process is running. Try again later.", nil)
	default:
		api.WriteAPIError(w, r, err)
	}
}

func respuestaDeCorreccion(resultado CorreccionAplicada) api.InteligenciaCorreccion {
	return api.InteligenciaCorreccion{
		Movimiento:     respuestaDeMovimiento(resultado.Movimiento),
		FilasCambiadas: resultado.FilasCambiadas,
	}
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

// ListarAvisosInteligencia responde las tarjetas vigentes del mes pedido.
func (h *Handler) ListarAvisosInteligencia(w http.ResponseWriter, r *http.Request,
	params api.ListarAvisosInteligenciaParams) {
	usuarioID, ok := usuarioDeLaPeticion(w, r)
	if !ok {
		return
	}
	mes := ""
	if params.Mes != nil {
		mes = strings.TrimSpace(*params.Mes)
	}
	delMes, err := h.servicio.AvisosDelUsuario(r.Context(), usuarioID, mes, h.reloj())
	if err != nil {
		if errors.Is(err, ErrMesInvalido) {
			api.WriteError(w, http.StatusBadRequest, "ValidationError", "Request validation failed.",
				[]api.FieldError{{Field: "mes", Rule: "format"}})
			return
		}
		api.WriteAPIError(w, r, err)
		return
	}
	respuesta := make([]api.InteligenciaAviso, 0, len(delMes))
	for _, aviso := range delMes {
		respuesta = append(respuesta, respuestaDeAviso(aviso))
	}
	api.WriteData(w, http.StatusOK, respuesta, nil)
}

// DescartarAvisoInteligencia marca un aviso propio como descartado.
func (h *Handler) DescartarAvisoInteligencia(w http.ResponseWriter, r *http.Request, id string) {
	usuarioID, ok := usuarioDeLaPeticion(w, r)
	if !ok {
		return
	}
	avisoID, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "id", Rule: "uuid"}})
		return
	}
	descarte, err := h.servicio.DescartarAviso(r.Context(), usuarioID, avisoID, h.reloj())
	if errors.Is(err, ErrAvisoNoEncontrado) {
		api.WriteError(w, http.StatusNotFound, "NotFound", "Notice not found.", nil)
		return
	}
	if err != nil {
		api.WriteAPIError(w, r, err)
		return
	}
	api.WriteData(w, http.StatusOK, api.InteligenciaAvisoDescarte{
		Id:           descarte.AvisoID,
		DescartadoEn: descarte.DescartadoEn,
	}, nil)
}

func respuestaDeAviso(aviso AvisoEmitido) api.InteligenciaAviso {
	respuesta := api.InteligenciaAviso{
		Id:                aviso.ID,
		Tipo:              api.InteligenciaAvisoTipo(aviso.Tipo),
		Clave:             aviso.Clave,
		Periodo:           aviso.Periodo.Format("2006-01-02"),
		SeveridadCentavos: strconv.FormatInt(aviso.SeveridadCentavos, 10),
		Prioridad:         aviso.Prioridad,
		Titulo:            aviso.Titulo,
		Texto:             aviso.Texto,
		Porque:            aviso.Porque,
		Accion:            aviso.Accion,
		CreadoEn:          aviso.CreadoEn,
	}
	if aviso.TransaccionID != nil {
		transaccion := *aviso.TransaccionID
		respuesta.TransaccionId = &transaccion
	}
	return respuesta
}
