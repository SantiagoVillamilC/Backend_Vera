# MiniLM contra TF-IDF: el congelado no gana, el SetFit exportado a ONNX int8 si, y el tokenizador cuesta mas que el modelo

**Fecha:** 12 de octubre de 2026
**Tarjeta:** (tesis, inteligencia)
**Archivos que toca esta sesión:** `dataset-inteligencia/experimentos/minilm/` (todo el experimento, con `PLAN-MINILM.md` y `resultados.csv` como fuente), y `dataset-inteligencia/analisis/clasificador_tfidf.py` (solo como comparacion). Esta entrada se registra en la bitácora.

## 1. Síntoma

El ítem 5 de `PLAN-ANALISIS.md` pide comparar el clasificador TF-IDF casero contra un sentence-transformer multilingüe (MiniLM) y contra un LLM por API, porque un modelo semantico podria reconocer marcas reales que el TF-IDF no. La pregunta era si MiniLM mejora al TF-IDF y a que costo. Un primer numero del TF-IDF estaba en 0,718, y despues de las cuatro mejoras de la bitácora anterior subio a 0,949 (con el token MCC). Faltaba medir el transformer.

## 2. Diagnóstico

**Protocolo.** El corte de prueba oficial tiene solo 39 comercios, asi que ademas se midio una validacion cruzada estratificada de 5 particiones sobre los 130 etiquetados (91 de entrenamiento mas 39 de prueba), con semilla 42. Cada metodo corre en su propio proceso para medir el RSS pico del kernel (VmHWM), porque `ru_maxrss` no es confiable en este entorno.

**Entorno.** Python 3.14.7, venv aislado en `~/.local/share/minilm-exp/venv`. Congelado: onnxruntime 1.31.0, transformers 5.19.0. Ajuste: torch 2.14.1 con CUDA 13, sentence-transformers 6.1.0, setfit 1.2.0, peft 0.21.2. CPU Ryzen 5 3550H (AVX2, sin AVX512, por eso la variante int8 es `model_quint8_avx2`). GPU GTX 1650 Mobile, 4 GB. El entrenamiento fue siempre en la GPU.

**Tres fases.** Fase 1: embeddings congelados con centroide, el mismo metodo del TF-IDF. Fase 2: una cabeza de regresion logistica sobre los embeddings congelados, que es la linea base profesional. Fase 3: ajuste del transformer con SetFit (contrastivo mas cabeza) y, despues, con LoRA.

**Tres iteraciones de mejora**, cada una comparada contra su costo y registrada en `resultados.csv`.

## 3. Causa

El corpus es sintetico y trae marcas inventadas ("D1", "SAN JORGE"). La señal discriminante de esas marcas es el token literal, no el significado, y eso favorece la coincidencia de tokens. La prueba es el coseno de control: "FARMACIA SAN JORGE" se parece mas a "COLEGIO SAN JOSE" (0,699) que a "DROGUERIA LA 80" (0,515), porque el modelo se pega a la estructura "SAN JOxx". Por eso el TF-IDF gana sobre el congelado y sobre el ajustado cuando solo mira el nombre.

## 4. Decisión

Fijar metas antes de medir (M1 superar al TF-IDF nombre solo, M2 acercarse al de produccion con MCC, techo de costo de 500 MB de disco, 500 MB de RSS y 20 ms por nombre). Medir las tres fases. Iterar sobre el costo: tokenizador Rust directo para bajar memoria, LoRA para bajar el entrenamiento, y exportacion a ONNX int8 para bajar el costo de despliegue. Todo queda en `resultados.csv` y en `PLAN-MINILM.md`.

## 5. Evidencia

### Exactitud (corte oficial de 39 y validacion cruzada de 130)

