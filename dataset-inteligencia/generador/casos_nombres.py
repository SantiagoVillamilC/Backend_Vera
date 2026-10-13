"""Casos de vista: persistencia privada (caso 2) y cambio de nombre del negocio (caso 3).

Cada pago marcado tiene una VistaEsperada: lo que ese usuario debe ver en ese momento.
Todos los pagos de un mismo comercio son de usuarios distintos y están separados por semanas
o meses. El nombre esperado se guarda canonizado, la interfaz lo muestra con mostrar().
"""

from datetime import date

from .contexto import Contexto
from .guion import Corrige, Evento, ejecutar_guion, usuario_id_de
from .modelos import Aporte, Destino, VistaEsperada
from .nombres import canonizar
from .reservados import (NOMBRE_LUCY_AHORA, NOMBRE_LUCY_ANTES, NOMBRE_ROBLE_AHORA, NOMBRE_ROBLE_ANTES)

PERSISTENCIA = "persistencia_privada"
CAMBIO_NOMBRE = "cambio_nombre"
CAMBIO_CATEGORIA = "cambio_nombre_categoria"
RUIDO_SIN_CAMBIO = "ruido_sin_cambio"
SIN_CATEGORIA = "sin_categoria"
ALIMENTOS = "alimentos_mercado"


def _ev(usuario: str, destino: Destino, mes: int, dia: int, monto: int, corrige: Corrige | None = None,
        marca: str = "", presentacion: str = "") -> Evento:
    return Evento(usuario, destino, date(2026, mes, dia), 14, monto, corrige, marca, presentacion)


def _eventos_persistencia(d: Destino) -> tuple[Evento, ...]:
    """Comercio sin nombre crudo. A lo corrige primero en privado, B y C coinciden meses después."""
    s = "sin_nombre"
    return (
        _ev("pep-e", d, 4, 10, 45_000, marca="pep-e1", presentacion=s),
        _ev("pep-a", d, 5, 12, 38_000, Corrige("PEPE SUPERMERCADO", ALIMENTOS), presentacion=s),
        _ev("pep-b", d, 6, 28, 52_000, Corrige("Supermercado Don Pepe", ALIMENTOS, "mayusculas"), presentacion=s),
        _ev("pep-c", d, 8, 18, 41_000, Corrige("SUPERMERCADO_DON_PEPE", ALIMENTOS, "mayusculas"), presentacion=s),
        _ev("pep-a", d, 8, 25, 29_000, marca="pep-a2", presentacion=s),
        _ev("pep-d", d, 9, 20, 47_000, marca="pep-d1", presentacion=s),
        _ev("pep-a", d, 9, 28, 33_000, marca="pep-a3", presentacion=s),
    )


def _vistas_persistencia(aporte: Aporte) -> tuple[VistaEsperada, ...]:
    privado = canonizar("PEPE SUPERMERCADO")
    global_ = canonizar("Supermercado Don Pepe")
    a, d, e = (usuario_id_de(aporte, c) for c in ("pep-a", "pep-d", "pep-e"))
    return (
        VistaEsperada("pep-e1", PERSISTENCIA, e, "", SIN_CATEGORIA, "crudo"),
        VistaEsperada("pep-a2", PERSISTENCIA, a, privado, ALIMENTOS, "privado"),
        VistaEsperada("pep-d1", PERSISTENCIA, d, global_, ALIMENTOS, "global"),
        VistaEsperada("pep-a3", PERSISTENCIA, a, privado, ALIMENTOS, "privado"),
    )


def _eventos_cambio(destino: Destino, prefijo: str, fechas: tuple[tuple[int, int], ...],
                    antes: tuple[str, str, str], ahora: tuple[str, str, str]) -> tuple[Evento, ...]:
    """Dos usuarios consolidan el nombre anterior, luego dos corrigen al nombre nuevo y uno lo ve."""
    (n_antes, c_antes, raw_antes), (n_ahora, c_ahora, raw_ahora) = antes, ahora
    f = fechas
    return (
        _ev(f"{prefijo}1", destino, *f[0], 33_000, Corrige(n_antes, c_antes)),
        _ev(f"{prefijo}2", destino, *f[1], 27_500, Corrige(raw_antes, c_antes, "mayusculas")),
        _ev(f"{prefijo}3", destino, *f[2], 44_000, Corrige(n_ahora, c_ahora), marca=f"{prefijo}3"),
        _ev(f"{prefijo}4", destino, *f[3], 36_000, Corrige(raw_ahora, c_ahora, "mayusculas"), marca=f"{prefijo}4"),
        _ev(f"{prefijo}5", destino, *f[4], 25_000, marca=f"{prefijo}5"),
    )


