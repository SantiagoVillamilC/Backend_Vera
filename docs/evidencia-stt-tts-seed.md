# Evidencia de la entrega STT/TTS y siembra de la demo

Registro de las pruebas con que se cerraron las tres tareas de la rama `feat/stt-tts-seed`
(documento de traspaso: `docs/entrega-stt-tts-seed.md`). Todo lo de abajo se ejecutó el
**2026-11-09** sobre el código de los commits indicados. Las cifras son las obtenidas en esas
corridas; lo que no se midió se dice expresamente en cada sección y en «Limitaciones».

| Tarea | Commit | Estado |
|---|---|---|
| CF-A3-03 · Kokoro en el chat | `08af2b1` | Cerrada |
| CF-A3-02 · Whisper, primera parte (servicio y cliente) | `b796972` | Parcial |
| CF-A2-03 · Siembra de la demo | `2eb8145` | Parcial |

## Entorno de las pruebas

- Docker en Windows 11. Go 1.27.0 (`golang:1.27.0`), `golangci-lint` v2.13.1, PostgreSQL 17.
- Kokoro: imagen `vera-laboratorio-voz:kokoro-0.6.1` (de `infra/voz-local/`).
- Whisper: imagen construida en la prueba a partir de `infra/stt-local/` con el código del commit;
  modelo `small` ya descargado en un volumen y el servicio arrancado con `--network none` y
  `HF_HUB_OFFLINE=1`, es decir, **sin ninguna llamada externa**.
- Siembra: PostgreSQL 17 nuevo, vacío, migrado con `cmd/migrate up` (versión 7), escuchando en
  `127.0.0.1:55437`, base `vera_semilla_verif`.

## 1. Kokoro en el chat (CF-A3-03)

### Qué se entregó
`POST /v1/vera/sintetizar` con `{"texto": "..."}` y `X-Vera-Token` → `audio/wav` con voz `ef_dora`.
Valida token, consentimiento del usuario y cuota de voz; texto de 1 a 8192 bytes; caché acotada
por texto (`vozlocal.NuevaCache`); si el sintetizador no responde, devuelve **200 sin audio** con
`{"audio_disponible": false, "voz_local": true}` y la cabecera `X-Vera-Audio: no-disponible`.
Se activa en `cmd/api/main.go` solo si existe `VERA_VOZ_TOKEN` (URL opcional en `VERA_VOZ_URL`,
por defecto `http://127.0.0.1:8766`).

### Pruebas automáticas
- `go vet` sin hallazgos; `go test -race` en verde en `internal/platform/httpserver`,
  `internal/copilot/vozlocal` y `cmd/api`.
- Caché: límites, caducidad, cancelación, repetición concurrente (una sola síntesis) y que no se
  conservan los fallos.
- Router: éxito, degradación con el sintetizador caído, consentimiento y cuota.
- `golangci-lint`: sin hallazgos en los archivos de esta tarea.

### Prueba con el modelo real
Se levantó el contenedor de Kokoro y se llamó dos veces, a través del adaptador y la caché del
backend, con el mismo texto
(«Hola, soy Vera. Tu pago de luz vence el día veinte.», voz `ef_dora`):

| Llamada | Resultado | Bytes | Cabecera | Tiempo |
|---|---|---|---|---|
| 1 | sin error | 154.124 | `RIFF` … `WAVE` | 1,78 s |
| 2 (mismo texto) | sin error | 154.124 | `RIFF` … `WAVE` | 56,7 µs (caché) |

### Criterios de aceptación del documento
| Criterio | Resultado |
|---|---|
| Un mensaje reproduce audio con voz `ef_dora` | Cumple: WAV válido generado por el modelo real. |
| Repetir el mismo texto no vuelve a sintetizar | Cumple: la segunda llamada salió de la caché (56,7 µs). |
| Con el servicio caído la respuesta sigue siendo 200 sin audio | Cumple, cubierto por `TestSintesisRouter` (sintetizador simulado que falla). |

## 2. Whisper, primera parte (CF-A3-02)

### Qué se entregó
Servicio `infra/stt-local/servidor.py` (`GET /healthz`, `POST /transcribir`, loopback, `X-Vera-Token`,
audio en memoria, texto vacío si no hay voz) y cliente Go `internal/copilot/sttlocal` con plazo,
destino restringido a `127.0.0.1`, límite de 8 MB y errores públicos del paquete `copilot`.
Configuración del modelo: `small`, `int8` en CPU, `language=es`, `beam_size=1`, filtro de VAD.
Se fijó `av==16.1.0` porque PyAV 19 falla al decodificar con faster-whisper 1.2.1.