| Clasificador | Variante | holdout | CV5 |
|---|---|---|---|
| TF-IDF | produccion con MCC | 0,949 | 0,946 ± 0,018 |
| MiniLM SetFit | fine-tune GPU completo | 0,821 | 0,796 ± 0,064 |
| MiniLM SetFit ONNX | fine-tune exportado a int8 | 0,821 | - |
| MiniLM SetFit+LoRA | fine-tune con adaptador | 0,769 | 0,723 ± 0,091 |
| TF-IDF | nombre solo | 0,763 | 0,686 ± 0,039 |
| MiniLM congelado | fp32, cabeza | 0,692 | 0,675 ± 0,064 |
| MiniLM congelado | fp32, centroide | 0,538 | 0,507 ± 0,060 |
| MiniLM congelado | int8, cabeza | 0,641 | - |
| MiniLM congelado | int8, centroide | 0,436 | - |

### Costo (cada configuracion en su propio proceso)

| Configuracion | Disco | RSS pico | Latencia | Entrenamiento | VRAM |
|---|---|---|---|---|---|
| TF-IDF | 0,02 MB | 18,5 MB | 0,85 ms | no aplica | no aplica |
| MiniLM int8 congelado (Rust tok) | 118 MB | 504 MB | 14,9 ms | no aplica | no aplica |
| MiniLM SetFit (torch) | 488 MB | 2006 MB | 11,1 ms | 222 s | 2176 MB |
| MiniLM SetFit+LoRA (torch) | 471 MB + 1,18 MB adaptador | ~2000 MB | ~11 ms | 99 s | 541 MB |
| MiniLM SetFit ONNX int8 | 118 MB | 500 MB | 12,0 ms | (heredado) | no usa |

### Hallazgos medidos

1. **MiniLM congelado no se justifica.** Empata o pierde con el TF-IDF nombre solo (CV 0,675 contra 0,686) a 40 a 90 veces la memoria. La cabeza entrenada sube de 0,44 a 0,69, o sea que los embeddings llevan señal, pero no alcanza.
2. **SetFit completo si gana cuando solo hay nombre.** 0,796 de CV contra 0,686, unos 11 puntos.
3. **LoRA cambia exactitud por costo de entrenamiento.** VRAM 2176 a 541 MB y 222 a 99 s con 0,25 % de parametros entrenables, pero la exactitud cae de 0,796 a 0,723. Con 91 ejemplos el ajuste completo rinde mas. LoRA no ahorra disco en despliegue.
4. **Exportar el SetFit a ONNX int8 es la mejor jugada.** Reproduce la exactitud del SetFit (0,821, sin perder una fila) y baja el disco de 488 a 118 MB y el RSS de 2006 a 500 MB, sin torch. El coseno entre los embeddings del ONNX y los de torch es 1,0.
5. **El costo grande del congelado es el import de transformers, no el modelo.** Cargar su modulo de tokenizacion cuesta unos 800 MB. Con el `tokenizers.Tokenizer` Rust directo y el import perezoso, el RSS del int8 baja de 1628 a 504 MB sin tocar la exactitud.
6. **Trampa medida: SetFit usa `normalize_embeddings=False`.** Aplicar la cabeza sobre embeddings normalizados baja la exactitud de 0,82 a 0,77.
7. **El MCC manda.** El TF-IDF con el token `mcc:<codigo>` saca 0,946. Si el MCC esta disponible (el QR Bre-B lo trae), ningun transformer se justifica.
8. **Trampa medida: la cabeza de SetFit predice indices enteros, no categorias.** Al exportar, guardar `classes_` (los indices 0 a 7) en vez de las etiquetas del modelo hizo que toda la evaluacion del ONNX diera 0,0. Se corrige mapeando cada indice a `model.labels`.

### El LLM por API (item 5, tercera pata)

Clasificacion zero-shot por el endpoint compatible OpenAI del gateway opendesign, sobre los 130
etiquetados. Sin entrenamiento y sin GPU.

