package vozlocal

import (
	"errors"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/destinolocal"
)

func TestNuevaSinVariableSoloAceptaLoopback(t *testing.T) {
	t.Setenv(destinolocal.VariableHostPermitido, "")
	if _, err := Nueva("http://127.0.0.1:8766", strings.Repeat("a", 32)); err != nil {
		t.Fatalf("loopback debe aceptarse: %v", err)
	}
	for _, endpoint := range []string{"http://172.18.0.1:8766", "http://10.0.0.5:8766", "http://192.168.1.10:8766", "http://localhost:8766"} {
		if _, err := Nueva(endpoint, strings.Repeat("a", 32)); !errors.Is(err, copilot.ErrSolicitudIA) {
			t.Fatalf("%s debe rechazarse sin la variable: %v", endpoint, err)
		}
	}
}

func TestNuevaAceptaLaIPPrivadaExactaDeLaLista(t *testing.T) {
	t.Setenv(destinolocal.VariableHostPermitido, "172.18.0.1")
	token := strings.Repeat("a", 32)
	if v, err := Nueva("http://172.18.0.1:8766", token); err != nil || v.endpoint != "http://172.18.0.1:8766" || v.token != token {
		t.Fatalf("la IP de la lista debe aceptarse conservando el token: %v", err)
	}
	for _, endpoint := range []string{"http://172.18.0.2:8766", "http://172.18.0.1", "http://172.18.0.1:8766/x", "http://u:p@172.18.0.1:8766", "https://172.18.0.1:8766", "http://8.8.8.8:8766", "http://host.docker.internal:8766"} {
		if _, err := Nueva(endpoint, token); err == nil {
			t.Fatalf("%s no debe aceptarse", endpoint)
		}
	}
	if _, err := Nueva("http://172.18.0.1:8766", "corto"); err == nil {
		t.Fatal("el token de al menos 32 caracteres se sigue exigiendo")
	}
}

func TestNuevaConListaInvalidaNoAutorizaNada(t *testing.T) {
	t.Setenv(destinolocal.VariableHostPermitido, "172.18.0.1,8.8.8.8")
	if _, err := Nueva("http://172.18.0.1:8766", strings.Repeat("a", 32)); err == nil {
		t.Fatal("una lista inválida no debe autorizar la IP")
	}
}
