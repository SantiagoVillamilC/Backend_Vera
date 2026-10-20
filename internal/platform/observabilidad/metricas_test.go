package observabilidad

import (
	"io"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
	dto "github.com/prometheus/client_model/go"
)

// valorDe busca una métrica por nombre y etiquetas y devuelve su valor. Falla si no existe.
func valorDe(t *testing.T, metricas *MetricasDeInteligencia, nombre string,
	etiquetas map[string]string) float64 {
	t.Helper()
	familias, err := metricas.Registro().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, familia := range familias {
		if familia.GetName() != nombre {
			continue
		}
		for _, muestra := range familia.GetMetric() {
			if !coincidenLasEtiquetas(muestra, etiquetas) {
				continue
			}
			return valorDeLaMuestra(muestra)
		}
	}
	t.Fatalf("la métrica %s con etiquetas %v no existe", nombre, etiquetas)
	return 0
}

// existeLaMetrica reporta si alguna serie del nombre coincide con las etiquetas.
func existeLaMetrica(t *testing.T, metricas *MetricasDeInteligencia, nombre string,
	etiquetas map[string]string) bool {
	t.Helper()
	familias, err := metricas.Registro().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, familia := range familias {
		if familia.GetName() != nombre {
			continue
		}
		for _, muestra := range familia.GetMetric() {
			if coincidenLasEtiquetas(muestra, etiquetas) {
				return true
			}
		}
	}
	return false
}

func coincidenLasEtiquetas(muestra *dto.Metric, etiquetas map[string]string) bool {
	for clave, valor := range etiquetas {
		encontrada := false
		for _, par := range muestra.GetLabel() {
			if par.GetName() == clave && par.GetValue() == valor {
				encontrada = true
				break
			}
		}
		if !encontrada {
			return false
		}
	}
	return true
}

func valorDeLaMuestra(muestra *dto.Metric) float64 {
	switch {
	case muestra.GetCounter() != nil:
		return muestra.GetCounter().GetValue()
	case muestra.GetGauge() != nil:
		return muestra.GetGauge().GetValue()
	case muestra.GetHistogram() != nil:
		return float64(muestra.GetHistogram().GetSampleCount())
	default:
		return 0
	}
}

func nuevasMetricas(t *testing.T) *MetricasDeInteligencia {
	t.Helper()
	metricas, err := NuevasMetricasDeInteligencia(nil)
	if err != nil {
		t.Fatalf("construir métricas: %v", err)
	}
	return metricas
}

// TestLoteTerminadoPublicaSusContadores fija que un lote confirmado se ve tal cual.
func TestLoteTerminadoPublicaSusContadores(t *testing.T) {
	t.Parallel()
	metricas := nuevasMetricas(t)
	metricas.LoteTerminado(inteligencia.Resultado{
		Asientos:            3,
		Movimientos:         2,
		SinContraparte:      1,
		SinCategoria:        1,
		Abstenciones:        1,
		PorOrigen:           map[string]int{inteligencia.OrigenTFIDF: 1, inteligencia.OrigenMemoria: 1},
		VersionClasificador: "tfidf-1:8382898374eb",
	}, 250*time.Millisecond)

	if valor := valorDe(t, metricas, "vera_inteligencia_lote_asientos_total", nil); valor != 3 {
		t.Fatalf("asientos = %v, quiero 3", valor)
	}
	if valor := valorDe(t, metricas, "vera_inteligencia_lote_movimientos_total", nil); valor != 2 {
		t.Fatalf("movimientos = %v, quiero 2", valor)
	}
	if valor := valorDe(t, metricas, "vera_inteligencia_lote_sin_categoria_total", nil); valor != 1 {
		t.Fatalf("sin_categoria = %v, quiero 1", valor)
	}
	if valor := valorDe(t, metricas, "vera_inteligencia_movimientos_por_origen_total",
		map[string]string{"origen": inteligencia.OrigenTFIDF}); valor != 1 {
		t.Fatalf("origen tfidf = %v, quiero 1", valor)
	}
	if valor := valorDe(t, metricas, "vera_inteligencia_clasificador_abstenciones_total",
		map[string]string{"version_modelo": "tfidf-1:8382898374eb"}); valor != 1 {
		t.Fatalf("abstenciones = %v, quiero 1", valor)
	}
	if valor := valorDe(t, metricas, "vera_inteligencia_lote_corridas_total",
		map[string]string{"desenlace": "ok"}); valor != 1 {
		t.Fatalf("corridas ok = %v, quiero 1", valor)
	}
	if valor := valorDe(t, metricas, "vera_inteligencia_lote_duracion_segundos",
		map[string]string{"desenlace": "ok"}); valor != 1 {
		t.Fatalf("muestras de duración = %v, quiero 1", valor)
	}
}

