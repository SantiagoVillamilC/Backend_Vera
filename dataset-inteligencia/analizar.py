#!/usr/bin/env python3
"""CLI del análisis. Recurrencia (evaluar, barrido, comparación), convergencia por multitud y avisos.

    python3 analizar.py --experimento recurrencia --corrida RUTA [--config ARCHIVO]
    python3 analizar.py --experimento recurrencia-barrido --entrenamiento RUTA --validacion RUTA [RUTA ...]
                        --rejilla ARCHIVO [--config ARCHIVO]
    python3 analizar.py --experimento convergencia --corridas RUTA [RUTA ...] [--config ARCHIVO]
    python3 analizar.py --experimento avisos --corridas RUTA [RUTA ...] [--config ARCHIVO]

Los archivos salen en resultados/<experimento>/<id>/ (ignorado por git). El tiempo de ejecución se
imprime por stderr y nunca se escribe en los archivos, para que la salida sea idéntica byte a byte.
"""

import argparse
import sys
import time
from pathlib import Path

from analisis.avisos_config import RUTA_DEFECTO_AVISOS, cargar_configuracion_avisos
from analisis.avisos_evaluacion import resumir
from analisis.avisos_experimento import correr_avisos
from analisis.barrido import cargar_rejilla
from analisis.comparacion import BOOTSTRAP_B_DEFECTO, SEMILLA_BOOTSTRAP_DEFECTO, correr_comparacion
from analisis.configuracion import ConfiguracionInvalida, RUTA_DEFECTO, cargar_configuracion
from analisis.convergencia_config import RUTA_DEFECTO_CONVERGENCIA, cargar_configuracion_convergencia
from analisis.convergencia_experimento import correr_convergencia
from analisis.evaluacion import Evaluacion
from analisis.experimentos import correr_barrido, correr_evaluacion
from analisis.reporte import texto

RAIZ = Path(__file__).resolve().parent
REJILLA_POR_DEFECTO = RAIZ / "analisis" / "config" / "rejilla_recurrencia.json"
REJILLA_RESTRINGIDA_POR_DEFECTO = RAIZ / "analisis" / "config" / "rejilla_recurrencia_restringida.json"
RESULTADOS_POR_DEFECTO = RAIZ / "resultados"
EXPERIMENTOS = ("recurrencia", "recurrencia-barrido", "recurrencia-comparacion", "convergencia", "avisos")


def construir_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(description="Análisis del corpus sintético: recurrencia, convergencia por multitud y avisos.")
    p.add_argument("--experimento", choices=EXPERIMENTOS, required=True)
    p.add_argument("--corrida", type=Path, help="carpeta de una corrida (experimento recurrencia)")
    p.add_argument("--config", type=Path, default=None,
                   help="configuración JSON (por defecto la del experimento elegido)")
    p.add_argument("--corridas", type=Path, nargs="+", help="una o más corridas (convergencia, avisos)")
    p.add_argument("--entrenamiento", type=Path, help="corrida de entrenamiento (barrido)")
    p.add_argument("--validacion", type=Path, nargs="+", help="una o más corridas de validación (barrido)")
    p.add_argument("--rejilla", type=Path, default=REJILLA_POR_DEFECTO, help="rejilla JSON del barrido")
    p.add_argument("--verificacion", type=Path, nargs="*", help="corridas solo evaluadas (comparación)")
    p.add_argument("--rejilla-restringida", type=Path, default=REJILLA_RESTRINGIDA_POR_DEFECTO)
    p.add_argument("--bootstrap-b", type=int, default=BOOTSTRAP_B_DEFECTO)
    p.add_argument("--semilla-bootstrap", type=int, default=SEMILLA_BOOTSTRAP_DEFECTO)
    p.add_argument("--resultados", type=Path, default=RESULTADOS_POR_DEFECTO, help="raíz de la salida")
    return p


def _resumen(nombre: str, e: Evaluacion) -> str:
    g = e.global_
    return (f"{nombre}: precisión {texto(g.precision)}, recall {texto(g.recall)}, F1 {texto(g.f1)} "
            f"(TP {g.tp}, FP {g.fp}, TN {g.tn}, FN {g.fn})")


