# Comparación de voces de Cartesia · 21 de octubre de 2026

La clave local se verificó consultando el catálogo público de voces. No se
registra ni se entrega al navegador. Las tres candidatas son voces públicas
femeninas; no se creó ni clonó ninguna voz. Se evalúa el snapshot
`sonic-3.6-2026-08-27`, con la API `2026-08-14`.

| Voz | Acento nativo del catálogo | Audio | Generación del WAV completo |
| --- | --- | --- | --- |
| Catalina | Colombia (`es-CO`) | [Escuchar](cartesia-Catalina.wav) | 3484 ms |
| Mariana | Colombia (`es-CO`) | [Escuchar](cartesia-Mariana.wav) | 2240 ms |
| Ximena | México (`es-MX`) | [Escuchar](cartesia-Ximena.wav) | 1864 ms |

Los tres WAV incluyen exactamente este texto ficticio, sin modificar los montos:

> Hola, soy Vera. Qué bueno hablar contigo. Tu saldo disponible es 120000.00 COP.
> Puedo preparar un envío de 30000.00 COP para Laura. Tú revisas los datos y
> confirmas en la aplicación. ¿Qué te gustaría hacer?

El texto sólo es una muestra de lectura: no se consultaron cuentas, no se
preparó una operación y no se movió dinero. Comprobar naturalidad, acento,
entonación y lectura de cifras corresponde a la audición humana. El tiempo
es hasta recibir el WAV completo, no hasta el primer fragmento ni una garantía
de latencia futura. Duraciones: 17,20, 17,36 y 18,00 segundos respectivamente.

La generación final usó 618 caracteres de texto entre las tres llamadas. Durante
el diagnóstico se hicieron otras dos síntesis del mismo texto: 1030 caracteres
en cinco generaciones en total. Son caracteres enviados; no una lectura del
saldo ni una conciliación de créditos de la cuenta. No se cambió facturación.

Se intentó una referencia de Aoede con el mismo texto una sola vez. Gemini
devolvió `ProviderLimit`; se conserva en `informe-antes-normalizacion.json`.
No se reemplaza por un audio con otro texto ni se presenta como comparación
completa Gemini–Cartesia. `informe.json` documenta las tres muestras aprobadas
técnicamente de Cartesia, sin asignar puntuaciones de calidad.

## Corrección de formato detectada

Cartesia devolvió WAV PCM16 mono de 24 kHz con bloques `fmt `, `LIST` y `data`.
RIFF y data usaban el marcador `0xffffffff`, habitual al generar audio sin poder
volver al principio del archivo para escribir su tamaño definitivo. La primera
muestra se rechazó por esa cabecera; no fue un fallo de generación del proveedor.

`audiopcm.Normalizar` fija tamaños sólo después de recibir todo el cuerpo HTTP o
JSON satisfactoriamente. Conserva los bytes de PCM y los metadatos. Rechaza
cuerpos conocidos truncados, cortes de transporte, muestras impares, tamaños
contradictorios y formatos distintos de PCM16 mono de 24 kHz. Se aplica a los
adaptadores de Cartesia y Gemini; no se afirmó disponibilidad real de Gemini
porque su cuota seguía limitada. Hay regresiones de JSON completo, WAV con
marcadores y respuesta HTTP truncada.

## Repetir

Desde la raíz del backend, con Docker y las claves en `.env.local`:

```powershell
docker run --rm --env-file .env.local -v "${PWD}:/src" -v vera117-gomod:/go/pkg/mod -v vera117-gocache:/root/.cache/go-build -w /src golang:1.27.0 go run ./cmd/comparar-voces-vera -nivel-gratuito-confirmado -incluir-gemini=false -salida docs/evaluaciones/comparacion-cartesia-nueva
```

Omitir `-incluir-gemini=false` agrega como máximo una solicitud de Aoede con el
mismo texto. El comando rechaza CI, exige confirmación y calcula previamente un
tope de texto; no reintenta errores ni continúa consumiendo Cartesia tras un
rechazo. No se activa Cartesia como respaldo del laboratorio al agregar la clave:
primero se elige la voz por calidad de audición.

Fuentes: [selección de voces y snapshot](https://docs.cartesia.ai/build-with-cartesia/tts-models/latest),
[catálogo de voces](https://docs.cartesia.ai/api-reference/voices/list) y
[contrato TTS](https://docs.cartesia.ai/api-reference/tts/bytes).
