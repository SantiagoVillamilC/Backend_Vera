"""Carga de una corrida para evaluar los avisos: movimientos, correcciones, etiquetas, comercios y series.

Aquí sí se carga la verdad (etiquetas esperadas, categoría de cada comercio y series de recurrencia). Este módulo es
del arnés de evaluación y no forma parte de la lógica de avisos, que nunca lo importa.
"""

from dataclasses import dataclass
from datetime import date
from pathlib import Path

from .cargar import cargar_entrada, cargar_manifiesto, cargar_verdad, sha256_de

TABLAS = ("avisos_esperados", "comercios", "series")
ARCHIVOS_HASHEADOS = ("entrada/correcciones.csv", "entrada/movimientos.csv", "manifiesto.json",
                      "verdad/avisos_esperados.csv", "verdad/comercios.csv", "verdad/series.csv")


@dataclass(frozen=True)
class Etiqueta:
    """Una fila de avisos_esperados.csv. `fila` es su posición (desde 1) en el archivo."""

    fila: int
    usuario_id: str
    tipo: str
    fecha_evaluacion: date
    esperado_mediana: bool
    esperado_media: bool
    detalle: str

    def esperado(self, linea_base: str) -> bool:
        return self.esperado_mediana if linea_base == "mediana" else self.esperado_media


@dataclass(frozen=True)
class CorridaAvisos:
    nombre: str
    semilla: int
    fecha_fin: date
    movimientos: tuple[dict, ...]
    correcciones: tuple[dict, ...]
    categorias: dict[str, str]
    series: tuple[dict, ...]
    etiquetas: tuple[Etiqueta, ...]
    hashes: dict[str, str]


def _etiqueta(posicion: int, f: dict) -> Etiqueta:
    return Etiqueta(posicion, f["usuario_id"], f["tipo"], date.fromisoformat(f["fecha_evaluacion"]),
                    f["debe_disparar_con_mediana"] == "true", f["debe_disparar_con_media"] == "true", f["detalle"])


def cargar_corrida_avisos(ruta: Path | str) -> CorridaAvisos:
    """Carga una corrida una sola vez. El nombre es `carpeta padre/carpeta`, para distinguir corridas homónimas."""
    ruta = Path(ruta)
    manifiesto = cargar_manifiesto(ruta)
    entrada = cargar_entrada(ruta)
    verdad = cargar_verdad(ruta, TABLAS).tablas
    parametros = manifiesto["parametros"]
    etiquetas = tuple(_etiqueta(i, f) for i, f in enumerate(verdad["avisos_esperados"], start=1))
    categorias = {c["llave_destino"]: c["categoria"] for c in verdad["comercios"]}
    return CorridaAvisos(f"{ruta.parent.name}/{ruta.name}", int(parametros["semilla"]),
                         date.fromisoformat(parametros["fecha_fin"]), entrada.movimientos, entrada.correcciones,
                         categorias, verdad["series"], etiquetas, sha256_de(ruta, ARCHIVOS_HASHEADOS))
