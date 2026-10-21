package observabilidad

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// espacioDeMetricas es el prefijo de todo lo que expone el contexto de inteligencia.
const espacioDeMetricas = "vera_inteligencia"

// largoTopeDeEtiqueta es el máximo de caracteres que se conserva de una etiqueta libre.
const largoTopeDeEtiqueta = 64

// origenesConocidos es la lista cerrada de valores de origen_categoria: los nueve del CHECK de la
// migración 0023 más el cajón para lo imprevisto, que acota la cardinalidad.
var origenesConocidos = map[string]struct{}{
	inteligencia.OrigenMemoria:      {},
	inteligencia.OrigenInterna:      {},
	inteligencia.OrigenMCC:          {},
	inteligencia.OrigenCatalogo:     {},
	inteligencia.OrigenLexico:       {},
	inteligencia.OrigenTFIDF:        {},
	inteligencia.OrigenLLM:          {},
	inteligencia.OrigenSinCategoria: {},
	"manual":                        {},
}

// versionValida acepta la etiqueta de versión del modelo tal como la produce el artefacto.
var versionValida = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

// MetricasDeInteligencia implementa inteligencia.Metricas sobre un registro Prometheus propio. El
// registro es propio y no el global para que las pruebas no colisionen y para no arrastrar los
// colectores de proceso a la superficie que se expone.
type MetricasDeInteligencia struct {
	registro *prometheus.Registry

	loteCorridas     *prometheus.CounterVec
	loteDuracion     *prometheus.HistogramVec
	asientos         prometheus.Counter
	movimientos      prometheus.Counter
	sinContraparte   prometheus.Counter
	sinCategoria     prometheus.Counter
	porOrigen        *prometheus.CounterVec
	abstenciones     *prometheus.CounterVec
	clasificadorVivo prometheus.Gauge

	correcciones   *prometheus.CounterVec
	conflictos     *prometheus.CounterVec
	filasCambiadas prometheus.Histogram

	avisosEmitidos       prometheus.Counter
	avisosSuprimidos     prometheus.Counter
	avisosYaEmitidos     prometheus.Counter
	avisosCorridas       *prometheus.CounterVec
	avisosDuracion       *prometheus.HistogramVec
	avisoUsuarioDuracion prometheus.Histogram
}

