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
	modo, err := inteligencia.InterpretarModoClasificador(os.Getenv("CLASIFICADOR"))
	if err != nil {
		return err
	}
	var opciones []inteligencia.OpcionServicio
	if modo == inteligencia.ModoClasificadorTFIDF {
		modelo, err := inteligencia.ModeloTFIDFEmbebido()
		if err != nil {
			return fmt.Errorf("clasificador: %w", err)
		}
		opciones = append(opciones, inteligencia.ConClasificador(modelo))
	}
	modoLexico, err := inteligencia.InterpretarModoLexico(os.Getenv("LEXICO"))
	if err != nil {
		return err
	}
	if modoLexico == inteligencia.ModoLexicoV2 {
		lexico, err := inteligencia.CargarLexicoV2()
		if err != nil {
			return fmt.Errorf("léxico: %w", err)
		}
		opciones = append(opciones, inteligencia.ConLexico(lexico))
	}
	// El catálogo curado enciende el escalón 4, igual que en la API. Lo que no se puede es fallar la
	// consulta: sin la tabla el lote aborta. Con la tabla vacía sigue adelante y el escalón 4 queda
	// apagado, así que la copia sale con los orígenes que la cascada decide sin catálogo.
	catalogoNombres := make(map[string]inteligencia.Categoria)
	catalogoLlaves := make(map[string]inteligencia.Categoria)
	opciones = append(opciones, inteligencia.ConCatalogo(catalogoNombres, catalogoLlaves))
	servicio := inteligencia.NuevoServicio(pool, opciones...)
	if err := servicio.CargarCatalogo(ctx); err != nil {
		return fmt.Errorf("catálogo curado: %w", err)
	}
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
