# Entrega de la línea de IA a backend · Santiago

Documento de traspaso de la parte de inteligencia artificial al frente de backend. No describe
tareas de IA: describe lo que IA entrega hecho, lo que queda como borrador y lo que backend debe
cerrar. Las rutas son relativas a este repositorio; lo que vive en el repositorio de modelos se
nombra así porque no se versiona acá.

Rama: `feat/stt-tts-seed`, cortada de `dev`. Este es el único documento de contexto de la
entrega; el agente que la tome debe leer solo este archivo y el código de `infra/stt-local/`.

Orden acordado: **Kokoro primero, después una parte de Whisper, al final la siembra de la demo**.
Este documento dice qué recibe hecho, qué le queda, con qué se acepta y qué **no** se le entrega.

## 1. Kokoro en el chat (CF-A3-03, 6 h)

**Lo que ya existe y se reutiliza tal cual:**
- El servicio de voz del laboratorio: `infra/voz-local/` (servidor, Dockerfile, descarga
  verificada por huella, pruebas y avisos de terceros).
- El adaptador Go que ya lo consume: `internal/copilot/vozlocal/voz.go`, con la restricción de
  loopback y token que hay que conservar.
- El cableado de referencia en el laboratorio y sus evaluaciones del 21-10, donde la voz propia sirvió
  20 de 20 turnos como respaldo.

**Decisión de contrato ya tomada (no hay que discutirla):**
- `POST /v1/vera/sintetizar` con `{"texto": "..."}` y `X-Vera-Token` → `audio/wav` en el cuerpo.
- Límites: texto acotado, tope de duración, caché por hash del texto y cuota por usuario.
- Si el sintetizador no responde: el mensaje se entrega igual, sin audio y marcado; la app usa su voz
  local. Nunca se rompe el turno.

**Su trabajo:** endpoint, caché, cuota, degradación, pruebas de formato WAV y de repetición.
**Se acepta con:** un mensaje reproduce audio con voz `ef_dora`; repetir el mismo texto no vuelve a
sintetizar; con el servicio caído la respuesta sigue siendo 200 sin audio.

## 2. Whisper, primera parte (CF-A3-02, 8 h; esta noche sólo el servicio y su cliente)

**Lo que se le entrega:** borrador del servicio en `infra/stt-local/`.
Replica el patrón del servicio de voz (loopback, `X-Vera-Token`, audio en memoria, sin registro),
expone `GET /healthz` y `POST /transcribir` (audio crudo → `{"transcripcion": "..."}`) y devuelve
texto vacío cuando no hay voz.

**Por qué no se le entrega la pipeline completa:** la pipeline es una herramienta de terminal con
micrófono, ffmpeg, VAD manos libres y síntesis; no es un servicio. Lo único reutilizable para el
backend son dos piezas: el sintetizador (ya existe) y el transcriptor (este borrador). Darle la
pipeline entera sería darle código que no puede desplegar.

**Configuración exacta del modelo, ya medida:** `small`, `int8` en CPU y `float16` en GPU,
`language=es`, `beam_size=1`, filtro de VAD. En CPU tarda varias veces el tiempo real; en GPU unos
2,3 s por 3,3 s de audio.

**Conjunto de prueba y línea base:** `salidas/stt_set/` del repositorio de modelos (24 clips), `stt_set.json` y
`salidas/wer_local.json` con WER 0,0808 sobre voz sintética. El procedimiento para repetir la medición
está en `evaluacion/wer_local.py` del repositorio de modelos.

**Su trabajo esta noche:** servicio corriendo, cliente Go con plazo y errores públicos, y una prueba
con un clip. El proveedor compuesto (chat local + transcripción local) espera al adaptador de
razonamiento; está escrito así en la tarjeta.

**Se acepta con:** un WAV del conjunto devuelve la transcripción esperada, sin llamadas externas, y
un clip en silencio devuelve texto vacío sin abrir conversación.

