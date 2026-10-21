package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// entornoDeMetricas deja el entorno mínimo que LoadConfig ya exige.
func entornoDeMetricas(t *testing.T) {
	t.Helper()
	t.Setenv("APP_DATABASE_URL", "postgres://nucleo:dsn@localhost:5432/centinela")
	t.Setenv("JWT_SECRET", "jwt-secreto-de-al-menos-treinta-y-dos-bytes")
	t.Setenv("CUSTODIO_TOKEN", strings.Repeat("c", 32))
	entornoDeAuth(t)
}

// TestLoadConfigMetricasApagadasPorDefecto mantiene la superficie cerrada sin tocar el entorno.
func TestLoadConfigMetricasApagadasPorDefecto(t *testing.T) {
	entornoDeMetricas(t)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.MetricasActivo {
		t.Fatal("METRICAS_ACTIVO debe empezar apagada")
	}
}

// TestLoadConfigMetricasExigeTokenLargo cierra la superficie con una credencial débil.
func TestLoadConfigMetricasExigeTokenLargo(t *testing.T) {
	casos := map[string]struct {
		activa     string
		token      string
		quieroErro string
	}{
		"encendida sin token":       {activa: "true", token: "", quieroErro: "METRICAS_TOKEN must be at least 32 bytes"},
		"encendida con token corto": {activa: "true", token: "corto", quieroErro: "METRICAS_TOKEN must be at least 32 bytes"},
		"encendida con token igual al custodio": {activa: "true", token: strings.Repeat("c", 32),
			quieroErro: "METRICAS_TOKEN must differ from CUSTODIO_TOKEN and JWT_SECRET"},
		"encendida con token igual al jwt": {activa: "true",
			token:      "jwt-secreto-de-al-menos-treinta-y-dos-bytes",
			quieroErro: "METRICAS_TOKEN must differ from CUSTODIO_TOKEN and JWT_SECRET"},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			entornoDeMetricas(t)
			t.Setenv("METRICAS_ACTIVO", caso.activa)
			t.Setenv("METRICAS_TOKEN", caso.token)

			_, err := LoadConfig()
			if err == nil || !strings.Contains(err.Error(), caso.quieroErro) {
				t.Fatalf("error = %v, quiero %q", err, caso.quieroErro)
			}
		})
	}
}

// TestLoadConfigMetricasConTokenPropioArranca deja la configuración válida en verde.
func TestLoadConfigMetricasConTokenPropioArranca(t *testing.T) {
	entornoDeMetricas(t)
	t.Setenv("METRICAS_ACTIVO", "true")
	t.Setenv("METRICAS_TOKEN", strings.Repeat("m", 32))

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.MetricasActivo || len(cfg.MetricasToken) != 32 {
		t.Fatalf("MetricasActivo = %v, token de %d bytes", cfg.MetricasActivo, len(cfg.MetricasToken))
	}
}

// TestConfigLogValueNoImprimeElTokenDeMetricas es la misma regla que ya vale para los demás secretos.
func TestConfigLogValueNoImprimeElTokenDeMetricas(t *testing.T) {
	t.Parallel()
	token := strings.Repeat("m", 32)
	cfg := Config{MetricasActivo: true, MetricasToken: token}

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("config loaded", "config", cfg)
	salida := buf.String()

	if strings.Contains(salida, token) {
		t.Fatalf("el token de métricas se filtró en el log: %s", salida)
	}
	if !strings.Contains(salida, `"metricas_token_set":true`) {
		t.Fatalf("falta la marca de que el token está puesto: %s", salida)
	}
}