### Pruebas automáticas
- Cliente Go: `go vet` y `go test -race` en verde.
- Servicio Python: `python -m unittest test_servidor` → 4 pruebas, OK.
- Durante la revisión se halló y corrigió un defecto **de la prueba** `TestClienteLimitesYPlazo`
  (se colgaba hasta el tope de 10 min porque el servidor simulado esperaba una cancelación que Go
  no detecta si el manejador no lee el cuerpo). Ahora el manejador se libera con un canal y la
  prueba dura 0,01 s. El cliente no tenía el defecto.

### Prueba con el modelo real (sin red)
Se generó un WAV con Kokoro y un clip de 1 s de silencio, y se transcribieron con el cliente Go
contra el servicio real:

| Clip | Transcripción devuelta | Tiempo |
|---|---|---|
| Voz sintética: «En los últimos tres meses, ¿a cuánto asciende lo pagado en el internet hogar?» | «en los últimos tres meses a cuanto haciéndelo pagado en el Internet hogar.» | 971 ms |
| Silencio | «» (vacía), sin error | 14 ms |

Una petición sin token responde **401**.

### Criterios de aceptación del documento
| Criterio | Resultado |
|---|---|
| Un WAV devuelve la transcripción esperada, sin llamadas externas | Cumple con una diferencia de una palabra («asciende lo» → «haciéndelo»), coherente con la línea base. Servicio con `--network none`. |
| Un clip en silencio devuelve texto vacío sin abrir conversación | Cumple: texto vacío, el cliente no crea conversaciones. |

La línea base del repositorio de modelos (WER 0,0808 sobre voz sintética, 24 clips) se cita del
documento de traspaso; **no se volvió a medir** aquí.

## 3. Siembra de la demo (CF-A2-03)

### Qué se entregó
`cmd/semilla-escenario` (con `ProcesarEscenario` en `internal/inteligencia/escenario.go`), el guion
`infra/local/sembrar-escenario-demo.sh`, `docs/escenario-demo.md`, `bd/migraciones/0034_create_demo_escenario.sql`
y el estado de cada corrida en `DEMO-ESCENARIO-STATE.json` (ignorado por git). Acepta
`DEMO_USUARIO`, `DEMO_FECHA`, `AVISOS_CUPO` y `REINICIAR=si`. Reutiliza el enriquecimiento, el
léxico v2 y los detectores de producción; no inserta avisos prefabricados y no mueve el cursor
global.

Ajuste respecto al documento: el detector de cobro nuevo exige **dos** cargos en 35 días, por lo
que STREAMING se siembra el 28 del mes anterior y el 12 del actual. Y la caída de ingreso es del
31 % porque la regla usa desigualdad estricta (exactamente 30 % no dispara). El cupo de la demo
se fija en 5 por entorno, pero no es lo que hace aparecer `presupuesto_anticipado`: los cuatro
hechos se reparten en dos períodos de dos (octubre y noviembre), así que también entrarían con el
cupo de producto 3. El 5 deja holgura y evita depender del recorte por prioridad.

### Comando de la prueba
```bash
export DATABASE_URL='postgres://<usuario>:<clave>@127.0.0.1:55437/vera_semilla_verif?sslmode=disable'
export DEMO_USUARIO='11111111-1111-4111-8111-111111111111'
export DEMO_FECHA='2026-11-19'
export AVISOS_CUPO='5'
bash infra/local/sembrar-escenario-demo.sh      # corrida 1
bash infra/local/sembrar-escenario-demo.sh      # corrida 2
REINICIAR=si bash infra/local/sembrar-escenario-demo.sh
```

### Evidencia (corrida 1; la corrida 2 devolvió un JSON idéntico byte a byte)
Avisos por familia (`inteligencia.aviso`):

| tipo | n |
|---|---|
| `caida_ingreso` | 1 |
| `cobro_nuevo` | 1 |
| `pago_no_alcanza` | 1 |
| `presupuesto_anticipado` | 1 |

Categorías (`inteligencia.movimiento_enriquecido`):

