"""Cargadores de una corrida del generador, separados a propósito.

`cargar_entrada` lee solo la carpeta de entrada, que es lo que el sistema vería. `cargar_verdad` lee
solo la carpeta de verdad, que únicamente sirve para evaluar. Un detector recibe filas ya cargadas y
nunca importa `cargar_verdad` (lo comprueba una prueba estática sobre el código de los módulos).
`cargar_manifiesto` lee los parámetros de la corrida (por ejemplo la fecha final), que no son verdad.
"""

import csv
import hashlib
import json
from dataclasses import dataclass
from pathlib import Path

TABLAS_DE_VERDAD_POR_DEFECTO = ("series",)


@dataclass(frozen=True)
class Entrada:
    movimientos: tuple[dict, ...]
    correcciones: tuple[dict, ...]


@dataclass(frozen=True)
class Verdad:
    tablas: dict[str, tuple[dict, ...]]

    @property
    def series(self) -> tuple[dict, ...]:
        return self.tablas["series"]


def _leer_csv(ruta: Path) -> tuple[dict, ...]:
    if not ruta.is_file():
        raise FileNotFoundError(f"No existe el archivo {ruta}. Se esperaba una corrida del generador.")
    with open(ruta, newline="", encoding="utf-8") as f:
        return tuple(csv.DictReader(f))


def cargar_entrada(ruta_corrida: Path | str) -> Entrada:
    base = Path(ruta_corrida) / "entrada"
    return Entrada(_leer_csv(base / "movimientos.csv"), _leer_csv(base / "correcciones.csv"))


def cargar_verdad(ruta_corrida: Path | str, tablas: tuple[str, ...] = TABLAS_DE_VERDAD_POR_DEFECTO) -> Verdad:
    base = Path(ruta_corrida) / "verdad"
    return Verdad({nombre: _leer_csv(base / f"{nombre}.csv") for nombre in tablas})


def cargar_manifiesto(ruta_corrida: Path | str) -> dict:
    ruta = Path(ruta_corrida) / "manifiesto.json"
    if not ruta.is_file():
        raise FileNotFoundError(f"No existe el archivo {ruta}. Se esperaba una corrida del generador.")
    return json.loads(ruta.read_text(encoding="utf-8"))


def sha256_de(ruta_corrida: Path | str, relativos: tuple[str, ...]) -> dict[str, str]:
    """sha256 de cada archivo de la corrida, indexado por su ruta relativa."""
    return {rel: hashlib.sha256((Path(ruta_corrida) / rel).read_bytes()).hexdigest() for rel in relativos}
