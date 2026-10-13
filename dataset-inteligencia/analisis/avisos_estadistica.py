"""Estadística exacta de los avisos: mediana, media, MAD y z modificado con `Fraction`.

Con `Fraction` una comparación que cae justo sobre el umbral no cambia de decisión por un error de redondeo.
La mediana de una cantidad par de valores es el promedio de los dos centrales (no el central alto ni el bajo).
El z modificado es el de Iglewicz y Hoaglin: 0,6745 por (x menos el centro) dividido entre la MAD, que es la
mediana de las desviaciones absolutas respecto de la mediana. Pura: no abre archivos ni mira el reloj.
"""

from collections.abc import Sequence
from fractions import Fraction

LINEAS_BASE = ("mediana", "media")


def mediana(valores: Sequence[int | Fraction]) -> Fraction:
    ordenados = sorted(valores)
    medio, resto = divmod(len(ordenados), 2)
    if resto:
        return Fraction(ordenados[medio])
    return (Fraction(ordenados[medio - 1]) + Fraction(ordenados[medio])) / 2


def media(valores: Sequence[int | Fraction]) -> Fraction:
    return sum((Fraction(v) for v in valores), Fraction(0)) / len(valores)


def mad(valores: Sequence[int | Fraction]) -> Fraction:
    """Mediana de las desviaciones absolutas respecto de la mediana de los valores."""
    centro = mediana(valores)
    return mediana([abs(Fraction(v) - centro) for v in valores])


def z_modificado(x: int | Fraction, centro: int | Fraction, desviacion: Fraction, factor: Fraction) -> Fraction:
    """`factor` x (x menos centro) / MAD. La MAD debe ser positiva (con MAD cero el z no está definido)."""
    if desviacion <= 0:
        raise ValueError("la MAD debe ser positiva para calcular el z modificado")
    return factor * (Fraction(x) - Fraction(centro)) / desviacion


def linea_base(valores: Sequence[int | Fraction], nombre: str) -> Fraction:
    """Mediana o media de los valores mensuales, según `nombre`."""
    if nombre == "mediana":
        return mediana(valores)
    if nombre == "media":
        return media(valores)
    raise ValueError(f"línea base desconocida {nombre!r}: se esperaba una de {LINEAS_BASE}")
