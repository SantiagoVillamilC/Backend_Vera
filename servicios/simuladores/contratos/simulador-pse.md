# Contrato del simulador de PSE

Servicio: `cmd/simulador-pse`. Puerto interno: `8090`. Almacenamiento: SQLite propio.
Es la contraparte del depósito: sirve la página de autorización y dispara dos caminos
independientes, uno al núcleo y otro al simulador de custodia.

## Página

- `GET /pse?referencia=<ref>&monto=<centavos>` muestra la referencia y el monto.
- `POST /pse/autorizar` y `POST /pse/cancelar` reciben
  `application/x-www-form-urlencoded` con `referencia` y `monto`.
- `GET /healthz` responde `200 ok` para el chequeo del contenedor.

## Desenlaces

Al resolver el formulario, el servicio registra el evento en su SQLite y crea dos patas
independientes:

- `leg_nucleo` a `NUCLEO_CONFIRMACION_URL`:

```json
{
  "referencia": "dep-abc",
  "estado": "aprobado",
  "monto": "150000",
  "moneda": "COP",
  "ocurrido_en": "2026-10-18T12:00:00Z",
  "cus": "cus-uuid"
}
```

- `leg_custodio` a `SIMULADOR_CUSTODIA_URL`:

```json
{
  "referencia": "dep-abc",
  "monto_centavos": "150000",
  "ocurrido_en": "2026-10-18T12:00:00Z"
}
```

Cada pata tiene su propio reintento, hasta cinco intentos con espera exponencial, y ninguna
consulta el resultado de la otra. Un rechazo no crea la pata del custodio.

## Idempotencia

- La pareja `(referencia, monto)` es única.
- Un reenvío del mismo desenlace devuelve el evento original con el mismo `cus` y no crea
  patas nuevas.
- Un desenlace distinto sobre la misma pareja responde `409` y no cambia el evento.
- El `cus` tiene la forma `cus-<uuid>` y solo se genera al crear el evento.

## Seguridad

- `CUSTODIO_TOKEN` debe tener 32 bytes o más y es el mismo del núcleo.
- El servicio exige loopback salvo `SIMULADOR_PSE_PERMITIR_EXPOSICION=true`.
- El límite es de 30 peticiones por minuto por IP sobre las rutas `/pse`. `/healthz` queda
  fuera del limitador. Detrás de nginx solo acepta `X-Real-IP` cuando la conexión viene de una
  dirección local o privada.
- El formulario valida `Origin` contra el `Host` de la petición: nginx debe enviar
  `proxy_set_header Host $host`.

## Variables

| Variable | Uso |
|---|---|
| `SIMULADOR_PSE_ADDR` | Dirección de escucha, por defecto `127.0.0.1` |
| `SIMULADOR_PSE_PORT` | Puerto, por defecto `8090` |
| `SIMULADOR_PSE_DB` | Ruta del SQLite propio |
| `SIMULADOR_PSE_PERMITIR_EXPOSICION` | Obligatoria en Docker si escucha en `0.0.0.0` |
| `CUSTODIO_TOKEN` | Token estático compartido con el núcleo |
| `NUCLEO_CONFIRMACION_URL` | Endpoint completo de confirmación del núcleo |
| `SIMULADOR_CUSTODIA_URL` | Endpoint completo de notificaciones del custodio |
