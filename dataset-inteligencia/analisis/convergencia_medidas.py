"""Medidas de convergencia de un conjunto de comercios (todos, o solo el subconjunto de prueba).

Recibe filas `FilaComercio` ya calculadas por la evaluación y devuelve un diccionario plano
{medida: valor}. Una razón sin denominador vale None y se reporta `n/a`. No lee archivos.
"""

import math
import statistics
from collections.abc import Sequence
from dataclasses import dataclass


@dataclass(frozen=True)
class FilaComercio:
    """Resultado de un comercio con un K y una política. Los días son corridos y pueden ser fraccionarios."""

    llave: str
    es_prueba: bool
    revela_naturaleza: bool
    colusion_t: int
    con_correcciones: bool
    nombre_global: str | None
    categoria_global: str | None
    ok_nombre: bool
    ok_categoria: bool
    dias_nombre: float | None
    dias_categoria: float | None
    espurios_nombre: int
    espurios_nombre_revertidos: int
    espurios_categoria: int
    espurios_categoria_revertidos: int
    equivocada_alguna_vez: bool
    dias_equivocada: float
    equivocada_al_corte: bool
    cambio_nombre: bool
    retraso_nombre: float | None
    cambio_categoria: bool
    retraso_categoria: float | None
    pagadores: int = 0
    correctores: int = 0


AMBITOS_DE_FILAS = ("todos", "prueba", "sin_colusion", "prueba_sin_colusion")
AMBITOS_CONTAMINADOS = ("todos", "prueba")
KS_COTA = (1, 2, 3)


def en_ambito(f: FilaComercio, ambito: str) -> bool:
    """Pertenencia de un comercio a un ámbito. `sin_colusion` excluye la población de colusión y la droguería a mano."""
    if ambito == "todos":
        return True
    if ambito == "prueba":
        return f.es_prueba
    if ambito == "sin_colusion":
        return f.colusion_t == 0
    if ambito == "prueba_sin_colusion":
        return f.es_prueba and f.colusion_t == 0
    raise ValueError(f"Ámbito desconocido: {ambito}")


def razon(numerador: int, denominador: int) -> float | None:
    return numerador / denominador if denominador else None


def mediana(valores: Sequence[float]) -> float | None:
    return float(statistics.median(valores)) if valores else None


def percentil_90(valores: Sequence[float]) -> float | None:
    """Rango más cercano: el valor en la posición ceil(0,9 n) de los ordenados."""
    if not valores:
        return None
    ordenados = sorted(valores)
    return float(ordenados[math.ceil(0.9 * len(ordenados)) - 1])


def percentil_rango(valores: Sequence[float], p: float) -> float | None:
    """Rango más cercano: el valor en la posición ceil(p n) de los ordenados."""
    if not valores:
        return None
    ordenados = sorted(valores)
    return float(ordenados[max(math.ceil(p * len(ordenados)), 1) - 1])


def _cobertura_y_acierto(filas: Sequence[FilaComercio]) -> dict:
    """Cobertura, acierto y el par riesgo-cobertura.

    Errores visibles: comercios con valor global distinto del vigente verdadero entre todos los comercios del ámbito
    (la abstención no cuenta). Riesgo: error entre los consolidados, es decir 1 menos el acierto entre consolidados.
    """
    n = len(filas)
    medidas: dict = {"n_comercios": n}
    for dim in ("nombre", "categoria"):
        con = sum(1 for f in filas if getattr(f, f"{dim}_global") is not None)
        aciertos = sum(1 for f in filas if getattr(f, f"ok_{dim}"))
        medidas |= {f"con_{dim}_global": con, f"cobertura_{dim}": razon(con, n), f"aciertos_{dim}": aciertos,
                    f"acierto_consolidados_{dim}": razon(aciertos, con), f"acierto_total_{dim}": razon(aciertos, n),
                    f"errores_visibles_{dim}": razon(con - aciertos, n), f"riesgo_{dim}": razon(con - aciertos, con)}
    return medidas


def _distribucion(prefijo: str, valores: Sequence[int]) -> dict:
    cinco = (min(valores) if valores else None, percentil_rango(valores, 0.25), mediana(valores),
             percentil_rango(valores, 0.75), max(valores) if valores else None)
    nombres = ("min", "p25", "mediana", "p75", "max")
    return {f"{prefijo}_{nombre}": valor for nombre, valor in zip(nombres, cinco)}


