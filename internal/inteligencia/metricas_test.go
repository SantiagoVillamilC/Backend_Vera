package inteligencia

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// metricasGrabadas es el doble del puerto: guarda lo que se le informó y no mide nada.
type metricasGrabadas struct {
	clasificadorActivo []bool
	lotes              []Resultado
	duracionesLote     []time.Duration
	desenlacesFallidos []DesenlaceDeLote
	avisos             []ResumenDeAvisos
	duracionAvisos     []time.Duration
	correcciones       []DesenlaceDeCorreccion
	filas              []int
	conflictos         []MotivoDeConflicto
}

func (m *metricasGrabadas) ClasificadorActivo(activo bool) {
	m.clasificadorActivo = append(m.clasificadorActivo, activo)
}

func (m *metricasGrabadas) LoteTerminado(resultado Resultado, duracion time.Duration) {
	m.lotes = append(m.lotes, resultado)
	m.duracionesLote = append(m.duracionesLote, duracion)
}

func (m *metricasGrabadas) LoteNoEjecutado(desenlace DesenlaceDeLote, duracion time.Duration) {
	m.desenlacesFallidos = append(m.desenlacesFallidos, desenlace)
	m.duracionesLote = append(m.duracionesLote, duracion)
}

func (m *metricasGrabadas) AvisosTerminados(resumen ResumenDeAvisos, duracion time.Duration) {
	m.avisos = append(m.avisos, resumen)
	m.duracionAvisos = append(m.duracionAvisos, duracion)
}

func (m *metricasGrabadas) CorreccionTerminada(desenlace DesenlaceDeCorreccion, filasCambiadas int) {
	m.correcciones = append(m.correcciones, desenlace)
	m.filas = append(m.filas, filasCambiadas)
}

func (m *metricasGrabadas) ConflictoDeCorreccion(motivo MotivoDeConflicto) {
	m.conflictos = append(m.conflictos, motivo)
}

var _ Metricas = (*metricasGrabadas)(nil)

// TestSinMetricasNoPanica fija el relleno del puerto: si se agrega un método y se olvida el no-op,
// esta prueba deja de compilar.
func TestSinMetricasNoPanica(t *testing.T) {
	t.Parallel()
	metricas := SinMetricas()
	metricas.ClasificadorActivo(true)
	metricas.LoteTerminado(Resultado{}, time.Second)
	metricas.LoteNoEjecutado(DesenlaceLoteError, time.Second)
	metricas.AvisosTerminados(ResumenDeAvisos{}, time.Second)
	metricas.CorreccionTerminada(DesenlaceCorreccionAplicada, 3)
	metricas.ConflictoDeCorreccion(ConflictoCorreccionEnCurso)
}

// TestConMetricasNuloConservaElRelleno evita que una opción nula deje el servicio sin registro.
func TestConMetricasNuloConservaElRelleno(t *testing.T) {
	t.Parallel()
	servicio := &Servicio{metricas: SinMetricas()}
	ConMetricas(nil)(servicio)
	if _, ok := servicio.metricas.(sinMetricas); !ok {
		t.Fatalf("metricas = %T, quiero el relleno sin métricas", servicio.metricas)
	}
}

// TestHandlerCorreccionAplicadaCuentaFilas comprueba que se mide el desenlace con sus filas.
func TestHandlerCorreccionAplicadaCuentaFilas(t *testing.T) {
	t.Parallel()
	grabadas := &metricasGrabadas{}
	falso := &servicioDeLecturaFalso{correccion: CorreccionAplicada{FilasCambiadas: 3}}
	handler := NuevoHandler(falso, ConMetricasDeLaCorreccion(grabadas))
	rec := httptest.NewRecorder()
	req := peticionConCuerpo(t, "/v1/inteligencia/movimientos/123e4567-e89b-42d3-a456-426614174000/correcciones",
		`{"categoria":"compras"}`)
	handler.CorregirMovimientoInteligencia(rec, req, "123e4567-e89b-42d3-a456-426614174000")

	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d, quiero 200: %s", rec.Code, rec.Body.String())
	}
	if len(grabadas.correcciones) != 1 || grabadas.correcciones[0] != DesenlaceCorreccionAplicada {
		t.Fatalf("desenlaces = %v, quiero una corrección aplicada", grabadas.correcciones)
	}
	if len(grabadas.filas) != 1 || grabadas.filas[0] != 3 {
		t.Fatalf("filas = %v, quiero 3", grabadas.filas)
	}
}

