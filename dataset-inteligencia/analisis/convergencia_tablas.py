"""Piezas comunes de las tablas de resumen.md: celdas con media y rango entre semillas, tablas de Markdown e índice."""

from collections.abc import Callable, Sequence

from .convergencia_agregado import FilaAgregada

Obtener = Callable[[str, str, int, str, str], FilaAgregada | None]


def numero(valor: float, decimales: int) -> str:
    return f"{valor:.{decimales}f}"


def celda(fila: FilaAgregada | None, decimales: int = 3) -> str:
    """`media (mínimo a máximo)` entre las semillas, o solo la media si todas coinciden. Indefinido es `n/a`."""
    if fila is None or fila.media is None:
        return "n/a"
    media = numero(fila.media, decimales)
    if fila.minimo == fila.maximo:
        return media
    return f"{media} ({numero(fila.minimo, decimales)} a {numero(fila.maximo, decimales)})"


def tabla(encabezado: Sequence[str], filas: Sequence[Sequence[str]]) -> list[str]:
    lineas = ["| " + " | ".join(encabezado) + " |", "|" + "|".join("---" for _ in encabezado) + "|"]
    lineas += ["| " + " | ".join(f) + " |" for f in filas]
    return lineas + [""]


def indice(agregado: Sequence[FilaAgregada]) -> Obtener:
    por_clave = {(f.tasa, f.politica, f.k, f.ambito, f.medida): f for f in agregado}
    return lambda tasa, politica, k, ambito, medida: por_clave.get((tasa, politica, k, ambito, medida))
