"""Casos adversos del experimento de multitud (a) a (d), con pagos escalonados en el tiempo.

(a) comercio con correcciones consistentes que alcanzan K=2 (dos sucursales de una empresa).
(b) llave de celular de una arrendadora: dos usuarios la etiquetan igual pero es una persona.
(c) dos usuarios coludidos etiquetan mal el mismo comercio.
(d) dos comercios de nombre parecido que son distintos (Ruta Veloz y Ruta Veloz Comidas).
El caso (e), el GMF, sale de los pagos y envíos que pasan del monto exento.
"""

from datetime import date

from .contexto import Contexto
from .guion import Corrige, Evento, ejecutar_guion, usuario_id_de
from .modelos import Aporte, Serie
from .reservados import Reservados

PANADERIA = "alimentos_mercado"


def _eventos_panaderia(r: Reservados) -> tuple[Evento, ...]:
    def ev(usuario, destino, mes, dia, monto, nombre, ruido):
        return Evento(usuario, destino, date(2026, mes, dia), 9, monto, Corrige(nombre, PANADERIA, ruido))

    return (
        ev("a1", r.panaderia_centro, 4, 20, 24_000, "PANADERÍA TRIGAL REAL CENTRO", "ninguno"),
        ev("a4", r.panaderia_suba, 5, 9, 31_500, "PANADERÍA TRIGAL REAL SUBA", "ninguno"),
        ev("a2", r.panaderia_centro, 6, 12, 18_000, "panadería trigal real centro", "mayusculas"),
        ev("a3", r.panaderia_centro, 8, 28, 27_000, "PANADERIA TRIGAL REAL CENTRO", "tildes"),
        ev("a5", r.panaderia_suba, 9, 15, 22_500, "PANADERIA_TRIGAL_REAL_SUBA", "mayusculas"),
    )


def _eventos_arrendadora(r: Reservados) -> tuple[Evento, ...]:
    renta_b1 = tuple(
        Evento("b1", r.arrendadora, date(2026, mes, 5), 10, 1_200_000,
               Corrige("ARRIENDO MARTA OCAMPO", "vivienda_servicios") if mes == 4 else None,
               descripcion="Arriendo")
        for mes in range(4, 10)
    )
    renta_b2 = tuple(
        Evento("b2", r.arrendadora, date(2026, mes, 6), 10, 950_000,
               Corrige("arriendo marta ocampo", "vivienda_servicios", "mayusculas") if mes == 8 else None,
               descripcion="Arriendo")
        for mes in (8, 9)
    )
    return renta_b1 + renta_b2


def _eventos_colusion(r: Reservados) -> tuple[Evento, ...]:
    nombre = "DROGUERÍA NUBE AZUL"
    return (
        Evento("c4", r.drogueria, date(2026, 4, 18), 16, 38_000, Corrige(nombre, "entretenimiento", "colusion")),
        Evento("c5", r.drogueria, date(2026, 6, 22), 11, 41_000, Corrige(nombre, "entretenimiento", "colusion")),
        Evento("c1", r.drogueria, date(2026, 8, 14), 15, 52_000, Corrige(nombre, "salud")),
        Evento("c2", r.drogueria, date(2026, 9, 9), 10, 36_000, Corrige("Droguería Nube Azul", "salud", "mayusculas")),
    )


def _eventos_ruta(r: Reservados) -> tuple[Evento, ...]:
    def ev(usuario, destino, mes, dia, monto, nombre, categoria, ruido="ninguno"):
        return Evento(usuario, destino, date(2026, mes, dia), 19, monto, Corrige(nombre, categoria, ruido))

    t, c = "transporte", "restaurantes_domicilios"
    return (
        ev("d1", r.ruta, 4, 10, 14_000, "RUTA VELOZ", t),
        ev("d1", r.ruta_comidas, 4, 12, 33_000, "RUTA VELOZ COMIDAS", c),
        ev("d2", r.ruta, 6, 20, 9_500, "Ruta Veloz", t, "mayusculas"),
        ev("d3", r.ruta_comidas, 8, 12, 41_000, "ruta veloz comidas", c, "mayusculas"),
        ev("d4", r.ruta, 9, 18, 12_500, "RUTA VELOZ", t),
        ev("d4", r.ruta_comidas, 9, 19, 29_000, "RUTA VELOZ COMIDAS", c),
    )


def construir(ctx: Contexto) -> Aporte:
    r = ctx.reservados
    eventos = (_eventos_panaderia(r) + _eventos_arrendadora(r) + _eventos_colusion(r) + _eventos_ruta(r))
    aporte = ejecutar_guion(ctx, eventos)
    renta = Serie(
        "arriendo-b1", usuario_id_de(aporte, "b1"), r.arrendadora.llave_destino, True, 30, "regular",
        "Arriendo de 1.200.000 pesos el día 5 de cada mes de abril a septiembre a una llave de persona",
    )
    return Aporte(usuarios=aporte.usuarios, movimientos=aporte.movimientos,
                  correcciones=aporte.correcciones, series=(renta,))
