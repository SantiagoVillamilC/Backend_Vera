package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

const jwtDePrueba = "jwt-secreto-de-al-menos-treinta-y-dos-bytes"

// entornoDeAuth fija las variables del bloque de auth que LoadConfig exige para aislar cada caso.
func entornoDeAuth(t *testing.T) {
	t.Helper()
	t.Setenv("REDIS_URL", "redis://:secreta@127.0.0.1:16381/0")
	t.Setenv("CONTADORES_PEPPER", strings.Repeat("11", 32))
	t.Setenv("OTP_CANAL", "simulador")
	t.Setenv("AUTH_ENFRIAMIENTO_CAMBIO_NUMERO", "24h")
	t.Setenv("AUTH_VENTANA_PENDIENTE", "24h")
	t.Setenv("AUTH_LIMPIEZA_INTERVAL", "15m")
}

func baseDeConfig(t *testing.T) {
	t.Helper()
	t.Setenv("APP_DATABASE_URL", "postgres://rol_api:api@127.0.0.1:35431/centinela?sslmode=disable")
	t.Setenv("JWT_SECRET", jwtDePrueba)
	t.Setenv("CUSTODIO_TOKEN", strings.Repeat("c", 32))
	entornoDeAuth(t)
}

func TestLoadConfigAuth(t *testing.T) {
	tests := map[string]struct {
		redisURL string
		otp      string
		token    string
		phoneID  string
		template string
		wantErr  string
	}{
		"valido simulador":        {redisURL: "redis://127.0.0.1:16381/0", otp: "simulador"},
		"valido rediss":           {redisURL: "rediss://127.0.0.1:16381/0", otp: "simulador"},
		"simulador sin cloud api": {redisURL: "redis://127.0.0.1:16381/0", otp: "simulador"},
		"demo admitido":           {redisURL: "redis://127.0.0.1:16381/0", otp: "demo"},
		"cloudapi completo":       {redisURL: "redis://127.0.0.1:16381/0", otp: "cloudapi", token: "tok", phoneID: "123", template: "hola"},
		"sin redis":               {otp: "simulador", wantErr: "REDIS_URL is required"},
		"redis sin host":          {redisURL: "redis://", otp: "simulador", wantErr: "REDIS_URL must be a redis:// or rediss:// URL with a host"},
		"redis esquema ajeno":     {redisURL: "http://127.0.0.1:16381/0", otp: "simulador", wantErr: "REDIS_URL must be a redis:// or rediss:// URL with a host"},
		"redis base no numerica":  {redisURL: "redis://127.0.0.1:16381/auth", otp: "simulador", wantErr: "REDIS_URL must be a valid Redis URL"},
		"sin otp canal":           {redisURL: "redis://127.0.0.1:16381/0", wantErr: "OTP_CANAL is required"},
		"otp canal desconocido":   {redisURL: "redis://127.0.0.1:16381/0", otp: "otro", wantErr: `OTP_CANAL must be simulador, cloudapi or demo, got "otro"`},
		"cloudapi sin token":      {redisURL: "redis://127.0.0.1:16381/0", otp: "cloudapi", phoneID: "123", template: "hola", wantErr: "WHATSAPP_TOKEN is required when OTP_CANAL=cloudapi"},
		"cloudapi sin phone id":   {redisURL: "redis://127.0.0.1:16381/0", otp: "cloudapi", token: "tok", template: "hola", wantErr: "WHATSAPP_PHONE_ID is required when OTP_CANAL=cloudapi"},
		"cloudapi sin template":   {redisURL: "redis://127.0.0.1:16381/0", otp: "cloudapi", token: "tok", phoneID: "123", wantErr: "WHATSAPP_TEMPLATE is required when OTP_CANAL=cloudapi"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			baseDeConfig(t)
			t.Setenv("REDIS_URL", tc.redisURL)
			t.Setenv("OTP_CANAL", tc.otp)
			t.Setenv("WHATSAPP_TOKEN", tc.token)
			t.Setenv("WHATSAPP_PHONE_ID", tc.phoneID)
			t.Setenv("WHATSAPP_TEMPLATE", tc.template)

			cfg, err := LoadConfig()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("LoadConfig error = %v, quiero que contenga %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if cfg.Auth.RedisURL != tc.redisURL || cfg.Auth.OTPCanal != tc.otp {
				t.Errorf("Auth = %+v, quiero RedisURL %q y OTP_CANAL %q", cfg.Auth, tc.redisURL, tc.otp)
			}
		})
	}
}

func TestLoadConfigAuthNoFiltraLaContrasenaDeRedis(t *testing.T) {
	const contrasena = "clave-secreta-de-redis"

	casos := map[string]struct {
		raw     string
		wantErr bool
	}{
		"url valida con clave": {raw: "redis://:" + contrasena + "@127.0.0.1:16381/0"},
		"esquema invalido":     {raw: "http://:" + contrasena + "@127.0.0.1:16381/0", wantErr: true},
		"host ausente":         {raw: "redis://:" + contrasena + "@", wantErr: true},
		"base no parseable":    {raw: "redis://:" + contrasena + "@127.0.0.1:16381/auth", wantErr: true},
	}
	for name, tc := range casos {
		t.Run(name, func(t *testing.T) {
			baseDeConfig(t)
			t.Setenv("REDIS_URL", tc.raw)

			_, err := LoadConfig()
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("LoadConfig: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("LoadConfig no falló")
			}
			if strings.Contains(err.Error(), contrasena) {
				t.Fatalf("el error filtra la contraseña de Redis: %v", err)
			}
		})
	}
}