| Modelo | exactitud (130) | exactitud (39) | ficticia | real (n=17) | tokens/llamada | latencia |
|---|---|---|---|---|---|---|
| gpt-6-luna | 0,600 | 0,513 | 0,540 | **1,000** | 217 | 2,7 s |
| deepseek-v4.1-flash | 0,592 | 0,513 | 0,531 | **1,000** | 1199 | 7,9 s |

Los dos reconocen marcas reales colombianas ("Exito", "Olimpica") y aciertan el bloque `real` al
100 %, donde el TF-IDF nombre solo saca 0,60. Pero en el resto del corpus quedan por debajo de todos
los clasificadores locales (0,60 contra 0,79 del SetFit y 0,95 del TF-IDF con MCC). La ventaja del
LLM es el conocimiento de marcas, no la comprensión del nombre, que es lo que la hipotesis del ítem 5
anticipaba. Trampa medida: con `max_tokens=40` los modelos con razonamiento dejaron 66 de 130
respuestas vacias, porque la razon consumio el limite. Con 512 tokens quedan en cero.

### Alternativas descartadas

- **claude-haiku-5.5.** Clasifico "ALMACENES EXITO" como compras, siendo alimentos_mercado, y corto el JSON en una prueba rapida. Descartado para este uso.
- **deepseek-v4-pro.** Acerto 5 de 5 en la prueba rapida, pero con razonamiento cuesta 1305 tokens y 12 s por llamada, contra 217 tokens y 2,7 s de gpt-6-luna. Descartado por costo.
- **El parametro `temperature`.** gpt-6-luna y claude-haiku-5.5 lo rechazan ("not supported with this model"). Hay que omitirlo en la peticion.
- **`optimum` para exportar a ONNX.** Degradaba `transformers` de 5.19 a 4.57 y rompia `datasets`. La exportacion se hizo a mano con `torch.onnx` y `quantize_dynamic`.

### Veredicto

- **Con MCC: TF-IDF** (0,946 a 18,5 MB).
- **Sin MCC: SetFit exportado a ONNX int8** (0,821 a 118 MB y 500 MB de RSS), el mejor equilibrio entre exactitud y costo.
- **LoRA: solo si el entrenamiento es la restriccion.** Congelado sin ajustar: no.
- **LLM por API: no reemplaza al clasificador** (0,60 contra 0,79 y 0,95). Solo sirve de respaldo para marcas reales que el modelo local no conoce, donde saca 1,0, y eso es el 13 % del corpus.
- **Decision de arquitectura (12 de octubre de 2026):** el clasificador del producto se queda con el **TF-IDF con MCC** como principal y el **SetFit exportado a ONNX int8** como respaldo cuando el MCC no este. Todo lo demas (MiniLM congelado, LoRA, LLM por API) queda fuera del alcance.

## 6. Qué queda abierto

- **El LLM por API ya se midio** (gpt-6-luna 0,600 y deepseek-v4.1-flash 0,592 sobre los 130). Queda abierto que el bloque `real` tiene solo 17 comercios y que falta verificar cuantos de los 130 son marcas colombianas de verdad, porque de eso depende que la ventaja medida en `real` sea robusta.
- **El 0,821 del ONNX es optimista.** Hereda el mismo corte de prueba que uso el SetFit completo. La estimacion robusta es la CV del SetFit completo, 0,796 ± 0,064.
- **No se barrio el umbral de abstención.** El SetFit y el ONNX no se abstienen (cobertura 1,0). Un umbral podria subir la precision a costa de cobertura.
- **`optimum` se descarto.** Degradaba `transformers` de 5.19 a 4.57 y rompia `datasets`. La exportacion se hizo a mano con `torch.onnx` y `quantize_dynamic`.
- **El corpus sintetico limita la extrapolacion.** En nombres reales de comercios colombianos el resultado puede cambiar, y por eso el corte `ficticia` se reporta aparte.
- **Nada esta versionado.** `dataset-inteligencia/` sigue untracked.
