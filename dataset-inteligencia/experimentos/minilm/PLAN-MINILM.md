# Plan de configuracion y entrenamiento: MiniLM contra TF-IDF

Fecha: 12 de octubre de 2026. Estado: tres iteraciones medidas y registradas en `resultados.csv`.

## Objetivo

Medir si un sentence-transformer multilingüe (paraphrase-multilingual-MiniLM-L12-v2) mejora al
clasificador TF-IDF casero sobre el residuo sin categoria del experimento A, y a que costo en
recursos. La regla de iteracion: cada mejora de exactitud se compara contra su costo en disco,
memoria y latencia, y todo queda en `resultados.csv`.

## Restricciones

- Solo el modelo multilingüe, para español.
- Mismo corte que `analisis.clasificador_tfidf.medir` y mismo corte `ficticia`/`real` de `verdad/`.
- El entrenamiento del transformer va en la GPU NVIDIA (GTX 1650, 4 GB). Las fases congeladas y la
  inferencia ONNX corren en CPU.
- Se rompe a proposito la regla de "solo biblioteca estándar", en un entorno aislado, por ser
  material de tesis.

## Entorno

- Python 3.14.7, venv aislado: `/home/chris/.local/share/minilm-exp/venv`.
- Congelado: `onnxruntime` 1.31.0, `transformers` 5.19.0, `numpy` 2.5.4.
- Ajuste: `torch` 2.14.1 (CUDA 13), `sentence-transformers` 6.1.0, `setfit` 1.2.0, `peft` 0.21.2.
- Exportacion: `torch.onnx` manual (se descarto `optimum` porque degradaba `transformers`).
- CPU: Ryzen 5 3550H (AVX2, sin AVX512). GPU: GTX 1650 Mobile, 4 GB.

## Datos

- 231 comercios. Etiquetados: 91 de entrenamiento mas 39 de prueba, 130, 8 categorias.
- `mcc_categoria` es un codigo numerico. El TF-IDF lo usa como token `mcc:<codigo>` y sube a 0,95.
- El corpus es sintetico, con marcas inventadas ("D1", "SAN JORGE"), que es la clave del resultado.

## Resultados: corte oficial (39) y validacion cruzada (130)

| Clasificador | Variante | holdout | CV5 |
|---|---|---|---|
| TF-IDF | produccion con MCC | 0,949 | **0,946 ± 0,018** |
| MiniLM SetFit | fine-tune GPU completo | 0,821 | **0,796 ± 0,064** |
| **MiniLM SetFit ONNX int8** | fine-tune exportado, sin torch | **0,821** | - |
| MiniLM SetFit+LoRA | fine-tune GPU con adaptador | 0,769 | 0,723 ± 0,091 |
| TF-IDF | nombre solo | 0,763 | 0,686 ± 0,039 |
| MiniLM congelado | fp32, cabeza | 0,692 | 0,675 ± 0,064 |
| MiniLM congelado | fp32, centroide | 0,538 | 0,507 ± 0,060 |
| MiniLM congelado | int8, cabeza | 0,641 | - |
| MiniLM congelado | int8, centroide | 0,436 | - |
| LLM API | gpt-6-luna (zero-shot) | 0,513 (39) | 0,600 (130) |
| LLM API | deepseek-v4.1-flash (zero-shot) | 0,513 (39) | 0,592 (130) |

## Consumo (cada configuracion en su propio proceso)

| Configuracion | Disco | RSS pico | Latencia | Entrenamiento | VRAM |
|---|---|---|---|---|---|
| TF-IDF | 0,02 MB | **18,5 MB** | 0,85 ms | no aplica | no aplica |
| MiniLM int8 congelado (Rust tok) | 118 MB | 504 MB | 14,9 ms | no aplica | no aplica |
| MiniLM fp32 congelado (Rust tok) | 470 MB | 905 MB | 8,0 ms | no aplica | no aplica |
| MiniLM SetFit (torch) | 488 MB | 2006 MB | 11,1 ms | 222 s | 2176 MB |
| MiniLM SetFit+LoRA (torch) | 471 MB base + 1,18 MB adaptador | ~2000 MB | ~11 ms | 99 s | 541 MB |
| **MiniLM SetFit ONNX int8** | **118 MB** | **500 MB** | 12,0 ms | (heredado) | no usa |

## Hallazgos

1. **MiniLM congelado no se justifica.** Empata o pierde con el TF-IDF nombre solo (CV 0,675 contra
   0,686) a 40 a 90 veces la memoria.
2. **SetFit completo si gana cuando solo hay nombre.** 0,796 de CV contra 0,686 del TF-IDF nombre
   solo, unos 11 puntos.
3. **LoRA cambia exactitud por costo de entrenamiento.** VRAM 2176 a 541 MB y 222 a 99 s, con 0,25 %
   de parametros entrenables, pero la exactitud cae de 0,796 a 0,723. Con 91 ejemplos, el ajuste
   completo rinde mas. LoRA no ahorra disco en despliegue.
4. **Exportar el SetFit a ONNX int8 es la mejor jugada de la iteracion 3.** Reproduce la exactitud
   del SetFit (0,821, sin perder una sola fila) y baja el disco de 488 a 118 MB y el RSS de 2006 a
   500 MB, sin torch. El coseno entre los embeddings del ONNX y los de torch es 1,0. Verificado.
