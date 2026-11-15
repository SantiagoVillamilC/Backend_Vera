# Lockfile de artefactos de modelos

Los pesos de los modelos no entran en Git: viven en discos locales y en la caché de los
contenedores. `modelos/lock.json` es el inventario versionado que fija qué archivo exacto sirve
cada despliegue. Sin él, dos equipos pueden correr el mismo nombre de modelo con archivos
distintos y nadie lo nota hasta que cambian las respuestas.

El verificador es `cmd/verificar-modelos`. Compara el inventario con los archivos reales y
falla si alguno falta, si pesa otra cosa o si su huella no coincide.

## Formato

```json
{
  "version": 1,
  "raices": {
    "gguf": "../../modelos-gemma/modelos",
    "voz": "/opt/voz/modelos"
  },
  "artefactos": [
    {
      "nombre": "gemma-4-e2b-it-q4_k_m",
      "raiz": "gguf",
      "origen": "de dónde salió el archivo y de dónde se tomó la huella",
      "ruta": "gemma4/gemma-4-E2B-it-Q4_K_M.gguf",
      "sha256": "740185b21d22ceb83a11c3aa62ad5842ef32c70f6096d756bbee85a1e4ec34b8",
      "bytes": 3106738272,
      "licencia": "Gemma Terms of Use",
      "fecha_captura": "2026-11-10",
      "version_herramienta": "ollama 0.40.10"
    },
    {
      "nombre": "whisper-small",
      "origen": "descarga automática, sin binario verificable",
      "ruta": "whisper/small",
      "pin": "faster-whisper==1.2.1, modelo small",
      "licencia": "MIT",
      "fecha_captura": "2026-11-10",
      "version_herramienta": "faster-whisper 1.2.1"
    }
  ]
}
```

Las raíces son un mapa con nombre porque los pesos no cuelgan todos del mismo árbol: los GGUF
viven en un disco local y los de voz en `/opt/voz/modelos`, dentro de la imagen del contenedor.
Una ruta relativa se resuelve contra la carpeta que contiene el lockfile. Una absoluta se usa tal
cual, que es lo que necesita el contenedor.

Campo por campo:

| Campo | Obligatorio | Qué es |
| --- | --- | --- |
| `version` | sí | versión del formato. Hoy solo existe la 1 |
| `raices` | sí | mapa de nombre de raíz a ruta de un directorio de modelos |
| `nombre` | sí | identificador único del artefacto. Es lo que se nombra en el reporte de fallos |
| `raiz` | con `sha256` | nombre de una entrada de `raices` contra la que se resuelve `ruta` |
| `origen` | sí | procedencia. Si la huella no se calculó sobre el archivo en disco, aquí se declara de qué manifiesto salió |
| `ruta` | sí | ruta relativa a su raíz, normalizada y sin `..` |
| `sha256` | una de dos | huella en hexadecimal minúsculas de 64 caracteres |
| `bytes` | con `sha256` | tamaño exacto en bytes, para detectar truncamientos sin leer los GB completos |
| `pin` | una de dos | versión de paquete y nombre de modelo cuando no hay binario verificable |
| `licencia` | sí | licencia del peso |
| `fecha_captura` | sí | fecha en formato `AAAA-MM-DD` en que se capturó la huella o el pin |
| `version_herramienta` | sí | versión de la herramienta que produjo o ejecuta el artefacto |

Un artefacto lleva `sha256` o `pin`, nunca los dos. `bytes` solo tiene sentido con huella, porque
un artefacto fijado por pin no declara archivo y no se compara con el disco.

## Agregar un artefacto

1. Localiza el archivo real. Las rutas conocidas son
   `/home/chris/Documents/universidad-2026B/proyecto-fin-de-grado/modelos-gemma/modelos` para los
   pesos de Gemma, y `/opt/voz/modelos` dentro de la imagen de `infra/voz-local` para los de voz.
