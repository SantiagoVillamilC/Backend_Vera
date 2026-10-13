"""Bootstrap pareado de errores visibles y cobertura entre el K de referencia y cada otro K, sin la población de colusión.

Reutiliza la maquinaria de `convergencia_agregado.py` (el intervalo) y de `bootstrap.py` (los remuestreos). La unidad
es el comercio y se juntan las cuatro semillas de cada tasa, igual que el bootstrap del acierto. Los remuestreos usan la
misma semilla y la misma clave `<tasa>:<ámbito>:<dimensión>` que aquel, así que en el ámbito `sin_colusion` los índices
coinciden con los del bootstrap existente. Los índices son los mismos para todos los K (emparejamiento exacto).
Cada indicador vale 0 o 1 por comercio: `cobertura` si tiene valor global y `errores_visibles` si lo tiene y es
distinto del vigente verdadero, de modo que la abstención no cuenta como error.
"""

from collections import defaultdict
from collections.abc import Sequence
from dataclasses import dataclass

from .bootstrap import Intervalo, generar_remuestreos
from .convergencia_agregado import DIMENSIONES, _intervalo
from .convergencia_config import ConfigConvergencia
from .convergencia_evaluacion import ResultadoCorrida
from .convergencia_medidas import FilaComercio, en_ambito

AMBITOS_RIESGO = ("sin_colusion", "prueba_sin_colusion")
MEDIDAS_RIESGO = ("errores_visibles", "cobertura")


@dataclass(frozen=True)
class FilaBootstrapRiesgo:
    tasa: str
    ambito: str
    medida: str
    dimension: str
    comparacion: str
    n: int
    intervalo: Intervalo


def _indicador(f: FilaComercio, medida: str, dimension: str) -> int:
    consolidado = (f.nombre_global if dimension == "nombre" else f.categoria_global) is not None
    acierto = f.ok_nombre if dimension == "nombre" else f.ok_categoria
    return int(consolidado) if medida == "cobertura" else int(consolidado and not acierto)


def _filas_por_k(corridas: Sequence[ResultadoCorrida], politica: str, ks: Sequence[int], ambito: str
                 ) -> dict[int, list[FilaComercio]]:
    return {k: [f for r in corridas for f in r.comercios[(politica, k)] if en_ambito(f, ambito)] for k in ks}


def _filas_de_un_grupo(tasa: str, corridas: Sequence[ResultadoCorrida], config: ConfigConvergencia, ambito: str,
                       dimension: str, b: int, semilla: int) -> list[FilaBootstrapRiesgo]:
    por_k = _filas_por_k(corridas, config.politica_principal, config.ks, ambito)
    n = len(por_k[config.k_referencia])
    if n == 0:
        return []
    remuestreos = generar_remuestreos(n, b, semilla, f"{tasa}:{ambito}:{dimension}")
    filas = []
    for medida in MEDIDAS_RIESGO:
        vectores = {k: [_indicador(f, medida, dimension) for f in por_k[k]] for k in config.ks}
        referencia = vectores[config.k_referencia]
        filas += [FilaBootstrapRiesgo(tasa, ambito, medida, dimension, f"K{config.k_referencia} menos K{k}", n,
                                      _intervalo([a - o for a, o in zip(referencia, vectores[k])], remuestreos))
                  for k in config.ks if k != config.k_referencia]
    return filas


def bootstrap_visibles(resultados: Sequence[ResultadoCorrida], config: ConfigConvergencia, b: int,
                       semilla: int) -> list[FilaBootstrapRiesgo]:
    """Diferencias de errores visibles y de cobertura (nombre y categoría) en los ámbitos sin colusión, por tasa."""
    por_tasa: dict[str, list[ResultadoCorrida]] = defaultdict(list)
    for r in sorted(resultados, key=lambda r: r.nombre):
        por_tasa[r.tasa].append(r)
    filas: list[FilaBootstrapRiesgo] = []
    for tasa in sorted(por_tasa):
        for ambito in AMBITOS_RIESGO:
            for dimension in DIMENSIONES:
                filas += _filas_de_un_grupo(tasa, por_tasa[tasa], config, ambito, dimension, b, semilla)
    return filas
