#!/usr/bin/env bash
# Entorno del banco de medición de M8. Todo apunta al Postgres desechable del puerto 55437 y al Redis
# del 6380. Ninguna credencial es de producción: el archivo se carga con `source`, no lo ejecuta el API.
#
# Uso: source cargas/inteligencia/entorno.sh
set -a
export PORT=8080
export DATABASE_URL='postgres://dueno:clave@127.0.0.1:55437/vera_semilla_m8?sslmode=disable'
export APP_DATABASE_URL='postgres://rol_api:clave_api@127.0.0.1:55437/vera_semilla_m8?sslmode=disable'
export REDIS_URL='redis://127.0.0.1:6380/0'
export JWT_SECRET='secreto-de-medicion-de-al-menos-treinta-y-dos-bytes'
export JWT_ISSUER='centinela-auth'
export JWT_AUDIENCE='centinela-api'
export CUSTODIO_TOKEN='token-de-custodio-de-medicion-de-32-bytes'
export CONTADORES_PEPPER='1111111111111111111111111111111111111111111111111111111111111111'
export OTP_PEPPER='2222222222222222222222222222222222222222222222222222222222222222'
export OTP_CANAL='simulador'
export OTP_SIMULADOR_CODIGO_FIJO='123456'
export AUTH_ENFRIAMIENTO_CAMBIO_NUMERO='24h'
export AUTH_CONTACTO_SOPORTE='soporte@sandbox.centinela.test'
export AUTH_RESET_TOKEN_TTL='10m'
export AUTH_VENTANA_PENDIENTE='24h'
export AUTH_LIMPIEZA_INTERVAL='15m'
export AUTH_OUTBOX_INTERVAL='10s'
export AUTH_OUTBOX_BACKOFF_BASE='5s'
export AUTH_OUTBOX_BACKOFF_FACTOR='2'
export AUTH_OUTBOX_BACKOFF_MAX='5m'
export AUTH_ARGON2_CONCURRENCIA='4'
export KYC_PROVEEDOR='simulador'
export KYC_SIMULADOR_RESULTADO='verificado'
export PSE_URL='https://pse.sandbox.centinela.test'
export ENRIQUECIMIENTO_INTERVAL='24h'
export METRICAS_ACTIVO='true'
export METRICAS_TOKEN='token-de-metricas-de-medicion-de-32-bytes'
export CLASIFICADOR='tfidf'
export AVISOS_ACTIVO='false'
set +a
