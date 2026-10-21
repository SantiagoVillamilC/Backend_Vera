// Comando login-demo crea las filas de auth de un usuario ya sembrado para que la app pueda
// entrar con celular y contraseña en una base desechable. No crea cuentas del ledger ni datos:
// eso lo hace la semilla. Es una herramienta de demostración, no un camino de producción.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	puertoDesechable = "55437"
	prefijoDeBase    = "vera_semilla_"
	largoMinimo      = 8
	largoMaximo      = 64
)

type solicitud struct {
	Usuario    uuid.UUID
	Celular    string
	Contrasena string
	Nombre     string
	Correo     string
}

type informe struct {
	Usuario    uuid.UUID `json:"usuario"`
	Celular    string    `json:"celular"`
	Nombre     string    `json:"nombre"`
	Correo     string    `json:"correo"`
	Credencial uuid.UUID `json:"credencial_id"`
	Aviso      string    `json:"aviso"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "login-demo:", err)
		os.Exit(1)
	}
}

func run() error {
	usuario := flag.String("usuario", "", "UUID del usuario sembrado, que será el parte_id de auth")
	celular := flag.String("celular", "", "celular de la demo, 10 dígitos o +57 y 10 dígitos")
	contrasena := flag.String("contrasena", "", "contraseña de la demo, de 8 a 64 caracteres")
	nombre := flag.String("nombre", "", "nombre visible del usuario de la demo")
	correo := flag.String("correo", "", "correo de la credencial, vacío para uno derivado del UUID")
	permitirRemoto := flag.Bool("permitir-remoto", false, "acepta un host que no sea loopback, solo con una base desechable")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("uso: login-demo -usuario <uuid> -celular <celular> -contrasena <contraseña> -nombre <nombre>")
	}

	dsn := os.Getenv("DATABASE_URL")
	if err := database.DSNLocalDeMedicion(dsn, puertoDesechable, prefijoDeBase, *permitirRemoto); err != nil {
		return err
	}
	pedido, err := validar(*usuario, *celular, *contrasena, *nombre, *correo)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("abrir pool: %w", err)
	}
	defer pool.Close()

	credencial, err := sembrar(ctx, pool, pedido)
	if err != nil {
		return err
	}
	salida := informe{
		Usuario: pedido.Usuario, Celular: pedido.Celular, Nombre: pedido.Nombre,
		Correo: pedido.Correo, Credencial: credencial,
		Aviso: "cuenta de demostración: no usar fuera de la base desechable",
	}
	crudo, err := json.MarshalIndent(salida, "", "  ")
	if err != nil {
		return fmt.Errorf("serializar el informe: %w", err)
	}
	fmt.Println(string(crudo))
	return nil
}

// validar normaliza y comprueba los datos antes de tocar la base.
func validar(usuario, celular, contrasena, nombre, correo string) (solicitud, error) {
	id, err := uuid.Parse(strings.TrimSpace(usuario))
	if err != nil {
		return solicitud{}, fmt.Errorf("el usuario no es un UUID válido: %w", err)
	}
	cel := normalizarCelular(celular)
	if cel == "" {
		return solicitud{}, errors.New("el celular debe ser +57 seguido de 10 dígitos o 10 dígitos")
	}
	largo := len([]rune(contrasena))
	if largo < largoMinimo || largo > largoMaximo {
		return solicitud{}, fmt.Errorf("la contraseña debe tener entre %d y %d caracteres", largoMinimo, largoMaximo)
	}
	nom := strings.TrimSpace(nombre)
	if nom == "" {
		return solicitud{}, errors.New("el nombre no puede estar vacío")
	}
	mail := strings.TrimSpace(correo)
	if mail == "" {
		mail = "demo-" + strings.ReplaceAll(id.String(), "-", "")[:8] + "@centinela.test"
	}
	return solicitud{Usuario: id, Celular: cel, Contrasena: contrasena, Nombre: nom, Correo: mail}, nil
}

// normalizarCelular deja el celular en E.164 colombiano o vacío si no es válido.
func normalizarCelular(valor string) string {
	limpio := strings.ReplaceAll(strings.TrimSpace(valor), " ", "")
	limpio = strings.TrimPrefix(limpio, "+57")
	if len(limpio) != 10 {
		return ""
	}
	for _, r := range limpio {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return "+57" + limpio
}

// sembrar crea o actualiza las tres filas de auth del usuario. Es idempotente por parte_id.
func sembrar(ctx context.Context, pool *pgxpool.Pool, pedido solicitud) (uuid.UUID, error) {
	derivador, err := auth.NuevoDerivadorArgon2(auth.ParametrosArgon2Produccion())
	if err != nil {
		return uuid.Nil, fmt.Errorf("crear el derivador: %w", err)
	}
	phc, err := derivador.Derivar(ctx, pedido.Contrasena)
	if err != nil {
		return uuid.Nil, fmt.Errorf("derivar la contraseña: %w", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("abrir transacción: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE rol_auth"); err != nil {
		return uuid.Nil, fmt.Errorf("bajar a rol_auth: %w", err)
	}

	parte := pgtype.UUID{Bytes: pedido.Usuario, Valid: true}
	documento := strings.ReplaceAll(pedido.Usuario.String(), "-", "")[:10]
	if _, err := tx.Exec(ctx,
		"INSERT INTO auth.partes (id, tipo) VALUES ($1, 'persona') ON CONFLICT (id) DO NOTHING",
		parte); err != nil {
		return uuid.Nil, fmt.Errorf("crear la parte: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO auth.personas (parte_id, tipo_documento, documento, nombre)
		 VALUES ($1, 'CC', $2, $3)
		 ON CONFLICT (parte_id) DO UPDATE SET nombre = EXCLUDED.nombre`,
		parte, documento, pedido.Nombre); err != nil {
		return uuid.Nil, fmt.Errorf("crear la persona: %w", err)
	}
	var credencial pgtype.UUID
	if err := tx.QueryRow(ctx,
		`INSERT INTO auth.credenciales
		   (id, parte_id, correo, celular, celular_verificado_en, valor_derivado, estado)
		 VALUES ($1, $2, $3, $4, now(), $5, 'activo')
		 ON CONFLICT (parte_id) DO UPDATE SET
		   correo = EXCLUDED.correo,
		   celular = EXCLUDED.celular,
		   celular_verificado_en = now(),
		   valor_derivado = EXCLUDED.valor_derivado,
		   estado = 'activo'
		 RETURNING id`,
		pgtype.UUID{Bytes: uuid.New(), Valid: true}, parte, pedido.Correo, pedido.Celular, phc).
		Scan(&credencial); err != nil {
		return uuid.Nil, fmt.Errorf("crear la credencial: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("confirmar: %w", err)
	}
	return credencial.Bytes, nil
}
