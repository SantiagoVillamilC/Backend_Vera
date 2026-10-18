# Contrato del simulador del banco custodio

Servicio: `cmd/simulador-custodia`. Puerto por defecto: `8099`. Almacenamiento: SQLite
propio, sin acceso a la base ni al código del núcleo. El núcleo lo consume con
`internal/platform/custodiosim`.

## Notificaciones

`POST /notificaciones/depositos` y `POST /notificaciones/retiros`

```json
{
  "referencia": "dep-abc",
  "monto_centavos": "150000",
  "ocurrido_en": "2026-10-18T12:00:00Z"
}
```

- Los tres campos son obligatorios. Un monto que no es un entero positivo se rechaza.
- La `referencia` es única por tipo: un reenvío idéntico conserva un solo movimiento y
  devuelve la misma respuesta. La misma referencia con otro monto o por el endpoint contrario
  responde `409`.
- La respuesta incluye `id`, `referencia`, `tipo`, `monto_centavos`, `ocurrido_en`,
  `registrado_en` y `reintento`.

## Extracto

`GET /extracto?corte_en=<RFC3339>`

```json
{
  "corte_en": "2026-10-18T12:00:00Z",
  "saldo_centavos": "857000",
  "origen": "simulador-custodia"
}
```

- El corte lo pide quien llama. Un corte futuro se rechaza con `400`.
- El saldo es la suma de depósitos menos retiros con `ocurrido_en <= corte_en`.
- Nunca devuelve un saldo suelto: siempre el corte completo.
- El saldo sobrevive al reinicio porque vive en el SQLite del servicio.

## Perillas

`GET /control/perillas` y `PUT /control/perillas`.

- `p_rechazo`, `latencia_ms`, `latencia_var_ms`.
- `modo_caida`: conexión rechazada o silencio.
- `p_respuesta_perdida`: el servicio ejecuta y no responde.
- `p_notificacion_perdida`: el custodio nunca se entera de un depósito, lo que produce una
  diferencia real contra el ledger.

## Variables

| Variable | Uso |
|---|---|
| `SIMULADOR_CUSTODIA_ADDR` | Dirección de escucha, por defecto `127.0.0.1` |
| `SIMULADOR_CUSTODIA_PORT` | Puerto, por defecto `8099` |
| `SIMULADOR_CUSTODIA_DB` | Ruta del SQLite propio |
| `SIMULADOR_CUSTODIA_ORIGEN` | Origen que viaja en el extracto |
| `SIMULADOR_CUSTODIA_PERMITIR_EXPOSICION` | Obligatoria en Docker si escucha en `0.0.0.0` |