def _evaluacion(a: argparse.Namespace, parser: argparse.ArgumentParser) -> None:
    if a.corrida is None:
        parser.error("el experimento recurrencia exige --corrida")
    carpeta, evaluacion = correr_evaluacion(a.corrida, cargar_configuracion(a.config or RUTA_DEFECTO), a.resultados)
    print(f"resultados: {carpeta}")
    print(_resumen(a.corrida.name, evaluacion))


def _barrido(a: argparse.Namespace, parser: argparse.ArgumentParser) -> None:
    if a.entrenamiento is None or not a.validacion:
        parser.error("el experimento recurrencia-barrido exige --entrenamiento y --validacion")
    carpeta, r = correr_barrido(a.entrenamiento, a.validacion, cargar_rejilla(a.rejilla),
                                cargar_configuracion(a.config or RUTA_DEFECTO), a.resultados)
    print(f"resultados: {carpeta}")
    print(f"configuraciones probadas: {len(r.filas)}. Mejor: índice {r.mejor.indice} {r.mejor.parametros}")
    print(_resumen(a.entrenamiento.name + " (entrenamiento)", r.entrenamiento))
    for ruta, (_, evaluacion) in zip(a.validacion, r.validacion):
        print(_resumen(ruta.name + " (validación)", evaluacion))
    print("Resultado: óptimo sobre el corpus sintético, no óptimo real.")


def _comparacion(a: argparse.Namespace, parser: argparse.ArgumentParser) -> None:
    if a.entrenamiento is None or not a.validacion:
        parser.error("el experimento recurrencia-comparacion exige --entrenamiento y --validacion")
    carpeta, lineas = correr_comparacion(
        a.entrenamiento, a.validacion, a.verificacion or [], cargar_rejilla(a.rejilla),
        cargar_rejilla(a.rejilla_restringida), cargar_configuracion(a.config or RUTA_DEFECTO), a.resultados, a.bootstrap_b,
        a.semilla_bootstrap)
    print(f"resultados: {carpeta}")
    print("\n".join(lineas))
    print("Resultado: óptimo sobre el corpus sintético, no óptimo real.")


def _convergencia(a: argparse.Namespace, parser: argparse.ArgumentParser) -> None:
    if not a.corridas:
        parser.error("el experimento convergencia exige --corridas")
    cfg = cargar_configuracion_convergencia(a.config or RUTA_DEFECTO_CONVERGENCIA)
    carpeta, evaluadas = correr_convergencia(a.corridas, cfg, a.resultados, a.bootstrap_b, a.semilla_bootstrap)
    print(f"resultados: {carpeta}")
    print(f"corridas evaluadas: {len(evaluadas)}. K: {', '.join(str(k) for k in cfg.ks)}. "
          f"Políticas: {', '.join(p.nombre for p in cfg.politicas)}.")


def _avisos(a: argparse.Namespace, parser: argparse.ArgumentParser) -> None:
    if not a.corridas:
        parser.error("el experimento avisos exige --corridas")
    cfg = cargar_configuracion_avisos(a.config or RUTA_DEFECTO_AVISOS)
    carpeta, evaluadas = correr_avisos(a.corridas, cfg, a.resultados)
    print(f"resultados: {carpeta}")
    print(f"corridas evaluadas: {len(evaluadas)}. Prueba de aceptación sobre {len(evaluadas[0].corrida.etiquetas)} "
          "etiquetas, no una medición estadística.")
    for s in resumir(evaluadas[0].filas):
        c = s.combinacion
        print(f"{evaluadas[0].corrida.nombre} {c.regla} {c.linea_base} {c.fuente}: {s.aprobadas} de {s.total} filas "
              f"aprobadas, falsos positivos {s.falsos_positivos} de {s.negativas}, "
              f"falsos negativos {s.falsos_negativos} de {s.positivas}")


def main(argv: list[str] | None = None) -> int:
    parser = construir_parser()
    a = parser.parse_args(argv)
    inicio = time.perf_counter()
    try:
        {"recurrencia": _evaluacion, "recurrencia-barrido": _barrido, "recurrencia-comparacion": _comparacion,
         "convergencia": _convergencia, "avisos": _avisos}[a.experimento](a, parser)
    except (ConfiguracionInvalida, FileNotFoundError, ValueError) as error:
        print(f"Error: {error}", file=sys.stderr)
        return 2
    print(f"tiempo: {time.perf_counter() - inicio:.2f} s", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
