package inteligencia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

const usuarioDePrueba = "9f1c2b3a-4d5e-4f60-8a71-2b3c4d5e6f70"

type servicioDeLecturaFalso struct {
	pagina  PaginaDeMovimientos
	resumen ResumenDelMes
	err     error

	llamado bool
	usuario string
	limite  int
	ancla   *CursorDeMovimiento
	mes     string
}

func (f *servicioDeLecturaFalso) Movimientos(_ context.Context, usuarioID string, ancla *CursorDeMovimiento,
	limite int) (PaginaDeMovimientos, error) {
	f.llamado = true
	f.usuario = usuarioID
	f.ancla = ancla
	f.limite = limite
	return f.pagina, f.err
}

func (f *servicioDeLecturaFalso) Resumen(_ context.Context, usuarioID, mes string) (ResumenDelMes, error) {
	f.llamado = true
	f.usuario = usuarioID
	f.mes = mes
	return f.resumen, f.err
}

func TestNuevoHandlerConServicioNuloFallaAlConstruir(t *testing.T) {
	defer func() {
		recuperado := recover()
		if recuperado == nil {
			t.Fatal("NuevoHandler(nil) no falló")
		}
		err, ok := recuperado.(error)
		if !ok || !errors.Is(err, ErrServicioDeLecturaNulo) {
			t.Fatalf("NuevoHandler(nil) falló con %v, quiero %v", recuperado, ErrServicioDeLecturaNulo)
		}
	}()
	NuevoHandler(nil)
}

func TestListarMovimientosExigeIdentidad(t *testing.T) {
	t.Parallel()

	falso := &servicioDeLecturaFalso{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/inteligencia/movimientos", nil)
	NuevoHandler(falso).ListarMovimientosInteligencia(rec, req, api.ListarMovimientosInteligenciaParams{})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("código = %d, want 401", rec.Code)
	}
	if falso.llamado {
		t.Error("el servicio se llamó sin identidad")
	}
	assertErrorDelSobre(t, rec, "Unauthorized")
}

func TestListarMovimientosValidaLimiteYCursor(t *testing.T) {
	t.Parallel()

	texto := func(valor string) *string { return &valor }
	cursorValido := CodificarCursor(CursorDeMovimiento{
		OcurridoEn:    time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
		TransaccionID: uuid.MustParse("123e4567-e89b-42d3-a456-426614174000"),
	})
	casos := map[string]struct {
		params   api.ListarMovimientosInteligenciaParams
		codigo   int
		limite   int
		conAncla bool
	}{
		"sin parámetros usa el tope por defecto": {params: api.ListarMovimientosInteligenciaParams{}, codigo: http.StatusOK, limite: LimiteMovimientosPorDefecto},
		"límite válido":                          {params: api.ListarMovimientosInteligenciaParams{Limit: texto("10")}, codigo: http.StatusOK, limite: 10},
		"límite sobre el tope se recorta":        {params: api.ListarMovimientosInteligenciaParams{Limit: texto("1000")}, codigo: http.StatusOK, limite: LimiteMovimientosMaximo},
		"límite cero":                            {params: api.ListarMovimientosInteligenciaParams{Limit: texto("0")}, codigo: http.StatusBadRequest},
		"límite negativo":                        {params: api.ListarMovimientosInteligenciaParams{Limit: texto("-3")}, codigo: http.StatusBadRequest},
		"límite no numérico":                     {params: api.ListarMovimientosInteligenciaParams{Limit: texto("abc")}, codigo: http.StatusBadRequest},
		"cursor válido":                          {params: api.ListarMovimientosInteligenciaParams{Cursor: &cursorValido}, codigo: http.StatusOK, limite: LimiteMovimientosPorDefecto, conAncla: true},
		"cursor inventado":                       {params: api.ListarMovimientosInteligenciaParams{Cursor: texto("basura")}, codigo: http.StatusBadRequest},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			falso := &servicioDeLecturaFalso{}
			rec := httptest.NewRecorder()
			req := peticionConIdentidad(t, "/v1/inteligencia/movimientos")
			NuevoHandler(falso).ListarMovimientosInteligencia(rec, req, caso.params)

			if rec.Code != caso.codigo {
				t.Fatalf("código = %d, want %d: %s", rec.Code, caso.codigo, rec.Body.String())
			}
			if caso.codigo != http.StatusOK {
				assertErrorDelSobre(t, rec, "ValidationError")
				if falso.llamado {
					t.Error("el servicio se llamó con un parámetro inválido")
				}
				return
			}
			if !falso.llamado {
				t.Fatal("el servicio no se llamó")
			}
			if falso.usuario != usuarioDePrueba {
				t.Errorf("usuario = %q, want %q", falso.usuario, usuarioDePrueba)
			}
			if falso.limite != caso.limite {
				t.Errorf("límite = %d, want %d", falso.limite, caso.limite)
			}
			if caso.conAncla {
				if falso.ancla == nil || !falso.ancla.OcurridoEn.Equal(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)) {
					t.Errorf("ancla = %+v, want el cursor decodificado", falso.ancla)
				}
			} else if falso.ancla != nil {
				t.Errorf("ancla = %+v, want nil", falso.ancla)
			}
		})
	}
}

