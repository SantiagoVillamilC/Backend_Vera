# Recurrencia consultable (`GET /v1/inteligencia/series`)

La recurrencia existía solo de forma indirecta: la usaban los avisos `cobro_nuevo` y
`pago_no_alcanza`, y una persona no podía ver qué pagos el motor considera recurrentes, con qué
cadencia ni cuál es el próximo esperado. El endpoint nuevo publica esa lectura para el usuario
autenticado, sin reimplementar la regla: `SeriesDelUsuario` arma el mismo `avisos.Contexto` que usa
el emisor y devuelve las series que ya calcula `avisos.NuevoContexto` (`internal/inteligencia/avisos/recurrencia.go`).

## Regla

- Se agrupan los pagos por identidad: la llave del destino o, si no hay, el nombre normalizado.
- `NMin = 3` ocurrencias como mínimo para que una serie sea madura; las cadencias son semanal,
  quincenal_14, mensual, bimestral y semestral, con las tolerancias de `recurrencia_estados.json`.
- Una serie sin próximo pago (dormida o cancelada) se publica sin `proximo_fecha` ni
  `proximo_monto_centavos`.
- La categoría es la del pago más reciente de esa identidad; si ninguno trae categoría, el campo
  va nulo.
- Es de solo lectura. No expone identidad de terceros: la identidad publicada es la llave o el
  nombre normalizado del destino del propio usuario, y la consulta filtra por usuario en el `WHERE`.

## Campos

| Campo | Tipo | Significado |
| --- | --- | --- |
| `tipo_identidad` | string | `llave` o `nombre` |
| `identidad` | string | llave o nombre normalizado del destino |
| `estado` | string | `emergente`, `madura`, `dormida` o `cancelada` |
| `cadencia` | string | `semanal`, `quincenal_14`, `mensual`, `bimestral` o `semestral` |
| `cadencia_dias` | integer | cadencia en días que eligió la regla |
| `ultimo_pago` | date-time | fecha del último pago de la serie |
| `proximo_fecha` | date-time, nullable | centro de la banda del próximo pago esperado |
| `proximo_monto_centavos` | string, nullable | monto esperado en centavos, como cadena |
| `categoria` | string, nullable | categoría del pago más reciente |

Los montos viajan como cadena de centavos, igual que el resto del contrato (`monto_centavos`,
`referencia_centavos`), nunca en punto flotante.

## Ejemplo

```bash
token="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))[0]["token"])' out/local/tokens.json)"
curl -s -H "Authorization: Bearer $token" -H 'X-Real-IP: 10.20.0.1' \
  http://127.0.0.1:8080/v1/inteligencia/series
```

```json
{
  "success": true,
  "data": [
    {
      "tipo_identidad": "llave",
      "identidad": "L-ARRENDADOR",
      "estado": "madura",
      "cadencia": "mensual",
      "cadencia_dias": 30,
      "ultimo_pago": "2026-09-01T00:00:00Z",
      "proximo_fecha": "2026-10-01T00:00:00Z",
      "proximo_monto_centavos": "5000000",
      "categoria": "alimentos_mercado"
    }
  ],
  "error": null,
  "meta": null
}
```

## Pruebas

```bash
go test -count=1 -run 'TestSeriesConsultables|TestListarSeries' ./internal/inteligencia/
set -a && source cargas/inteligencia/entorno.sh && set +a
go test -tags integration -count=1 -v -run TestSeriesDelUsuarioSeparaDosUsuariosConLaMismaLlave ./internal/inteligencia/
go generate ./...   # el contrato regenerado deja el mismo sha256
```

La unitaria arma el contexto a mano y exige que la proyección conserve estado, cadencia, próximo
pago y la última categoría. La de integración siembra dos usuarios con la misma llave y montos
distintos: cada uno ve solo su serie y la de otro usuario no aparece. La tercera comprueba que
`openapi.gen.go` es idempotente.

La serie que dispara `cobro_nuevo` aparece aquí con la misma cadencia y el mismo monto esperado que
usa el aviso, porque ambos leen la misma regla.
