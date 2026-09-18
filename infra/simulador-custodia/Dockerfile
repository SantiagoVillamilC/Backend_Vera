FROM golang:1.27-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o simulador-custodia ./cmd/simulador-custodia

FROM alpine:3.20
WORKDIR /app
COPY --from=build /app/simulador-custodia ./simulador-custodia
EXPOSE 8099
CMD ["./simulador-custodia"]
