"""Agregación de las corridas de convergencia por tasa de corrección y bootstrap pareado de K contra K.

K no se calibra, así que no hay entrenamiento ni validación: se reportan todas las semillas con media, mínimo y
máximo. El bootstrap remuestrea comercios con reemplazo sobre el conjunto de todas las semillas de una tasa y
usa los mismos índices para todos los K (emparejamiento exacto). Reutiliza de `bootstrap.py` los remuestreos, el
rango del percentil y el intervalo, pero no `intervalo_pareado`, que está atado al F1: aquí la medida es el acierto.
"""

import math
from collections import defaultdict
from collections.abc import Sequence
from dataclasses import dataclass

from .bootstrap import Intervalo, generar_remuestreos, indices_percentil
from .convergencia_config import ConfigConvergencia
from .convergencia_evaluacion import ResultadoCorrida
from .convergencia_medidas import FilaComercio, en_ambito

DIMENSIONES = ("nombre", "categoria")
AMBITOS_BOOTSTRAP = ("todos", "prueba", "sin_colusion")


@dataclass(frozen=True)
class FilaAgregada:
    tasa: str
    politica: str
    k: int
    ambito: str
    medida: str
    semillas: int
    media: float | None
    minimo: float | None
    maximo: float | None


@dataclass(frozen=True)
class FilaBootstrap:
    tasa: str
    ambito: str
    dimension: str
    comparacion: str
    n: int
    intervalo: Intervalo


def agregar(resultados: Sequence[ResultadoCorrida]) -> tuple[FilaAgregada, ...]:
    """Media, mínimo y máximo de cada medida entre las corridas de una misma tasa. Los None no cuentan."""
    grupos: dict[tuple, list] = defaultdict(list)
    for r in resultados:
        for m in r.medidas:
            grupos[(r.tasa, m.politica, m.k, m.ambito, m.medida)].append(m.valor)
    filas = []
    for (tasa, politica, k, ambito, medida), valores in sorted(grupos.items()):
        definidos = [v for v in valores if v is not None]
        if definidos:
            fila = FilaAgregada(tasa, politica, k, ambito, medida, len(definidos),
                                math.fsum(definidos) / len(definidos), min(definidos), max(definidos))
        else:
            fila = FilaAgregada(tasa, politica, k, ambito, medida, 0, None, None, None)
        filas.append(fila)
    return tuple(filas)


def _acierto(f: FilaComercio, dimension: str) -> int:
    return int(f.ok_nombre if dimension == "nombre" else f.ok_categoria)


def _vector(corridas: Sequence[ResultadoCorrida], politica: str, k: int, ambito: str, dimension: str) -> list[int]:
    filas = [f for r in corridas for f in r.comercios[(politica, k)] if en_ambito(f, ambito)]
    return [_acierto(f, dimension) for f in filas]


def _intervalo(diferencias: Sequence[int], remuestreos: Sequence[Sequence[int]]) -> Intervalo:
    n = len(diferencias)
    medias = sorted(sum(diferencias[i] for i in r) / n for r in remuestreos)
    bajo, alto = indices_percentil(len(medias))
    return Intervalo(sum(diferencias) / n, math.fsum(medias) / len(medias), medias[bajo], medias[alto])


def _filas_de_un_grupo(tasa: str, corridas: Sequence[ResultadoCorrida], config: ConfigConvergencia, ambito: str,
                       dimension: str, b: int, semilla: int) -> list[FilaBootstrap]:
    vectores = {k: _vector(corridas, config.politica_principal, k, ambito, dimension) for k in config.ks}
    referencia = vectores[config.k_referencia]
    n = len(referencia)
    if n == 0:
        return []
    remuestreos = generar_remuestreos(n, b, semilla, f"{tasa}:{ambito}:{dimension}")
    return [FilaBootstrap(tasa, ambito, dimension, f"K{config.k_referencia} menos K{k}", n,
                          _intervalo([a - o for a, o in zip(referencia, vectores[k])], remuestreos))
            for k in config.ks if k != config.k_referencia]


def diferencias_bootstrap(resultados: Sequence[ResultadoCorrida], config: ConfigConvergencia, b: int,
                          semilla: int) -> list[FilaBootstrap]:
    """Diferencia de acierto total (nombre y categoría) entre el K de referencia y cada otro K, por tasa y ámbito."""
    por_tasa: dict[str, list[ResultadoCorrida]] = defaultdict(list)
    for r in sorted(resultados, key=lambda r: r.nombre):
        por_tasa[r.tasa].append(r)
    filas: list[FilaBootstrap] = []
    for tasa in sorted(por_tasa):
        for ambito in AMBITOS_BOOTSTRAP:
            for dimension in DIMENSIONES:
                filas += _filas_de_un_grupo(tasa, por_tasa[tasa], config, ambito, dimension, b, semilla)
    return filas
