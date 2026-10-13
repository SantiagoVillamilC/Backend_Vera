"""Evaluación del detector de recurrencia contra verdad/series.csv.

Universo primario: las parejas (usuario, llave) que aparecen en series.csv, con `ahora` igual al final
de la corrida (el fin del día de `fecha_fin`). Métrica secundaria: tasa de falsos positivos sobre las
parejas con 2 o más pagos que NO están en series.csv, tratadas como no recurrentes. Esa es una
SUPOSICIÓN: series.csv no es exhaustivo (supuesto 18 de la especificación), así que una pareja ausente
podría ser recurrente de verdad y la tasa sería una cota superior.

Aquí sí se carga la verdad. Este módulo es el evaluador y no forma parte del detector.
"""

import statistics
from collections.abc import Callable, Mapping
from dataclasses import dataclass
from datetime import date, datetime, time, timezone
from fractions import Fraction
from pathlib import Path

from .cargar import cargar_entrada, cargar_manifiesto, cargar_verdad, sha256_de
from .configuracion import Configuracion
from .pagos import Pago, extraer_pagos
from .recurrencia import Grupos, Pareja, agrupar_pagos, detectar_agrupado

ARCHIVOS_HASHEADOS = ("entrada/correcciones.csv", "entrada/movimientos.csv", "manifiesto.json",
                      "verdad/series.csv")
FIN_DEL_DIA = time(23, 59, 59)


def _razon(numerador: int, denominador: int) -> float | None:
    return numerador / denominador if denominador else None


@dataclass(frozen=True)
class Matriz:
    """Matriz de confusión. Las razones indefinidas (denominador 0) valen None y se reportan como n/a."""

    tp: int
    fp: int
    tn: int
    fn: int

    @property
    def precision(self) -> float | None:
        return _razon(self.tp, self.tp + self.fp)

    @property
    def recall(self) -> float | None:
        return _razon(self.tp, self.tp + self.fn)

    @property
    def f1(self) -> float:
        """F1 = 2TP / (2TP + FP + FN). Vale 0 si no hay positivos reales ni predichos."""
        return float(self.f1_exacto)

    @property
    def f1_exacto(self) -> Fraction:
        denominador = 2 * self.tp + self.fp + self.fn
        return Fraction(2 * self.tp, denominador) if denominador else Fraction(0)

    @property
    def tasa_falsos_positivos(self) -> float | None:
        return _razon(self.fp, self.fp + self.tn)

    def sumar(self, predicho: bool, real: bool) -> "Matriz":
        """Matriz nueva con una decisión más contada."""
        return Matriz(self.tp + (predicho and real), self.fp + (predicho and not real),
                      self.tn + (not predicho and not real), self.fn + (not predicho and real))


@dataclass(frozen=True)
class Secundaria:
    parejas: int
    falsos_positivos: int

    @property
    def tasa(self) -> float | None:
        return _razon(self.falsos_positivos, self.parejas)


@dataclass(frozen=True)
class DecisionSerie:
    serie_id: str
    usuario_id: str
    llave_destino: str
    es_recurrente: bool
    motivo: str
    detalle: str
    ocurrencias: int
    r: float | None
    predicho: bool


@dataclass(frozen=True)
class Evaluacion:
    global_: Matriz
    por_motivo: dict[str, Matriz]
    secundaria: Secundaria
    por_serie: tuple[DecisionSerie, ...]
    pares_sin_pagos: int


@dataclass(frozen=True)
class CorridaPreparada:
    nombre: str
    semilla: int
    ahora: datetime
    grupos: Grupos
    series: tuple[dict, ...]
    hashes: dict[str, str]


def ahora_de_corrida(manifiesto: Mapping) -> datetime:
    """Final del día `fecha_fin` en UTC. Con medianoche se perdería el último día de datos."""
    fin = date.fromisoformat(manifiesto["parametros"]["fecha_fin"])
    return datetime.combine(fin, FIN_DEL_DIA, tzinfo=timezone.utc)


def _pareja_de(serie: Mapping[str, str]) -> Pareja:
    return serie["usuario_id"], ("llave", serie["llave_destino"])


def _podar(grupos: Mapping[Pareja, tuple[Pago, ...]], parejas_de_series: frozenset[Pareja]) -> dict:
    """Quita las parejas de un solo pago que no están en series.csv: no pueden ser recurrentes ni contar."""
    return {p: g for p, g in grupos.items() if len(g) >= 2 or p in parejas_de_series}


def preparar_corrida(ruta: Path | str) -> CorridaPreparada:
    """Carga una corrida una sola vez: pagos agrupados, series de verdad, reloj de evaluación y hashes."""
    ruta = Path(ruta)
    manifiesto = cargar_manifiesto(ruta)
    series = cargar_verdad(ruta).series
    grupos = agrupar_pagos(extraer_pagos(cargar_entrada(ruta).movimientos))
    podados = _podar(grupos, frozenset(_pareja_de(s) for s in series))
    return CorridaPreparada(ruta.name, int(manifiesto["parametros"]["semilla"]), ahora_de_corrida(manifiesto),
                            podados, tuple(series), sha256_de(ruta, ARCHIVOS_HASHEADOS))


