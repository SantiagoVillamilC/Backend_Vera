package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// todasLasClaves son las variables que LoadConfig lee, para limpiarlas entre casos.
var todasLasClaves = []string{
	"SIMULADOR_CUSTODIA_ADDR", "SIMULADOR_CUSTODIA_PORT", "SIMULADOR_CUSTODIA_DB",
	"SIMULADOR_CUSTODIA_ORIGEN", "SIMULADOR_CUSTODIA_PERMITIR_EXPOSICION",
	"SIMULADOR_CUSTODIA_P_RECHAZO", "SIMULADOR_CUSTODIA_LATENCIA_MS", "SIMULADOR_CUSTODIA_LATENCIA_VAR_MS",
	"SIMULADOR_CUSTODIA_MODO_CAIDA", "SIMULADOR_CUSTODIA_P_RESPUESTA_PERDIDA",
	"SIMULADOR_CUSTODIA_P_NOTIFICACION_PERDIDA",
}

// LoadConfig resuelve el entorno y reporta el primer defecto de cada caso.
func TestLoadConfig(t *testing.T) {
	tests := map[string]struct {
		override map[string]string
		wantErr  bool
		puerto   string
		addr     string
		origen   string
	}{
		"valores por defecto":         {puerto: "8099", addr: "127.0.0.1", origen: "simulador-custodia"},
		"puerto explícito":            {override: map[string]string{"SIMULADOR_CUSTODIA_PORT": "9000"}, puerto: "9000", addr: "127.0.0.1", origen: "simulador-custodia"},
		"addr localhost":              {override: map[string]string{"SIMULADOR_CUSTODIA_ADDR": "localhost"}, puerto: "8099", addr: "localhost", origen: "simulador-custodia"},
		"addr loopback ipv6":          {override: map[string]string{"SIMULADOR_CUSTODIA_ADDR": "::1"}, puerto: "8099", addr: "::1", origen: "simulador-custodia"},
		"addr no loopback falla":      {override: map[string]string{"SIMULADOR_CUSTODIA_ADDR": "0.0.0.0"}, wantErr: true},
		"addr no loopback habilitada": {override: map[string]string{"SIMULADOR_CUSTODIA_ADDR": "0.0.0.0", "SIMULADOR_CUSTODIA_PERMITIR_EXPOSICION": "true"}, puerto: "8099", addr: "0.0.0.0", origen: "simulador-custodia"},
		"habilitación no explícita":   {override: map[string]string{"SIMULADOR_CUSTODIA_ADDR": "0.0.0.0", "SIMULADOR_CUSTODIA_PERMITIR_EXPOSICION": "1"}, wantErr: true},
		"puerto cero":                 {override: map[string]string{"SIMULADOR_CUSTODIA_PORT": "0"}, wantErr: true},
		"puerto fuera de rango":       {override: map[string]string{"SIMULADOR_CUSTODIA_PORT": "70000"}, wantErr: true},
		"origen en blanco":            {override: map[string]string{"SIMULADOR_CUSTODIA_ORIGEN": "   "}, wantErr: true},
		"origen explícito":            {override: map[string]string{"SIMULADOR_CUSTODIA_ORIGEN": "banco-simulado"}, puerto: "8099", addr: "127.0.0.1", origen: "banco-simulado"},
		"ruta de base explícita":      {override: map[string]string{"SIMULADOR_CUSTODIA_DB": "/tmp/x.db"}, puerto: "8099", addr: "127.0.0.1", origen: "simulador-custodia"},
		"latencia fuera del tope":     {override: map[string]string{"SIMULADOR_CUSTODIA_LATENCIA_MS": "999999999"}, wantErr: true},
	}

	for nombre, tc := range tests {
		t.Run(nombre, func(t *testing.T) {
			for _, clave := range todasLasClaves {
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
			if cfg.Origen != tc.origen {
				t.Errorf("Origen = %q, quiero %q", cfg.Origen, tc.origen)
			}
		})
	}
}

// El rechazo de una dirección no loopback nombra la variable que habilita la exposición.
func TestLoadConfigRechazaAddrNoLoopbackConIndicacionDeHabilitar(t *testing.T) {
	for _, clave := range todasLasClaves {
		t.Setenv(clave, "")
	}
	t.Setenv("SIMULADOR_CUSTODIA_ADDR", "0.0.0.0")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("LoadConfig aceptó una dirección no loopback sin habilitación")
	}
	if !strings.Contains(err.Error(), "SIMULADOR_CUSTODIA_PERMITIR_EXPOSICION=true") {
		t.Errorf("el error no indica cómo habilitar la exposición: %v", err)
	}
}

// El LogValue de la config no expone la ruta de la base.
func TestConfigLogValueNoExponeLaRutaDeLaBase(t *testing.T) {
	cfg := Config{Port: "8099", Addr: "127.0.0.1", DBPath: "/ruta/secreta/movimiento.db", Origen: "simulador-custodia"}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("config loaded", "config", cfg)

	if strings.Contains(buf.String(), "secreta") {
		t.Errorf("el log expuso la ruta de la base: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"db_path_set":true`) {
		t.Errorf("el log no reportó la presencia de la base: %s", buf.String())
	}
}