func TestListarMovimientosDevuelveElSobreYElCursor(t *testing.T) {
	t.Parallel()

	siguiente := "cursor-opaco"
	transaccion := uuid.MustParse("123e4567-e89b-42d3-a456-426614174000")
	falso := &servicioDeLecturaFalso{
		pagina: PaginaDeMovimientos{
			Movimientos: []MovimientoDeLectura{{
				TransaccionID:       transaccion,
				OcurridoEn:          time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
				Moneda:              "COP",
				Direccion:           "salida",
				MontoCentavos:       150000,
				Contraparte:         "Tienda Sintética",
				ContraparteResuelta: true,
				Categoria:           "compras",
				CategoriaNombre:     "Compras",
				OrigenCategoria:     "memoria",
			}},
			Siguiente: &siguiente,
			HayMas:    true,
		},
	}
	rec := httptest.NewRecorder()
	req := peticionConIdentidad(t, "/v1/inteligencia/movimientos")
	NuevoHandler(falso).ListarMovimientosInteligencia(rec, req, api.ListarMovimientosInteligenciaParams{})

	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if contenido := rec.Header().Get("Content-Type"); !strings.HasPrefix(contenido, "application/json") {
		t.Fatalf("Content-Type = %q", contenido)
	}
	cuerpo := rec.Body.String()
	for _, clave := range []string{`"success":true`, `"error":null`, `"descripcion":null`, `"monto_centavos":"150000"`} {
		if !strings.Contains(cuerpo, clave) {
			t.Errorf("el cuerpo no contiene %s: %s", clave, cuerpo)
		}
	}

	var respuesta struct {
		Data []struct {
			TransaccionID       string  `json:"transaccion_id"`
			Moneda              string  `json:"moneda"`
			Direccion           string  `json:"direccion"`
			MontoCentavos       string  `json:"monto_centavos"`
			Contraparte         string  `json:"contraparte"`
			ContraparteResuelta bool    `json:"contraparte_resuelta"`
			Descripcion         *string `json:"descripcion"`
			Categoria           string  `json:"categoria"`
			CategoriaNombre     string  `json:"categoria_nombre"`
			OrigenCategoria     string  `json:"origen_categoria"`
		} `json:"data"`
		Meta *struct {
			Limit      int     `json:"limit"`
			NextCursor *string `json:"next_cursor"`
			HasMore    bool    `json:"has_more"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &respuesta); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if len(respuesta.Data) != 1 {
		t.Fatalf("movimientos = %d, want 1", len(respuesta.Data))
	}
	movimiento := respuesta.Data[0]
	if movimiento.TransaccionID != transaccion.String() || movimiento.Direccion != "salida" ||
		movimiento.MontoCentavos != "150000" || movimiento.Categoria != "compras" {
		t.Errorf("movimiento = %+v", movimiento)
	}
	if respuesta.Meta == nil || respuesta.Meta.Limit != LimiteMovimientosPorDefecto ||
		respuesta.Meta.NextCursor == nil || *respuesta.Meta.NextCursor != siguiente || !respuesta.Meta.HasMore {
		t.Errorf("meta = %+v", respuesta.Meta)
	}
}

func TestListarMovimientosVacioSerializaUnaLista(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := peticionConIdentidad(t, "/v1/inteligencia/movimientos")
	NuevoHandler(&servicioDeLecturaFalso{}).ListarMovimientosInteligencia(rec, req,
		api.ListarMovimientosInteligenciaParams{})

	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Errorf("data vacía no es lista: %s", rec.Body.String())
	}
}

func TestResumenDevuelveMontosComoTextoYMedianaNula(t *testing.T) {
	t.Parallel()

	mediana := int64(200000)
	falso := &servicioDeLecturaFalso{
		resumen: ResumenDelMes{
			Mes:             "2026-09",
			Desde:           time.Date(2026, 9, 1, 0, 0, 0, 0, zonaBogota),
			Hasta:           time.Date(2026, 10, 1, 0, 0, 0, 0, zonaBogota),
			Alcance:         AlcanceBilletera,
			Moneda:          MonedaDeLaBilletera,
			Evaluable:       true,
			MesesCompletos:  3,
			MesesRequeridos: MesesRequeridosParaEvaluar,
			TotalCentavos:   570000,
			CupoAvisos:      CupoMensualDeAvisos,
			AvisosEmitidos:  1,
			Categorias: []ResumenDeCategoria{
				{Codigo: "compras", Nombre: "Compras", TotalCentavos: 500000, MedianaCentavos: &mediana, MesesConGasto: 3},
				{Codigo: "sin_categoria", Nombre: "Sin categoría", TotalCentavos: 70000, MesesConGasto: 1},
			},
		},
	}
	rec := httptest.NewRecorder()
	req := peticionConIdentidad(t, "/v1/inteligencia/resumen")
	NuevoHandler(falso).ResumenInteligencia(rec, req, api.ResumenInteligenciaParams{})

	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if falso.mes != "" {
		t.Errorf("mes = %q, want vacío para que el servicio use el actual", falso.mes)
	}
	cuerpo := rec.Body.String()
	for _, clave := range []string{
		`"total_centavos":"570000"`, `"mediana_centavos":"200000"`, `"mediana_centavos":null`,
		`"alcance":"billetera"`, `"evaluable":true`, `"cupo_avisos":3`,
	} {
		if !strings.Contains(cuerpo, clave) {
			t.Errorf("el cuerpo no contiene %s: %s", clave, cuerpo)
		}
	}
}

func TestResumenPasaElMesYRechazaElInvalido(t *testing.T) {
	t.Parallel()

	texto := func(valor string) *string { return &valor }

	falso := &servicioDeLecturaFalso{}
	rec := httptest.NewRecorder()
	req := peticionConIdentidad(t, "/v1/inteligencia/resumen?mes=2026-09")
	NuevoHandler(falso).ResumenInteligencia(rec, req, api.ResumenInteligenciaParams{Mes: texto("2026-09")})
	if rec.Code != http.StatusOK || falso.mes != "2026-09" {
		t.Fatalf("código = %d, mes = %q", rec.Code, falso.mes)
	}

	falso = &servicioDeLecturaFalso{err: fmt.Errorf("%w: 2026-13", ErrMesInvalido)}
	rec = httptest.NewRecorder()
	NuevoHandler(falso).ResumenInteligencia(rec, req, api.ResumenInteligenciaParams{Mes: texto("2026-13")})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("código = %d, want 400", rec.Code)
	}
	assertErrorDelSobre(t, rec, "ValidationError")
}

func TestServicioQueFallaResponde500Generico(t *testing.T) {
	t.Parallel()

	falso := &servicioDeLecturaFalso{err: errors.New("la base no responde")}
	rec := httptest.NewRecorder()
	req := peticionConIdentidad(t, "/v1/inteligencia/movimientos")
	NuevoHandler(falso).ListarMovimientosInteligencia(rec, req, api.ListarMovimientosInteligenciaParams{})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("código = %d, want 500", rec.Code)
	}
	assertErrorDelSobre(t, rec, "InternalError")
}

func peticionConIdentidad(t *testing.T, ruta string) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, ruta, nil)
	return req.WithContext(identidad.ConUsuarioID(req.Context(), usuarioDePrueba))
}

func assertErrorDelSobre(t *testing.T, rec *httptest.ResponseRecorder, codigo string) {
	t.Helper()
	var respuesta struct {
		Success bool `json:"success"`
		Error   *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &respuesta); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if respuesta.Success || respuesta.Error == nil || respuesta.Error.Code != codigo {
		t.Fatalf("sobre = %s, want error %q", rec.Body.String(), codigo)
	}
}
