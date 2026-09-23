package correosim

import "testing"

func TestSimuladorGuardaElUltimoAviso(t *testing.T) {
	t.Parallel()

	const destino = "aviso@centinela.test"
	simulador := Nuevo()

	id, err := simulador.Enviar(t.Context(), destino, "asunto de prueba", "cuerpo de prueba")
	if err != nil {
		t.Fatalf("Enviar: %v", err)
	}
	if id == "" {
		t.Error("Enviar devolvió un id vacío")
	}

	aviso, ok := simulador.UltimoAviso(destino)
	if !ok {
		t.Fatalf("UltimoAviso(%q) no encontró el aviso", destino)
	}
	if aviso.Asunto != "asunto de prueba" || aviso.Cuerpo != "cuerpo de prueba" {
		t.Errorf("aviso = %+v, quiero el asunto y el cuerpo enviados", aviso)
	}
	if _, ok := simulador.UltimoAviso("otro@centinela.test"); ok {
		t.Error("UltimoAviso devolvió un aviso para un correo que nunca recibió nada")
	}
}