def _decision(serie: Mapping[str, str], indices: Mapping) -> DecisionSerie:
    indice = indices.get(_pareja_de(serie))
    ocurrencias, r, predicho = (indice.ocurrencias, indice.r, indice.es_recurrente) if indice else (0, 0.0, False)
    return DecisionSerie(serie["serie_id"], serie["usuario_id"], serie["llave_destino"],
                         serie["es_recurrente"] == "true", serie["motivo"], serie["detalle"],
                         ocurrencias, r, predicho)


def _secundaria(indices: Mapping, parejas_de_series: frozenset[Pareja]) -> Secundaria:
    fuera = [i for pareja, i in indices.items() if pareja not in parejas_de_series and i.ocurrencias >= 2]
    return Secundaria(len(fuera), sum(1 for i in fuera if i.es_recurrente))


def evaluar(corrida: CorridaPreparada, cfg: Configuracion) -> Evaluacion:
    """Decisiones del índice con `ahora` igual al fin de la corrida, comparadas con series.csv."""
    return evaluar_con(corrida, lambda grupos, ahora: detectar_agrupado(grupos, cfg, ahora))


def evaluar_con(corrida: CorridaPreparada, decidir: Callable[[Grupos, datetime], Mapping]) -> Evaluacion:
    """Evalúa cualquier detector `decidir(grupos, ahora)` (índice, línea base) contra series.csv."""
    indices = decidir(corrida.grupos, corrida.ahora)
    decisiones = tuple(_decision(s, indices) for s in corrida.series)
    global_ = Matriz(0, 0, 0, 0)
    por_motivo: dict[str, Matriz] = {}
    for d in decisiones:
        global_ = global_.sumar(d.predicho, d.es_recurrente)
        por_motivo[d.motivo] = por_motivo.get(d.motivo, Matriz(0, 0, 0, 0)).sumar(d.predicho, d.es_recurrente)
    parejas = frozenset(_pareja_de(s) for s in corrida.series)
    sin_pagos = sum(1 for s in corrida.series if _pareja_de(s) not in indices)
    return Evaluacion(global_, dict(sorted(por_motivo.items())), _secundaria(indices, parejas), decisiones, sin_pagos)


@dataclass(frozen=True)
class SerieNecesaria:
    serie_id: str
    usuario_id: str
    llave_destino: str
    motivo: str
    pagos: int
    necesarias: int | None


@dataclass(frozen=True)
class ResumenMotivo:
    total: int
    detectadas: int
    no_detectadas: int
    mediana: float | None


@dataclass(frozen=True)
class ResumenOcurrencias:
    por_serie: tuple[SerieNecesaria, ...]
    total: int
    detectadas: int
    no_detectadas: int
    mediana: float | None
    por_motivo: dict[str, ResumenMotivo]


def _sostenida(decisiones: list[bool]) -> int | None:
    """Menor k tal que la serie queda detectada con k pagos y sigue detectada hasta el último pago."""
    if not decisiones or not decisiones[-1]:
        return None
    k = len(decisiones)
    while k > 1 and decisiones[k - 2]:
        k -= 1
    return k


def _decisiones_sucesivas(pareja: Pareja, pagos: tuple[Pago, ...], cfg: Configuracion) -> list[bool]:
    """Decisión con `ahora` igual al instante de cada pago sucesivo, el primero incluido."""
    return [detectar_agrupado({pareja: pagos}, cfg, p.ocurrido_en)[pareja].es_recurrente for p in pagos]


def _resumir(series: list[SerieNecesaria]) -> ResumenMotivo:
    halladas = [s.necesarias for s in series if s.necesarias is not None]
    mediana = statistics.median(halladas) if halladas else None
    return ResumenMotivo(len(series), len(halladas), len(series) - len(halladas), mediana)


def ocurrencias_necesarias(corrida: CorridaPreparada, cfg: Configuracion) -> ResumenOcurrencias:
    """Cuántos pagos hacen falta para detectar cada serie recurrente de forma sostenida (hasta el final)."""
    series: list[SerieNecesaria] = []
    for s in corrida.series:
        if s["es_recurrente"] != "true":
            continue
        pareja = _pareja_de(s)
        pagos = tuple(p for p in corrida.grupos.get(pareja, ()) if p.ocurrido_en <= corrida.ahora)
        necesarias = _sostenida(_decisiones_sucesivas(pareja, pagos, cfg)) if pagos else None
        series.append(SerieNecesaria(s["serie_id"], s["usuario_id"], s["llave_destino"], s["motivo"],
                                     len(pagos), necesarias))
    motivos = sorted({s.motivo for s in series})
    resumen = _resumir(series)
    return ResumenOcurrencias(tuple(series), resumen.total, resumen.detectadas, resumen.no_detectadas,
                              resumen.mediana, {m: _resumir([s for s in series if s.motivo == m]) for m in motivos})
