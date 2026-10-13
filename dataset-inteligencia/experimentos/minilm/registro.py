"""Metas y registro de resultados del experimento MiniLM, para comparar cada iteracion de mejora.

Metas (resultados deseados), decididas antes de medir:
- M1 (minima): superar al TF-IDF sin MCC (0,763 de exactitud sobre el corte de prueba).
- M2 (ideal): acercarse o superar al TF-IDF de produccion, que usa el MCC (0,949).
- Restriccion de costo: una iteracion se adopta solo si sube la exactitud sin violar el techo de
  costo (disco, memoria y latencia). El techo se fija por debajo de 500 MB de disco, 500 MB de RSS y
  20 ms por nombre, que es lo razonable para un clasificador que corre junto al backend.

El archivo `resultados.csv` acumula una fila por corrida: cada iteracion se puede comparar contra la
anterior sin perder ninguna medicion. `rss_mb` es el pico del kernel (VmHWM). `latencia_ms` es por
nombre en inferencia. En las filas de SetFit, `entrenamiento_s` es el tiempo de GPU y `latencia_ms`
queda vacio porque SetFit no reporta por nombre suelto.
"""

from __future__ import annotations

import csv
from pathlib import Path

ARCHIVO = Path(__file__).resolve().parent / "resultados.csv"

COLUMNAS = [
    "iteracion",
    "clasificador",
    "variante",
    "exactitud",
    "cobertura",
    "exactitud_revela",
    "exactitud_ficticia",
    "exactitud_real",
    "disco_modelo_mb",
    "rss_mb",
    "latencia_ms",
    "entrenamiento_s",
    "nota",
]


def registrar(fila: dict[str, object]) -> None:
    """Anexa una fila al registro. Crea el encabezado si el archivo no existe."""
    nuevo = not ARCHIVO.exists()
    with open(ARCHIVO, "a", newline="", encoding="utf-8") as archivo:
        escritor = csv.DictWriter(archivo, fieldnames=COLUMNAS, extrasaction="ignore")
        if nuevo:
            escritor.writeheader()
        escritor.writerow({clave: fila.get(clave, "") for clave in COLUMNAS})


if __name__ == "__main__":
    with open(ARCHIVO, encoding="utf-8") as archivo:
        for renglon in csv.reader(archivo):
            print("\t".join(renglon))
