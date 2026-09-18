FROM golang:1.27-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o simulador-riel ./cmd/simulador-riel

FROM alpine:3.20
WORKDIR /app
COPY --from=build /app/simulador-riel ./simulador-riel
EXPOSE 8098
CMD ["./simulador-riel"]
