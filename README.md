Repositorio: Backend (Go - monolito modular)

Estructura de carpetas

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
│   │   ├── domain.go                # Entidad ChatMessage, Intent (consulta vs. transacción)
│   │   ├── service.go               # Arma contexto, decide intención, orquesta la respuesta
│   │   ├── llm_client.go            # Cliente hacia Groq / OpenAI
│   │   └── handler.go               # POST /copilot
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
├── migrations/
│   ├── 0001_create_users.sql
│   ├── 0002_create_wallets.sql
│   └── 0003_create_transactions.sql
│
├── Dockerfile
├── .env.example
├── go.mod
└── go.sum




Variables de entorno esperadas (.env.example)
PORT=8080
DATABASE_URL=postgres://user:password@host:5432/dbname
JWT_SECRET=
SUPABASE_URL=
SUPABASE_SERVICE_KEY=
GROQ_API_KEY=
OPENAI_API_KEY=
OCR_WORKER_URL=http://localhost:8000


Ejemplo de referencia - cómo debe verse un handler.go
go
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

Este patrón — handler delgado, toda la lógica real en service.go — debe repetirse en cada módulo del proyecto.