## 3. Siembra de la demo (CF-A2-03, 4 h)

**Lo que ya existe:** los datos ficticios de la billetera con sus treinta y dos movimientos, el
backend simulado que los usa, los vectores de prueba de los cinco avisos y la configuración de
recurrencia congelada. Los guiones locales de demostración ya encienden las banderas correctas.

**Lo que debe producir:** un guion idempotente que deje tres o más pagos iguales a un mismo destino,
un cobro nuevo, una caída de ingreso y gasto en al menos cinco categorías, y que ejecute el
enriquecimiento y la emisión de avisos. Debe imprimir el conteo por origen, categoría y tipo de aviso
para pegarlo como evidencia.

**Aviso de alcance:** el conteo por origen sólo mostrará los seis escalones cuando Christian cierre
la cascada (CF-A1-03). Esta noche el criterio es avisos y recurrencia; los orígenes se verifican
después, sin rehacer la siembra.

**Se acepta con:** correrlo dos veces no duplica nada, deja al menos tres avisos vigentes y una serie
recurrente, y el conteo queda en la tarjeta.

## 4. Qué no se le entrega

- La pipeline de voz completa, por lo dicho arriba.
- La decisión de aprovisionamiento y la elección de modelo: siguen del lado de IA, que entrega
  parámetros y medición.
- La cascada de categorías y el artefacto: son de Christian y bloquean el conteo por origen.
- La tesis y el análisis: se citan, no se delegan.

## 5. Qué hacemos nosotros mientras tanto (lado IA)

1. Cerrar CF-A1-03 para que la siembra pueda mostrar los seis orígenes.
2. Entregar la configuración del razonador local (modelo, cuantización, parámetros de muestreo) el
   mismo día que él empiece el adaptador.
3. Medir el arranque en frío en el destino elegido y pasárselo para el runbook de la demo.
4. Dejar escritas las cifras de aceptación del agente (20 preguntas, cero ejecuciones de dinero,
   p95 menor a 30 s) antes de la primera corrida.

## Anexo · Entrega concreta (rutas verificadas)

Todo lo de esta tabla existe en el repositorio a la fecha del documento. Las rutas son relativas a
``.

| Tarea | Qué recibe | Ruta | Estado |
|---|---|---|---|
| A3-03 | Servicio de voz del laboratorio | `infra/voz-local/servidor.py` | Funciona (usado el 21-10) |
| A3-03 | Imagen y descarga verificada del modelo | `infra/voz-local/Dockerfile`, `descargar.py`, `requirements.txt` | Funciona |
| A3-03 | Pruebas del servidor de voz | `infra/voz-local/test_servidor.py` | Pasan |
| A3-03 | Adaptador Go que ya lo consume | `internal/copilot/vozlocal/voz.go` | Funciona |
| A3-03 | Cableado de referencia como respaldo | `internal/copilot/laboratorio/voz_respaldo.go` | Funciona |
| A3-03 | Documento del laboratorio de voz | `docs/asistente-laboratorio-voz.md` | Vigente |
| A3-03 | Evidencia de uso real (20/20 turnos) | `docs/evaluaciones/voz-natural-con-respaldo.json` | Medido 21-10 |
| A3-03 | Contrato decidido | `POST /v1/vera/sintetizar` con `{"texto"}` y `X-Vera-Token` → `audio/wav` | Decidido |
| A3-02 | Servicio de transcripción (borrador) | `infra/stt-local/servidor.py` | Compila; sin ejecutar |
| A3-02 | Empaquetado del servicio | `infra/stt-local/{Dockerfile,requirements.txt}` | Listo para probar |
| A3-02 | Cliente de humo | `infra/stt-local/probar.py` | Compila |
| A3-02 | Instrucciones y contrato | este documento, sección «Contrato del servicio» | Escrito |
| A3-02 | Conjunto de 24 audios y sus referencias | `salidas/stt_set/` del repositorio de modelos y `stt_set.json` | Existe |
| A3-02 | Línea base de error | `salidas/wer_local.json` del repositorio de modelos (WER 0,0808) | Medido 27-10 |
| A3-02 | Procedimiento para repetir la medición | `evaluacion/wer_local.py` del repositorio de modelos | Funciona |
| A2-03 | Movimientos ficticios (32) | `datos-ficticios/billetera.json` del repositorio de modelos | Existe |
| A2-03 | Herramientas simuladas de la billetera | `backend_simulado.py` del repositorio de modelos | Funciona |
| A2-03 | Vectores de los cinco avisos | `internal/inteligencia/avisos/testdata/vectores-avisos.json` | Existe |
| A2-03 | Guiones de siembra y banderas | `infra/local/{levantar-base.sh,demostrar.sh,sembrar-demo-prod.sh}` | Funcionan |

