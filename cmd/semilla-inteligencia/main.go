// Comando semilla-inteligencia construye un ledger sintético determinista para la prueba de carga de
// M8. Solo escribe contra el Postgres desechable del puerto 55437 y nunca usa datos reales.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/database"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("semilla-inteligencia: %v", err)
	}
}

func run() error {
	usuarios := flag.Int("usuarios", 5000, "usuarios sintéticos a sembrar")
	meses := flag.Int("meses", 10, "meses completos por usuario")
	extra := flag.Int("asientos-extra", 5000, "asientos sueltos para llenar lotes del lote de enriquecimiento")
	semilla := flag.Uint64("semilla", Semilla, "semilla fija del generador")
	rutaTokens := flag.String("tokens", "", "ruta donde escribir los access de la prueba de carga")
	soloTokens := flag.Bool("solo-tokens", false, "acuña los access sin volver a sembrar el banco")
	permitirRemoto := flag.Bool("permitir-remoto", false, "acepta un host que no sea loopback, solo con una base desechable")
	flag.Parse()

	dsn := os.Getenv("DATABASE_URL")
	if err := VerificarDSN(dsn, *semilla, *permitirRemoto); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if *soloTokens {
		if *rutaTokens == "" {
			return errors.New("--solo-tokens necesita --tokens")
		}
		return escribirTokens(*rutaTokens, *semilla, *usuarios)
	}

	pool, err := database.NewPool(ctx, dsn)
	if err != nil {
		return fmt.Errorf("pool: %w", err)
	}
	defer pool.Close()

	inicio := time.Now()
	informe, err := Sembrar(ctx, pool, Opciones{
		DSN:           dsn,
		Semilla:       *semilla,
		Usuarios:      *usuarios,
		Meses:         *meses,
		AsientosExtra: *extra,
		RutaTokens:    *rutaTokens,
	})
	if err != nil {
		return err
	}
	informe.Usuarios = *usuarios

	if *rutaTokens != "" {
		if err := escribirTokens(*rutaTokens, *semilla, *usuarios); err != nil {
			return err
		}
	}

	salida, err := json.MarshalIndent(struct {
		Informe
		DuracionMs int64  `json:"duracion_ms"`
		Semilla    uint64 `json:"semilla"`
	}{informe, time.Since(inicio).Milliseconds(), *semilla}, "", "  ")
	if err != nil {
		return fmt.Errorf("serializar el informe: %w", err)
	}
	fmt.Println(string(salida))
	return nil
}

// escribirTokens acuña un access por usuario. El access vive cinco minutos, así que se reacuñan
// antes de cada corrida de carga.
func escribirTokens(ruta string, semilla uint64, usuarios int) error {
	tokens, err := AcunarTokens(os.Getenv("JWT_SECRET"), os.Getenv("JWT_ISSUER"),
		os.Getenv("JWT_AUDIENCE"), semilla, usuarios)
	if err != nil {
		return err
	}
	crudo, err := json.Marshal(tokens)
	if err != nil {
		return fmt.Errorf("serializar tokens: %w", err)
	}
	if err := os.WriteFile(ruta, crudo, 0o600); err != nil {
		return fmt.Errorf("escribir tokens: %w", err)
	}
	return nil
}
