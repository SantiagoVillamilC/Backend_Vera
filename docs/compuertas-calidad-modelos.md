# Compuertas de calidad de los modelos

Cuatro compuertas corren en CI sin GPU, sin imagenes y sin credenciales, una quinta necesita los
discos de pesos y una sexta, manual, decide el release a partir de un informe traído de un pod. El trabajo `calidad-modelos.yml` las separa para que un fallo diga qué compuerta
rompió y no un rojo genérico. Toda cifra citada aquí sale de un archivo del repositorio o de una
medición con comando escrito, como pide el resto del cierre.

| Compuerta | Job | Qué sostiene | Necesita |
| --- | --- | --- | --- |
| Lockfile de pesos | `lockfile` | `modelos/lock.json` es un inventario válido y el verificador no se rompe | Go |
| Aceptación archivada | `aceptacion-archivada` | El informe del proveedor local sigue cumpliendo sus umbrales | Python |
| Humo de voz | `voz` | El servidor de voz responde 200 con token, 401 sin token y produce un WAV válido | Python y dependencias de `infra/voz-local` |
| Transcripción | `transcripcion` | El WER sobre los clips sintéticos no supera 0,10 | Los clips, sus referencias y el medidor |
| Bytes de los pesos | fuera de CI | Los pesos en disco coinciden con las huellas del lockfile | Los discos con varios GB |
| Release de modelos | `release-modelos` (manual) | Un informe y una base con los mismos 200 ids y veredicto "mejora demostrada" sobre 200 pares | Un runner propio con el árbol `modelos-gemma` y sus dependencias Python, más el informe de un pod efímero |

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
`medir_wer.py --manifiesto ... --tope 0.10`. Antes corren las pruebas del contrato HTTP de
`infra/stt-local/test_servidor.py`, que incluyen el caso de bytes que no son audio y esperan 422. La corrida 43454401867 pasó en 1 min 15 s con el
tope 0,10; el agregado es 0,0808.

Reproducción local:

```bash
python3 infra/stt-local/medir_wer.py --manifiesto infra/stt-local/clips/manifiesto.json --tope 0.10
```

## 5. Compuerta de release de modelos

El job `release-modelos` solo corre con `workflow_dispatch` y falla cerrado: no mide nada, exige un
informe de 200 casos que ya existe. En CI no hay GPU, así que ese informe viene de un pod efímero.
Se genera allí, se descarga y se entrega al job como carpeta o como artefacto de un run.

### Dónde corre

El job declara `runs-on: [self-hosted, linux, modelos-gemma]`. Solo puede dar verde en un runner
propio que tenga el árbol `modelos-gemma` de la tesis y sus dependencias Python, y que lleve el label
`modelos-gemma`. **El runner hospedado (`ubuntu-latest`) no sirve**: no trae ese árbol, que no vive en
este repositorio, ni las dependencias, así que cambiar el label a `ubuntu-latest` solo produce un rojo
que no dice nada del modelo. Tampoco hay `actions/checkout` en este job: nada del repositorio se usa, y
el checkout limpia el workspace, que es donde puede estar la carpeta `modelos-gemma` si se pasa con ruta
relativa.

El primer paso falla con mensaje claro si la carpeta `modelos_gemma` no existe, si faltan
`evaluacion/veredicto.py` o `dataset/datos/evaluacion.jsonl`, si falta la CLI `gh` o si `python3` no
puede importar `colab.datos_ft`, `evaluacion.lote` y `evaluacion.veredicto` desde esa carpeta.

Entradas del despacho manual:

- `informe`: carpeta con `casos.jsonl` y `resumen.json`, o el ID numérico de un run que subió el
  artefacto `informe-200-casos`.
- `base`: lo mismo para la corrida base, con el artefacto `informe-base-200-casos`.
- `modelos_gemma`: carpeta de `modelos-gemma` en el runner propio (por defecto `modelos-gemma`, relativa
  al workspace del runner). Ese árbol trae `evaluacion/veredicto.py` y
  `dataset/datos/evaluacion.jsonl`. No vive en este repositorio, y si no está el job falla nombrándolo.

