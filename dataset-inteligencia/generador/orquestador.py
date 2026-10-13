"""Orquesta una corrida: mundo fijo, población de fondo, correcciones y escritura en disco."""

import itertools
import random
from pathlib import Path

from . import (casos_multitud, casos_nombres, casos_recurrencia, colusion_poblacion, correcciones, escritor,
               fondo, gasto_poblacion, perfiles_gasto, perfiles_ingreso)
from .catalogo import MAX_FICTICIAS, construir_catalogo, construir_personas
from .contexto import Contexto
from .dataset import Dataset, ensamblar, numerar_movimientos
from .llaves import GeneradorLlaves
from .modelos import Aporte, Empresa, combinar
from .parametros import Parametros
from .reservados import construir_reservados

NOMBRES_FLUJOS = ("llaves", "catalogo", "personas", "escenarios", "fondo", "correcciones", "presentacion",
                  "particion")


class FilasInsuficientes(ValueError):
    def __init__(self, minimo: int, solicitadas: int) -> None:
        self.minimo = minimo
        self.solicitadas = solicitadas
        super().__init__(
            f"--filas {solicitadas} es menor que el mínimo necesario para los escenarios fijos: {minimo}")


def _flujos(semilla: int) -> dict[str, random.Random]:
    """Un generador por tarea, todos derivados de random.Random(semilla) en orden fijo."""
    maestro = random.Random(semilla)
    return {nombre: random.Random(maestro.getrandbits(64)) for nombre in NOMBRES_FLUJOS}


def _contexto(params: Parametros, flujos: dict[str, random.Random]) -> tuple[Contexto, tuple[Empresa, ...]]:
    llaves = GeneradorLlaves(flujos["llaves"])
    ficticias = min(MAX_FICTICIAS, max(32, params.filas // 60))
    empresas, destinos = construir_catalogo(params.semilla, flujos["catalogo"], llaves, ficticias)
    personas = construir_personas(flujos["personas"], llaves, max(24, params.filas // 30))
    reservados = construir_reservados(params.semilla, llaves)
    ctx = Contexto(params, llaves, itertools.count(1), destinos, personas, reservados)
    return ctx, empresas


def _escenarios_fijos(ctx: Contexto, azar: random.Random, empresas: tuple[Empresa, ...]) -> Aporte:
    r = ctx.reservados
    return combinar([
        perfiles_ingreso.perfil_1(ctx, azar),
        perfiles_ingreso.perfil_2(ctx, azar),
        perfiles_ingreso.perfil_3(ctx, azar),
        perfiles_gasto.perfil_4(ctx, azar),
        perfiles_gasto.perfil_5(ctx, azar),
        perfiles_gasto.casos_de_envio(ctx, azar),
        casos_multitud.construir(ctx),
        casos_nombres.construir(ctx),
        casos_recurrencia.construir(ctx),
        colusion_poblacion.construir(ctx, empresas),
        gasto_poblacion.construir(ctx),
        Aporte(destinos=r.destinos(), historiales=r.historiales, guionadas=r.llaves_guionadas()),
    ])


def _preparar(params: Parametros):
    params.validar()
    flujos = _flujos(params.semilla)
    ctx, empresas = _contexto(params, flujos)
    return ctx, empresas, flujos, _escenarios_fijos(ctx, flujos["escenarios"], empresas)


def minimo_filas(params: Parametros) -> int:
    """Filas que ocupan los escenarios fijos. --filas no puede ser menor."""
    return len(_preparar(params)[3].movimientos)


def construir(params: Parametros) -> Dataset:
    ctx, empresas, flujos, fijo = _preparar(params)
    minimo = len(fijo.movimientos)
    if params.filas < minimo:
        raise FilasInsuficientes(minimo, params.filas)
    relleno = fondo.construir_fondo(ctx, flujos["fondo"], params.filas - minimo)
    todo = combinar([fijo, relleno])
    aleatorias = correcciones.generar_correcciones(
        params, flujos["correcciones"], numerar_movimientos(todo.movimientos), frozenset(todo.guionadas))
    base = flujos["presentacion"].getrandbits(64)
    return ensamblar(ctx, todo, aleatorias, base, empresas + ctx.reservados.empresas + todo.empresas, flujos["particion"])


def generar_en_disco(params: Parametros, salida: Path) -> Path:
    """Genera la corrida en <salida>/<semilla>-<filas>-<variante>/ y devuelve esa carpeta."""
    dataset = construir(params)
    carpeta = Path(salida) / f"{params.semilla}-{params.filas}-{params.variante}"
    escritor.escribir_dataset(dataset, carpeta)
    return carpeta
