package inteligencia

import (
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/avisos"
)

// TestConCupoDeAvisosCambiaSoloElTope deja claro que la opción toca el cupo y no la configuración
// completa que ConConfiguracionDeAvisos reemplaza.
func TestConCupoDeAvisosCambiaSoloElTope(t *testing.T) {
	t.Parallel()
	defecto := avisos.ConfiguracionPorDefecto()
	servicio := NuevoServicio(nil, ConCupoDeAvisos(5))

	if cupo := servicio.avisosCfg.CupoMensualDeAvisos(); cupo != 5 {
		t.Fatalf("cupo = %d, quiero 5", cupo)
	}
	if servicio.avisosCfg.MesesMinimos != defecto.MesesMinimos ||
		servicio.avisosCfg.VentanaPagoDias != defecto.VentanaPagoDias ||
		len(servicio.avisosCfg.TiposAsientoIngreso) != len(defecto.TiposAsientoIngreso) {
		t.Fatalf("la opción cambió algo más que el cupo: %+v", servicio.avisosCfg)
	}
}

// TestConCupoDeAvisosIgnoraUnTopeMenorQueUno mantiene el valor por defecto cuando la cifra no
// puede ser un cupo, para que una lectura mala no deje el emisor sin tope.
func TestConCupoDeAvisosIgnoraUnTopeMenorQueUno(t *testing.T) {
	t.Parallel()
	for _, tope := range []int{0, -1} {
		servicio := NuevoServicio(nil, ConCupoDeAvisos(tope))
		if cupo := servicio.avisosCfg.CupoMensualDeAvisos(); cupo != avisos.CupoMensualPorDefecto {
			t.Fatalf("con tope %d el cupo quedó en %d, quiero %d", tope, cupo, avisos.CupoMensualPorDefecto)
		}
	}
}

// TestConCupoDeAvisosLlegaAlResumen comprueba que la cifra configurada es la misma que el resumen
// informa al usuario, que es lo que hace visible el cambio de AVISOS_CUPO.
func TestConCupoDeAvisosLlegaAlResumen(t *testing.T) {
	t.Parallel()
	inicio := marzoDe2026(1)
	siguiente := inicio.AddDate(0, 1, 0)
	servicio := NuevoServicio(nil, ConCupoDeAvisos(5))

	resumen, err := servicio.resumenCon(t.Context(), &consultasDeResumenFalsas{}, "usuario", inicio, siguiente)
	if err != nil {
		t.Fatalf("resumenCon: %v", err)
	}
	if resumen.CupoAvisos != 5 {
		t.Fatalf("cupo del resumen = %d, quiero 5", resumen.CupoAvisos)
	}
}

// TestConConfiguracionDeAvisosSigueMandandoSinCupo blinda el camino que ya usaban las pruebas.
func TestConConfiguracionDeAvisosSigueMandandoSinCupo(t *testing.T) {
	t.Parallel()
	configuracion := avisos.ConfiguracionPorDefecto()
	configuracion.TopeAvisosMes = 7
	servicio := NuevoServicio(nil, ConConfiguracionDeAvisos(configuracion))
	if cupo := servicio.avisosCfg.CupoMensualDeAvisos(); cupo != 7 {
		t.Fatalf("cupo = %d, quiero 7", cupo)
	}
}
