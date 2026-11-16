package sttlocal

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/destinolocal"
)

func TestNuevoSinVariableSoloAceptaLoopback(t *testing.T) {
	t.Setenv(destinolocal.VariableHostPermitido, "")
	if _, err := Nuevo("http://127.0.0.1:8777", strings.Repeat("a", 32), time.Second); err != nil {
		t.Fatalf("loopback debe aceptarse: %v", err)
	}
	for _, endpoint := range []string{"http://172.18.0.1:8777", "http://10.0.0.5:8777", "http://192.168.1.10:8777", "http://localhost:8777"} {
		if _, err := Nuevo(endpoint, strings.Repeat("a", 32), time.Second); !errors.Is(err, copilot.ErrSolicitudIA) {
			t.Fatalf("%s debe rechazarse sin la variable: %v", endpoint, err)
		}
	}
}

func TestNuevoAceptaLaIPPrivadaExactaDeLaLista(t *testing.T) {
	t.Setenv(destinolocal.VariableHostPermitido, "172.18.0.1")
	token := strings.Repeat("a", 32)
	if c, err := Nuevo("http://172.18.0.1:8777", token, time.Second); err != nil || c.endpoint != "http://172.18.0.1:8777" || c.token != token {
		t.Fatalf("la IP de la lista debe aceptarse conservando el token: %v", err)
	}
	for _, endpoint := range []string{"http://172.18.0.2:8777", "http://172.18.0.1", "http://172.18.0.1:8777/x", "http://u:p@172.18.0.1:8777", "https://172.18.0.1:8777", "http://8.8.8.8:8777", "http://host.docker.internal:8777"} {
		if _, err := Nuevo(endpoint, token, time.Second); err == nil {
			t.Fatalf("%s no debe aceptarse", endpoint)
		}
	}
	if _, err := Nuevo("http://172.18.0.1:8777", "corto", time.Second); err == nil {
		t.Fatal("el token de al menos 32 caracteres se sigue exigiendo")
	}
	if _, err := Nuevo("http://172.18.0.1:8777", token, 3*time.Minute); err == nil {
		t.Fatal("el plazo máximo se sigue exigiendo")
	}
}
