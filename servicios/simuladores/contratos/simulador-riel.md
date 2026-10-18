# Contrato del simulador del riel Bre-B

Servicio: `cmd/simulador-riel`. Puerto por defecto: `8098`. Almacenamiento: SQLite propio.
El núcleo lo consume con `internal/platform/rielsim` y `internal/platform/directoriosim`.

## Órdenes

`POST /transferencias`

```json
{
  "transferencia_id": "uuid",
  "idempotency_key": "texto",
  "llave": "3001234567",
  "monto_centavos": "150000",
  "moneda": "COP"
}
```

- `monto_centavos` viaja como cadena JSON, nunca como número.
- La clave de idempotencia del simulador es `transferencia_id`: un reenvío con el mismo
  identificador y el mismo cuerpo responde lo mismo y no duplica la operación. Un reenvío con
  otro cuerpo responde `409`.
- Desenlaces: `confirmada`, `rechazada`. El único motivo de rechazo es
  `fondos_insuficientes`.
- La respuesta incluye `transferencia_id`, `resultado`, `motivo`, `ocurrido_en` y `reintento`.

`GET /transferencias/{id}` devuelve el mismo cuerpo para cerrar una `indeterminada`.

## Extracto

`GET /extracto?desde=<RFC3339>&hasta=<RFC3339>`

```json
{
  "corte": "2026-10-18T12:00:00Z",
  "operaciones": [
    {
      "transferencia_id": "uuid",
      "estado": "confirmada",
      "monto_centavos": "150000",
      "ocurrido_en": "2026-10-18T11:59:00Z"
    }
  ]
}
```

- Incluye las órdenes rechazadas: sin ellas no se detecta la diferencia de estado.
- El emparejamiento es por `transferencia_id`, nunca por la clave de idempotencia del usuario.

## Directorio de llaves

`GET /resolver?tipo=<tipo>&valor=<valor>` responde `{"entidad": "...", "producto": "..."}` o
`404` si la llave no está registrada. Los cinco tipos válidos son los de Bre-B: `documento`,
`celular`, `correo`, `alfanumerico` y `comercio`.

## Perillas

`GET /control` y `PUT /control`.

- `p_rechazo`: probabilidad de responder `rechazada`.
- `latencia` y `latencia_variacion`.
- `modo_caida`: `ninguna`, `conexion_rechazada` o `silencio`.
- `p_respuesta_perdida`: ejecuta la orden y no responde, para producir una `indeterminada`
  real.

`p_rechazo` aquí registra una operación `rechazada` y responde `200`. En el simulador de
custodia la misma perilla responde `503` sin registrar nada. No son comparables entre
servicios y así debe consignarse en el registro del experimento.

## Variables

| Variable | Uso |
|---|---|
| `SIMULADOR_RIEL_ADDR` | Dirección de escucha, por defecto `127.0.0.1` |
| `SIMULADOR_RIEL_PORT` | Puerto, por defecto `8098` |
| `SIMULADOR_RIEL_DB` | Ruta del SQLite propio |
| `SIMULADOR_RIEL_ORIGEN` | Nombre que viaja en el extracto |
| `SIMULADOR_RIEL_PERMITIR_EXPOSICION` | Obligatoria en Docker si escucha en `0.0.0.0` |
