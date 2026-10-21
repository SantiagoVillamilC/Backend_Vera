// Comando avisos corre el emisor de avisos una vez y reporta lo que tardó. Sirve para medir el job
// sin esperar las 06:00 ni depender de River.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "avisos:", err)
		os.Exit(1)
	}
}

func run() error {
	ahora := flag.String("ahora", "", "instante de evaluación en RFC3339, por defecto el reloj del sistema")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("uso: avisos -ahora=<RFC3339>")
	}
	dsn := os.Getenv("APP_DATABASE_URL")
	if dsn == "" {
		return errors.New("APP_DATABASE_URL es obligatoria")
	}
	instante := time.Now()
	if *ahora != "" {
		leido, err := time.Parse(time.RFC3339, *ahora)
		if err != nil {
			return fmt.Errorf("--ahora inválido: %w", err)
		}
		instante = leido
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("abrir pool: %w", err)
	}
	defer pool.Close()

	servicio := inteligencia.NuevoServicio(pool)
	inicio := time.Now()
	emitidos, err := servicio.EmitirAvisos(ctx, instante)
	duracion := time.Since(inicio)
	informe := struct {
		Emitidos   int    `json:"emitidos"`
		DuracionMs int64  `json:"duracion_ms"`
		Ahora      string `json:"ahora"`
		Error      string `json:"error,omitempty"`
	}{Emitidos: emitidos, DuracionMs: duracion.Milliseconds(), Ahora: instante.Format(time.RFC3339)}
	if err != nil {
		informe.Error = err.Error()
	}
	salida, errMarshal := json.MarshalIndent(informe, "", "  ")
	if errMarshal != nil {
		return errMarshal
	}
	fmt.Println(string(salida))
	return err
}
