# Compuertas de calidad de los modelos

Tres compuertas corren en CI sin GPU, sin imagenes y sin credenciales, mas una cuarta que necesita
los discos de pesos. El trabajo `calidad-modelos.yml` las separa para que un fallo diga qué compuerta
rompió y no un rojo genérico. Toda cifra citada aquí sale de un archivo del repositorio o de una
medición con comando escrito, como pide el resto del cierre.

| Compuerta | Job | Qué sostiene | Necesita |
| --- | --- | --- | --- |
| Lockfile de pesos | `lockfile` | `modelos/lock.json` es un inventario válido y el verificador no se rompe | Go |
| Aceptación archivada | `aceptacion-archivada` | El informe del proveedor local sigue cumpliendo sus umbrales | Python |
| Transcripción | `transcripcion` | El WER sobre los clips sintéticos no supera 0,15 | Los clips, sus referencias y el medidor |
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

## 3. Transcripción

La línea base del WER (0,0808 sobre 24 clips sintéticos) es del repositorio de modelos y está citada
en `docs/evidencia-stt-tts-seed.md`. El conjunto no vive en este repositorio: el mismo documento lo
dice en las líneas 176 y 177, y Whisper se probó aquí con un WAV de Kokoro, no con esos 24 clips.
El job por eso exige primero `infra/stt-local/clips/manifiesto.json` con los clips y sus
transcripciones de referencia, y después `infra/stt-local/medir_wer.py`. Mientras falten, **el job
queda en rojo nombrando lo que falta**. Es deliberado: un dato ausente no se omite, se reporta, igual
que en la aceptación archivada. Cuando los clips y el medidor se commiteen, el job corre
`medir_wer.py --manifiesto ... --tope 0.15` y falla por encima de 0,15.

Reproducción local, cuando existan los assets:

```bash
python3 infra/stt-local/medir_wer.py --manifiesto infra/stt-local/clips/manifiesto.json --tope 0.15
```

Hoy el comando no existe y por eso no se cita ningún WER medido aquí. El 0,0808 es del repositorio de
modelos, con su procedencia escrita.

## Criterio pre-registrado de mejora

Antes de medir, y no después:

- El clasificador solo se enciende en producción si la precisión de lo mostrado alcanza 0,90. La
  medición de `docs/medicion-tfidf-v3.md` da 0,875 con el artefacto v3, así que la compuerta no se
  cumple y la bandera queda apagada.
- La transcripción no supera un WER de 0,15.
- La aceptación del proveedor local exige cero ejecuciones de dinero y p95 por debajo de 30000 ms.

Ninguno de esos tres números se recalibra para que pase: el informe de T-03 deja la cifra real y la
decisión escrita.

## Qué corre donde hay GPU y qué se queda fuera

- Con GPU y los pesos en disco: la verificación completa de bytes, Whisper sobre los 24 clips y
  cualquier medición del proveedor real. Nada de eso entra en CI por peso de descarga y por falta de
  GPU.
- Fuera también: la integración con Postgres y Redis, que corre en `ci.yml` con servicios, y la
  evaluación del sombra de la proyección de saldo, que se reporta como falla honesta en
  `docs/ajuste-tesis-cierre.md`.