**No se entrega:** la pipeline de voz completa (la pipeline del repositorio de modelos (`pipeline.py`) y su entorno), la decisión
de aprovisionamiento, la cascada de categorías, el artefacto TF-IDF, la tesis ni el análisis.

## Anexo C · Entregable exacto de CF-A2-03 (siembra de demo)

### Archivos que debe dejar en el repositorio
1. `infra/local/sembrar-escenario-demo.sh` — ejecutable e idempotente, con las mismas
   guardas que el guion de siembra existente: solo `127.0.0.1:55437` y bases `vera_semilla_*`, y
   negativa explícita si la base no es de demo. Acepta `DEMO_USUARIO` y `REINICIAR=si`.
2. `bd/semillas/escenario-demo.sql` (o un comando Go `cmd/semilla-escenario` si prefiere
   transacciones) con los asientos del escenario. Nada de datos fuera de ese usuario.
3. `docs/escenario-demo.md` — qué dispara cada bloque, cómo reiniciar y qué no toca.
4. Estado de la corrida en `DEMO-ESCENARIO-STATE.json`, con el usuario, los identificadores sembrados
   y los conteos obtenidos.

### Escenario mínimo (valores concretos, ajustables si la proyección no cuadra)
| Destino | Día | Monto | Repetición | Qué debe provocar |
|---|---|---|---|---|
| ENEL (luz) | 20 | 50.000 | 3 meses iguales | serie madura + `pago_no_alcanza` |
| INTERNET HOGAR | 25 | 89.900 | 3 meses iguales | segunda serie recurrente |
| STREAMING | 12 | 24.900 | primera vez | `cobro_nuevo` |
| Ingreso del mes | — | 30 % menor | un mes | `caida_ingreso` |
| Gastos | — | — | 5 categorías | alimentos, transporte, restaurantes, salud, entretenimiento |
| Comercio desconocido | — | — | una vez | `sin_categoria` (abstención) |
| Saldo | — | proyectado menor a 50.000 al día 20 | — | es lo que hace saltar el aviso de la luz |

### Evidencia que debe pegar en la tarjeta (dos corridas)
```sql
-- avisos por familia
select tipo, count(*) from inteligencia.aviso
 where usuario_id = :'usuario' group by 1 order by 1;
-- categorías y orígenes
select categoria, count(*) from inteligencia.movimiento_enriquecido
 where usuario_id = :'usuario' group by 1 order by 2 desc;
select origen_categoria, count(*) from inteligencia.movimiento_enriquecido
 where usuario_id = :'usuario' group by 1 order by 2 desc;
```
La segunda corrida del guion debe devolver exactamente los mismos números.

### Se considera terminado cuando
- Desde una base de demo vacía deja al menos tres avisos vigentes de familias distintas y dos series
  recurrentes, con gasto en cinco categorías y una abstención.
- Correrlo dos veces no duplica movimientos ni avisos y el estado queda escrito.
- El guion se niega a correr contra una base que no sea de demo.
- Queda el commit y su SHA como comentario de la tarjeta.