// NuevasMetricasDeInteligencia registra todas las métricas en el registro y falla si alguna choca.
// Con registro nulo crea uno propio y vacío.
func NuevasMetricasDeInteligencia(registro *prometheus.Registry) (*MetricasDeInteligencia, error) {
	if registro == nil {
		registro = prometheus.NewRegistry()
	}
	metricas := &MetricasDeInteligencia{
		registro: registro,
		loteCorridas: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: espacioDeMetricas,
			Name:      "lote_corridas_total",
			Help:      "Corridas del lote de enriquecimiento por desenlace (ok, error, lote_en_curso).",
		}, []string{"desenlace"}),
		loteDuracion: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: espacioDeMetricas,
			Name:      "lote_duracion_segundos",
			Help:      "Duración de una corrida del lote de enriquecimiento, incluido el commit.",
			Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20, 30, 60, 120},
		}, []string{"desenlace"}),
		asientos: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: espacioDeMetricas,
			Name:      "lote_asientos_total",
			Help:      "Asientos leídos por el lote de enriquecimiento.",
		}),
		movimientos: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: espacioDeMetricas,
			Name:      "lote_movimientos_total",
			Help:      "Movimientos escritos en la copia enriquecida.",
		}),
		sinContraparte: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: espacioDeMetricas,
			Name:      "lote_sin_contraparte_total",
			Help:      "Movimientos escritos sin contraparte resuelta.",
		}),
		sinCategoria: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: espacioDeMetricas,
			Name:      "lote_sin_categoria_total",
			Help:      "Movimientos escritos sin categoría, incluida la abstención del clasificador.",
		}),
		porOrigen: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: espacioDeMetricas,
			Name:      "movimientos_por_origen_total",
			Help:      "Movimientos escritos por origen de la categoría.",
		}, []string{"origen"}),
		abstenciones: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: espacioDeMetricas,
			Name:      "clasificador_abstenciones_total",
			Help:      "Movimientos en los que el clasificador se abstuvo, por versión del modelo.",
		}, []string{"version_modelo"}),
		clasificadorVivo: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: espacioDeMetricas,
			Name:      "clasificador_activo",
			Help:      "1 si el clasificador está cargado en este proceso y 0 si no.",
		}),
		correcciones: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: espacioDeMetricas,
			Name:      "correcciones_total",
			Help:      "Correcciones por desenlace (aplicada, no_encontrada, invalida, conflicto, error).",
		}, []string{"desenlace"}),
		conflictos: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: espacioDeMetricas,
			Name:      "correcciones_conflicto_total",
			Help:      "Correcciones que respondieron 409, por motivo.",
		}, []string{"motivo"}),
		filasCambiadas: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: espacioDeMetricas,
			Name:      "correccion_filas_cambiadas",
			Help:      "Filas de la copia que tocó una corrección aplicada.",
			Buckets:   []float64{0, 1, 2, 3, 5, 8, 13, 21, 50},
		}),
		avisosEmitidos: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: espacioDeMetricas,
			Name:      "avisos_emitidos_total",
			Help:      "Avisos nuevos guardados por la corrida de avisos.",
		}),
		avisosSuprimidos: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: espacioDeMetricas,
			Name:      "avisos_suprimidos_por_cupo_total",
			Help:      "Avisos candidatos que el cupo mensual dejó fuera.",
		}),
		avisosYaEmitidos: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: espacioDeMetricas,
			Name:      "avisos_ya_emitidos_total",
			Help:      "Avisos candidatos descartados porque ya estaban emitidos en el periodo.",
		}),
		avisosCorridas: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: espacioDeMetricas,
			Name:      "avisos_corridas_total",
			Help:      "Corridas del emisor de avisos por desenlace (ok, error).",
		}, []string{"desenlace"}),
		avisosDuracion: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: espacioDeMetricas,
			Name:      "avisos_duracion_segundos",
			Help:      "Duración de una corrida completa del emisor de avisos.",
			Buckets:   []float64{1, 5, 10, 20, 30, 60, 120, 180, 240, 300, 450, 600},
		}, []string{"desenlace"}),
		avisoUsuarioDuracion: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: espacioDeMetricas,
			Name:      "avisos_duracion_por_usuario_segundos",
			Help:      "Duración de la evaluación de avisos de un usuario.",
			Buckets:   []float64{0.002, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}),
	}
	colectores := []prometheus.Collector{
		metricas.loteCorridas, metricas.loteDuracion, metricas.asientos, metricas.movimientos,
		metricas.sinContraparte, metricas.sinCategoria, metricas.porOrigen, metricas.abstenciones,
		metricas.clasificadorVivo, metricas.correcciones, metricas.conflictos, metricas.filasCambiadas,
		metricas.avisosEmitidos, metricas.avisosSuprimidos, metricas.avisosYaEmitidos,
		metricas.avisosCorridas, metricas.avisosDuracion, metricas.avisoUsuarioDuracion,
	}
	for _, colector := range colectores {
		if err := registro.Register(colector); err != nil {
			return nil, fmt.Errorf("observabilidad: registrar la métrica de inteligencia: %w", err)
		}
	}
	return metricas, nil
}

// Registro devuelve el registro propio donde viven las métricas.
func (m *MetricasDeInteligencia) Registro() *prometheus.Registry { return m.registro }

// Handler sirve la exposición en formato Prometheus.
func (m *MetricasDeInteligencia) Handler() http.Handler {
	return promhttp.HandlerFor(m.registro, promhttp.HandlerOpts{})
}

// ClasificadorActivo publica si el clasificador está cargado.
func (m *MetricasDeInteligencia) ClasificadorActivo(activo bool) {
	if activo {
		m.clasificadorVivo.Set(1)
		return
	}
	m.clasificadorVivo.Set(0)
}

// LoteTerminado publica el resultado de un lote que confirmó su transacción.
func (m *MetricasDeInteligencia) LoteTerminado(resultado inteligencia.Resultado, duracion time.Duration) {
	m.loteCorridas.WithLabelValues(string(inteligencia.DesenlaceLoteOK)).Inc()
	m.loteDuracion.WithLabelValues(string(inteligencia.DesenlaceLoteOK)).Observe(duracion.Seconds())
	m.asientos.Add(float64(resultado.Asientos))
	m.movimientos.Add(float64(resultado.Movimientos))
	m.sinContraparte.Add(float64(resultado.SinContraparte))
	m.sinCategoria.Add(float64(resultado.SinCategoria))
	for origen, cantidad := range resultado.PorOrigen {
		m.porOrigen.WithLabelValues(origenAcotado(origen)).Add(float64(cantidad))
	}
	if resultado.Abstenciones > 0 {
		m.abstenciones.WithLabelValues(versionAcotada(resultado.VersionClasificador)).
			Add(float64(resultado.Abstenciones))
	}
}

