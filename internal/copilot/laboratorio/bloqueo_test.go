package laboratorio

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

func TestMensajeBloqueoDistingueProveedorYBackend(t *testing.T) {
	for _, motivo := range []copilot.MotivoBloqueo{copilot.BloqueoEntrada, copilot.BloqueoSeguridad, copilot.BloqueoRecitacion, copilot.BloqueoDatos, copilot.BloqueoOtro, copilot.BloqueoInstrucciones, "contenido privado"} {
		t.Run(string(motivo), func(t *testing.T) {
			w := httptest.NewRecorder()
			(&servidor{}).error(w, &copilot.ErrorBloqueo{Motivo: motivo})
			cuerpo := w.Body.String()
			if w.Code != 422 || !strings.Contains(cuerpo, "ResponseBlocked") || strings.Contains(cuerpo, "contenido privado") {
				t.Fatal(cuerpo)
			}
			if motivo == copilot.BloqueoInstrucciones {
				if !strings.Contains(cuerpo, "backend") || strings.Contains(cuerpo, "Google") {
					t.Fatal("atribuyó el bloqueo interno al proveedor")
				}
			} else if motivo != "contenido privado" && !strings.Contains(cuerpo, "Google") {
				t.Fatal("perdió el origen del proveedor")
			}
		})
	}
}