2. Si el archivo está en disco, calcula su huella y su tamaño:

   ```bash
   sha256sum gemma4/gemma-4-E4B-it-Q4_K_M.gguf
   stat -c '%n %s' gemma4/gemma-4-E4B-it-Q4_K_M.gguf
   ```

3. Si el archivo no está en disco, toma la huella y el tamaño del manifiesto que ya los declare,
   por ejemplo `infra/voz-local/descargar.py`, y escribe esa procedencia en `origen`. Nunca
   inventes un `sha256` ni un `bytes`. Si no hay fuente verificable, deja el artefacto fuera y
   anótalo en el reporte del cambio.
4. Añade la entrada a `modelos/lock.json` con `raiz` igual al nombre de una de las raíces, `ruta`
   relativa a esa raíz, `licencia`, `fecha_captura` del día y `version_herramienta`.
5. Comprueba la estructura:

   ```bash
   go run ./cmd/verificar-modelos -solo-lock
   ```

6. Comprueba los bytes contra el disco donde estén los pesos:

   ```bash
   go run ./cmd/verificar-modelos -lock modelos/lock.json -raiz voz=/tmp/voz
   ```

7. Corre las pruebas del verificador:

   ```bash
   go test ./cmd/verificar-modelos/
   ```

Para un modelo que se descarga desde un paquete y nunca deja un binario fijo, como Whisper con
`faster-whisper`, usa `pin` con la versión del paquete y el nombre del modelo. Ese artefacto no se
compara contra el disco, se informa como fijado por pin.

## Qué cubre cada modo

`-solo-lock` valida estructura, nombres únicos, campos obligatorios, formato de la fecha, forma
del `sha256`, nombre de raíz declarado y normalización de las rutas. No abre ningún archivo de
pesos, así que sirve en máquinas sin modelos y es lo que corre en CI.

Sin `-solo-lock` recorre cada artefacto con huella y reporta todos los fallos juntos: archivo
ausente, tamaño distinto y huella distinta. Los artefactos con `pin` se informan aparte.

## Cómo apuntar a una raíz real

Sin `-raiz`, el verificador usa las rutas de `raices` tal como están escritas en el lockfile. En
otra máquina reemplaza las que haga falta. La bandera es repetible y acepta dos formas:

```bash
# una raíz concreta por nombre
go run ./cmd/verificar-modelos -raiz voz=/tmp/voz -raiz gguf=/srv/modelos/gguf

# todas a la vez, cuando el árbol completo se copió a otro sitio
go run ./cmd/verificar-modelos -raiz /srv/modelos
```

Si nombras una raíz que el lockfile no declara, el verificador falla, así que una errata en el
nombre no pasa inadvertida.

En este equipo los GGUF están en `../../modelos-gemma/modelos` y pasan. Los pesos de voz solo
existen dentro de la imagen, así que una corrida sin banderas los reporta ausentes. Para
comprobarlos, saca el directorio de la imagen y apunta la raíz ahí:

```bash
docker create --name voz-temporal vera-laboratorio-voz:kokoro-0.6.1
docker cp voz-temporal:/opt/voz/modelos /tmp/voz
docker rm voz-temporal
go run ./cmd/verificar-modelos -raiz voz=/tmp/voz
```

La imagen CPU solo lleva el `int8` y las voces, así que esa corrida también reporta ausentes el
`fp32` y el `fp16`. Antes de verificar, completa la carpeta con los opcionales (ver el párrafo
sobre la corrida completa más abajo).

La copia local de las voces en `modelos-gemma/modelos/kokoro/voices.bin` coincide byte a byte con
el asset publicado. Si la usas para verificar, cópiala o enlázala como `voices-v1.0.bin` dentro de
la raíz que pases, porque ese es el nombre que carga el contenedor.

## Estado actual del inventario

