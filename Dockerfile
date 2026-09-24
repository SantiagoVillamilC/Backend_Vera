FROM golang:1.27-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o api ./cmd/api

FROM alpine:3.20
WORKDIR /app
# wget: lo único que necesita el healthcheck de compose.api.yaml (infra/servidor/pasos/60-api.sh)
# contra /healthz, sin arrastrar curl completo.
RUN apk add --no-cache wget
COPY --from=build /app/api ./api
EXPOSE 8080
CMD ["./api"]