Con un ID numérico, antes de `gh run download` el job consulta el run con `gh api` y exige las cuatro
cosas siguientes. Si alguna no se cumple, no baja nada:

- El run pertenece a este repositorio y su repositorio de cabeza es el mismo (no un fork).
- La conclusión es `success`.
- El run es de este workflow (`.github/workflows/calidad-modelos.yml`) o su rama es `main`, `staging`
  o `dev`.

El job falla si ocurre cualquiera de estas cosas:

- Falta `informe` o `base`, o el run no tiene el artefacto esperado.
- Falta `casos.jsonl` o `resumen.json` en alguno de los dos, o el segundo no es JSON válido.
- Alguno de los dos no trae exactamente 200 registros con 200 ids distintos, o una línea de
  `casos.jsonl` no es JSON válido o no trae `id`.
- El informe y la base no tienen EXACTAMENTE el mismo conjunto de 200 ids. Un id presente en un lado y
  ausente en el otro no se empareja: se rechaza en vez de recortar a la intersección.
- La fila `global` de la tabla de `veredicto.py` no tiene `n = 200`, es decir, el veredicto no se
  calculó sobre 200 pares.
- Un run dado por ID no cumple los criterios de arriba, o `gh` no puede consultarlo.
- Falta `veredicto.py` o el dataset de evaluación.
- `veredicto.py` termina con código distinto de cero.
- La línea de salida no es exactamente `Veredicto: mejora demostrada`. Ojo: `veredicto.py` sale con
  código 0 aunque el veredicto sea negativo, por eso el job lee el texto y no el código.

El criterio es el pre-registrado de `veredicto.py` (10 puntos o más, o McNemar con p menor que 0,05,
y el holdout no puede empeorar). No se recalibra para que pase.

Comando en el pod, con la corrida reanudable y la base archivada en
`docs/evaluaciones/local-e2b-seleccion-20.json`:

```bash
cd modelos-gemma
python3 evaluar.py --modelo MODELO --think default --etiqueta INFORME --reanudar
python3 -m evaluacion.veredicto evaluacion/resultados/BASE evaluacion/resultados/INFORME
```

La segunda línea es la misma que ejecuta el job, útil para ver el veredicto antes de despachar el
release. El archivo `local-e2b-seleccion-20.json` es la aceptación archivada del proveedor local y
no tiene el formato `casos.jsonl`, así que la comparación pareada necesita la carpeta base completa.

Limitaciones conocidas de la validación del run, que el job NO verifica y por eso no se afirman:

- Que el artefacto lo haya producido `evaluar.py --reanudar` en el pod. `gh` solo dice qué run lo
  subió y con qué conclusión, no cómo se generó su contenido. Una rama conocida puede subir un
  artefacto con ese nombre desde cualquier workflow del repositorio.
- Que el run corresponda al commit que se va a liberar. El job no compara `head_sha` con nada.
- Que el artefacto no haya caducado o haya sido reemplazado después del run: `gh run download` baja
  lo que haya bajo ese nombre en el momento.
- Las entradas dadas como carpeta. No tienen run que consultar, así que el único respaldo es la
  confianza en quien controla el runner propio y la coincidencia de ids de la compuerta.

Artefactos descargables:

| Artefacto | Job | Contenido |
| --- | --- | --- |
| `voz-pruebas-log` | `voz` | Log de las pruebas del servidor de voz |
| `wer-transcripcion` | `transcripcion` | `wer.json` con n, WER, tope y `pass` |
| `veredicto-release-modelos` | `release-modelos` | `veredicto.txt` con la tabla pareada y `veredicto.err` |

Los tres se suben aunque el paso falle, para poder ver la cifra que rompió la compuerta. Los de voz y
WER se conservan 30 días y el veredicto 90. Las acciones de subida van fijadas por SHA a la versión
v7.0.1 de `actions/upload-artifact`.

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