// TestLoteFallidoNoPublicaAsientos es la prueba de que una métrica no miente: si la transacción se
// revirtió, el lote no corrió y no hay asientos que contar.
func TestLoteFallidoNoPublicaAsientos(t *testing.T) {
	t.Parallel()
	metricas := nuevasMetricas(t)
	metricas.LoteNoEjecutado(inteligencia.DesenlaceLoteError, time.Second)

	if valor := valorDe(t, metricas, "vera_inteligencia_lote_asientos_total", nil); valor != 0 {
		t.Fatalf("asientos = %v, un lote fallido no cuenta asientos", valor)
	}
	if existeLaMetrica(t, metricas, "vera_inteligencia_movimientos_por_origen_total", nil) {
		t.Fatal("un lote fallido no debe abrir series de origen")
	}
	if valor := valorDe(t, metricas, "vera_inteligencia_lote_corridas_total",
		map[string]string{"desenlace": "error"}); valor != 1 {
		t.Fatalf("corridas de error = %v, quiero 1", valor)
	}
}

// TestLoteEnCursoNoSeCuentaComoError separa la omisión por candado del fallo real.
func TestLoteEnCursoNoSeCuentaComoError(t *testing.T) {
	t.Parallel()
	metricas := nuevasMetricas(t)
	metricas.LoteNoEjecutado(inteligencia.DesenlaceLoteEnCurso, time.Second)

	if valor := valorDe(t, metricas, "vera_inteligencia_lote_corridas_total",
		map[string]string{"desenlace": "lote_en_curso"}); valor != 1 {
		t.Fatalf("corridas en curso = %v, quiero 1", valor)
	}
	if existeLaMetrica(t, metricas, "vera_inteligencia_lote_corridas_total",
		map[string]string{"desenlace": "error"}) {
		t.Fatal("una omisión por candado no es un error")
	}
}

// TestOrigenDesconocidoSeAcota impide que un valor nuevo abra cardinalidad sin límite.
func TestOrigenDesconocidoSeAcota(t *testing.T) {
	t.Parallel()
	metricas := nuevasMetricas(t)
	metricas.LoteTerminado(inteligencia.Resultado{
		PorOrigen: map[string]int{"inventado": 2},
	}, time.Second)

	if valor := valorDe(t, metricas, "vera_inteligencia_movimientos_por_origen_total",
		map[string]string{"origen": "desconocido"}); valor != 2 {
		t.Fatalf("origen desconocido = %v, quiero 2", valor)
	}
}

// TestVersionLargaNoColapsaDosArtefactos comprueba que dos versiones raras no comparten serie.
func TestVersionLargaNoColapsaDosArtefactos(t *testing.T) {
	t.Parallel()
	metricas := nuevasMetricas(t)
	larga := strings.Repeat("tfidf-1:", 12)
	metricas.LoteTerminado(inteligencia.Resultado{Abstenciones: 1, VersionClasificador: larga + "a"},
		time.Second)
	metricas.LoteTerminado(inteligencia.Resultado{Abstenciones: 1, VersionClasificador: larga + "b"},
		time.Second)

	familias, err := metricas.Registro().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	series := 0
	for _, familia := range familias {
		if familia.GetName() != "vera_inteligencia_clasificador_abstenciones_total" {
			continue
		}
		for _, muestra := range familia.GetMetric() {
			for _, par := range muestra.GetLabel() {
				if len(par.GetValue()) > largoTopeDeEtiqueta {
					t.Fatalf("la etiqueta no se recortó: %q", par.GetValue())
				}
			}
			series++
		}
	}
	if series != 2 {
		t.Fatalf("series = %d, quiero 2: dos artefactos distintos no pueden colapsar", series)
	}
}

// TestAbstencionesSinClasificadorDeclaraSinModelo evita que un cero se lea como ausencia de datos.
func TestAbstencionesSinClasificadorDeclaraSinModelo(t *testing.T) {
	t.Parallel()
	metricas := nuevasMetricas(t)
	metricas.LoteTerminado(inteligencia.Resultado{Abstenciones: 1}, time.Second)

	if valor := valorDe(t, metricas, "vera_inteligencia_clasificador_abstenciones_total",
		map[string]string{"version_modelo": "sin_modelo"}); valor != 1 {
		t.Fatalf("abstenciones sin modelo = %v, quiero 1", valor)
	}
}

// TestClasificadorActivoPublicaElGauge distingue apagado de cero abstenciones.
func TestClasificadorActivoPublicaElGauge(t *testing.T) {
	t.Parallel()
	metricas := nuevasMetricas(t)
	if valor := valorDe(t, metricas, "vera_inteligencia_clasificador_activo", nil); valor != 0 {
		t.Fatalf("gauge inicial = %v, quiero 0", valor)
	}
	metricas.ClasificadorActivo(true)
	if valor := valorDe(t, metricas, "vera_inteligencia_clasificador_activo", nil); valor != 1 {
		t.Fatalf("gauge encendido = %v, quiero 1", valor)
	}
}

