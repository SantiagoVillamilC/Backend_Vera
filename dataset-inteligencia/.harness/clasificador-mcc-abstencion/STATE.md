# STATE: clasificador-mcc-abstencion

- Objetivo: Mejorar el clasificador TF-IDF de categoria por nombre de comercio de analisis/clasificador_tfidf.py con cuatro capacidades (usar el MCC, abstenerse por umbral, ampliar el vectorizador y sumar un lexico semilla), sin bajar la exactitud de linea base 0,718 sobre salida/2026-3000-A y sin romper la suite en 629 pruebas.
- Estado: blocked
- Proyecto: /home/chris/Documents/universidad-2026B/proyecto-fin-de-grado/dataset-inteligencia
- Base commit: be7e0a85290ef0cd15b8b15c5bd68177f1581436
- Plan: d8268717ba8c1f59424b7a9106f0ddd1c4c2f1dbc7af193a15ed0916e8bc142d
- Actualizado: 2026-10-12T17:26:38.424Z
- Presupuesto: 2585147/1200000 tokens, 0 USD

## Pasos

| Paso | Estado | Hash | Intentos | Tokens |
|---|---|---|---|---|
| S1 | done | 6ffe44902f22 | 1 | 2585147 |

## Tokens por modelo

| Modelo | Tokens | Costo USD |
|---|---|---|
| opendesign/deepseek-v4.1-flash | 2585147 | 0 |

## Contabilidad

- ACCOUNTING: S1 session.messages

CURRENT: S1
ULTIMO PASO EXITOSO: S1

LIVE: S1 2585147 tokens, 0 USD, 17 mensajes, 2026-10-12T17:26:38.368Z

## Bloqueos

- S1: paso consumio 2585147 tokens, supera su max_tokens (300000); corrida consumio 2585147 tokens, supera budget_total (1200000) (2026-10-12T17:26:38.368Z)

## Siguiente accion

Revisar S1 y reanudar con `harness resume`.
