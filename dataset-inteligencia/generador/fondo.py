"""Población de fondo: usuarios adicionales con gasto aleatorio sobre comercios del catálogo.

Rellena exactamente las filas que dejan libres los escenarios fijos.
"""

import itertools
import random
from datetime import date

from .constantes import INICIO_PERIODO, MONTO_EXENTO_CENTAVOS
from .contexto import Contexto, crear_usuario
from .modelos import Aporte, Destino, Movimiento, Usuario
from .series_fondo import construir_series
from .movimientos import deposito, envio_persona, pago_comercio, pesos
from .tiempo import instante_co, meses_hasta

MONTOS_RESERVADOS = (150_000, 200_000)
CATEGORIAS_INGRESO = ("transferencia_recibida", "reembolso", "otros_ingresos")
TOPE_UNITARIO_PESOS = MONTO_EXENTO_CENTAVOS // 100
ESTRATOS = 6
FRACCION_SERIES = 0.40


def cantidad_de_usuarios(solicitados: int | None, filas: int) -> int:
    base = solicitados if solicitados is not None else max(5, filas // 40)
    return max(1, min(base, filas))


def _monto_pesos(azar: random.Random, tope: int | None = None) -> int:
    monto = int(min(400_000, max(5_000, round(azar.lognormvariate(10.8, 0.8), -2))))
    if monto in MONTOS_RESERVADOS:
        monto += 100
    return min(monto, tope) if tope else monto


def _instante_aleatorio(azar: random.Random, fecha_fin: date, estrato: int = -1):
    """Instante local entre las 7 y las 18 horas. Con `estrato` solo cae en esa sexta parte de la ventana."""
    dias = (fecha_fin - INICIO_PERIODO).days + 1
    desde, hasta = 0, dias
    if estrato >= 0:
        largo = dias // ESTRATOS
        desde = estrato * largo
        hasta = dias if estrato == ESTRATOS - 1 else desde + largo
    dia = date.fromordinal(INICIO_PERIODO.toordinal() + azar.randrange(desde, hasta))
    return instante_co(dia, azar.randrange(7, 19), azar.randrange(60), azar.randrange(60))


def _salarios(ctx: Contexto, azar: random.Random, usuarios: list[Usuario], restantes: int) -> list[Movimiento]:
    fin = ctx.parametros.fecha_fin
    meses = meses_hasta(fin)
    con_salario = min(len(usuarios), (restantes // 3) // len(meses))
    filas: list[Movimiento] = []
    for usuario in usuarios[:con_salario]:
        monto = pesos(azar.randrange(15, 60) * 100_000)
        for anio, mes in meses:
            dia = azar.randrange(1, 4)
            if (anio, mes) == (fin.year, fin.month):
                dia = min(dia, fin.day)
            instante = instante_co(date(anio, mes, dia), 9)
            filas.append(deposito(usuario.usuario_id, instante, monto, "salario", "Salario"))
    return filas


def _libre(uid: str, bloqueados: set, elegir) -> Destino:
    """Destino que el usuario no usa en una serie etiquetada. Reintenta y, si no hay otro, acepta el último."""
    destino = elegir()
    for _ in range(25):
        if (uid, destino.llave_destino) not in bloqueados:
            break
        destino = elegir()
    return destino


def _unidad(ctx: Contexto, azar: random.Random, uid: str, restantes: int, acumulados: list[float],
            visitas: dict[str, int], bloqueados: set) -> tuple[Movimiento, ...]:
    """Una fila (depósito, pago o envío) o dos si lleva GMF. Con una fila libre nunca cobra GMF.

    Los pagos sucesivos a un mismo comercio rotan entre las sextas partes de la ventana, para que
    quienes pagan en él lo hagan en momentos muy distintos, separados por meses. Nunca se paga a una
    llave que el usuario ya usa en una serie etiquetada, para no contaminar esa serie.
    """
    fecha_fin = ctx.parametros.fecha_fin
    tope = TOPE_UNITARIO_PESOS if restantes == 1 else None
    tipo = azar.random()
    if tipo < 0.10:
        categoria = azar.choice(CATEGORIAS_INGRESO)
        instante = _instante_aleatorio(azar, fecha_fin)
        return (deposito(uid, instante, pesos(_monto_pesos(azar)), categoria, "Ingreso recibido"),)
    if tipo < 0.80:
        destino = _libre(uid, bloqueados, lambda: azar.choices(ctx.catalogo, cum_weights=acumulados, k=1)[0])
        n = visitas.get(destino.llave_destino, 0)
        visitas[destino.llave_destino] = n + 1
        instante = _instante_aleatorio(azar, fecha_fin, n % ESTRATOS)
        return pago_comercio(uid, instante, pesos(_monto_pesos(azar, tope)), destino, ctx.grupos)
    instante = _instante_aleatorio(azar, fecha_fin)
    persona = _libre(uid, bloqueados, lambda: azar.choice(ctx.personas))
    return envio_persona(uid, instante, pesos(_monto_pesos(azar, tope)), persona, ctx.grupos)


def construir_fondo(ctx: Contexto, azar: random.Random, restantes: int) -> Aporte:
    if restantes <= 0:
        return Aporte()
    cantidad = cantidad_de_usuarios(ctx.parametros.usuarios, restantes)
    usuarios = [crear_usuario(ctx, f"fondo-{i}") for i in range(cantidad)]
    filas = _salarios(ctx, azar, usuarios, restantes)
    presupuesto = min(int(FRACCION_SERIES * restantes), restantes - len(filas))
    bloqueados: set = set()
    visitas: dict[str, int] = {}
    de_series, series = construir_series(ctx, azar, usuarios, presupuesto, bloqueados, visitas)
    filas.extend(de_series)
    acumulados = list(itertools.accumulate(1 / (i + 1) ** 0.7 for i in range(len(ctx.catalogo))))
    while len(filas) < restantes:
        uid = azar.choice(usuarios).usuario_id
        filas.extend(_unidad(ctx, azar, uid, restantes - len(filas), acumulados, visitas, bloqueados))
    return Aporte(usuarios=tuple(usuarios), movimientos=tuple(filas), series=series)