def _vistas_cambio(aporte: Aporte, caso: str, prefijo: str, antes: tuple[str, str],
                   ahora: tuple[str, str]) -> tuple[VistaEsperada, ...]:
    filas = []
    for i, (nombre, categoria) in zip((3, 4, 5), (antes, antes, ahora)):
        filas.append(VistaEsperada(f"{prefijo}{i}", caso, usuario_id_de(aporte, f"{prefijo}{i}"),
                                   canonizar(nombre), categoria, "global"))
    return tuple(filas)


def _eventos_ruido(d: Destino) -> tuple[Evento, ...]:
    """Comercio consolidado sin cambio real: el desacuerdo posterior es solo ruido."""
    v = "vivienda_servicios"
    return (
        _ev("fer-1", d, 4, 12, 22_000, Corrige("Ferretería Los Andes", v)),
        _ev("fer-2", d, 5, 28, 31_000, Corrige("FERRETERIA LOS ANDES", v, "mayusculas")),
        _ev("fer-3", d, 7, 18, 18_500, Corrige("Ferreteria Los Andez", v, "tipeo"), marca="fer-3"),
        _ev("fer-4", d, 8, 30, 26_000, Corrige("FERRETERIA L. ANDES", v, "abreviatura"), marca="fer-4"),
        _ev("fer-5", d, 9, 25, 15_000, Corrige("ferretería los andes", "compras", "etiqueta_erronea"), marca="fer-5"),
    )


def _vistas_ruido(aporte: Aporte, d: Destino) -> tuple[VistaEsperada, ...]:
    return tuple(
        VistaEsperada(f"fer-{i}", RUIDO_SIN_CAMBIO, usuario_id_de(aporte, f"fer-{i}"),
                      d.nombre_canonico, d.categoria, "global")
        for i in (3, 4, 5)
    )


def construir(ctx: Contexto) -> Aporte:
    r = ctx.reservados
    fechas_lucy = ((4, 8), (5, 20), (8, 5), (9, 12), (10, 1))
    fechas_roble = ((4, 15), (6, 3), (8, 28), (9, 17), (10, 2))
    lucy = _eventos_cambio(
        r.lucy, "luc", fechas_lucy,
        (NOMBRE_LUCY_ANTES, ALIMENTOS, "MINIMERCADO LA ESQUINA"),
        (NOMBRE_LUCY_AHORA, ALIMENTOS, "TIENDA DOÑA LUCY"))
    roble = _eventos_cambio(
        r.roble, "rob", fechas_roble,
        (NOMBRE_ROBLE_ANTES, "restaurantes_domicilios", "CAFETERIA EL ROBLE"),
        (NOMBRE_ROBLE_AHORA, "educacion", "LIBRERIA EL ROBLE"))
    eventos = (_eventos_persistencia(r.pepe) + lucy + roble + _eventos_ruido(r.ferreteria))
    aporte = ejecutar_guion(ctx, eventos, r.historiales)
    vistas = (
        _vistas_persistencia(aporte)
        + _vistas_cambio(aporte, CAMBIO_NOMBRE, "luc", (NOMBRE_LUCY_ANTES, ALIMENTOS), (NOMBRE_LUCY_AHORA, ALIMENTOS))
        + _vistas_cambio(aporte, CAMBIO_CATEGORIA, "rob", (NOMBRE_ROBLE_ANTES, "restaurantes_domicilios"),
                         (NOMBRE_ROBLE_AHORA, "educacion"))
        + _vistas_ruido(aporte, r.ferreteria)
    )
    return Aporte(usuarios=aporte.usuarios, movimientos=aporte.movimientos,
                  correcciones=aporte.correcciones, vistas=vistas)