// TestAvisosPublicaSuprimidosYDuraciones mide lo que el cupo recorta, que antes no dejaba rastro.
func TestAvisosPublicaSuprimidosYDuraciones(t *testing.T) {
	t.Parallel()
	metricas := nuevasMetricas(t)
	metricas.AvisosTerminados(inteligencia.ResumenDeAvisos{
		Usuarios:             2,
		Emitidos:             3,
		YaEmitidos:           4,
		SuprimidosPorCupo:    5,
		DuracionesPorUsuario: []time.Duration{time.Millisecond, 2 * time.Millisecond},
	}, 90*time.Second)

	if valor := valorDe(t, metricas, "vera_inteligencia_avisos_emitidos_total", nil); valor != 3 {
		t.Fatalf("emitidos = %v, quiero 3", valor)
	}
	if valor := valorDe(t, metricas, "vera_inteligencia_avisos_suprimidos_por_cupo_total", nil); valor != 5 {
		t.Fatalf("suprimidos = %v, quiero 5", valor)
	}
	if valor := valorDe(t, metricas, "vera_inteligencia_avisos_ya_emitidos_total", nil); valor != 4 {
		t.Fatalf("ya emitidos = %v, quiero 4", valor)
	}
	if valor := valorDe(t, metricas, "vera_inteligencia_avisos_duracion_por_usuario_segundos",
		nil); valor != 2 {
		t.Fatalf("muestras por usuario = %v, quiero 2", valor)
	}
}

// TestCorreccionSoloRegistraFilasCuandoAplica separa el 200 de los errores.
func TestCorreccionSoloRegistraFilasCuandoAplica(t *testing.T) {
	t.Parallel()
	metricas := nuevasMetricas(t)
	metricas.CorreccionTerminada(inteligencia.DesenlaceCorreccionAplicada, 3)
	metricas.CorreccionTerminada(inteligencia.DesenlaceCorreccionConflicto, 0)
	metricas.ConflictoDeCorreccion(inteligencia.ConflictoCorreccionEnCurso)
	metricas.ConflictoDeCorreccion(inteligencia.ConflictoCopiaSinReconstruir)

	if valor := valorDe(t, metricas, "vera_inteligencia_correccion_filas_cambiadas", nil); valor != 1 {
		t.Fatalf("muestras de filas = %v, quiero 1", valor)
	}
	if valor := valorDe(t, metricas, "vera_inteligencia_correcciones_total",
		map[string]string{"desenlace": "aplicada"}); valor != 1 {
		t.Fatalf("aplicadas = %v, quiero 1", valor)
	}
	if valor := valorDe(t, metricas, "vera_inteligencia_correcciones_conflicto_total",
		map[string]string{"motivo": "correccion_en_curso"}); valor != 1 {
		t.Fatalf("conflicto en curso = %v, quiero 1", valor)
	}
	if valor := valorDe(t, metricas, "vera_inteligencia_correcciones_conflicto_total",
		map[string]string{"motivo": "copia_sin_reconstruir"}); valor != 1 {
		t.Fatalf("conflicto de copia = %v, quiero 1", valor)
	}
}

// TestRegistroPropioNoTraeColectoresGlobales mantiene la superficie sin métricas de proceso.
func TestRegistroPropioNoTraeColectoresGlobales(t *testing.T) {
	t.Parallel()
	metricas := nuevasMetricas(t)
	familias, err := metricas.Registro().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, familia := range familias {
		nombre := familia.GetName()
		if strings.HasPrefix(nombre, "go_") || strings.HasPrefix(nombre, "process_") {
			t.Fatalf("el registro propio no debe traer %q", nombre)
		}
		if !strings.HasPrefix(nombre, espacioDeMetricas+"_") {
			t.Fatalf("métrica fuera del espacio de inteligencia: %q", nombre)
		}
	}
}

// TestExposicionNoTraeIdentificadores cierra la restricción de no filtrar usuarios ni transacciones.
func TestExposicionNoTraeIdentificadores(t *testing.T) {
	t.Parallel()
	metricas := nuevasMetricas(t)
	metricas.LoteTerminado(inteligencia.Resultado{
		Asientos:            1,
		Movimientos:         1,
		PorOrigen:           map[string]int{inteligencia.OrigenTFIDF: 1},
		VersionClasificador: "tfidf-1:8382898374eb",
	}, time.Second)

	rec := httptest.NewRecorder()
	metricas.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), "GET", "/metrics", nil))
	cuerpo, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("leer la exposición: %v", err)
	}
	uuidSuelto := regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}`)
	if uuidSuelto.Match(cuerpo) {
		t.Fatalf("la exposición trae algo con forma de identificador: %s", cuerpo)
	}
	for _, prohibida := range []string{"usuario", "transaccion", "monto", "cuenta", "sesion"} {
		if strings.Contains(strings.ToLower(string(cuerpo)), prohibida+"=") {
			t.Fatalf("la exposición trae una etiqueta prohibida %q", prohibida)
		}
	}
}

// TestHandlerSirveLaExposicion verifica el Content-Type de Prometheus.
func TestHandlerSirveLaExposicion(t *testing.T) {
	t.Parallel()
	metricas := nuevasMetricas(t)
	rec := httptest.NewRecorder()
	metricas.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), "GET", "/metrics", nil))

	if rec.Code != 200 {
		t.Fatalf("status = %d, quiero 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatalf("Content-Type = %q, quiero text/plain", ct)
	}
	if !strings.Contains(rec.Body.String(), "vera_inteligencia_clasificador_activo") {
		t.Fatalf("la exposición no trae el gauge del clasificador: %q", rec.Body.String())
	}
}
