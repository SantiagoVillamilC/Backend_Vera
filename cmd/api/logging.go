package main

import (
	"log/slog"
	"os"
)

// newHandler construye el handler de slog a partir de la config ya validada. Producción
// usa JSON y desarrollo usa texto, con el nivel que fijó LOG_LEVEL.
func newHandler(cfg *Config) slog.Handler {
	var lvl slog.Level
	_ = lvl.UnmarshalText([]byte(cfg.LogLevel)) // no puede fallar: LoadConfig ya lo validó
	opts := &slog.HandlerOptions{Level: lvl}
	if cfg.LogFormat == "text" {
		return slog.NewTextHandler(os.Stderr, opts)
	}
	return slog.NewJSONHandler(os.Stderr, opts)
}