5. **El costo grande del congelado es el import de transformers, no el modelo.** Con el tokenizador
   Rust directo y el import perezoso, el RSS del int8 baja de 1628 a 504 MB sin tocar la exactitud.
6. **Trampa medida: SetFit usa `normalize_embeddings=False`.** Aplicar la cabeza sobre embeddings
   normalizados baja la exactitud de 0,82 a 0,77. La cabeza espera el vector crudo del mean pooling.
7. **El MCC manda.** El TF-IDF con `mcc:<codigo>` saca 0,946 y le gana a todo. Si el MCC esta
   disponible, ningun transformer se justifica.
8. **La causa del resultado es el corpus.** La señal discriminante de un corpus sintetico es el token
   de la marca inventada. El coseno de control lo confirma: "FARMACIA SAN JORGE" se parece mas a
   "COLEGIO SAN JOSE" (0,699) que a "DROGUERIA LA 80" (0,515).
9. **El LLM por API reconoce marcas reales pero no gana en el resto.** gpt-6-luna saca 0,600 y
   deepseek-v4.1-flash 0,592 sobre los 130, por debajo de todos los clasificadores, pero los dos
   aciertan el bloque `real` al **100 %** (17 de 17), donde el TF-IDF nombre solo saca 0,60. Es la
   hipotesis del ítem 5 confirmada a medias: la ventaja del LLM es el conocimiento de marcas, no la
   comprension del nombre. gpt-6-luna es ademas 5 veces mas barato en tokens (217 contra 1199 por
   llamada) y 3 veces mas rapido que deepseek-v4.1-flash.
10. **El clasificador zero-shot es fragil al prompt.** Con `max_tokens=40` los modelos con
   razonamiento dejaron 66 de 130 respuestas vacias (la razon consumio el limite). Con 512 tokens
   quedan en cero. Lo aprendido: a un modelo que razona hay que darle margen de tokens o el contenido
   sale vacio y parece una abstencion.

## Veredicto

- **Si el MCC esta disponible: TF-IDF** (0,946 a 18,5 MB). Ningun transformer se justifica.
- **Si solo hay nombre: SetFit exportado a ONNX int8** (0,821 a 118 MB y 500 MB de RSS). Es el mejor
  equilibrio: la exactitud del fine-tune completo al costo de memoria de un modelo congelado en int8.
- **LoRA: solo si el entrenamiento es la restriccion** y se aceptan 7 puntos menos.
- **MiniLM congelado (sin ajustar): no se adopta.**
- **LLM por API: no reemplaza al clasificador.** 0,60 contra 0,79 del SetFit y 0,95 del TF-IDF con
  MCC. Se justifica solo como respaldo para marcas reales que el modelo local no conoce, donde saca
  1,0, pero eso es el 13 % del corpus (17 de 130 comercios `real`).
- **Decision de alcance (12 de octubre de 2026):** el trabajo se limita al **TF-IDF con MCC** como
  principal y al **SetFit exportado a ONNX int8** como respaldo. Lo demas (MiniLM congelado, LoRA,
  LLM por API) queda medido, documentado y descartado.

## Regla de iteracion

Cada fila nueva de `resultados.csv` se compara contra este cuadro. Una iteracion se adopta solo si
sube la exactitud sin violar el techo de costo. Iteraciones candidatas pendientes: barrido del umbral
de abstención (el SetFit y el ONNX no se abstienen, cobertura 1,0), y medir el LLM por API del item 5.

## Riesgos

- Sobreajuste: 91 ejemplos y 118 M de parametros. La CV del SetFit completo es 0,796 ± 0,064. El ONNX
  hereda ese modelo, asi que su 0,821 de holdout es optimista por venir del mismo corte usado en CV.
- El corpus sintetico favorece la coincidencia de tokens. En nombres reales puede cambiar.

## Como reproducir

```bash
V=/home/chris/.local/share/minilm-exp/venv/bin/python
export MINILM_CACHE=/home/chris/.local/share/minilm-exp/models
"$V" experimentos/minilm/clasificador_minilm.py                  # congelado (CPU) y registra
"$V" experimentos/minilm/ajuste_setfit.py                        # SetFit completo (GPU), cachea
"$V" experimentos/minilm/ajuste_setfit_lora.py                   # SetFit+LoRA (GPU)
"$V" experimentos/minilm/exportar_onnx.py                        # exporta el SetFit a ONNX int8
"$V" experimentos/minilm/evaluar_onnx_setfit.py                  # evalua el ONNX (sin torch)
"$V" experimentos/minilm/evaluar_cv.py                           # CV5 de los congelados
"$V" experimentos/minilm/cv_setfit.py                            # CV5 del SetFit completo (GPU)
LORA=1 "$V" experimentos/minilm/cv_setfit.py                     # CV5 de LoRA (GPU)
MINILM_RUST_TOK=1 "$V" experimentos/minilm/medir_inferencia.py onnx_int8
"$V" experimentos/minilm/registro.py                             # imprime el registro acumulado
```
