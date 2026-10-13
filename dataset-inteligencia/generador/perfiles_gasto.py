"""Perfiles 4 y 5 y el usuario de los casos de movimiento (envío fallido e indeterminado)."""

import random
from datetime import date

from .contexto import Contexto, crear_usuario
from .modelos import Aporte, Aviso, Movimiento
from .movimientos import deposito, envio_con_devolucion, pago_comercio, pesos
from .perfiles_ingreso import corte_de_octubre, pagos_de_octubre
from .tiempo import instante_co, meses_hasta, ultimo_dia

PAGOS_POR_MES_P4 = (10, 13, 11, 13, 12, 13)
PAGO_UNITARIO_P4 = 100_000
FECHA_AVISO_P4 = date(2026, 10, 20)


def _pagos_del_mes_p4(ctx: Contexto, destino, uid: str, mes: int, cantidad: int) -> list[Movimiento]:
    dias = ultimo_dia(2026, mes)
    filas: list[Movimiento] = []
    for i in range(cantidad):
        dia = 1 + (i * dias) // cantidad
        filas.extend(pago_comercio(uid, instante_co(date(2026, mes, dia), 15), pesos(PAGO_UNITARIO_P4),
                                   destino, ctx.grupos))
    return filas


def _octubre_p4(ctx: Contexto, destino, uid: str) -> list[Movimiento]:
    """12 pagos hasta el 19 (acumulado igual al promedio) y el 13.º el día 20 (lo supera)."""
    filas: list[Movimiento] = []
    for i in range(12):
        filas.extend(pago_comercio(uid, instante_co(date(2026, 10, 1 + (i * 18) // 11), 15),
                                   pesos(PAGO_UNITARIO_P4), destino, ctx.grupos))
    filas.extend(pago_comercio(uid, instante_co(FECHA_AVISO_P4, 15), pesos(PAGO_UNITARIO_P4),
                               destino, ctx.grupos))
    return filas


def perfil_4(ctx: Contexto, azar: random.Random) -> Aporte:
    """Gasto acumulado de octubre que supera el promedio mensual el 20 de octubre.

    Es la única excepción a --fecha-fin: sus pagos de octubre llegan hasta el día 20 porque el
    aviso depende de esa fecha.
    """
    u = crear_usuario(ctx, "p4")
    uid = u.usuario_id
    filas = [deposito(uid, instante_co(date(2026, m, 1), 9), pesos(3_000_000), "salario", "Salario")
             for _, m in meses_hasta(date(2026, 10, 1))]
    mercado = azar.choice(ctx.comercios_de("alimentos_mercado"))
    for mes, cantidad in zip(range(4, 10), PAGOS_POR_MES_P4):
        filas.extend(_pagos_del_mes_p4(ctx, mercado, uid, mes, cantidad))
    filas.extend(_octubre_p4(ctx, mercado, uid))
    aviso = Aviso(uid, "presupuesto_anticipado", FECHA_AVISO_P4, True, True,
                  "Gasto acumulado de octubre 1.300.000 el día 20, sobre el promedio mensual de abril a "
                  "septiembre de 1.200.000 (mediana 1.250.000)")
    categoria = Aviso(uid, "categoria_sobre_media", FECHA_AVISO_P4, True, True,
                      "alimentos_mercado concentra todo el gasto: 1.300.000 el día 20 de octubre frente a mediana "
                      "de 1.250.000 y media de 1.200.000")
    return Aporte(usuarios=(u,), movimientos=tuple(sorted(filas, key=lambda m: m.ocurrido_en)),
                  avisos=(aviso, categoria))


def perfil_5(ctx: Contexto, azar: random.Random) -> Aporte:
    """Usuario creado el 15 de agosto: historial insuficiente, no debe disparar avisos."""
    u = crear_usuario(ctx, "p5")
    uid = u.usuario_id
    restaurante = azar.choice(ctx.comercios_de("restaurantes_domicilios"))
    corte = corte_de_octubre(ctx)
    filas = [deposito(uid, instante_co(date(2026, 8, 15), 9), pesos(2_000_000), "salario", "Salario"),
             deposito(uid, instante_co(date(2026, 9, 1), 9), pesos(2_000_000), "salario", "Salario"),
             deposito(uid, instante_co(date(2026, 10, 1), 9), pesos(2_000_000), "salario", "Salario")]
    for mes, dias in ((8, (18, 27)), (9, (6, 14, 22))):
        for dia in dias:
            filas.extend(pago_comercio(uid, instante_co(date(2026, mes, dia), 13), pesos(100_000),
                                       restaurante, ctx.grupos))
    filas.extend(pagos_de_octubre(ctx, uid, restaurante, corte, 6, 100_000))
    aviso = Aviso(uid, "categoria_sobre_media", corte, False, False,
                  "historial insuficiente: usuario creado el 2026-08-15, menos de 3 meses completos de datos")
    presupuesto = Aviso(uid, "presupuesto_anticipado", corte, False, False,
                        "historial insuficiente: usuario creado el 2026-08-15, menos de 3 meses completos de datos")
    return Aporte(usuarios=(u,), movimientos=tuple(sorted(filas, key=lambda m: m.ocurrido_en)),
                  avisos=(aviso, presupuesto))


def casos_de_envio(ctx: Contexto, azar: random.Random) -> Aporte:
    """Envío de 200.000 que falla (5 al 6 de septiembre) y de 150.000 indeterminado (30 de septiembre al 1 de octubre)."""
    u = crear_usuario(ctx, "m1")
    uid = u.usuario_id
    fallido, indeterminado = azar.sample(ctx.personas, 2)
    filas = [deposito(uid, instante_co(date(2026, 9, 1), 9), pesos(2_000_000), "salario", "Salario")]
    filas.extend(envio_con_devolucion(
        uid, instante_co(date(2026, 9, 5), 10), instante_co(date(2026, 9, 6), 9), pesos(200_000),
        fallido, ctx.grupos, "retencion", "Envío a persona"))
    filas.extend(envio_con_devolucion(
        uid, instante_co(date(2026, 9, 30), 18), instante_co(date(2026, 10, 1), 10), pesos(150_000),
        indeterminado, ctx.grupos, "despacho_riel", "Envío a persona"))
    for dia in (10, 15, 20):
        filas.extend(pago_comercio(uid, instante_co(date(2026, 9, dia), 12), pesos(azar.randrange(30, 81) * 1000),
                                   azar.choice(ctx.catalogo), ctx.grupos))
    return Aporte(usuarios=(u,), movimientos=tuple(sorted(filas, key=lambda m: m.ocurrido_en)))
