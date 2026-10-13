# dataset-inteligencia

Generador de datasets sintéticos para evaluar el aprendizaje colectivo de comercios (K=2) y los
avisos de hábitos de consumo. Solo biblioteca estándar de Python 3.14.

```bash
python3 generar.py --filas 3000 --variante A      # salida en salida/2026-3000-A/
python3 generar.py --filas 3000 --variante B --semilla 7 --salida /ruta/de/salida
python3 -m unittest                               # ejecutar las pruebas
```

La especificación completa de columnas, valores, fuentes y supuestos está en `ESPECIFICACION.md`.
Cada corrida escribe `manifiesto.json`, `entrada/` (lo que ve el sistema) y `verdad/` (para evaluar).
La carpeta `salida/` está ignorada por git.