### Lo que no entra
- Cambios a la siembra masiva existente ni a datos de producción.
- Cambios de modelo o de cascada: el conteo de los seis orígenes se verifica cuando Christian cierre
  CF-A1-03, sin rehacer esta siembra.


## Contrato del servicio de transcripción

- `GET /healthz` con `X-Vera-Token` → `{"transcripcion": true, "modelo": "small"}`.
- `POST /transcribir` con el audio crudo (`audio/wav`, `audio/m4a` o `audio/mpeg`) y `X-Vera-Token`
  → `{"transcripcion": "...", "modelo": "small", "idioma": "es"}`.
- Códigos: 401 sin token, 415 tipo no admitido, 413 cuerpo mayor de 8 MB, 409 si ya hay una
  transcripción en curso, 422 si el audio no se puede decodificar.
- Sin voz devuelve `transcripcion` vacía; el backend la traduce a «audio no entendido».
- Límites a dejar escritos en el despliegue: tamaño y duración máximos por turno, modelo y
  cuantización, y comportamiento cuando el servicio no responde (el turno sigue en texto).

## Cómo probarlo en tu máquina (sin esperar al despliegue)

Los dos servicios escuchan solo en `127.0.0.1`: se prueban desde la misma máquina. Ambos exigen un
token de al menos 32 caracteres, que después el backend debe enviar igual.

### Kokoro (voz de salida, puerto 8766)

La vía corta es el contenedor, porque trae el modelo y verifica su huella al construir:

```bash
cd infra/voz-local
export VERA_LAB_TOKEN=$(python3 -c "import secrets;print(secrets.token_urlsafe(32))")
docker build -t vera-voz .
docker run --rm --network host -e VERA_LAB_TOKEN="$VERA_LAB_TOKEN" \
  --entrypoint python vera-voz /opt/voz/servidor.py
```

Comprobar en otra terminal:

```bash
curl -s -H "X-Vera-Token: $VERA_LAB_TOKEN" http://127.0.0.1:8766/healthz
curl -s -H "X-Vera-Token: $VERA_LAB_TOKEN" -H 'Content-Type: application/json' \
  -d '{"texto":"Hola, soy Vera."}' http://127.0.0.1:8766/sintetizar -o hola.wav
```

`hola.wav` debe abrir y sonar con voz femenina en español. Sin Docker, el servidor espera los pesos en
`/opt/voz/modelos/`, que exige permisos de administrador; por eso conviene el contenedor.

### Whisper small (transcripción, puerto 8767)

```bash
cd infra/stt-local
python3 -m venv .venv && . .venv/bin/activate
pip install -r requirements.txt
export WHISPER_TOKEN=$(python3 -c "import secrets;print(secrets.token_urlsafe(32))")
export WHISPER_MODELO=small
python servidor.py
```

El primer arranque descarga el modelo (unos 500 MB) en la caché de Hugging Face. Con GPU:
`WHISPER_GPU=si` y el runtime de CUDA instalado; sin GPU funciona en CPU, más lento que el tiempo real.

Humo, con un clip del conjunto de prueba del repositorio de modelos:

```bash
python probar.py /ruta/al/repositorio-de-modelos/salidas/stt_set/000_eva-A-001.wav
```

Debe devolver algo equivalente a «en los últimos tres meses, ¿a cuánto asciende lo pagado en el
internet hogar?». Un clip en silencio devuelve `transcripcion` vacía, que el backend traduce a «audio
no entendido».

### Qué probar antes de integrar

- Reiniciar el servicio y ver que el healthcheck responde en menos de un minuto.
- Mandar dos veces el mismo texto a Kokoro y confirmar que la segunda responde más rápido (caché).
- Transcribir tres clips distintos y comparar a ojo contra la referencia.
- Cortar el servicio de voz y comprobar que el chat sigue respondiendo sin audio.