func TestConfigLogValueRegistraAuth(t *testing.T) {
	t.Parallel()

	const contrasena = "clave-secreta-de-redis"
	cfg := Config{Auth: ConfigAuth{
		RedisURL:         "redis://:" + contrasena + "@127.0.0.1:16381/0",
		OTPCanal:         "simulador",
		WhatsAppToken:    "token-de-whatsapp",
		ContadoresPepper: bytes.Repeat([]byte{0x44}, 32),
	}}

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("config loaded", "config", cfg)

	if strings.Contains(buf.String(), contrasena) || strings.Contains(buf.String(), "token-de-whatsapp") {
		t.Fatalf("el log filtró un secreto: %s", buf.String())
	}

	var registro map[string]any
	if err := json.Unmarshal(buf.Bytes(), &registro); err != nil {
		t.Fatalf("registro inválido: %v (%q)", err, buf.String())
	}
	grupo, ok := registro["config"].(map[string]any)
	if !ok {
		t.Fatalf("config no se agrupó: %s", buf.String())
	}
	if grupo["redis_url_set"] != true || grupo["contadores_pepper_set"] != true || grupo["otp_canal"] != "simulador" || grupo["whatsapp_token_set"] != true {
		t.Errorf("faltan los campos de auth: redis_url_set=%v contadores_pepper_set=%v otp_canal=%v whatsapp_token_set=%v",
			grupo["redis_url_set"], grupo["contadores_pepper_set"], grupo["otp_canal"], grupo["whatsapp_token_set"])
	}
}

func TestLoadConfigAuthExigeEnfriamientoCambioNumero(t *testing.T) {
	casos := map[string]struct {
		valor   string
		wantErr string
	}{
		"valido":      {valor: "24h"},
		"ausente":     {valor: "", wantErr: "AUTH_ENFRIAMIENTO_CAMBIO_NUMERO is required"},
		"cero":        {valor: "0s", wantErr: "AUTH_ENFRIAMIENTO_CAMBIO_NUMERO must be a positive duration"},
		"negativo":    {valor: "-1h", wantErr: "AUTH_ENFRIAMIENTO_CAMBIO_NUMERO must be a positive duration"},
		"no numerico": {valor: "veinticuatro", wantErr: "AUTH_ENFRIAMIENTO_CAMBIO_NUMERO must be a positive duration"},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			baseDeConfig(t)
			t.Setenv("AUTH_ENFRIAMIENTO_CAMBIO_NUMERO", caso.valor)

			cfg, err := LoadConfig()
			if caso.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), caso.wantErr) {
					t.Fatalf("LoadConfig error = %v, quiero que contenga %q", err, caso.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if cfg.Auth.EnfriamientoCambioNumero != 24*time.Hour {
				t.Errorf("EnfriamientoCambioNumero = %s, quiero 24h", cfg.Auth.EnfriamientoCambioNumero)
			}
		})
	}
}

func TestLoadConfigAuthExigeContadoresPepper(t *testing.T) {
	casos := map[string]struct {
		valor   string
		wantErr string
	}{
		"valido":       {valor: strings.Repeat("ab", 32)},
		"hex invalido": {valor: strings.Repeat("zz", 32), wantErr: "CONTADORES_PEPPER must be hex-encoded"},
		"ausente":      {valor: "", wantErr: "CONTADORES_PEPPER is required"},
		"corto":        {valor: strings.Repeat("ab", 31), wantErr: "CONTADORES_PEPPER must decode to at least 32 bytes"},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			baseDeConfig(t)
			t.Setenv("CONTADORES_PEPPER", caso.valor)

			cfg, err := LoadConfig()
			if caso.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), caso.wantErr) {
					t.Fatalf("LoadConfig error = %v, quiero que contenga %q", err, caso.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if len(cfg.Auth.ContadoresPepper) != 32 {
				t.Errorf("ContadoresPepper = %d bytes, quiero 32", len(cfg.Auth.ContadoresPepper))
			}
		})
	}
}

func TestLoadConfigAuthExigeVentanaPendienteYLimpieza(t *testing.T) {
	casos := map[string]struct {
		ventana  string
		limpieza string
		wantErr  string
	}{
		"validos":           {ventana: "24h", limpieza: "15m"},
		"ventana ausente":   {ventana: "", limpieza: "15m", wantErr: "AUTH_VENTANA_PENDIENTE is required"},
		"ventana cero":      {ventana: "0s", limpieza: "15m", wantErr: "AUTH_VENTANA_PENDIENTE must be a positive duration"},
		"limpieza ausente":  {ventana: "24h", limpieza: "", wantErr: "AUTH_LIMPIEZA_INTERVAL is required"},
		"limpieza negativa": {ventana: "24h", limpieza: "-1m", wantErr: "AUTH_LIMPIEZA_INTERVAL must be a positive duration"},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			baseDeConfig(t)
			t.Setenv("AUTH_VENTANA_PENDIENTE", caso.ventana)
			t.Setenv("AUTH_LIMPIEZA_INTERVAL", caso.limpieza)

			cfg, err := LoadConfig()
			if caso.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), caso.wantErr) {
					t.Fatalf("LoadConfig error = %v, quiero que contenga %q", err, caso.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if cfg.Auth.VentanaPendiente != 24*time.Hour || cfg.Auth.LimpiezaInterval != 15*time.Minute {
				t.Errorf("VentanaPendiente = %s, LimpiezaInterval = %s, quiero 24h y 15m", cfg.Auth.VentanaPendiente, cfg.Auth.LimpiezaInterval)
			}
		})
	}
}
