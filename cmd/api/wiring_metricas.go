package main

import (
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/observabilidad"
)

// metricasDeInteligencia arma el registro de métricas cuando la superficie está encendida. Con la
// bandera apagada devuelve nil y no se construye ni se registra nada.
func metricasDeInteligencia(cfg *Config) (*observabilidad.MetricasDeInteligencia, error) {
	if !cfg.MetricasActivo {
		return nil, nil
	}
	return observabilidad.NuevasMetricasDeInteligencia(nil)
}

// metricasDeCorreccion devuelve el puerto del handler sin obligarlo a saber si hay métricas.
func metricasDeCorreccion(metricas *observabilidad.MetricasDeInteligencia) inteligencia.Metricas {
	if metricas == nil {
		return inteligencia.SinMetricas()
	}
	return metricas
}
