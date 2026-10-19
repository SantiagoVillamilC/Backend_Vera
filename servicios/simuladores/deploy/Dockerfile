FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG SERVICIO
RUN test -n "$SERVICIO" \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/servicio "./cmd/${SERVICIO}"

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /out/servicio /app/servicio
ENTRYPOINT ["/app/servicio"]
