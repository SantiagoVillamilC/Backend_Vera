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
	t.Setenv("JWT_ISSUER", "centinela-auth")
	t.Setenv("JWT_AUDIENCE", "centinela-api")
	t.Setenv("CONTADORES_PEPPER", strings.Repeat("11", 32))
	t.Setenv("OTP_PEPPER", strings.Repeat("22", 32))
	t.Setenv("OTP_CANAL", "simulador")
	t.Setenv("AUTH_ENFRIAMIENTO_CAMBIO_NUMERO", "24h")
	t.Setenv("AUTH_RESET_TOKEN_TTL", "10m")
	t.Setenv("AUTH_VENTANA_PENDIENTE", "24h")
	t.Setenv("AUTH_LIMPIEZA_INTERVAL", "15m")
	t.Setenv("AUTH_OUTBOX_INTERVAL", "10s")
	t.Setenv("AUTH_OUTBOX_BACKOFF_BASE", "5s")
	t.Setenv("AUTH_OUTBOX_BACKOFF_FACTOR", "2")
	t.Setenv("AUTH_OUTBOX_BACKOFF_MAX", "5m")
	t.Setenv("AUTH_ARGON2_CONCURRENCIA", "4")
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
		OTPPepper:        bytes.Repeat([]byte{0x55}, 32),
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
	if grupo["redis_url_set"] != true || grupo["contadores_pepper_set"] != true || grupo["otp_pepper_set"] != true ||
		grupo["otp_canal"] != "simulador" || grupo["whatsapp_token_set"] != true {
		t.Errorf("faltan los campos de auth: redis_url_set=%v contadores_pepper_set=%v otp_pepper_set=%v otp_canal=%v whatsapp_token_set=%v",
			grupo["redis_url_set"], grupo["contadores_pepper_set"], grupo["otp_pepper_set"], grupo["otp_canal"], grupo["whatsapp_token_set"])
	}
}

func TestLoadConfigAuthExigeOTPPepperDistinto(t *testing.T) {
	casos := map[string]struct {
		valor   string
		wantErr string
	}{
		"valido":       {valor: strings.Repeat("ab", 32)},
		"hex invalido": {valor: strings.Repeat("zz", 32), wantErr: "OTP_PEPPER must be hex-encoded"},
		"ausente":      {valor: "", wantErr: "OTP_PEPPER is required"},
		"corto":        {valor: strings.Repeat("ab", 31), wantErr: "OTP_PEPPER must decode to at least 32 bytes"},
		"igual":        {valor: strings.Repeat("11", 32), wantErr: "OTP_PEPPER must differ from CONTADORES_PEPPER"},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			baseDeConfig(t)
			t.Setenv("OTP_PEPPER", caso.valor)

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
			if len(cfg.Auth.OTPPepper) != 32 {
				t.Errorf("OTPPepper = %d bytes, quiero 32", len(cfg.Auth.OTPPepper))
			}
		})
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

func TestLoadConfigAuthExigeResetTokenTTL(t *testing.T) {
	casos := map[string]struct {
		valor   string
		wantErr string
	}{
		"valido":      {valor: "10m"},
		"ausente":     {valor: "", wantErr: "AUTH_RESET_TOKEN_TTL is required"},
		"cero":        {valor: "0s", wantErr: "AUTH_RESET_TOKEN_TTL must be a positive duration"},
		"negativo":    {valor: "-1m", wantErr: "AUTH_RESET_TOKEN_TTL must be a positive duration"},
		"no numerico": {valor: "diez minutos", wantErr: "AUTH_RESET_TOKEN_TTL must be a positive duration"},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			baseDeConfig(t)
			t.Setenv("AUTH_RESET_TOKEN_TTL", caso.valor)

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
			if cfg.Auth.ResetTokenTTL != 10*time.Minute {
				t.Errorf("ResetTokenTTL = %s, quiero 10m", cfg.Auth.ResetTokenTTL)
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

func TestLoadConfigAuthExigeElOutbox(t *testing.T) {
	casos := map[string]struct {
		intervalo string
		base      string
		factor    string
		tope      string
		wantErr   string
	}{
		"validos":         {intervalo: "10s", base: "5s", factor: "2", tope: "5m"},
		"sin intervalo":   {intervalo: "", base: "5s", factor: "2", tope: "5m", wantErr: "AUTH_OUTBOX_INTERVAL is required"},
		"intervalo cero":  {intervalo: "0s", base: "5s", factor: "2", tope: "5m", wantErr: "AUTH_OUTBOX_INTERVAL must be a positive duration"},
		"sin base":        {intervalo: "10s", base: "", factor: "2", tope: "5m", wantErr: "AUTH_OUTBOX_BACKOFF_BASE is required"},
		"sin factor":      {intervalo: "10s", base: "5s", factor: "", tope: "5m", wantErr: "AUTH_OUTBOX_BACKOFF_FACTOR is required"},
		"factor no num":   {intervalo: "10s", base: "5s", factor: "dos", tope: "5m", wantErr: "AUTH_OUTBOX_BACKOFF_FACTOR must be a finite number"},
		"factor negativo": {intervalo: "10s", base: "5s", factor: "-2", tope: "5m", wantErr: "AUTH_OUTBOX_BACKOFF_FACTOR must be a finite number"},
		"sin tope":        {intervalo: "10s", base: "5s", factor: "2", tope: "", wantErr: "AUTH_OUTBOX_BACKOFF_MAX is required"},
		"tope negativo":   {intervalo: "10s", base: "5s", factor: "2", tope: "-5m", wantErr: "AUTH_OUTBOX_BACKOFF_MAX must be a positive duration"},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			baseDeConfig(t)
			t.Setenv("AUTH_OUTBOX_INTERVAL", caso.intervalo)
			t.Setenv("AUTH_OUTBOX_BACKOFF_BASE", caso.base)
			t.Setenv("AUTH_OUTBOX_BACKOFF_FACTOR", caso.factor)
			t.Setenv("AUTH_OUTBOX_BACKOFF_MAX", caso.tope)

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
			if cfg.Auth.OutboxInterval != 10*time.Second || cfg.Auth.OutboxBackoffBase != 5*time.Second ||
				cfg.Auth.OutboxBackoffFactor != 2 || cfg.Auth.OutboxBackoffMax != 5*time.Minute {
				t.Errorf("outbox = (%s, %s, %v, %s), quiero (10s, 5s, 2, 5m)",
					cfg.Auth.OutboxInterval, cfg.Auth.OutboxBackoffBase, cfg.Auth.OutboxBackoffFactor, cfg.Auth.OutboxBackoffMax)
			}
		})
	}
}

func TestLoadConfigAuthExigeArgon2Concurrencia(t *testing.T) {
	casos := map[string]struct {
		valor   string
		wantErr string
	}{
		"valido":      {valor: "4"},
		"ausente":     {valor: "", wantErr: "AUTH_ARGON2_CONCURRENCIA is required"},
		"cero":        {valor: "0", wantErr: "AUTH_ARGON2_CONCURRENCIA must be a positive integer"},
		"negativo":    {valor: "-1", wantErr: "AUTH_ARGON2_CONCURRENCIA must be a positive integer"},
		"no numerico": {valor: "cuatro", wantErr: "AUTH_ARGON2_CONCURRENCIA must be a positive integer"},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			baseDeConfig(t)
			t.Setenv("AUTH_ARGON2_CONCURRENCIA", caso.valor)

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
			if cfg.Auth.Argon2Concurrencia != 4 {
				t.Errorf("Argon2Concurrencia = %d, quiero 4", cfg.Auth.Argon2Concurrencia)
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
