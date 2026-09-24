package auth

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// consultorDeConsentimientoFalso emula la ultima fila del log por tipo para probar la regla de vigencia.
type consultorDeConsentimientoFalso struct {
	aceptados  []bool
	encontrado bool
	err        error
	tipoVisto  string
}

// UltimoConsentimientoPorTipo devuelve el ultimo valor registrado y la ausencia cuando no hay filas.
func (c *consultorDeConsentimientoFalso) UltimoConsentimientoPorTipo(_ context.Context, _ uuid.UUID, tipo string) (bool, bool, error) {
	c.tipoVisto = tipo
	if c.err != nil {
		return false, false, c.err
	}
	if !c.encontrado || len(c.aceptados) == 0 {
		return false, false, nil
	}
	return c.aceptados[len(c.aceptados)-1], true, nil
}

// La secuencia otorgar, revocar, otorgar deja el opt-in segun la ultima fila, no segun la primera aceptada.
func TestOptInVigenteMandaLaUltimaFila(t *testing.T) {
	casos := map[string]struct {
		aceptados []bool
		want      bool
	}{
		"otorgar, revocar, otorgar da true": {aceptados: []bool{true, false, true}, want: true},
		"otorgar, revocar da false":         {aceptados: []bool{true, false}, want: false},
		"revocar, otorgar da true":          {aceptados: []bool{false, true}, want: true},
		"sin filas da false":                {aceptados: nil, want: false},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			consultor := &consultorDeConsentimientoFalso{aceptados: caso.aceptados, encontrado: len(caso.aceptados) > 0}
			lector := &LectorOptInDeConsentimientos{consultor: consultor}

			vigente, err := lector.OptInVigente(t.Context(), uuid.New())
			if err != nil {
				t.Fatalf("OptInVigente: %v", err)
			}
			if vigente != caso.want {
				t.Fatalf("vigente = %v, quiero %v", vigente, caso.want)
			}
			if consultor.tipoVisto != TipoConsentimientoWhatsApp {
				t.Fatalf("tipo consultado = %q, quiero %q", consultor.tipoVisto, TipoConsentimientoWhatsApp)
			}
		})
	}
}

// Una fila aceptada de otro tipo no enciende el opt-in de WhatsApp.
func TestOptInVigenteIgnoraOtrosTipos(t *testing.T) {
	consultor := &consultorDeConsentimientoFalso{aceptados: []bool{true}, encontrado: true}
	lector := &LectorOptInDeConsentimientos{consultor: consultor}

	if _, err := lector.OptInVigente(t.Context(), uuid.New()); err != nil {
		t.Fatalf("OptInVigente: %v", err)
	}
	if consultor.tipoVisto != TipoConsentimientoWhatsApp {
		t.Fatalf("tipo consultado = %q, quiero WhatsApp", consultor.tipoVisto)
	}
}
