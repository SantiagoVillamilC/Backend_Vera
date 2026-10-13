"""Calibración de un umbral de similitud para decidir "mismo comercio".

Funciones puras, sin lectura de archivos. La etiqueta sale siempre de `Par.misma_llave` (la verdad del
generador), nunca de un puntaje. Los textos se canonizan con `normalizar_nombre` (la misma canonización
que usa la clave de nombre del análisis) antes de puntuar. Un par se declara "mismo" si puntaje >= umbral.

Convención: sin predicciones (tp + fp = 0) la precisión es 1.0 (vacua) y el recall 0.0. El resultado de
cualquier calibración es "óptimo sobre el corpus sintético", no óptimo real.
"""

from collections.abc import Callable, Sequence
from dataclasses import dataclass

from .pagos import normalizar_nombre
from .similitud_pares import Par

Puntaje = Callable[[str, str], float]

UMBRALES = tuple(round(0.50 + i * 0.01, 2) for i in range(50))
CRITERIO_MAX_F1 = "max_f1"
CRITERIO_RECALL_CON_PRECISION = "recall_con_precision_minima"
CRITERIOS = (CRITERIO_MAX_F1, CRITERIO_RECALL_CON_PRECISION)
PRECISION_MINIMA = 0.99


@dataclass(frozen=True)
class Medida:
    umbral: float | None
    tp: int
    fp: int
    fn: int
    precision: float
    recall: float
    f1: float


def puntuar(pares: Sequence[Par], puntaje: Puntaje) -> tuple[tuple[float, bool], ...]:
    """(puntaje, es_positivo) por par, sin los ambiguos (colusión o cambio real), con los dos textos canonizados antes de puntuar."""
    return tuple((puntaje(normalizar_nombre(p.texto_a), normalizar_nombre(p.texto_b)), p.misma_llave)
                 for p in pares if not p.ambiguo)


def _medida(puntuados: Sequence[tuple[float, bool]], umbral: float | None) -> Medida:
    tp = fp = fn = 0
    for valor, positivo in puntuados:
        predicho = umbral is not None and valor >= umbral
        if predicho and positivo:
            tp += 1
        elif predicho:
            fp += 1
        elif positivo:
            fn += 1
    precision = tp / (tp + fp) if tp + fp else 1.0
    recall = tp / (tp + fn) if tp + fn else 0.0
    f1 = 2 * precision * recall / (precision + recall) if precision + recall else 0.0
    return Medida(umbral, tp, fp, fn, precision, recall, f1)


def medir(puntuados: Sequence[tuple[float, bool]], umbral: float | None) -> Medida:
    """Medida sobre puntajes ya calculados (permite reutilizarlos entre cortes)."""
    return _medida(puntuados, umbral)


def barrer(pares: Sequence[Par], puntaje: Puntaje, umbrales: Sequence[float] = UMBRALES) -> tuple[Medida, ...]:
    puntuados = puntuar(pares, puntaje)
    return tuple(_medida(puntuados, u) for u in umbrales)


def elegir_umbral(medidas: Sequence[Medida], criterio: str, precision_minima: float = PRECISION_MINIMA) -> Medida:
    """Elige la medida de entrenamiento. Los empates se resuelven por el umbral más alto (más conservador)."""
    if criterio == CRITERIO_MAX_F1:
        return max(medidas, key=lambda m: (m.f1, m.umbral))
    if criterio == CRITERIO_RECALL_CON_PRECISION:
        admisibles = [m for m in medidas if m.precision >= precision_minima]
        if not admisibles:
            # Ningún umbral de la rejilla alcanza la precisión mínima: se abstiene siempre (umbral None).
            return Medida(None, 0, 0, sum(m.tp + m.fn for m in medidas[:1]), 1.0, 0.0, 0.0)
        return max(admisibles, key=lambda m: (m.recall, m.umbral))
    raise ValueError(f"criterio desconocido: {criterio}")


def evaluar_umbral_fijo(pares: Sequence[Par], puntaje: Puntaje, umbral: float | None) -> Medida:
    """Evalúa un umbral ya elegido en otra semilla. `None` equivale a abstenerse siempre."""
    return _medida(puntuar(pares, puntaje), umbral)
