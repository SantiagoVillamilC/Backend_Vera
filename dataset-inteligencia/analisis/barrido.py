"""Barrido en cuadrícula de la configuración del detector de recurrencia.

Se prueba cada combinación de la rejilla sobre la corrida de ENTRENAMIENTO y se optimiza F1. La mejor
configuración se evalúa luego sobre las corridas de VALIDACIÓN, que el barrido nunca usa para elegir.
El resultado es el óptimo sobre el corpus sintético y no un óptimo real: la verdad sale del modelo de
recurrencia del generador, así que los índices calibrados se ajustan a ese modelo.

Ejes de la rejilla: `w_monto` (con `w_fecha = 1 - w_monto`, porque los pesos suman 1 y no son un eje
independiente), `tol_monto`, `factor_tolerancia_dias` (multiplica las tolerancias de días de la
configuración base), `n_min` y `umbral`. Las cadencias vienen de la configuración base.

Desempate determinista, en este orden: mayor F1, mayor precisión, menor tasa de falsos positivos
secundaria y por último el menor índice de la rejilla (el orden es el producto cartesiano de los ejes
en el orden de EJES, con el último eje variando más rápido). Las comparaciones usan fracciones exactas.
"""

import dataclasses
import itertools
import json
from collections.abc import Sequence
from dataclasses import dataclass
from fractions import Fraction
from pathlib import Path

from .configuracion import ConfiguracionInvalida, Configuracion, es_entero, es_numero
from .evaluacion import CorridaPreparada, Evaluacion, Matriz, Secundaria, evaluar

EJES = ("w_monto", "tol_monto", "factor_tolerancia_dias", "n_min", "umbral")
DECIMALES_PESO = 6


@dataclass(frozen=True)
class Rejilla:
    valores: dict[str, tuple]


@dataclass(frozen=True)
class Candidata:
    indice: int
    parametros: dict
    configuracion: Configuracion


@dataclass(frozen=True)
class FilaBarrido:
    indice: int
    parametros: dict
    configuracion: Configuracion
    matriz: Matriz
    secundaria: Secundaria


@dataclass(frozen=True)
class ResultadoBarrido:
    filas: tuple[FilaBarrido, ...]
    mejor: FilaBarrido
    entrenamiento: Evaluacion
    validacion: tuple[tuple[str, Evaluacion], ...]
    referencia_entrenamiento: Evaluacion
    referencia_validacion: tuple[tuple[str, Evaluacion], ...]


def _problemas_de_eje(eje: str, valores: object) -> list[str]:
    if not isinstance(valores, list) or not valores:
        return [f"{eje} debe ser una lista no vacía de valores (llegó {valores!r})"]
    if len({repr(v) for v in valores}) != len(valores):
        return [f"{eje} tiene valores repetidos ({valores})"]
    reglas = {
        "w_monto": (es_numero, lambda v: 0 <= v <= 1, "números entre 0 y 1"),
        "tol_monto": (es_numero, lambda v: 0 < v <= 1, "números mayores que 0 y menores o iguales que 1"),
        "factor_tolerancia_dias": (es_numero, lambda v: v > 0, "números mayores que 0"),
        "n_min": (es_entero, lambda v: v >= 1, "enteros mayores o iguales que 1"),
        "umbral": (es_numero, lambda v: 0 <= v <= 1, "números entre 0 y 1"),
    }
    tipo_ok, rango_ok, descripcion = reglas[eje]
    malos = [v for v in valores if not tipo_ok(v) or not rango_ok(v)]
    return [f"{eje} solo admite {descripcion} (no sirven: {malos})"] if malos else []


def rejilla_desde_dict(datos: object) -> Rejilla:
    if not isinstance(datos, dict):
        raise ConfiguracionInvalida("Rejilla inválida: se esperaba un objeto JSON con listas de valores")
    problemas = [f"falta el eje {e}" for e in EJES if e not in datos]
    problemas += [f"el eje {e} no existe (los ejes son {', '.join(EJES)})" for e in sorted(map(str, datos))
                  if e not in EJES]
    for eje in EJES:
        if eje in datos:
            problemas += _problemas_de_eje(eje, datos[eje])
    if problemas:
        raise ConfiguracionInvalida("Rejilla inválida:\n- " + "\n- ".join(problemas))
    return Rejilla({eje: tuple(datos[eje]) for eje in EJES})


def cargar_rejilla(ruta: Path | str) -> Rejilla:
    try:
        datos = json.loads(Path(ruta).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ConfiguracionInvalida(f"No se pudo leer la rejilla {ruta}: {error}") from error
    return rejilla_desde_dict(datos)


def expandir(rejilla: Rejilla, base: Configuracion) -> tuple[Candidata, ...]:
    """Todas las configuraciones de la rejilla, en el orden del producto cartesiano de EJES."""
    candidatas = []
    for indice, combinacion in enumerate(itertools.product(*(rejilla.valores[e] for e in EJES))):
        parametros = dict(zip(EJES, combinacion))
        escalada = base.con_factor_tolerancia(parametros["factor_tolerancia_dias"])
        configuracion = dataclasses.replace(
            escalada, w_monto=parametros["w_monto"], w_fecha=round(1 - parametros["w_monto"], DECIMALES_PESO),
            tol_monto=parametros["tol_monto"], n_min=parametros["n_min"], umbral=parametros["umbral"])
        candidatas.append(Candidata(indice, parametros, configuracion))
    return tuple(candidatas)


def clave_de_orden(matriz: Matriz, secundaria: Secundaria, indice: int) -> tuple:
    """Clave ascendente: la menor es la mejor. Ver el desempate en la documentación del módulo."""
    con_predicciones = matriz.tp + matriz.fp
    precision = Fraction(matriz.tp, con_predicciones) if con_predicciones else Fraction(-1)
    fpr = Fraction(secundaria.falsos_positivos, secundaria.parejas) if secundaria.parejas else Fraction(0)
    return (-matriz.f1_exacto, -precision, fpr, indice)


def _validar_semillas(entrenamiento: CorridaPreparada, validaciones: Sequence[CorridaPreparada]) -> None:
    if not validaciones:
        raise ValueError("El barrido necesita al menos una corrida de validación.")
    semillas = [c.semilla for c in validaciones]
    if entrenamiento.semilla in semillas:
        raise ValueError(f"La semilla de entrenamiento ({entrenamiento.semilla}) no puede repetirse en validación.")
    if len(set(semillas)) != len(semillas):
        raise ValueError(f"Las semillas de validación deben ser distintas (llegaron {semillas}).")


def barrer(entrenamiento: CorridaPreparada, validaciones: Sequence[CorridaPreparada], rejilla: Rejilla,
           base: Configuracion) -> ResultadoBarrido:
    """Optimiza F1 sobre `entrenamiento` y evalúa la mejor (y la base, de referencia) en `validaciones`."""
    _validar_semillas(entrenamiento, validaciones)
    filas = []
    for candidata in expandir(rejilla, base):
        evaluacion = evaluar(entrenamiento, candidata.configuracion)
        filas.append(FilaBarrido(candidata.indice, candidata.parametros, candidata.configuracion,
                                 evaluacion.global_, evaluacion.secundaria))
    mejor = min(filas, key=lambda f: clave_de_orden(f.matriz, f.secundaria, f.indice))
    return ResultadoBarrido(
        tuple(filas), mejor, evaluar(entrenamiento, mejor.configuracion),
        tuple((c.nombre, evaluar(c, mejor.configuracion)) for c in validaciones),
        evaluar(entrenamiento, base), tuple((c.nombre, evaluar(c, base)) for c in validaciones))
