"""Perfiles 1, 2 y 3: ingreso estable, ingreso que cae e ingreso irregular."""

import random
from datetime import date

from .contexto import Contexto, crear_usuario
from .modelos import Aporte, Aviso, Movimiento, Serie
from .movimientos import (calcular_gmf, deposito, monto_que_deja_saldo, pago_comercio, particionar,
                          pesos, saldo)
from .tiempo import fin_de_mes, instante_co, meses_hasta

OCTUBRE_1 = date(2026, 10, 1)
GASTOS_P2 = ("alimentos_mercado", "transporte", "salud", "alimentos_mercado", "restaurantes_domicilios",
             "compras", "alimentos_mercado", "entretenimiento")
SALDO_OBJETIVO_P2_PESOS = 36_000


def pagos_de_octubre(ctx: Contexto, usuario_id: str, destino, corte: date, cantidad: int,
                     monto: int) -> tuple[Movimiento, ...]:
    """`cantidad` pagos de `monto` pesos repartidos entre el 1 de octubre y el corte."""
    dias = (corte - OCTUBRE_1).days + 1
    filas: list[Movimiento] = []
    for i in range(cantidad):
        instante = instante_co(date(2026, 10, 1 + (i * dias) // cantidad), 13)
        filas.extend(pago_comercio(usuario_id, instante, pesos(monto), destino, ctx.grupos))
    return tuple(filas)


def corte_de_octubre(ctx: Contexto) -> date:
    return min(ctx.parametros.fecha_fin, date(2026, 10, 31))


def perfil_1(ctx: Contexto, azar: random.Random) -> Aporte:
    """Restaurantes 300.000 al mes, julio con 2.400.000 (boda), octubre con 600.000 al corte."""
    u = crear_usuario(ctx, "p1")
    restaurante = azar.choice(ctx.comercios_de("restaurantes_domicilios"))
    corte = corte_de_octubre(ctx)
    filas: list[Movimiento] = []
    for anio, mes in meses_hasta(corte):
        filas.append(deposito(u.usuario_id, instante_co(date(anio, mes, 1), 9), pesos(4_000_000),
                              "salario", "Salario"))
    for mes in (4, 5, 6, 8, 9):
        for dia in (6, 14, 22):
            filas.extend(pago_comercio(u.usuario_id, instante_co(date(2026, mes, dia), 13), pesos(100_000),
                                       restaurante, ctx.grupos))
    filas.extend(pago_comercio(u.usuario_id, instante_co(date(2026, 7, 18), 20), pesos(2_400_000),
                               restaurante, ctx.grupos, "Boda"))
    filas.extend(pagos_de_octubre(ctx, u.usuario_id, restaurante, corte, 6, 100_000))
    aviso = Aviso(u.usuario_id, "categoria_sobre_media", corte, True, False,
                  "restaurantes_domicilios: octubre 600.000 frente a mediana de 6 meses 300.000 y media 650.000 "
                  "(la media queda inflada por la boda de julio)")
    presupuesto = Aviso(u.usuario_id, "presupuesto_anticipado", corte, True, False,
                        "Gasto total de octubre 600.000 frente a mediana mensual de 300.000 (dispara) y media "
                        "de 650.000 (no dispara, la media queda inflada por la boda de julio)")
    return Aporte(usuarios=(u,), movimientos=tuple(filas), avisos=(aviso, presupuesto))


def _gastos_mensuales_p2(ctx: Contexto, azar: random.Random, uid: str, internet) -> list[Movimiento]:
    filas: list[Movimiento] = []
    for mes in range(4, 10):
        filas.extend(pago_comercio(uid, instante_co(date(2026, mes, 5), 10), pesos(89_900), internet,
                                   ctx.grupos, "Internet"))
        for i, parte in enumerate(particionar(800_000 - 89_900)):
            destino = azar.choice(ctx.comercios_de(GASTOS_P2[i]))
            filas.extend(pago_comercio(uid, instante_co(date(2026, mes, 7 + 3 * i), 12), pesos(parte),
                                       destino, ctx.grupos))
    return filas


def perfil_2(ctx: Contexto, azar: random.Random) -> Aporte:
    """Ingreso 1.000.000, gasto 800.000 e internet de 89.900 el día 5. El saldo del corte se calcula."""
    u = crear_usuario(ctx, "p2")
    uid, internet = u.usuario_id, ctx.reservados.internet
    filas = [deposito(uid, instante_co(date(2026, 4, 1), 8), pesos(800_000), "otros_ingresos", "Saldo inicial")]
    for mes in range(4, 10):
        monto = 600_000 if mes == 8 else 1_000_000
        filas.append(deposito(uid, instante_co(fin_de_mes(2026, mes), 21, 30), pesos(monto), "salario", "Salario"))
    filas.extend(_gastos_mensuales_p2(ctx, azar, uid, internet))
    compra, saldo_final = _compra_que_agota_el_saldo(ctx, azar, uid, filas)
    avisos = (
        Aviso(uid, "caida_ingreso", date(2026, 8, 31), True, True,
              "Ingreso de agosto 600.000 frente a 1.000.000 de los meses anteriores"),
        Aviso(uid, "pago_no_alcanza", date(2026, 10, 3), True, True,
              f"Internet de 89.900 el 2026-10-05 y saldo proyectado de {saldo_final // 100} pesos"),
        Aviso(uid, "presupuesto_anticipado", date(2026, 10, 2), True, True,
              "La compra del 2 de octubre lleva el gasto del mes muy por encima del promedio mensual de 800.000"),
        Aviso(uid, "categoria_sobre_media", date(2026, 10, 2), True, True,
              "compras: la cuota inicial del 2 de octubre supera de lejos la mediana y la media mensuales de "
              "la categoría (cerca de 88.700 al mes)"),
    )
    serie = Serie("internet-p2", uid, internet.llave_destino, True, 30, "regular",
                  "Internet de 89.900 el día 5 de cada mes de abril a septiembre")
    return Aporte(usuarios=(u,), movimientos=tuple(sorted(filas, key=lambda m: m.ocurrido_en)) + compra,
                  avisos=avisos, series=(serie,))


def _compra_que_agota_el_saldo(ctx: Contexto, azar: random.Random, uid: str,
                               filas: list[Movimiento]) -> tuple[tuple[Movimiento, ...], int]:
    """Compra del 2 de octubre cuyo monto se calcula para dejar el saldo muy por debajo de 89.900."""
    disponible = saldo(filas) - pesos(SALDO_OBJETIVO_P2_PESOS)
    monto = monto_que_deja_saldo(disponible)
    tienda = azar.choice(ctx.comercios_de("compras"))
    compra = pago_comercio(uid, instante_co(date(2026, 10, 2), 14), monto, tienda, ctx.grupos,
                           "Cuota inicial de compra")
    return compra, saldo(filas) - monto - calcular_gmf(monto)


def perfil_3(ctx: Contexto, azar: random.Random) -> Aporte:
    """Ingreso irregular y Claro internet: día 5, un mes día 9 y alza de 5,6 % en septiembre."""
    u = crear_usuario(ctx, "p3")
    uid, internet = u.usuario_id, ctx.reservados.internet
    corte = ctx.parametros.fecha_fin
    filas = [deposito(uid, instante_co(date(a, m, 1), 9), pesos(azar.randrange(40, 121) * 10_000),
                      "otros_ingresos", "Ingreso por honorarios") for a, m in meses_hasta(corte)]
    claro = ((4, 5, 89_900), (5, 5, 89_900), (6, 5, 89_900), (7, 5, 89_900), (8, 9, 89_900), (9, 5, 94_900))
    for mes, dia, monto in claro:
        filas.extend(pago_comercio(uid, instante_co(date(2026, mes, dia), 10), pesos(monto), internet,
                                   ctx.grupos, "Claro internet"))
    filas.extend(_compras_p3(ctx, azar, uid))
    detalle = ("Claro internet: día 5 de abril a julio, día 9 en agosto y alza de 5,6 % (de 89.900 a 94.900) "
               "el día 5 de septiembre. Es una sola serie")
    serie = Serie("claro-p3", uid, internet.llave_destino, True, 30, "regular", detalle)
    return Aporte(usuarios=(u,), movimientos=tuple(sorted(filas, key=lambda m: m.ocurrido_en)),
                  avisos=_avisos_falsos_p3(uid, corte), series=(serie,))


def _compras_p3(ctx: Contexto, azar: random.Random, uid: str) -> list[Movimiento]:
    filas: list[Movimiento] = []
    for mes in range(4, 10):
        for dia, categoria in ((8, "alimentos_mercado"), (15, "transporte"), (23, "alimentos_mercado")):
            monto = azar.randrange(40_000, 100_001, 500)
            filas.extend(pago_comercio(uid, instante_co(date(2026, mes, dia), 12), pesos(monto),
                                       azar.choice(ctx.comercios_de(categoria)), ctx.grupos))
    return filas


def _avisos_falsos_p3(uid: str, corte: date) -> tuple[Aviso, ...]:
    return (
        Aviso(uid, "cobro_nuevo", corte, False, False,
              "El cambio de día y de monto de Claro internet no es un cobro nuevo, es la misma serie"),
        Aviso(uid, "pago_no_alcanza", corte, False, False, "Saldo suficiente para el pago de 94.900"),
        Aviso(uid, "caida_ingreso", corte, False, False,
              "El ingreso es irregular entre 400.000 y 1.200.000, la variación es normal en este perfil"),
    )
