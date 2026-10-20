// Package main corre un lote de enriquecimiento o una reconstrucción completa de la copia.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		slog.Error("enriquecer", "error", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		return errors.New("uso: enriquecer <lote|reconstruir> [limite]")
	}
	dsn := os.Getenv("APP_DATABASE_URL")
	if dsn == "" {
		return errors.New("APP_DATABASE_URL es obligatoria")
	}
	limite := 500
	if len(os.Args) == 3 {
		valor, err := strconv.Atoi(os.Args[2])
		if err != nil || valor <= 0 {
			return fmt.Errorf("límite inválido: %q", os.Args[2])
		}
		limite = valor
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("abrir pool: %w", err)
	}
	defer pool.Close()
	servicio := inteligencia.NuevoServicio(pool)
	switch os.Args[1] {
	case "lote":
		resultado, err := servicio.Enriquecer(ctx, limite)
		if err != nil {
			return err
		}
		slog.Info("lote", "asientos", resultado.Asientos, "movimientos", resultado.Movimientos,
			"sin_contraparte", resultado.SinContraparte, "sin_categoria", resultado.SinCategoria,
			"por_origen", resultado.PorOrigen)
	case "reconstruir":
		huella, err := servicio.Reconstruir(ctx, limite)
		if err != nil {
			return err
		}
		slog.Info("reconstruccion", "huella", huella)
	default:
		return fmt.Errorf("comando desconocido: %q", os.Args[1])
	}
	return nil
}
