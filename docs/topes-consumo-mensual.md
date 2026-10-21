# Topes de consumo mensual expuestos a la app · requerimiento de contrato

Sin tarjeta SCRUM todavía: este documento fija lo que necesita el frontend para la
pantalla de topes y lo que el backend debe decidir antes de implementarlo. Describe
el estado de `dev` en el commit c1cca25 y no propone código.

## Qué pide el frontend

Una vista que muestre, para el usuario autenticado y el mes calendario en curso:

1. cuánto ha movido en el mes,
2. cuánto le falta para agotar el cupo de 210,50 UVT,
3. el tope por operación y el tope de saldo, cuando apliquen.

## Qué hay hoy

| Pieza | Estado | Dónde |
| --- | --- | --- |
| Reglas sembradas: 21050 centésimas de UVT para `operacion` y `mensual` | listo | `bd/migraciones/0014_create_regla_limite_y_valor_uvt.sql:25-31` |
| Regla que grava: 6500 centésimas y 4000 ppm | listo | `bd/migraciones/0016_create_gmf_rule.sql:12-16` |
| Índice del débito del mes | listo | `bd/migraciones/0016_create_gmf_rule.sql:18-19` |
| Acumulado del mes, leído de `nucleo.transferencia` | calculado en memoria, no expuesto | `bd/consultas-nucleo/limites.sql:12-18` |
| Estados que suman al acumulado | listo | `internal/nucleo/limites.go:37-43` |
| Corte del mes en Colombia UTC-5 | listo | `internal/nucleo/limites.go:446-450` |
| Desglose del máximo enviable | existe, solo lo consume el asistente | `internal/nucleo/limites.go:129-215` |
| `GET /v1/transferencias/maximo` | devuelve un solo `monto` | `internal/nucleo/handler.go:39-44` y `:122-148` |
| Tope de saldo de 210,50 UVT | no modelado | ver `hu-transferencias.md:196` en el repositorio de documentos |

`DesgloseLimites` ya calcula `MaximoEnviable`, `SaldoDespuesGMF`, `TopeOperacion`,
`CupoMensualRestante`, `GMF` y `AplicaGMF`, y `docs/asistente-saldo-limites.md`
documenta ese uso. El problema no es el cálculo: es que la API HTTP no lo publica y
que faltan dos números que el motor usa pero descarta: el acumulado y el tope mensual
en centavos.

`GET /v1/transferencias/maximo` no sirve para la vista porque `monto` es el mínimo
entre saldo con GMF, cupo restante y tope por operación, recortado por el saldo real
del usuario. Un usuario con 10.000 pesos en la cuenta ve 10.000 y no el estado del cupo.

## Lo que falta, por dato

| Dato de la vista | ¿Se puede hoy? | Origen |
| --- | --- | --- |
| Gastado del mes | No | `contexto.acumulado` en `internal/nucleo/limites.go:104-114` |
| Tope mensual en centavos | No | `topeDeRegla(..., VentanaLimiteMensual, ...)` en `internal/nucleo/limites.go:176` |
| Restante del cupo | Solo interno | `DesgloseLimites.CupoMensualRestante` |
| Tope por operación | Solo interno | `DesgloseLimites.TopeOperacion` |
| Tope de saldo | No existe la regla | requiere migración y chequeo en la confirmación del depósito |

## Contrato propuesto

Ruta nueva, autenticada, de solo lectura, sin parámetros de usuario (la identidad sale
del token, igual que en `/v1/saldo` y `/v1/transferencias/maximo`):

```
GET /v1/limites/topes
```

```json
{
  "success": true,
  "data": {
    "nivel_kyc": "deposito_bajo_monto",
    "moneda": "COP",
    "uvt_centavos": "5237400",
    "ventana_desde": "2026-10-01T00:00:00-05:00",
    "ventana_hasta": "2026-11-01T00:00:00-05:00",
    "tope_mensual": "1102472700",
    "consumido_mes": "250000000",
    "restante_mes": "852472700",
    "tope_operacion": "1102472700",
    "tope_saldo": null,
    "saldo_disponible": "12000000",
    "calculado_en": "2026-10-21T15:04:05Z"
  },
  "error": null,
  "meta": null
}
```

Reglas del contrato:

- Todos los montos viajan como cadena de dígitos, no como número. Es la convención de
  `MontoCalculado` en `api/openapi.yaml:2981-2997` y la razón por la que `Centavos`
  serializa a texto.
