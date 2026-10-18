package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// limpiarEntorno deja el entorno del simulador en sus valores por defecto.
func limpiarEntorno(t *testing.T) {
	t.Helper()

	for _, clave := range []string{
		"SIMULADOR_RIEL_ADDR", "SIMULADOR_RIEL_PORT", "SIMULADOR_RIEL_DB", "SIMULADOR_RIEL_ORIGEN",
		"SIMULADOR_RIEL_P_RECHAZO", "SIMULADOR_RIEL_LATENCIA", "SIMULADOR_RIEL_LATENCIA_VARIACION",
		"SIMULADOR_RIEL_MODO_CAIDA", "SIMULADOR_RIEL_P_RESPUESTA_PERDIDA", "SIMULADOR_RIEL_PERMITIR_EXPOSICION",
	} {
		t.Setenv(clave, "")
	}
}

func TestLoadConfigPorDefecto(t *testing.T) {
	limpiarEntorno(t)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Port != "8098" || cfg.Addr != "127.0.0.1" || cfg.Origen != "simulador-riel" {
		t.Errorf("config = %+v, quiero los valores por defecto", cfg)
	}
	if cfg.Perillas.ModoCaida != ModoCaidaNinguna || cfg.Perillas.PRechazo != 0 || cfg.Perillas.PRespuestaPerdida != 0 {
		t.Errorf("perillas = %+v, quiero sin caída y sin rechazo", cfg.Perillas)
	}
}

func TestLoadConfigLeeLasPerillas(t *testing.T) {
	limpiarEntorno(t)
	t.Setenv("SIMULADOR_RIEL_P_RECHAZO", "0.5")
	t.Setenv("SIMULADOR_RIEL_P_RESPUESTA_PERDIDA", "0.25")
	t.Setenv("SIMULADOR_RIEL_LATENCIA", "250ms")
	t.Setenv("SIMULADOR_RIEL_LATENCIA_VARIACION", "50ms")
	t.Setenv("SIMULADOR_RIEL_MODO_CAIDA", string(ModoCaidaSilencio))

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	quiero := Perillas{PRechazo: 0.5, PRespuestaPerdida: 0.25, Latencia: 250 * time.Millisecond, Variacion: 50 * time.Millisecond, ModoCaida: ModoCaidaSilencio}
	if cfg.Perillas != quiero {
		t.Errorf("perillas = %+v, quiero %+v", cfg.Perillas, quiero)
	}
}

func TestLoadConfigRechazaPerillasInvalidas(t *testing.T) {
	tests := map[string]struct {
		clave  string
		valor  string
		nombre string
	}{
		"p rechazo no numérica": {"SIMULADOR_RIEL_P_RECHAZO", "mucho", "SIMULADOR_RIEL_P_RECHAZO"},
		"p rechazo fuera":       {"SIMULADOR_RIEL_P_RECHAZO", "1.5", "SIMULADOR_RIEL_P_RECHAZO"},
		"p perdida negativa":    {"SIMULADOR_RIEL_P_RESPUESTA_PERDIDA", "-0.1", "SIMULADOR_RIEL_P_RESPUESTA_PERDIDA"},
		"latencia negativa":     {"SIMULADOR_RIEL_LATENCIA", "-1ms", "SIMULADOR_RIEL_LATENCIA"},
		"variacion inválida":    {"SIMULADOR_RIEL_LATENCIA_VARIACION", "pronto", "SIMULADOR_RIEL_LATENCIA_VARIACION"},
		"modo desconocido":      {"SIMULADOR_RIEL_MODO_CAIDA", "apagado", "modo_caida"},
		"puerto inválido":       {"SIMULADOR_RIEL_PORT", "0", "SIMULADOR_RIEL_PORT"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			limpiarEntorno(t)
			t.Setenv(tc.clave, tc.valor)

			cfg, err := LoadConfig()
			if err == nil || !strings.Contains(err.Error(), tc.nombre) {
				t.Fatalf("LoadConfig error = %v, quiero que contenga %q", err, tc.nombre)
			}
			if cfg != nil {
				t.Errorf("config = %+v, quiero nil", cfg)
			}
		})
	}
}

func TestLoadConfigExigeLoopbackSalvoExposicion(t *testing.T) {
	limpiarEntorno(t)
	t.Setenv("SIMULADOR_RIEL_ADDR", "0.0.0.0")

	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "SIMULADOR_RIEL_ADDR") {
		t.Fatalf("LoadConfig con 0.0.0.0 error = %v, quiero que exija loopback", err)
	}

	t.Setenv("SIMULADOR_RIEL_PERMITIR_EXPOSICION", "true")
	if _, err := LoadConfig(); err != nil {
		t.Fatalf("LoadConfig expuesto: %v", err)
	}
}

func TestConfigLogValueNoImprimeLaRuta(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Addr:     "127.0.0.1",
		Port:     "8098",
		DBPath:   "/datos/secreto-del-simulador.db",
		Origen:   "simulador-riel",
		Perillas: Perillas{PRechazo: 0.5, ModoCaida: ModoCaidaSilencio},
	}

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("config loaded", "config", cfg)

	if salida := buf.String(); strings.Contains(salida, cfg.DBPath) {
		t.Errorf("la ruta de la base se filtró en el log: %s", salida)
	}
	var registro map[string]any
	if err := json.Unmarshal(buf.Bytes(), &registro); err != nil {
		t.Fatalf("registro inválido: %v (%q)", err, buf.String())
	}
	grupo, ok := registro["config"].(map[string]any)
	if !ok {
		t.Fatalf("config no se agrupó: %s", buf.String())
	}
	if grupo["db_path_set"] != true {
		t.Errorf("db_path_set = %v, quiero true", grupo["db_path_set"])
	}
	perillas, ok := grupo["perillas"].(map[string]any)
	if !ok {
		t.Fatalf("perillas no se agruparon: %s", buf.String())
	}
	if perillas["modo_caida"] != string(ModoCaidaSilencio) {
		t.Errorf("modo_caida = %v, quiero %q", perillas["modo_caida"], ModoCaidaSilencio)
	}
}
