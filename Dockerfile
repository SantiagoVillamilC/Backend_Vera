FROM golang:1.27-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# -o /app/bin/api, no -o api: el repo ya tiene una carpeta api/ (el contrato OpenAPI) en la
# raíz, y "go build -o api" con ese nombre existente como directorio deja el binario adentro
# (/app/api/api) en vez de como archivo. Primer deploy real, nunca se había corrido esta imagen.
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/bin/api ./cmd/api
# El mismo binario de migraciones que usa la CI, para que el despliegue aplique
# el esquema antes de arrancar la API sin depender de una máquina con Go.
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/bin/migrate ./cmd/migrate

FROM alpine:3.20
WORKDIR /app
# wget: lo único que necesita el healthcheck de compose.api.yaml (infra/servidor/pasos/60-api.sh)
# contra /healthz, sin arrastrar curl completo.
RUN apk add --no-cache wget
COPY --from=build /app/bin/api ./api
COPY --from=build /app/bin/migrate ./migrate
EXPOSE 8080
CMD ["./api"]
