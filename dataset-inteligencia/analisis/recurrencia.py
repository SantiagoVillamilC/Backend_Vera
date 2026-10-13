"""Detector de recurrencia por usuario e identidad. Función pura de (pagos, configuración, ahora).

Índice: R = N x (w_monto x Monto + w_fecha x Fecha)

- N = min(1, ocurrencias / n_min). Con menos de 2 ocurrencias no hay intervalo y R vale 0.
- Monto: fracción de montos dentro de la tolerancia relativa `tol_monto` de la MEDIANA de montos.
- Fecha: se toma la cadencia candidata más cercana a la MEDIANA de los intervalos en días entre pagos
  consecutivos, y Fecha es la fracción de intervalos dentro de la tolerancia de esa cadencia.
- Es recurrente si R es mayor o igual que el umbral.

Todo el cálculo usa `Fraction`, porque en coma flotante 0,6 x 1 + 0,4 x 0,5 no vale 0,8 exacto y un R
que cae justo sobre el umbral (el caso Movistar da 0,4) cambiaría de decisión por un error de redondeo.
Las comparaciones de tolerancia y de umbral son inclusivas. Cuando la mediana de intervalos queda
exactamente a la misma distancia de dos cadencias, gana la cadencia menor.

El reloj es el parámetro `ahora`: solo cuentan los pagos con `ocurrido_en` menor o igual que él. Este
módulo nunca consulta la hora del sistema ni abre archivos.
"""

from collections.abc import Iterable, Mapping, Sequence
from dataclasses import dataclass
from datetime import date, datetime
from fractions import Fraction
from functools import lru_cache

from .configuracion import Configuracion, exacto
from .pagos import Identidad, Pago

DECIMALES_R = 9
Pareja = tuple[str, Identidad]
Grupos = Mapping[Pareja, tuple[Pago, ...]]


@dataclass(frozen=True)
class Indice:
    """Resultado del detector para una pareja (usuario, identidad) en un instante."""

    ocurrencias: int
    n: float
    monto: float
    fecha: float
    r: float
    es_recurrente: bool
    cadencia_dias: int | None


@dataclass(frozen=True)
class _Exactos:
    w_monto: Fraction
    w_fecha: Fraction
    tol_monto: Fraction
    cadencias: tuple[int, ...]
    tolerancias: tuple[Fraction, ...]
    n_min: Fraction
    umbral: Fraction


@lru_cache(maxsize=None)
def _exactos(cfg: Configuracion) -> _Exactos:
    return _Exactos(exacto(cfg.w_monto), exacto(cfg.w_fecha), exacto(cfg.tol_monto), cfg.cadencias_dias,
                    tuple(exacto(t) for t in cfg.tolerancias_dias), Fraction(cfg.n_min), exacto(cfg.umbral))


def _mediana(valores: Sequence[int | Fraction]) -> Fraction:
    ordenados = sorted(valores)
    medio, resto = divmod(len(ordenados), 2)
    if resto:
        return Fraction(ordenados[medio])
    return (Fraction(ordenados[medio - 1]) + Fraction(ordenados[medio])) / 2


def _fraccion_de_montos(montos: Sequence[int], tolerancia: Fraction) -> Fraction:
    mediana = _mediana(montos)
    dentro = sum(1 for m in montos if abs(m - mediana) <= tolerancia * mediana)
    return Fraction(dentro, len(montos))


def _cadencia_mas_cercana(mediana: Fraction, cadencias: Sequence[int]) -> int:
    """Posición de la cadencia más cercana a la mediana. En un empate gana la cadencia menor."""
    return min(range(len(cadencias)), key=lambda i: (abs(mediana - cadencias[i]), cadencias[i]))


def _fraccion_de_fechas(intervalos: Sequence[int], exactos: _Exactos) -> tuple[Fraction, int]:
    posicion = _cadencia_mas_cercana(_mediana(intervalos), exactos.cadencias)
    cadencia, tolerancia = exactos.cadencias[posicion], exactos.tolerancias[posicion]
    dentro = sum(1 for dias in intervalos if abs(dias - cadencia) <= tolerancia)
    return Fraction(dentro, len(intervalos)), cadencia


def _sin_intervalo(ocurrencias: int, exactos: _Exactos) -> Indice:
    n = min(Fraction(1), Fraction(ocurrencias) / exactos.n_min)
    return Indice(ocurrencias, float(n), 0.0, 0.0, 0.0, False, None)


def calcular_indice(fechas: Sequence[date], montos: Sequence[int], cfg: Configuracion) -> Indice:
    """Índice R de una serie. `fechas` (fechas locales) y `montos` (centavos) vienen ordenados en el tiempo."""
    exactos = _exactos(cfg)
    ocurrencias = len(fechas)
    if ocurrencias < 2:
        return _sin_intervalo(ocurrencias, exactos)
    n = min(Fraction(1), Fraction(ocurrencias) / exactos.n_min)
    monto = _fraccion_de_montos(montos, exactos.tol_monto)
    intervalos = [(b - a).days for a, b in zip(fechas, fechas[1:])]
    fecha, cadencia = _fraccion_de_fechas(intervalos, exactos)
    r = n * (exactos.w_monto * monto + exactos.w_fecha * fecha)
    return Indice(ocurrencias, float(n), float(monto), float(fecha), round(float(r), DECIMALES_R),
                  r >= exactos.umbral, cadencia)


def agrupar_pagos(pagos: Iterable[Pago]) -> dict[Pareja, tuple[Pago, ...]]:
    """Pagos por pareja (usuario, identidad), cada grupo en orden cronológico y las parejas en orden fijo."""
    acumulado: dict[Pareja, list[Pago]] = {}
    for pago in sorted(pagos, key=lambda p: (p.ocurrido_en, p.movimiento_id)):
        acumulado.setdefault((pago.usuario_id, pago.identidad), []).append(pago)
    return {pareja: tuple(acumulado[pareja]) for pareja in sorted(acumulado)}


def _exigir_zona_horaria(ahora: datetime) -> None:
    if ahora.tzinfo is None or ahora.utcoffset() is None:
        raise ValueError("ahora debe llevar zona horaria (por ejemplo UTC): el reloj se inyecta, no se adivina")


def detectar_agrupado(grupos: Grupos, cfg: Configuracion, ahora: datetime) -> dict[Pareja, Indice]:
    """Índice de cada pareja usando solo los pagos con ocurrido_en <= ahora. Parejas sin pagos no aparecen."""
    _exigir_zona_horaria(ahora)
    resultado: dict[Pareja, Indice] = {}
    for pareja, pagos in grupos.items():
        vigentes = [p for p in pagos if p.ocurrido_en <= ahora]
        if vigentes:
            resultado[pareja] = calcular_indice([p.fecha_local for p in vigentes],
                                                [p.monto_centavos for p in vigentes], cfg)
    return resultado


def detectar(pagos: Iterable[Pago], cfg: Configuracion, ahora: datetime) -> dict[Pareja, Indice]:
    """Detector completo: agrupa los pagos y decide cada pareja en el instante `ahora`."""
    return detectar_agrupado(agrupar_pagos(pagos), cfg, ahora)