| categoria | n |
|---|---|
| `sin_categoria` | 13 |
| `comisiones_costos_financieros` | 8 |
| `alimentos_mercado` | 6 |
| `transporte` | 6 |
| `restaurantes_domicilios` | 6 |
| `salud` | 6 |
| `entretenimiento` | 6 |
| `compras` | 1 |

Orígenes: `lexico` 39 y `sin_categoria` 13. Series maduras recurrentes: **7**. Asientos
sembrados: **52**.

Las 13 filas `sin_categoria` incluyen el comercio desconocido (`ZXQ987654`, la abstención pedida) y
movimientos que el léxico actual no clasifica (p. ej. pagos de ENEL, INTERNET HOGAR y
STREAMING); los 8 ingresos los clasifica el léxico existente como comisiones, y la siembra no
cambia esa regla.

### Criterios de aceptación del documento
| Criterio | Resultado |
|---|---|
| Desde base vacía: ≥ 3 avisos vigentes de familias distintas, dos series, cinco categorías y una abstención | Cumple: 4 avisos de 4 familias, 7 series, 5 categorías de gasto y `sin_categoria` ≥ 1. |
| Correrlo dos veces no duplica movimientos ni avisos; el estado queda escrito | Cumple: JSON idéntico; la base conserva 52 asientos y 4 avisos tras varias corridas. |
| Se niega a correr contra una base que no sea de demo | Cumple (ver abajo). |
| Reinicio acotado al usuario | Cumple: mismos conteos, `journal_entry` nuevos y una corrida posterior estable. Los identificadores de transferencia se derivan del usuario, del mes y del índice, así que dentro del mismo mes se repiten. |

Guardas verificadas: se rechaza con mensaje claro `…/postgres` (base sin prefijo `vera_semilla_`),
el puerto 5432 y un `DEMO_USUARIO` nulo.

Pruebas automáticas: `go vet -tags=integration` y `go test -race -tags=integration
./cmd/semilla-escenario` (dos corridas idénticas, reinicio, cursor global intacto y censo exacto de
categorías y orígenes) en verde; `go test -race ./internal/inteligencia/...` en verde;
`golangci-lint` sin hallazgos en los archivos de esta tarea. La guarda del cursor vale sobre una base
solo migrada porque `ProcesarEscenario` no avanza el cursor global, no hace falta enriquecerla antes.

## Limitaciones y pendientes (dichas sin adornos)

1. **Whisper no está conectado al chat.** Solo existen el servicio y el cliente; el proveedor
   compuesto espera al adaptador de razonamiento, como indica el documento de traspaso.
2. **Whisper se probó con un WAV de Kokoro y silencio**, no con los 24 clips oficiales del
   repositorio de modelos (no están en este repositorio). El WER 0,0808 no se re-midió.
3. **Kokoro por HTTP completo:** el modelo real se probó a través del adaptador y la caché; el
   endpoint completo con base de datos se cubre con pruebas que usan un sintetizador simulado.
   En un despliegue hace falta el servicio de voz corriendo y `VERA_VOZ_TOKEN` definido (sin él
   el endpoint responde 401). La app debe llamar al endpoint y reconocer la respuesta sin audio.
4. **Seis orígenes de categoría:** hoy solo salen `lexico` y `sin_categoria`. La cascada ya escribe
   los siete orígenes (CF-A1-03, `d114d0c`); lo que falta son las entradas del escenario que los
   disparan: MCC en el QR, marca del catálogo, corrección en memoria privada y contraparte interna.
   El criterio de CF-A2-03 de cinco escalones por origen no se cumple con esta siembra, la tarjeta
   queda en `parcial` por ese criterio, y el censo de siete orígenes se demuestra con la prueba de
   integración de A1-03.
5. **Suite completa de Go:** en la corrida completa previa a la corrección todos los paquetes
   pasaron salvo `internal/copilot/sttlocal` (el defecto de prueba descrito arriba). Tras la
   corrección se re-ejecutaron los paquetes tocados (`sttlocal`, `inteligencia`, `httpserver`,
   `vozlocal`, `cmd/api`, `cmd/semilla-escenario`), no la suite completa otra vez.
6. **Fecha de la demo:** la siembra usa un reloj simulado (siempre día 19); si se consulta la demo
   en otro periodo, algunos avisos podrían no aparecer vigentes. Usar una fecha del mes en que se
   demuestre.
7. **Pendiente administrativo:** pegar el conteo y los SHA en las tarjetas del tablero.
