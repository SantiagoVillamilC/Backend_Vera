package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// baseDeEntorno es el entorno mínimo con el que el binario arranca.
var baseDeEntorno = map[string]string{
	"SIMULADOR_PSE_DB":             "/tmp/custodio.db",
	"NUCLEO_CONFIRMACION_URL": "http://127.0.0.1:38080/v1/depositos/confirmaciones",
	"CUSTODIO_TOKEN":          "fedcba9876543210fedcba9876543210",
	"SIMULADOR_CUSTODIA_URL":  "http://127.0.0.1:8099/notificaciones/depositos",
}

// todasLasClaves son las variables que LoadConfig lee, para limpiarlas entre casos.
var todasLasClaves = []string{
	"SIMULADOR_PSE_ADDR", "SIMULADOR_PSE_PERMITIR_EXPOSICION", "SIMULADOR_PSE_PORT", "SIMULADOR_PSE_DB", "NUCLEO_CONFIRMACION_URL", "CUSTODIO_TOKEN", "SIMULADOR_CUSTODIA_URL",
}

// LoadConfig valida el entorno completo y reporta el primer defecto de cada caso.
func TestLoadConfig(t *testing.T) {
	tests := map[string]struct {
		override map[string]string
		vaciar   []string
		wantErr  bool
		puerto   string
		addr     string
	}{
		"valida con el puerto por defecto": {puerto: "8090", addr: "127.0.0.1"},
		"puerto explícito":                 {override: map[string]string{"SIMULADOR_PSE_PORT": "9000"}, puerto: "9000", addr: "127.0.0.1"},
		"addr localhost":                   {override: map[string]string{"SIMULADOR_PSE_ADDR": "localhost"}, puerto: "8090", addr: "localhost"},
		"addr loopback ipv6":               {override: map[string]string{"SIMULADOR_PSE_ADDR": "::1"}, puerto: "8090", addr: "::1"},
		"addr no loopback falla":           {override: map[string]string{"SIMULADOR_PSE_ADDR": "0.0.0.0"}, wantErr: true},
		"addr no loopback habilitada":      {override: map[string]string{"SIMULADOR_PSE_ADDR": "0.0.0.0", "SIMULADOR_PSE_PERMITIR_EXPOSICION": "true"}, puerto: "8090", addr: "0.0.0.0"},
		"habilitación no explícita":        {override: map[string]string{"SIMULADOR_PSE_ADDR": "0.0.0.0", "SIMULADOR_PSE_PERMITIR_EXPOSICION": "1"}, wantErr: true},
		"puerto cero":                      {override: map[string]string{"SIMULADOR_PSE_PORT": "0"}, wantErr: true},
		"sin base sqlite":                  {vaciar: []string{"SIMULADOR_PSE_DB"}, wantErr: true},
		"sin token":                        {vaciar: []string{"CUSTODIO_TOKEN"}, wantErr: true},
		"token de 31 bytes":                {override: map[string]string{"CUSTODIO_TOKEN": strings.Repeat("a", 31)}, wantErr: true},
		"token de 32 bytes arranca":        {override: map[string]string{"CUSTODIO_TOKEN": strings.Repeat("a", 32)}, puerto: "8090", addr: "127.0.0.1"},
		"sin url del núcleo":               {vaciar: []string{"NUCLEO_CONFIRMACION_URL"}, wantErr: true},
		"sin url del simulador":            {vaciar: []string{"SIMULADOR_CUSTODIA_URL"}, wantErr: true},
		"url sin esquema":                  {override: map[string]string{"NUCLEO_CONFIRMACION_URL": "127.0.0.1:38080"}, wantErr: true},
		"url sin host":                     {override: map[string]string{"SIMULADOR_CUSTODIA_URL": "http://"}, wantErr: true},
	}

	for nombre, tc := range tests {
		t.Run(nombre, func(t *testing.T) {
			for _, clave := range todasLasClaves {
				t.Setenv(clave, "")
			}
			for clave, valor := range baseDeEntorno {
				t.Setenv(clave, valor)
			}
			for _, clave := range tc.vaciar {
				t.Setenv(clave, "")
			}
			for clave, valor := range tc.override {
				t.Setenv(clave, valor)
			}

			cfg, err := LoadConfig()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("LoadConfig = %+v, quiero error", cfg)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if cfg.Port != tc.puerto {
				t.Errorf("Port = %q, quiero %q", cfg.Port, tc.puerto)
			}
			if cfg.Addr != tc.addr {
				t.Errorf("Addr = %q, quiero %q", cfg.Addr, tc.addr)
			}
		})
	}
}

