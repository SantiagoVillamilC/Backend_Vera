# Clasificador TF-IDF: cuatro mejoras medidas y el cierre idempotente que el harness necesitó para no mentir en su journal

**Fecha:** 12 de octubre de 2026
**Tarjeta:** (tesis, inteligencia)
**Archivos que toca esta sesión:** `dataset-inteligencia/analisis/clasificador_tfidf.py` (entregable), `dataset-inteligencia/analisis/similitud.py` (vectorizador y Jaro-Winkler), `dataset-inteligencia/tests/test_analisis_clasificador_tfidf.py` (pruebas), `dataset-inteligencia/harness/src/chain/{gate,engine,close}.ts` y `dataset-inteligencia/harness/src/planner/plan.schema.ts` (motor del harness). Esta entrada se registra en la bitácora.

## 1. Síntoma

El ítem 5 de `PLAN-ANALISIS.md` dejó al TF-IDF local con 0,718 de exactitud sobre la prueba, 0,95 cuando el nombre revela la naturaleza, 0,735 en ficticias y 0,60 en reales. Faltaba subirlo antes de compararlo contra MiniLM y un LLM por API. Se decidió aplicar cuatro mejoras mediante el `opencode-harness` montado dentro del proyecto, con un solo modelo de punta a punta.

Al terminar, el código de las cuatro mejoras estaba completo y pasaba pruebas, pero el harness marcó el último paso (`S4`, léxico semilla) como `failed`. Quien leyera solo el journal creería que el léxico semilla no se hizo, cuando sí estaba en disco y pasaba pruebas.

## 2. Diagnóstico

**Las cuatro mejoras, cada una su propio paso del plan.**

- S1, MCC: el clasificador acepta un token `mcc:<codigo>` y lo usa como voto de categoría (`clasificador_tfidf.py:134`).
- S2, abstención por umbral: `predecir` devuelve `None` cuando el mejor coseno queda bajo `UMBRAL_COSENO`, y `medir` reporta `cobertura` y `abstenciones` (`:237`).
- S3, vectorizador ampliado: `vector_tfidf_ampliado` combina palabras (`w:`) con trigramas de carácter (`c:`) y aplica tf sublineal, reutilizando `analisis/similitud.py` sin romper `vector_tfidf`.
- S4, léxico semilla: `LEXICO_SEMILLA`, `cargar_lexico` y `_categoria_lexico` con Jaro-Winkler, aplicado solo cuando el coseno no es concluyente.

**El motivo del `failed` en S4 no fue el código.** El journal muestra tres intentos (`.harness/clasificador-mcc-abstencion-s2s4/journal.jsonl`):

- Intento 1 y 2: `StructuredOutputError` por un corte del proveedor (`AI_APICallError: [code=workspace_billing_scope_unavailable]`). El agente ya había dejado el léxico escrito, pero no alcanzó a emitir el STEP_RESULT.
- Intento 3, ya con el código completo en disco: el gate lo rechazó por `sin evidencia de cambio: verify vacuo o output preexistente sin modificar`. El verify pasaba, los outputs existían, pero no habían cambiado de sha porque el efecto ya estaba en disco. Editar el plan para declarar el paso idempotente no servía: `computePlanSha` hashea el plan parseado y el journal encadenó el sha original, así que cualquier cambio del plan rompe el replay.

## 3. Causa

El gate del harness exige, además del `expect_exit` de cada `verify`, **evidencia de cambio**: diff de `outputs` o un `verify` que falla contra el estado base. Esa regla es correcta para un paso que trabaja, pero no tiene salida para un paso cuyo efecto ya se materializó en un intento anterior que se cortó antes de emitir su STEP_RESULT. Reintentar solo repite el rechazo, porque no queda nada que modificar.

## 4. Decisión

**Medir el conjunto completo** y no solo las ficticias, para dejar la línea base y la mejorada comparables.

**Agregar al gate una vía estrecha y explícita para lo idempotente.** `runGate` gana `idempotent?: boolean`. Con `true`, un paso cuyos `outputs` existen y pasan todos los `verify` cierra igual, sin exigir diff. No exime nada más: un `verify` que falla, un output ausente o un `plan_sha256` distinto siguen cerrando en falso. El schema lo deja como campo `.optional()`, nunca con `default`, porque un default habría agregado una clave a cada paso parseado y cambiado el `plan_sha256` de lo ya almacenado.

**Cerrar la operación existente con un comando, no volviendo a llamar al modelo.** `harness close` relee la copia almacenada del plan, corre el gate con `idempotent` y, si pasa, escribe el `step_done` y finaliza la operación. El cierre no suma tokens (el gasto de los intentos previos ya está en el journal) y deja el motivo en el `reason` del evento. Exige `--accept-no-change` para que un cierre deliberado no se confunda con un paso que sí trabajó. Se descartó reintentar `resume`, que había fallado tres veces y cuesta tokens por un resultado no determinista.

## 5. Evidencia

Medición sobre `salida/2026-3000-A`, el 12 de octubre de 2026:

| Métrica | Línea base | Con las cuatro mejoras |
|---|---|---|
| Exactitud | 0,718 | 0,949 |
| Cobertura | no reportada | 1,0 |
| Abstenciones | no reportada | 0 |
| Exactitud en las que revelan la naturaleza (n=20) | 0,95 | 1,0 |
| Exactitud en ficticias (n=34) | 0,735 | 0,941 |
| Exactitud en reales (n=5) | 0,60 | 1,0 |

Pruebas: `python3 -m unittest` con 657 pruebas OK. Harness: `pnpm typecheck` limpio y 174 pruebas verdes, incluida la nueva suite de `close`. La operación `clasificador-mcc-abstencion-s2s4` quedó en `status: done`, `last_successful_step: S4`, con el `plan_sha256` intacto (`e2df3ba5...`) y los dos criterios de aceptación globales en verde. El journal conserva los tres `step_failed` y agrega un `step_done` con el motivo del cierre, así que la historia completa queda legible.

## 6. Qué queda abierto

- **MiniLM y el LLM por API no se midieron.** Era el ítem 5 original y sigue pendiente. El LLM puede reconocer marcas reales y `verdad/empresas.csv` tiene la columna `ficticia`, así que hay que medirlo aparte sobre esa columna, no heredar el tope de las ficticias.
- **El camino de texto con `variant: high` no se probó en un spike aislado.** El executor corrió en `high` por el camino de texto y S1 a S3 cerraron, lo que es evidencia indirecta, no un spike dedicado. `opendesign/deepseek-v4.1-flash` rechaza `format: json_schema` en las cuatro variantes (`Thinking mode does not support this tool_choice`), así que el harness usa el camino de texto.
- **El conteo de tokens del harness infla por cache.** `normalizeTokens` suma `cache_read` a `total`, y deepseek relee el contexto en cada turno. Se sorteó con topes altos (6 a 8 M por paso, 24 M totales), no arreglando el cálculo.
- **El gate no puede re-cerrar un paso ya hecho.** La vía nueva es `close`, que exige que el paso esté `failed`. Un paso idempotente cerrado por la vía normal (`idempotent: true` en el plan) sigue el camino del executor.
- **Nada está versionado.** `dataset-inteligencia/` está untracked y el harness quedó sin repositorio propio. Falta decidir qué se sube.