- `consumido_mes` es la misma suma que decide el rechazo del envío: `SUM(monto)` de
  `nucleo.transferencia` por `usuario_id`, con los estados de `limites.go:37-43` y la
  ventana en UTC-5. No es el historial ni las líneas del diario.
- `restante_mes` es `max(tope_mensual - consumido_mes, 0)`. No se recorta por el saldo
  ni por el tope por operación: es estado del cupo, no un máximo enviable. El máximo
  enviable ya vive en `/v1/transferencias/maximo`.
- `tope_mensual` y `tope_operacion` se convierten desde centésimas de UVT con la UVT del
  año de la fecha del servidor y redondeo a la mitad hacia arriba
  (`internal/nucleo/limites.go:430`), no se hardcodean.
- `tope_saldo` viaja como `null` mientras la regla no exista. El frontend esconde esa
  barra en vez de mostrar un número inventado.
- Los tres errores de configuración (`ErrValorUVTNoVigente`, `ErrReglaLimiteNoVigente`,
  `ErrReglaGMFNoVigente`) y `ErrKYCNoDisponible` responden `503 LimitsUnavailable`, como
  ya hace `internal/nucleo/handler.go:138-143`. Nunca se responde con ceros.
- Lectura en una sola transacción, sin escrituras.

Alternativas y por qué no:

| Opción | Problema |
| --- | --- |
| Agregar campos a `/v1/transferencias/maximo` | su `data` usa `MontoCalculado`, compartido con `/v1/saldo`; agregar campos acopla las dos respuestas |
| Usar `KycEstado.limites` (`api/openapi.yaml:601-604`, `KycEstado` lo declara como objeto libre) | la ruta hoy responde 501 en `internal/platform/httpserver/servidor_v1.go:278-284` y mezcla estado de identidad con consumo mensual mutable |
| Sumar `GET /v1/movimientos` en el cliente | la página trae 50 y admite 100 (`internal/nucleo/handler.go:25-26`), así que el total no es confiable y los estados no coinciden con los del motor |

## Decisiones que el backend debe cerrar antes de implementar

1. **GMF dentro del acumulado.** Hoy `SUM(monto)` no incluye el gravamen. La norma habla
   del "monto acumulado de las operaciones débito"; si el 4x1000 cuenta, cambia el número
   que ve el usuario en el borde del tope y también el que rechaza el envío.
2. **Envíos compensados.** `compensada` no suma. La historia lo deja como pregunta abierta
   (`hu-transferencias.md`), y la pantalla haría visible la inconsistencia si el criterio
   cambia.
3. **Tope de saldo.** Modelarlo exige una fila nueva en `regla_limite`, un chequeo en la
   confirmación del depósito (`internal/nucleo/deposito.go`) y decidir si el crédito se
   rechaza o queda en revisión. CR-027 lo declara no modelado; si sigue fuera de alcance,
   `tope_saldo: null` es la respuesta honesta.
4. **Precisión de la ventana.** Confirmar que el corte es medianoche de Colombia (UTC-5)
   y que `ventana_desde`/`ventana_hasta` se publican en esa zona, para que el frontend no
   calcule "mes" con la zona del teléfono.
5. **Nombre y forma de la ruta.** `GET /v1/limites/topes` es una propuesta; lo que no
   debería pasar es que la vista dependa de restar dos campos que la API no expone.

## Criterios de aceptación

- `api/openapi.yaml` con la ruta nueva y el esquema de respuesta, y
  `go generate ./internal/platform/api` regenerando `openapi.gen.go` sin cambios a mano.
- Prueba unitaria del desglose con tres bordes: acumulado cero, acumulado a un centavo
  del tope y acumulado por encima del tope (`restante_mes` en cero, nunca negativo).
- Prueba de integración por el router real: 401 sin token, rechazo de `usuario_id` en la
  consulta, ventana del mes en UTC-5 y exclusión de los estados que no son débito.
- Prueba de que la consulta no escribe: huella de cuentas, asientos y postings antes y
  después, como hace `docs/asistente-saldo-limites.md`.
- Sin migraciones nuevas, salvo que se cierre la decisión 3.

## Hallazgos colaterales, fuera de este requerimiento

- La columna `fuente` de las dos reglas que rechazan cita el Decreto 2555 de 2010 y no el
  Decreto 2642 de 2022 que fija el 210,50 UVT, y la del GMF incluye el numeral 28 del
  artículo 879. CR-027 ya pide corregirlas con una migración aparte.
- `KycEstado` y `KycVerificacion` siguen respondiendo 501 cuando `authDeps.KYC` es nulo,
  así que la vista no debe depender de esas rutas para mostrar topes.
