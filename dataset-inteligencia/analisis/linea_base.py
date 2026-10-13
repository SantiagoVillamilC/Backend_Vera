"""Línea base citable: la regla de práctica del repositorio lyndonkl (recurring-charge-detector).

Una pareja (usuario, identidad) es recurrente si cumple las tres condiciones:

1. Tiene 3 o más ocurrencias.
2. Al menos el 80 % de los intervalos entre pagos consecutivos cae dentro de la banda de la cadencia
   más cercana a la MEDIANA de los intervalos (semanal 7 días más o menos 2, quincenal 14 más o menos 3,
   mensual 30 más o menos 4).
3. El coeficiente de variación del monto (desviación estándar poblacional dividida por la media) es
   0,40 o menor.

Dos precisiones frente a la fuente, documentadas en SUPUESTOS.md: la fuente elige la cadencia por el
"ajuste más estrecho" y aquí se usa la cadencia más cercana a la mediana, y se omiten las cadencias
trimestral, semestral y anual. Los pagos, la identidad, el reloj y las fechas locales son los mismos que
usa el índice. Todo se calcula con enteros y `Fraction`, así que los límites inclusivos son exactos.
"""

import math
from collections.abc import Iterable, Sequence
from dataclasses import dataclass
from datetime import date, datetime
from fractions import Fraction

from .configuracion import exacto
from .pagos import Pago
from .recurrencia import Grupos, Pareja, _cadencia_mas_cercana, _exigir_zona_horaria, _mediana, agrupar_pagos


@dataclass(frozen=True)
class ReglaPractica:
    n_min: int = 3
    fraccion_intervalos: float = 0.8
    cv_max: float = 0.40
    cadencias_dias: tuple[int, ...] = (7, 14, 30)
    bandas_dias: tuple[int, ...] = (2, 3, 4)


REGLA_PRACTICA = ReglaPractica()


@dataclass(frozen=True)
class DecisionBase:
    ocurrencias: int
    intervalos: int
    en_banda: int
    cv: float | None
    cadencia_dias: int | None
    cumple_ocurrencias: bool
    cumple_intervalos: bool
    cumple_cv: bool
    es_recurrente: bool
    r: None = None


def _cumple_cv(montos: Sequence[int], cv_max: Fraction) -> tuple[bool, float]:
    """CV <= cv_max sin raíces: n x suma de cuadrados <= (1 + cv_max al cuadrado) x suma al cuadrado."""
    n, suma, cuadrados = len(montos), sum(montos), sum(m * m for m in montos)
    cv = math.sqrt(n * cuadrados - suma * suma) / suma
    return n * cuadrados <= (1 + cv_max * cv_max) * suma * suma, round(cv, 9)


def decidir_linea_base(fechas: Sequence[date], montos: Sequence[int], regla: ReglaPractica = REGLA_PRACTICA
                       ) -> DecisionBase:
    """Decisión de la regla de práctica para una serie ordenada en el tiempo (fechas locales, centavos)."""
    n = len(fechas)
    if n < regla.n_min:
        return DecisionBase(n, max(n - 1, 0), 0, None, None, False, False, False, False)
    intervalos = [(b - a).days for a, b in zip(fechas, fechas[1:])]
    posicion = _cadencia_mas_cercana(_mediana(intervalos), regla.cadencias_dias)
    cadencia, banda = regla.cadencias_dias[posicion], regla.bandas_dias[posicion]
    en_banda = sum(1 for dias in intervalos if abs(dias - cadencia) <= banda)
    cumple_intervalos = Fraction(en_banda, len(intervalos)) >= exacto(regla.fraccion_intervalos)
    cumple_cv, cv = _cumple_cv(montos, exacto(regla.cv_max))
    return DecisionBase(n, len(intervalos), en_banda, cv, cadencia, True, cumple_intervalos, cumple_cv,
                        cumple_intervalos and cumple_cv)


def detectar_linea_base_agrupado(grupos: Grupos, regla: ReglaPractica, ahora: datetime) -> dict[Pareja, DecisionBase]:
    """Decisión de cada pareja usando solo los pagos con ocurrido_en <= ahora."""
    _exigir_zona_horaria(ahora)
    resultado: dict[Pareja, DecisionBase] = {}
    for pareja, pagos in grupos.items():
        vigentes = [p for p in pagos if p.ocurrido_en <= ahora]
        if vigentes:
            resultado[pareja] = decidir_linea_base([p.fecha_local for p in vigentes],
                                                   [p.monto_centavos for p in vigentes], regla)
    return resultado


def detectar_linea_base(pagos: Iterable[Pago], regla: ReglaPractica, ahora: datetime) -> dict[Pareja, DecisionBase]:
    return detectar_linea_base_agrupado(agrupar_pagos(pagos), regla, ahora)
