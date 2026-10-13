"""Ensamblado final del dataset: orden cronológico, identificadores y tablas de verdad derivadas."""

import random
from dataclasses import dataclass, replace

from .contexto import Contexto
from .particion import asignar_conjuntos
from .modelos import (Aporte, Aviso, Correccion, Destino, Empresa, Movimiento, NombreVigente, Serie,
                      Usuario, VistaEsperada, uuid_determinista)
from .constantes import INICIO_HISTORIAL
from .parametros import Parametros


@dataclass(frozen=True)
class Dataset:
    parametros: Parametros
    empresas: tuple[Empresa, ...]
    destinos: tuple[Destino, ...]
    usuarios: tuple[Usuario, ...]
    movimientos: tuple[Movimiento, ...]
    correcciones: tuple[Correccion, ...]
    avisos: tuple[Aviso, ...]
    series: tuple[tuple[str, Serie], ...]
    vistas: tuple[tuple[VistaEsperada, Movimiento], ...]
    historial: tuple[NombreVigente, ...]
    base_presentacion: int
    conjuntos: dict[str, str]

    @property
    def llaves_propias(self) -> dict[str, str]:
        return {u.usuario_id: u.llave_propia for u in self.usuarios}


def numerar_movimientos(movimientos: tuple[Movimiento, ...]) -> tuple[Movimiento, ...]:
    """Ordena por instante (estable) y asigna movimiento_id creciente desde 1."""
    ordenados = sorted(movimientos, key=lambda m: m.ocurrido_en)
    return tuple(replace(m, movimiento_id=i) for i, m in enumerate(ordenados, start=1))


def numerar_correcciones(semilla: int, correcciones: tuple[Correccion, ...]) -> tuple[Correccion, ...]:
    ordenadas = sorted(correcciones, key=lambda c: c.ocurrido_en)
    return tuple(replace(c, correccion_id=uuid_determinista(semilla, "correccion", i))
                 for i, c in enumerate(ordenadas))


def historial_completo(destinos: tuple[Destino, ...], especiales: tuple[NombreVigente, ...]) -> tuple[NombreVigente, ...]:
    """Toda llave de comercio tiene al menos una fila. Las del caso 3 traen dos."""
    con_historia = {h.llave_destino for h in especiales}
    filas = list(especiales)
    for d in destinos:
        if d.es_comercio and d.llave_destino not in con_historia:
            filas.append(NombreVigente(d.llave_destino, d.nombre_canonico, d.categoria, INICIO_HISTORIAL, None))
    return tuple(filas)


def vistas_con_movimiento(vistas: tuple[VistaEsperada, ...],
                          movimientos: tuple[Movimiento, ...]) -> tuple[tuple[VistaEsperada, Movimiento], ...]:
    por_marca = {m.marca: m for m in movimientos if m.marca}
    resultado = [(v, por_marca[v.marca]) for v in vistas]
    return tuple(sorted(resultado, key=lambda par: par[1].movimiento_id))


def ensamblar(ctx: Contexto, aporte: Aporte, aleatorias: tuple[Correccion, ...], base_presentacion: int,
              empresas: tuple[Empresa, ...], azar_particion: random.Random) -> Dataset:
    semilla = ctx.semilla
    movimientos = numerar_movimientos(aporte.movimientos)
    destinos = ctx.catalogo + ctx.personas + aporte.destinos
    return Dataset(
        parametros=ctx.parametros,
        empresas=empresas,
        destinos=destinos,
        usuarios=aporte.usuarios,
        movimientos=movimientos,
        correcciones=numerar_correcciones(semilla, aporte.correcciones + aleatorias),
        avisos=aporte.avisos,
        series=tuple((uuid_determinista(semilla, "serie", s.clave), s) for s in aporte.series),
        vistas=vistas_con_movimiento(aporte.vistas, movimientos),
        historial=historial_completo(destinos, aporte.historiales),
        base_presentacion=base_presentacion,
        conjuntos=asignar_conjuntos(destinos, azar_particion),
    )
