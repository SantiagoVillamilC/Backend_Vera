package main

import (
	"fmt"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
)

// modeloDelClasificador resuelve el artefacto según la bandera. Apagado no parsea nada: el costo de
// arranque del clasificador solo se paga cuando el escalón 6 está encendido.
func modeloDelClasificador(cfg *Config) (*inteligencia.Modelo, error) {
	switch cfg.Clasificador {
	case inteligencia.ModoClasificadorTFIDF:
		modelo, err := inteligencia.ModeloTFIDFEmbebido()
		if err != nil {
			return nil, fmt.Errorf("clasificador: %w", err)
		}
		return modelo, nil
	case inteligencia.ModoClasificadorApagado, "":
		return nil, nil
	default:
		return nil, fmt.Errorf("clasificador: modo desconocido %q", cfg.Clasificador)
	}
}