def _densidad(filas: Sequence[FilaComercio]) -> dict:
    """Pagadores y correctores distintos por comercio. La fracción con al menos K correctores acota la cobertura."""
    n = len(filas)
    pagadores, correctores = [f.pagadores for f in filas], [f.correctores for f in filas]
    medidas = _distribucion("pagadores", pagadores) | _distribucion("correctores", correctores)
    medidas["pagadores_fraccion_un_solo"] = razon(sum(1 for p in pagadores if p == 1), n)
    for k in KS_COTA:
        medidas[f"correctores_fraccion_ge{k}"] = razon(sum(1 for c in correctores if c >= k), n)
    return medidas


def _dias_hasta_consolidar(filas: Sequence[FilaComercio]) -> dict:
    medidas: dict = {}
    for dim in ("nombre", "categoria"):
        dias = [d for d in (getattr(f, f"dias_{dim}") for f in filas) if d is not None]
        medidas |= {f"dias_consolidar_{dim}_n": len(dias), f"dias_consolidar_{dim}_mediana": mediana(dias),
                    f"dias_consolidar_{dim}_p90": percentil_90(dias)}
    return medidas


def _espurios(filas: Sequence[FilaComercio]) -> dict:
    medidas: dict = {}
    for dim in ("nombre", "categoria"):
        medidas[f"espurios_{dim}"] = sum(getattr(f, f"espurios_{dim}") for f in filas)
        medidas[f"espurios_{dim}_revertidos"] = sum(getattr(f, f"espurios_{dim}_revertidos") for f in filas)
        medidas[f"espurios_{dim}_revertidos_sin_colusion"] = sum(
            getattr(f, f"espurios_{dim}_revertidos") for f in filas if f.colusion_t == 0)
    return medidas


def _retraso(filas: Sequence[FilaComercio]) -> dict:
    medidas: dict = {}
    for dim in ("nombre", "categoria"):
        con_cambio = [f for f in filas if getattr(f, f"cambio_{dim}")]
        dias = [d for d in (getattr(f, f"retraso_{dim}") for f in con_cambio) if d is not None]
        medidas |= {f"retraso_{dim}_cambios": len(con_cambio), f"retraso_{dim}_alcanzados": len(dias),
                    f"retraso_{dim}_no_ocurren": len(con_cambio) - len(dias), f"retraso_{dim}_mediana": mediana(dias)}
    return medidas


def _envenenamiento(filas: Sequence[FilaComercio]) -> dict:
    medidas: dict = {}
    for t in sorted({f.colusion_t for f in filas if f.colusion_t > 0}):
        grupo = [f for f in filas if f.colusion_t == t]
        envenenados = [f for f in grupo if f.equivocada_alguna_vez]
        prefijo = f"envenenamiento_T{t}"
        medidas |= {f"{prefijo}_comercios": len(grupo), f"{prefijo}_envenenados": len(envenenados),
                    f"{prefijo}_fraccion": razon(len(envenenados), len(grupo)),
                    f"{prefijo}_dias_mediana": mediana([f.dias_equivocada for f in envenenados]),
                    f"{prefijo}_sin_relevo": sum(1 for f in grupo if f.equivocada_al_corte)}
    return medidas


def _residuo(filas: Sequence[FilaComercio]) -> dict:
    sin = [f for f in filas if f.categoria_global is None]
    sin_correcciones = sum(1 for f in sin if not f.con_correcciones)
    return {"residuo_sin_categoria": len(sin), "residuo_fraccion": razon(len(sin), len(filas)),
            "residuo_sin_correcciones": sin_correcciones,
            "residuo_con_correcciones_sin_consenso": len(sin) - sin_correcciones,
            "residuo_revela_naturaleza": sum(1 for f in sin if f.revela_naturaleza)}


def medidas_de_comercios(filas: Sequence[FilaComercio]) -> dict:
    """Todas las medidas por comercio de un ámbito. Las llaves de envenenamiento dependen de los T presentes."""
    return (_cobertura_y_acierto(filas) | _dias_hasta_consolidar(filas) | _espurios(filas) | _retraso(filas)
            | _envenenamiento(filas) | _residuo(filas) | _densidad(filas))