// El rechazo de una dirección no loopback nombra la variable que habilita la exposición.
func TestLoadConfigRechazaAddrNoLoopbackConIndicacionDeHabilitar(t *testing.T) {
	for _, clave := range todasLasClaves {
		t.Setenv(clave, "")
	}
	for clave, valor := range baseDeEntorno {
		t.Setenv(clave, valor)
	}
	t.Setenv("SIMULADOR_PSE_ADDR", "0.0.0.0")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("LoadConfig aceptó una dirección no loopback sin habilitación")
	}
	if !strings.Contains(err.Error(), "SIMULADOR_PSE_PERMITIR_EXPOSICION=true") {
		t.Errorf("el error no indica cómo habilitar la exposición: %v", err)
	}
}

// El LogValue de la config no expone el token ni la ruta de la base.
func TestConfigLogValueNoExponeElToken(t *testing.T) {
	cfg := Config{
		Port: "8090", DBPath: "/tmp/custodio.db", CustodioToken: "token-secreto",
		NucleoConfirmacionURL: baseDeEntorno["NUCLEO_CONFIRMACION_URL"],
		SimuladorCustodiaURL:  baseDeEntorno["SIMULADOR_CUSTODIA_URL"],
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("config loaded", "config", cfg)

	if strings.Contains(buf.String(), "token-secreto") {
		t.Errorf("el log expuso el token: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"custodio_token_set":true`) {
		t.Errorf("el log no reportó la presencia del token: %s", buf.String())
	}
}

// El LogValue redacta userinfo y query de las URLs y conserva a lo sumo esquema y host.
func TestConfigLogValueRedactaLasCredencialesDeLaURL(t *testing.T) {
	cfg := Config{
		Port: "8090", DBPath: "/tmp/custodio.db", CustodioToken: "token-secreto",
		NucleoConfirmacionURL: "https://usuario:secreto@nucleo.interno/confirmaciones?clave=abc",
		SimuladorCustodiaURL:  "http://custodio.interno/notificaciones",
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("config loaded", "config", cfg)
	salida := buf.String()

	for _, prohibido := range []string{"usuario", "secreto", "clave=abc", "/confirmaciones"} {
		if strings.Contains(salida, prohibido) {
			t.Errorf("el log expuso %q: %s", prohibido, salida)
		}
	}
	if !strings.Contains(salida, "nucleo.interno") {
		t.Errorf("el log no conservó el host: %s", salida)
	}
}

// El error de urlObligatoria nombra la variable sin volcar el valor.
func TestURLObligatoriaNoVuelcaElValor(t *testing.T) {
	errs := urlObligatoria("NUCLEO_CONFIRMACION_URL", "ftp://usuario:secreto@interno/x?clave=abc")
	if len(errs) != 1 {
		t.Fatalf("errores = %d, quiero 1", len(errs))
	}
	mensaje := errs[0].Error()
	if !strings.Contains(mensaje, "NUCLEO_CONFIRMACION_URL") {
		t.Errorf("el error no nombró la variable: %s", mensaje)
	}
	for _, prohibido := range []string{"usuario", "secreto", "interno", "clave=abc"} {
		if strings.Contains(mensaje, prohibido) {
			t.Errorf("el error volcó %q: %s", prohibido, mensaje)
		}
	}
}
