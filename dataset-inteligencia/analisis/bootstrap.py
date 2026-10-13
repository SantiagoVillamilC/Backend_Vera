"""Bootstrap percentil pareado para la diferencia de F1 entre dos métodos sobre las MISMAS series.

Se remuestrean con reemplazo las series etiquetadas. Los índices de cada remuestreo se generan una sola
vez por corpus (`generar_remuestreos`) y se reutilizan en todas las comparaciones, de modo que el
emparejamiento es exacto. El azar sale de `random.Random("<semilla>:<corpus>")`: una semilla de texto es
determinista y no depende de `PYTHONHASHSEED`. El intervalo de 95 % usa el rango más cercano simétrico:
los índices ceil(0,025 x B) - 1 y ceil(0,975 x B) - 1 de las diferencias ordenadas (49 y 1949 con B = 2000).
"""

import math
import random
from collections.abc import Sequence
from dataclasses import dataclass

Resultado = tuple[bool, bool]  # (predicho, real) de una serie


@dataclass(frozen=True)
class Intervalo:
    observada: float
    media: float
    inferior: float
    superior: float


def indices_percentil(b: int) -> tuple[int, int]:
    return -(-25 * b // 1000) - 1, -(-975 * b // 1000) - 1


def f1_de(resultados: Sequence[Resultado], indices: Sequence[int]) -> float:
    tp = fp = fn = 0
    for i in indices:
        predicho, real = resultados[i]
        tp += predicho and real
        fp += predicho and not real
        fn += real and not predicho
    denominador = 2 * tp + fp + fn
    return 2 * tp / denominador if denominador else 0.0


def generar_remuestreos(n: int, b: int, semilla: int, corpus: str) -> list[list[int]]:
    azar = random.Random(f"{semilla}:{corpus}")
    return [[azar.randrange(n) for _ in range(n)] for _ in range(b)]


def intervalo_pareado(a: Sequence[Resultado], b: Sequence[Resultado], remuestreos: Sequence[Sequence[int]]
                      ) -> Intervalo:
    """Diferencia F1(a) menos F1(b): valor observado, media bootstrap e intervalo percentil de 95 %."""
    if len(a) != len(b):
        raise ValueError(f"Los métodos deben evaluar las mismas series ({len(a)} frente a {len(b)}).")
    diferencias = sorted(f1_de(a, r) - f1_de(b, r) for r in remuestreos)
    bajo, alto = indices_percentil(len(diferencias))
    observada = f1_de(a, range(len(a))) - f1_de(b, range(len(b)))
    return Intervalo(observada, math.fsum(diferencias) / len(diferencias), diferencias[bajo], diferencias[alto])


def frase(intervalo: Intervalo, referencia: str) -> str:
    """Frase fija del reporte. `referencia` es "la línea base" o "el restringido"."""
    if intervalo.inferior <= 0 <= intervalo.superior:
        destino = "del " + referencia[3:] if referencia.startswith("el ") else "de " + referencia
        return f"el intervalo incluye 0 (no se distingue {destino})"
    return "el intervalo no incluye 0"
