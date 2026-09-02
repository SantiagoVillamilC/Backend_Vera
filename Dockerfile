FROM golang:1.22-alpine AS build
WORKDIR /app
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o api ./cmd/api

FROM alpine:3.20
WORKDIR /app
COPY --from=build /app/api ./api
EXPOSE 8080
CMD ["./api"]
