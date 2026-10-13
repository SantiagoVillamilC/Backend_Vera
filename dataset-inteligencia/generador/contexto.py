"""Contexto compartido por los escenarios: parámetros, llaves, catálogo y secuencias."""

import itertools
from dataclasses import dataclass

from .llaves import GeneradorLlaves
from .modelos import Destino, Usuario, uuid_determinista
from .parametros import Parametros
from .reservados import Reservados


@dataclass(frozen=True)
class Contexto:
    parametros: Parametros
    llaves: GeneradorLlaves
    grupos: itertools.count
    catalogo: tuple[Destino, ...]
    personas: tuple[Destino, ...]
    reservados: Reservados

    @property
    def semilla(self) -> int:
        return self.parametros.semilla

    def comercios_de(self, categoria: str) -> tuple[Destino, ...]:
        return tuple(d for d in self.catalogo if d.categoria == categoria)


def crear_usuario(ctx: Contexto, clave: str) -> Usuario:
    return Usuario(uuid_determinista(ctx.semilla, "usuario", clave), clave, ctx.llaves.nueva("celular"))
