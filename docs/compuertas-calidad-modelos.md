# Compuertas de calidad de los modelos

Cuatro compuertas corren en CI sin GPU, sin imagenes y sin credenciales, mas una quinta que necesita
los discos de pesos. El trabajo `calidad-modelos.yml` las separa para que un fallo diga qué compuerta
rompió y no un rojo genérico. Toda cifra citada aquí sale de un archivo del repositorio o de una
medición con comando escrito, como pide el resto del cierre.

| Compuerta | Job | Qué sostiene | Necesita |
| --- | --- | --- | --- |
| Lockfile de pesos | `lockfile` | `modelos/lock.json` es un inventario válido y el verificador no se rompe | Go |
| Aceptación archivada | `aceptacion-archivada` | El informe del proveedor local sigue cumpliendo sus umbrales | Python |
| Humo de voz | `voz` | El servidor de voz responde 200 con token, 401 sin token y produce un WAV válido | Python y dependencias de `infra/voz-local` |
| Transcripción | `transcripcion` | El WER sobre los clips sintéticos no supera 0,10 | Los clips, sus referencias y el medidor |
| Bytes de los pesos | fuera de CI | Los pesos en disco coinciden con las huellas del lockfile | Los discos con varios GB |

## 1. Lockfile de pesos

`go run ./cmd/verificar-modelos -solo-lock -lock modelos/lock.json` valida la estructura sin abrir
ningún archivo pesado, y `go test -count=1 ./cmd/verificar-modelos/` cubre la comparación de bytes
sobre un directorio temporal. Si el lockfile desaparece, el paso lo nombra antes de que el comando
falle con un error de apertura.

Reproducción local:

```bash
go run ./cmd/verificar-modelos -solo-lock -lock modelos/lock.json
go test -count=1 ./cmd/verificar-modelos/
```

La verificación completa de bytes corre donde estén los discos: `-lock modelos/lock.json -raiz RUTA`.
Bajar varios GB en cada PR no es viable, y por eso CI no la corre. Queda fuera por costo, no por
descuido.

## 2. Aceptación archivada

`docs/evaluaciones/local-e2b-seleccion-20.json` guarda la medición del 11 de noviembre de 2026 con
el proveedor local: 27 muestras con llamada, 21 llamadas correctas sobre 21 exigidas, cero
ejecuciones de dinero y p95 de 19968 ms. La compuerta revalida ese archivo contra umbrales escritos
en el propio worklow:

- `llamadas_correctas >= exigidas_llamadas_correctas` (21 de 21).
- `ejecuciones_de_dinero == 0`.
- `p95_ms <= 30000`.
- `cumple == true`.

Si falta el archivo o falta cualquiera de esos campos, el job falla nombrando lo que falta. Editar el
informe a la baja también falla: las cifras que la tesis cita tienen que seguir sosteniendo la
aceptación, y un informe ausente no puede pasar por verde.

## 3. Humo del servicio de voz

El job `voz` corre las pruebas de `infra/voz-local/test_servidor.py`. Cubre healthcheck 200 con
token y 401 sin token, `/sintetizar` con WAV PCM mono y `/flujo` con el orden de los eventos. No
descarga pesos ni necesita GPU: usa el motor falso de la prueba para aislar el contrato HTTP. La
corrida 43454401867 pasó en 53 s.

## 4. Transcripción

La línea base del WER (0,0808 sobre 24 clips sintéticos) se reprodujo localmente el 13-11-2026 y
vive en `docs/mediciones-locales-2026-11-13.md`. El conjunto ya está en `infra/stt-local/clips`,
con `manifiesto.json` y `medir_wer.py` versionados; el job exige los tres y corre
`medir_wer.py --manifiesto ... --tope 0.10`. La corrida 43454401867 pasó en 1 min 15 s con el
tope 0,10; el agregado es 0,0808.

Reproducción local:

```bash
python3 infra/stt-local/medir_wer.py --manifiesto infra/stt-local/clips/manifiesto.json --tope 0.10
```

## Criterio pre-registrado de mejora

Antes de medir, y no después:

- El clasificador solo se enciende en producción si la precisión de lo mostrado alcanza 0,90. La
  medición de `docs/medicion-tfidf-v3.md` da 0,875 con el artefacto v3, así que la compuerta no se
  cumple y la bandera queda apagada.
- La transcripción no supera un WER de 0,10.
- La aceptación del proveedor local exige cero ejecuciones de dinero y p95 por debajo de 30000 ms.

Ninguno de esos tres números se recalibra para que pase: el informe de T-03 deja la cifra real y la
decisión escrita.

## La bandera del clasificador

La medición de T-03 con el artefacto v3 (huella `7aa998a8…`, 5728 nombres, sin semillas) deja el
clasificador en 8 filas mostradas y 7 correctas sobre la hoja de prueba de 150 filas: precisión
0,875, Wilson 0,529 a 0,978, cobertura 0,066. `primer-corte` repite la cifra y `todos` con el catálogo
v3 queda en 0,833. La compuerta de 0,90 no se alcanza.

| Destino | `CLASIFICADOR` | Por qué |
| --- | --- | --- |
| `local` | `tfidf` | `infra/local/demostrar.sh` enriquece con `CLASIFICADOR=tfidf`; apagarlo dejaría el lote de la demo sin el escalón 6. |
| `demo` | `tfidf` | La demo muestra la cascada completa; el valor queda versionado en `banderas.tsv`. |
| `produccion` | `apagado` | La medición da 0,875 y la compuerta exige 0,90. Encenderlo afirmaría una precisión que el artefacto v3 no sostiene. |

El valor vive en `infra/servidor/banderas.tsv` y en `.env.produccion.example`, `.env.demo.example`,
`.env.local.example` y `.env.example`, con la cifra y el motivo escritos al lado. `cmd/api` no parsea
el artefacto cuando la bandera está apagada, y la prueba `TestLoadConfigClasificadorTFIDF` exige que
el modo `tfidf` parsee el artefacto embebido sin error, así que la cifra que sostiene la decisión
también tiene una prueba de que el artefacto carga.

## Qué corre donde hay GPU y qué se queda fuera

- Con GPU y los pesos en disco: la verificación completa de bytes, Whisper sobre los 24 clips y
  cualquier medición del proveedor real. Nada de eso entra en CI por peso de descarga y por falta de
  GPU.
- Fuera también: la integración con Postgres y Redis, que corre en `ci.yml` con servicios, y la
  evaluación del sombra de la proyección de saldo, que se reporta como falla honesta en
  `docs/ajuste-tesis-cierre.md`.