// TestHandlerConflictoCuentaElMotivo separa los dos 409, que no significan lo mismo.
func TestHandlerConflictoCuentaElMotivo(t *testing.T) {
	t.Parallel()
	casos := map[string]struct {
		err    error
		motivo MotivoDeConflicto
	}{
		"copia sin reconstruir": {err: ErrCopiaSinDestino, motivo: ConflictoCopiaSinReconstruir},
		"corrección en curso":   {err: ErrCorreccionOcupada, motivo: ConflictoCorreccionEnCurso},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			grabadas := &metricasGrabadas{}
			falso := &servicioDeLecturaFalso{correccionErr: caso.err}
			handler := NuevoHandler(falso, ConMetricasDeLaCorreccion(grabadas))
			rec := httptest.NewRecorder()
			req := peticionConCuerpo(t,
				"/v1/inteligencia/movimientos/123e4567-e89b-42d3-a456-426614174000/correcciones",
				`{"categoria":"compras"}`)
			handler.CorregirMovimientoInteligencia(rec, req, "123e4567-e89b-42d3-a456-426614174000")

			if len(grabadas.conflictos) != 1 || grabadas.conflictos[0] != caso.motivo {
				t.Fatalf("motivos = %v, quiero %q", grabadas.conflictos, caso.motivo)
			}
			if len(grabadas.correcciones) != 1 || grabadas.correcciones[0] != DesenlaceCorreccionConflicto {
				t.Fatalf("desenlaces = %v, quiero un conflicto", grabadas.correcciones)
			}
		})
	}
}

// TestHandlerSinMetricasNoSeCaeDejaElRelleno blinda la firma que ya usaban las pruebas de M4 y M5.
func TestHandlerSinMetricasNoSeCaeDejaElRelleno(t *testing.T) {
	t.Parallel()
	falso := &servicioDeLecturaFalso{correccionErr: ErrMovimientoNoEncontrado}
	handler := NuevoHandler(falso)
	if _, ok := handler.metricas.(sinMetricas); !ok {
		t.Fatalf("metricas = %T, quiero el relleno sin métricas", handler.metricas)
	}
	rec := httptest.NewRecorder()
	req := peticionConCuerpo(t, "/v1/inteligencia/movimientos/123e4567-e89b-42d3-a456-426614174000/correcciones",
		`{"categoria":"compras"}`)
	handler.CorregirMovimientoInteligencia(rec, req, "123e4567-e89b-42d3-a456-426614174000")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("código = %d, quiero 404", rec.Code)
	}
}

// TestHandlerCorreccionInvalidaSeCuenta cubre el 400 y el 422 previos al servicio.
func TestHandlerCorreccionInvalidaSeCuenta(t *testing.T) {
	t.Parallel()
	casos := map[string]struct {
		transaccion string
		cuerpo      string
	}{
		"uuid inválido":      {transaccion: "no-es-uuid", cuerpo: `{"categoria":"compras"}`},
		"cuerpo vacío":       {transaccion: "123e4567-e89b-42d3-a456-426614174000", cuerpo: `{}`},
		"cuerpo mal formado": {transaccion: "123e4567-e89b-42d3-a456-426614174000", cuerpo: `{"categoria":`},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			grabadas := &metricasGrabadas{}
			falso := &servicioDeLecturaFalso{}
			handler := NuevoHandler(falso, ConMetricasDeLaCorreccion(grabadas))
			rec := httptest.NewRecorder()
			req := peticionConCuerpo(t, "/v1/inteligencia/movimientos/"+caso.transaccion+"/correcciones", caso.cuerpo)
			handler.CorregirMovimientoInteligencia(rec, req, caso.transaccion)

			if falso.correccionLlamada {
				t.Fatal("el servicio no debía llamarse")
			}
			if len(grabadas.correcciones) != 1 || grabadas.correcciones[0] != DesenlaceCorreccionInvalida {
				t.Fatalf("desenlaces = %v, quiero una inválida", grabadas.correcciones)
			}
		})
	}
}

// TestHandlerErrorInesperadoSeCuentaComoError separa el 500 del 422.
func TestHandlerErrorInesperadoSeCuentaComoError(t *testing.T) {
	t.Parallel()
	grabadas := &metricasGrabadas{}
	falso := &servicioDeLecturaFalso{correccionErr: errors.New("la base no responde")}
	handler := NuevoHandler(falso, ConMetricasDeLaCorreccion(grabadas))
	rec := httptest.NewRecorder()
	req := peticionConCuerpo(t, "/v1/inteligencia/movimientos/123e4567-e89b-42d3-a456-426614174000/correcciones",
		`{"categoria":"compras"}`)
	handler.CorregirMovimientoInteligencia(rec, req, "123e4567-e89b-42d3-a456-426614174000")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("código = %d, quiero 500", rec.Code)
	}
	if len(grabadas.correcciones) != 1 || grabadas.correcciones[0] != DesenlaceCorreccionError {
		t.Fatalf("desenlaces = %v, quiero un error", grabadas.correcciones)
	}
	if len(grabadas.filas) != 1 || grabadas.filas[0] != 0 {
		t.Fatalf("filas = %v, un error no registra filas cambiadas", grabadas.filas)
	}
}

// TestVersionDelClasificadorDeclaraLaAusencia evita que un cero se lea como ausencia de abstenciones.
func TestVersionDelClasificadorDeclaraLaAusencia(t *testing.T) {
	t.Parallel()
	servicio := &Servicio{}
	if version := servicio.versionDelClasificador(); version != "sin_modelo" {
		t.Fatalf("versión = %q, quiero sin_modelo", version)
	}
	modelo := &Modelo{Version: VersionArtefacto, Huella: HuellaArtefactoTFIDF}
	servicio.clasificador = modelo
	if version := servicio.versionDelClasificador(); version != modelo.VersionModelo() {
		t.Fatalf("versión = %q, quiero %q", version, modelo.VersionModelo())
	}
}
