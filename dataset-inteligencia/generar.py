#!/usr/bin/env python3
"""CLI del generador de datasets sintéticos para evaluar el aprendizaje colectivo y los hábitos."""

import argparse
import sys
from datetime import date
from pathlib import Path

from generador.constantes import FECHA_FIN_POR_DEFECTO, TAMANOS_COLUSION_PERMITIDOS
from generador.orquestador import FilasInsuficientes, generar_en_disco
from generador.parametros import Parametros

SALIDA_POR_DEFECTO = Path(__file__).resolve().parent / "salida"


def _fecha(texto: str) -> date:
    try:
        return date.fromisoformat(texto)
    except ValueError as error:
        raise argparse.ArgumentTypeError(f"fecha inválida: {texto}") from error


def _tamanos(texto: str) -> tuple[int, ...]:
    try:
        valores = tuple(int(x) for x in texto.split(","))
    except ValueError as error:
        raise argparse.ArgumentTypeError(f"lista inválida: {texto!r}, se espera por ejemplo 2,3") from error
    permitidos = ", ".join(str(t) for t in TAMANOS_COLUSION_PERMITIDOS)
    if any(v not in TAMANOS_COLUSION_PERMITIDOS for v in valores) or len(set(valores)) != len(valores):
        raise argparse.ArgumentTypeError(f"valores permitidos: {permitidos}, sin repetir. Se recibió {texto!r}")
    return valores


def _no_negativo(texto: str) -> int:
    try:
        valor = int(texto)
    except ValueError as error:
        raise argparse.ArgumentTypeError(f"entero inválido: {texto!r}") from error
    if valor < 0:
        raise argparse.ArgumentTypeError("debe ser mayor o igual a 0")
    return valor


def construir_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(description="Genera el corpus sintético de evaluación (K=2 y hábitos).")
    p.add_argument("--filas", type=int, required=True, help="filas exactas de entrada/movimientos.csv")
    p.add_argument("--semilla", type=int, default=2026)
    p.add_argument("--variante", type=str.upper, choices=("A", "B"), default="A")
    p.add_argument("--usuarios", type=int, default=None, help="usuarios de la población de fondo")
    p.add_argument("--tasa-correccion", type=float, default=0.35)
    p.add_argument("--ruido-nombre", type=float, default=0.30)
    p.add_argument("--tasa-etiqueta-erronea", type=float, default=0.08)
    p.add_argument("--fecha-fin", type=_fecha, default=FECHA_FIN_POR_DEFECTO)
    p.add_argument("--colusion-comercios", type=_no_negativo, default=0,
                   help="comercios envenenados por colusión POR CADA tamaño de grupo (0 la desactiva)")
    p.add_argument("--colusion-tamanos", type=_tamanos, default=(2, 3),
                   help="tamaños de grupo colusor separados por comas, solo 2 y 3 (por defecto 2,3)")
    p.add_argument("--colusion-tardia-comercios", type=_no_negativo, default=0,
                   help="POR CADA tamaño, comercios con ataque POSTERIOR: 3 honestos consolidan y luego T "
                        "colusores corrigen el nombre (M de ellos) o la categoría (otros M). 0 la desactiva")
    p.add_argument("--gasto-usuarios", type=_no_negativo, default=0,
                   help="usuarios de la población de gasto con verdad por diseño (0 la desactiva)")
    p.add_argument("--salida", type=Path, default=SALIDA_POR_DEFECTO)
    return p


def main(argv: list[str] | None = None) -> int:
    a = construir_parser().parse_args(argv)
    params = Parametros(filas=a.filas, semilla=a.semilla, variante=a.variante, usuarios=a.usuarios,
                        tasa_correccion=a.tasa_correccion, ruido_nombre=a.ruido_nombre,
                        tasa_etiqueta_erronea=a.tasa_etiqueta_erronea, fecha_fin=a.fecha_fin,
                        colusion_comercios=a.colusion_comercios, colusion_tamanos=a.colusion_tamanos,
                        colusion_tardia_comercios=a.colusion_tardia_comercios,
                        gasto_usuarios=a.gasto_usuarios)
    try:
        carpeta = generar_en_disco(params, a.salida)
    except (FilasInsuficientes, ValueError) as error:
        print(f"Error: {error}", file=sys.stderr)
        return 2
    print(carpeta)
    return 0


if __name__ == "__main__":
    sys.exit(main())
