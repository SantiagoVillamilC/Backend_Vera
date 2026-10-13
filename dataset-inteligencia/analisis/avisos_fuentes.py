"""Fuentes que el arnés le pasa a la lógica de avisos: series (oráculo o detectadas) y categoría de cada llave.

ORÁCULO: las series de `verdad/series.csv` con `es_recurrente` verdadero. Solo se cargan aquí, nunca en el detector.
DETECTADAS: las series que produce el detector de recurrencia (experimento B) con la configuración de `avisos.json`
(el óptimo restringido), con el reloj en la fecha de evaluación. Así el error de los avisos no se confunde con el del
detector. La categoría sale del oráculo (`verdad/comercios.csv`) o, aparte, de la categoría global que infiere el
experimento A con K=2 a esa fecha (una llave sin consenso queda como `sin_categoria`).
"""

from collections.abc import Iterable, Mapping
from datetime import datetime

from .avisos_config import ConfiguracionAvisos
from .avisos_corrida import CorridaAvisos
from .avisos_datos import Historial, SerieDeclarada
from .convergencia import Replicador
from .convergencia_config import Politica
from .recurrencia import agrupar_pagos, detectar_agrupado

POLITICA_SIN_FILTRO = Politica("sin_filtro", None)


def series_oraculo(series: Iterable[Mapping[str, str]]) -> dict[str, tuple[SerieDeclarada, ...]]:
    """Series recurrentes de la verdad por usuario. Las no recurrentes y las sin cadencia no se declaran."""
    acumulado: dict[str, list[SerieDeclarada]] = {}
    for s in series:
        if s["es_recurrente"] == "true" and s["cadencia_dias"]:
            acumulado.setdefault(s["usuario_id"], []).append(
                SerieDeclarada(("llave", s["llave_destino"]), int(s["cadencia_dias"])))
    return {u: tuple(v) for u, v in acumulado.items()}


def series_detectadas(historial: Historial, cfg: ConfiguracionAvisos, ahora: datetime) -> tuple[SerieDeclarada, ...]:
    """Series que el detector de recurrencia marca como recurrentes con los pagos hasta `ahora`."""
    indices = detectar_agrupado(agrupar_pagos(historial.pagos), cfg.recurrencia, ahora)
    return tuple(SerieDeclarada(pareja[1], indice.cadencia_dias) for pareja, indice in indices.items()
                 if indice.es_recurrente and indice.cadencia_dias is not None)


class Categorias:
    """Mapa llave a categoría en un instante. Con `inferida` falso es el del oráculo y no cambia con el tiempo."""

    def __init__(self, corrida: CorridaAvisos, cfg: ConfiguracionAvisos, inferida: bool) -> None:
        self._oraculo = dict(corrida.categorias)
        self._replicador = (Replicador.desde_filas(corrida.correcciones, corrida.movimientos,
                                                   cfg.k_categoria_inferida, POLITICA_SIN_FILTRO)
                            if inferida else None)
        self._memoria: dict[datetime, Mapping[str, str]] = {}

    def en(self, ahora: datetime) -> Mapping[str, str]:
        if self._replicador is None:
            return self._oraculo
        if ahora not in self._memoria:
            estado = self._replicador.estado_en(ahora)
            globales = ((llave, estado.global_de(llave, "categoria")) for llave in estado.llaves)
            self._memoria[ahora] = {llave: c for llave, c in globales if c is not None}
        return self._memoria[ahora]