| Artefacto | Raíz | Procedencia de la huella |
| --- | --- | --- |
| `gemma-4-e2b-it-q4_k_m`, `gemma-4-e4b-it-q4_k_m` | `gguf` | calculada sobre el GGUF en disco |
| `kokoro-v1.0-int8-onnx` | `voz` | manifiesto `infra/voz-local/descargar.py`, que además comprueba el tamaño. Coincide con el asset de la release `model-files-v1.1`. Es el archivo que carga el contenedor |
| `kokoro-v1.0-fp32-onnx`, `kokoro-v1.0-fp16-onnx` | `voz` | mismo manifiesto, secciones `ARCHIVOS_GPU` (fp32) y `ARCHIVOS_FP16`, y coinciden con los assets de la release `model-files-v1.1`. Son opcionales: solo los carga la variante en GPU (`VERA_VOZ_DISPOSITIVO=cuda`). `descargar.py` baja el fp32 únicamente con `VERA_VOZ_DESCARGAR_GPU=1` y el fp16 únicamente con `VERA_VOZ_DESCARGAR_FP16=1`, un opt-in aparte porque en CUDA produjo audio vacío. La imagen CPU no los lleva |
| `kokoro-voces-v1.0` | `voz` | mismo manifiesto. El archivo se llama `voices-v1.0.bin`, como en el contenedor |
| `whisper-small` | ninguna | fijado por `pin` con `faster-whisper==1.2.1` y el modelo `small` |

La copia local del `fp32` de Kokoro en el disco de este equipo sigue fuera como referencia: su huella
no coincide con el asset publicado del mismo nombre. El inventario usa la huella del asset de la
release, y quien quiera el `fp32` lo baja con `descargar.py`, que la comprueba.

Una corrida completa de `verificar-modelos` sobre la raíz `voz` exige los cuatro archivos de la
raíz: tres modelos de Kokoro (`int8`, `fp32` y `fp16`) y el archivo de voces. En la imagen CPU, que
solo lleva el `int8` y las voces, el `fp32` y el `fp16` salen como ausentes, y esto es un cambio
respecto de cuando el lockfile solo declaraba dos archivos en la raíz `voz`. Para comprobar una
raíz así, baja primero los opcionales a esa carpeta con
`VERA_VOZ_DESCARGAR_GPU=1 VERA_VOZ_DESCARGAR_FP16=1 VERA_VOZ_MODELOS_DESTINO=<dir> python infra/voz-local/descargar.py`.
Cada peso se escribe en `<nombre>.parcial` y solo pasa a su nombre final tras verificar tamaño y
SHA256, y uno ya presente y correcto no se vuelve a bajar.

## Cruce con el manifiesto de voz

El build de `infra/voz-local` descarga con `descargar.py` los archivos de Kokoro (dos por defecto, tres con
`VERA_VOZ_DESCARGAR_GPU=1` y cuatro si se suma `VERA_VOZ_DESCARGAR_FP16=1`), y ese archivo trae su
propia copia de huellas y tamaños. Para que el lockfile y ese manifiesto no se separen, la prueba
`TestLockfileCoincideConElManifiestoDeVoz` lee `descargar.py` y compara cada archivo con el
artefacto del lockfile que usa la raíz `voz`. Si una de las dos fuentes cambia sola, la prueba
falla. `infra/voz-local/THIRD_PARTY.md` no repite las huellas, apunta a estas dos fuentes.

## CI

`.github/workflows/verificar-modelos.yml` corre en `pull_request` y en `push` cuando cambian el
lockfile, el verificador, el manifiesto de voz o el propio workflow. Ejecuta `-solo-lock` y las
pruebas unitarias. No usa GPU, no descarga pesos y termina en menos de un minuto.

El paso de validación usa `set -o pipefail` y junta stderr con stdout antes de `tee`, así que el
estado del paso es el del verificador y los fallos quedan en el resumen del job. Un lockfile que
no parsea, con huellas inválidas o con nombres repetidos pone el job en rojo.

La comparación de bytes completos no puede correr en CI porque los pesos no están ahí. Ese paso
corre en las máquinas y en los servidores que sí los tienen, con `-raiz` apuntando a su raíz de
modelos.