// LoteNoEjecutado publica un lote que falló o que no corrió por el candado.
func (m *MetricasDeInteligencia) LoteNoEjecutado(desenlace inteligencia.DesenlaceDeLote, duracion time.Duration) {
	etiqueta := string(desenlace)
	switch desenlace {
	case inteligencia.DesenlaceLoteError, inteligencia.DesenlaceLoteEnCurso:
	default:
		etiqueta = string(inteligencia.DesenlaceLoteError)
	}
	m.loteCorridas.WithLabelValues(etiqueta).Inc()
	m.loteDuracion.WithLabelValues(etiqueta).Observe(duracion.Seconds())
}

// AvisosTerminados publica una corrida del emisor con todos sus usuarios ya confirmados.
func (m *MetricasDeInteligencia) AvisosTerminados(resumen inteligencia.ResumenDeAvisos, duracion time.Duration) {
	desenlace := "ok"
	if resumen.Fallo {
		desenlace = "error"
	}
	m.avisosCorridas.WithLabelValues(desenlace).Inc()
	m.avisosDuracion.WithLabelValues(desenlace).Observe(duracion.Seconds())
	m.avisosEmitidos.Add(float64(resumen.Emitidos))
	m.avisosSuprimidos.Add(float64(resumen.SuprimidosPorCupo))
	m.avisosYaEmitidos.Add(float64(resumen.YaEmitidos))
	for _, porUsuario := range resumen.DuracionesPorUsuario {
		m.avisoUsuarioDuracion.Observe(porUsuario.Seconds())
	}
}

// CorreccionTerminada publica el desenlace del endpoint de corrección y, si aplicó, cuántas filas tocó.
func (m *MetricasDeInteligencia) CorreccionTerminada(desenlace inteligencia.DesenlaceDeCorreccion,
	filasCambiadas int) {
	etiqueta := string(desenlace)
	switch desenlace {
	case inteligencia.DesenlaceCorreccionAplicada, inteligencia.DesenlaceCorreccionNoEncontrada,
		inteligencia.DesenlaceCorreccionInvalida, inteligencia.DesenlaceCorreccionConflicto:
	default:
		etiqueta = string(inteligencia.DesenlaceCorreccionError)
	}
	m.correcciones.WithLabelValues(etiqueta).Inc()
	if desenlace == inteligencia.DesenlaceCorreccionAplicada {
		m.filasCambiadas.Observe(float64(filasCambiadas))
	}
}

// ConflictoDeCorreccion publica el motivo de un 409 de corrección.
func (m *MetricasDeInteligencia) ConflictoDeCorreccion(motivo inteligencia.MotivoDeConflicto) {
	etiqueta := string(motivo)
	switch motivo {
	case inteligencia.ConflictoCorreccionEnCurso, inteligencia.ConflictoCopiaSinReconstruir:
	default:
		etiqueta = string(inteligencia.ConflictoCorreccionEnCurso)
	}
	m.conflictos.WithLabelValues(etiqueta).Inc()
}

// origenAcotado reduce un origen desconocido a una sola serie para no crear cardinalidad sin límite.
func origenAcotado(origen string) string {
	if _, conocido := origenesConocidos[origen]; conocido {
		return origen
	}
	return "desconocido"
}

// versionAcotada sanea la versión del modelo. Una etiqueta larga o con caracteres raros se recorta y
// se le añade un prefijo del hash, de modo que dos artefactos distintos no colapsen en la misma serie.
func versionAcotada(version string) string {
	if version == "" {
		return "sin_modelo"
	}
	if len(version) <= largoTopeDeEtiqueta && versionValida.MatchString(version) {
		return version
	}
	suma := sha256.Sum256([]byte(version))
	return strings.Join([]string{
		"recortada",
		hex.EncodeToString(suma[:4]),
	}, ":")
}

var _ inteligencia.Metricas = (*MetricasDeInteligencia)(nil)
