# Repositorio: Backend (Go — monolito modular)

## Estructura de carpetas

```text
backend/
├── cmd/
│   └── api/
│       └── main.go                  # Punto de entrada — arma el router y arranca el servidor
│
├── internal/
│   ├── auth/
│   │   ├── domain.go                # Entidades: User, Session, MFAConfig
│   │   ├── service.go               # Login, validar JWT, generar clave dinámica (TOTP)
│   │   ├── repository.go            # Acceso a Supabase Auth / tabla de usuarios
│   │   └── handler.go               # POST /login, POST /register, POST /mfa/verify
│   │
│   ├── wallets/
│   │   ├── domain.go                # Entidad Wallet
│   │   ├── service.go               # Reglas de bolsillos y presupuesto
│   │   ├── repository.go
│   │   └── handler.go               # GET /wallets, POST /wallets
│   │
│   ├── transactions/
│   │   ├── domain.go                # Entidad Transaction
│   │   ├── service.go               # Ejecutar transferencia, validar saldo real
│   │   ├── confirmation.go          # Exige confirmación (huella/PIN) antes de ejecutar
│   │   ├── repository.go
│   │   ├── realtime.go              # Publica el evento a Supabase Realtime al confirmar
│   │   └── handler.go               # POST /transactions
│   │
│   ├── copilot/
│   │   ├── transaccion.go           # Acceso con rol_asistente
│   │   └── internal/repositorio/    # Conversaciones, mensajes y acciones (sqlc)
│   │
│   ├── receipts/
│   │   ├── domain.go                # Entidad Receipt, ReceiptItem
│   │   ├── service.go               # Orquesta la llamada al ocr-worker y guarda el resultado
│   │   ├── ocr_client.go            # Cliente interno hacia el servicio Python (OCR_WORKER_URL)
│   │   └── handler.go               # POST /receipts
│   │
│   └── platform/
│       ├── config/
│       │   └── config.go            # Carga variables de entorno
│       ├── httpserver/
│       │   ├── router.go            # Registra las rutas de cada módulo
│       │   └── middleware.go        # Validación de JWT, logging, manejo de errores
│       ├── database/
│       │   └── postgres.go          # Conexión a Supabase/Postgres
│       └── errors/
│           └── errors.go            # Tipos de error comunes de la API
│
├── bd/
│   ├── migraciones/
│   │   ├── 0001_create_users.sql
│   │   ├── 0002_create_wallets.sql
│   │   ├── 0003_create_transactions.sql
│   │   └── 0004_create_nucleo_schema_and_role.sql
│   └── embed.go                     # Migraciones embebidas en cmd/migrate
│
├── Dockerfile
├── .env.example
├── go.mod
└── go.sum
```

## Servicios simulados

Los tres sistemas externos (riel Bre-B, banco custodio y pasarela PSE) no viven en este
repositorio. Están en el monorepo `Simuladores_Vera`, como procesos independientes con su
propio almacenamiento, y este backend los consume por HTTP con `RIEL_SIMULADOR_URL`,
`DIRECTORIO_SIMULADOR_URL`, `CUSTODIO_EXTRACTO_URL`, `PSE_URL` y `CUSTODIO_TOKEN`.

## Compilar y demostrar

Los binarios no se versionan. `make build` compila los siete comandos en `out/bin` con
`CGO_ENABLED=0`, igual que la imagen de Docker: `api`, `migrate`, `enriquecer`, `avisos`,
`semilla-inteligencia`, `evaluar-clasificador` y `rollback-m8`.

```bash
make build       # compila los siete comandos en out/bin
make verificar   # build, go vet y go test -race -count=1 ./...
make imagen      # construye la imagen vera-api:local con api y migrate
make demo        # levanta todo sobre una base desechable y lo comprueba
make limpiar     # borra out/bin y los contenedores de la demo
```

`make demo` ejecuta `infra/local/demostrar.sh`. Ese script levanta Postgres 16 y Redis 7 en
contenedores desechables, aplica las migraciones, siembra 1.200 usuarios con 10 meses de
historial, corre dos lotes de enriquecimiento con `LEXICO=v2` y `CLASIFICADOR=tfidf`, emite
avisos una vez, arranca la API con `METRICAS_ACTIVO=true` y `AVISOS_ACTIVO=true`, y comprueba
`/healthz`, `/metrics`, `/v1/inteligencia/movimientos`, `/v1/inteligencia/resumen` y
`/v1/inteligencia/avisos` con un access real. Al final mide el primer corte con
`evaluar-clasificador` y deja los informes en `out/demo/`.

El sembrador solo acepta loopback, el puerto 55437 y bases cuyo nombre empiece por
`vera_semilla_`, así que el script no puede escribir en una base real. Los avisos se encienden
solo en esta base desechable: en un despliegue real `AVISOS_ACTIVO` sigue apagado hasta que
haya un piloto con personas. Se puede ajustar el tamaño con `USUARIOS`, `MESES`, `EXTRA` y
`LOTES`, el puerto con `PUERTO_PG`, `PUERTO_REDIS` y `PUERTO_API`, y dejar el stack corriendo
con `DEJAR=si`.

## Variables de entorno esperadas (`.env.example`)

El asistente tiene su contrato bajo `/v1/vera` en `api/openapi.yaml`; sus handlers
siguen pendientes de implementación. La ruta experimental `POST /copilot` fue
retirada y devuelve `404 NotFound`. El adaptador de IA se implementará en SCRUM-119.

```env
PORT=8080
DATABASE_URL=postgres://user:password@host:5432/dbname
APP_DATABASE_URL=postgres://rol_api:password@host:5432/dbname
JWT_SECRET=
OCR_WORKER_URL=http://localhost:8000
```

`DATABASE_URL` es el DSN del rol dueño del esquema y lo usa `cmd/migrate` para correr goose;
`APP_DATABASE_URL` es el del pool de `cmd/api`, con el rol `rol_api` (no dueño), que baja a
`rol_nucleo` con `SET LOCAL ROLE` en cada transacción del núcleo.

## Ejemplo de referencia — cómo debe verse un `handler.go`

```go
// internal/transactions/handler.go
package transactions

import (
	"encoding/json"
	"net/http"
)

type Handler struct {
	service *Service
}

func NewHandler(s *Service) *Handler {
	return &Handler{service: s}
}

// POST /transactions
// El middleware de auth ya validó el JWT antes de llegar acá.
func (h *Handler) CreateTransaction(w http.ResponseWriter, r *http.Request) {
	var req CreateTransactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "cuerpo inválido", http.StatusBadRequest)
		return
	}

	// El service vuelve a validar todo (monto, saldo, confirmación) —
	// nunca confía en lo que ya validó la app.
	result, err := h.service.Execute(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	json.NewEncoder(w).Encode(result)
}
```

Este patrón — handler delgado, toda la lógica real en `service.go` — debe repetirse en cada módulo del proyecto.
