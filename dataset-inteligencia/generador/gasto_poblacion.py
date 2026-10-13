"""Población de usuarios de gasto con verdad por diseño (experimento C).

La verdad se etiqueta por la INTENCIÓN con la que se genera cada usuario, nunca por el resultado de correr la
regla, igual que series_fondo.py etiqueta es_recurrente. Cada diseño es positivo en su tipo y negativo en los
otros cuatro, con brechas grandes (por ejemplo 3x o 0,3x la base) que no se ajustan al umbral de la regla.

El módulo cicla la lista DISENOS hasta crear ctx.parametros.gasto_usuarios usuarios. Usa su propio
random.Random derivado de la semilla, separado del flujo principal, para no alterar el resto. Con
gasto_usuarios igual a 0 devuelve un Aporte vacío sin consumir llaves ni movimientos, así la salida de datos
queda byte a byte igual a la de antes.
"""

import random
from datetime import date

from .contexto import Contexto, crear_usuario
from .modelos import Aporte, Aviso, Movimiento, Serie
from .movimientos import deposito, pago_comercio, pesos
from .tiempo import instante_co

FECHA_EVALUACION = date(2026, 10, 20)
MESES_COMPLETOS = (4, 5, 6, 7, 8, 9)
TODOS_LOS_MESES = (4, 5, 6, 7, 8, 9, 10)
DIAS_SEIS = (3, 8, 13, 18, 23, 28)
DIAS_CUATRO = (3, 8, 13, 18)
OCTUBRE = 10

TIPOS_GASTO = ("presupuesto_anticipado", "categoria_sobre_media", "caida_ingreso", "pago_no_alcanza", "cobro_nuevo")
DISENOS = ("presupuesto_pos", "presupuesto_neg", "categoria_pos", "categoria_neg", "caida_pos", "caida_neg",
           "pago_no_alcanza_pos", "cobro_nuevo_pos", "sin_avisos", "tope_excedido")
POSITIVO = {
    "presupuesto_pos": ("presupuesto_anticipado",),
    "categoria_pos": ("categoria_sobre_media",),
    "caida_pos": ("caida_ingreso",),
    "pago_no_alcanza_pos": ("pago_no_alcanza",),
    "cobro_nuevo_pos": ("cobro_nuevo",),
    "tope_excedido": ("presupuesto_anticipado", "categoria_sobre_media", "caida_ingreso", "cobro_nuevo"),
}


def azar_de_gasto(semilla: int) -> random.Random:
    return random.Random(f"{semilla}-gasto")


def _pago(ctx: Contexto, uid: str, destino, mes: int, dia: int, monto_pesos: int) -> tuple[Movimiento, ...]:
    return pago_comercio(uid, instante_co(date(2026, mes, dia), 13), pesos(monto_pesos), destino, ctx.grupos)


def _gastos(ctx: Contexto, uid: str, destino, monto_pesos: int, dias: tuple[int, ...],
            meses: tuple[int, ...]) -> list[Movimiento]:
    """Un pago de `monto_pesos` en cada día de `dias` para cada mes de `meses`. Octubre corta el día de evaluación."""
    filas: list[Movimiento] = []
    for mes in meses:
        for dia in dias:
            if mes == OCTUBRE and dia > FECHA_EVALUACION.day:
                continue
            filas.extend(_pago(ctx, uid, destino, mes, dia, monto_pesos))
    return filas


def _ingresos(ctx: Contexto, uid: str, montos: dict[int, int]) -> list[Movimiento]:
    """Un depósito de salario el día 1 de cada mes de `montos`, que va de mes a monto en pesos."""
    return [deposito(uid, instante_co(date(2026, mes, 1), 9), pesos(monto), "salario", "Salario")
            for mes, monto in montos.items()]


def _presupuesto_pos(ctx: Contexto, azar: random.Random, uid: str):
    """Octubre muy por encima de la base: el exceso va en una categoría nueva como un solo pago."""
    mercado = azar.choice(ctx.comercios_de("alimentos_mercado"))
    compras = azar.choice(ctx.comercios_de("compras"))
    filas = _gastos(ctx, uid, mercado, 200_000, DIAS_SEIS, TODOS_LOS_MESES)
    filas += _ingresos(ctx, uid, {mes: 6_000_000 for mes in TODOS_LOS_MESES})
    filas.extend(_pago(ctx, uid, compras, OCTUBRE, FECHA_EVALUACION.day, 4_000_000))
    return filas, ()


def _presupuesto_neg(ctx: Contexto, azar: random.Random, uid: str):
    """Octubre muy por debajo de la base: todo falso."""
    mercado = azar.choice(ctx.comercios_de("alimentos_mercado"))
    filas = _gastos(ctx, uid, mercado, 200_000, DIAS_SEIS, MESES_COMPLETOS)
    filas += _gastos(ctx, uid, mercado, 180_000, (5, 15), (OCTUBRE,))
    filas += _ingresos(ctx, uid, {mes: 3_000_000 for mes in TODOS_LOS_MESES})
    return filas, ()


def _categoria_pos(ctx: Contexto, azar: random.Random, uid: str):
    """Una categoría muy por encima de su base y el total por debajo: solo categoria_sobre_media."""
    mercado = azar.choice(ctx.comercios_de("alimentos_mercado"))
    transporte = azar.choice(ctx.comercios_de("transporte"))
    filas = _gastos(ctx, uid, mercado, 200_000, (2, 7, 12, 17, 22), MESES_COMPLETOS)
    filas += _gastos(ctx, uid, transporte, 200_000, (10,), MESES_COMPLETOS)
    filas += _gastos(ctx, uid, mercado, 100_000, (5,), (OCTUBRE,))
    filas += _gastos(ctx, uid, transporte, 600_000, (12,), (OCTUBRE,))
    filas += _ingresos(ctx, uid, {mes: 3_000_000 for mes in TODOS_LOS_MESES})
    return filas, ()


def _categoria_neg(ctx: Contexto, azar: random.Random, uid: str):
    """Todas las categorías por debajo de su base: todo falso."""
    mercado = azar.choice(ctx.comercios_de("alimentos_mercado"))
    transporte = azar.choice(ctx.comercios_de("transporte"))
    filas = _gastos(ctx, uid, mercado, 200_000, (2, 7, 12, 17, 22), MESES_COMPLETOS)
    filas += _gastos(ctx, uid, transporte, 200_000, (10,), MESES_COMPLETOS)
    filas += _gastos(ctx, uid, mercado, 100_000, (5,), (OCTUBRE,))
    filas += _gastos(ctx, uid, transporte, 60_000, (12,), (OCTUBRE,))
    filas += _ingresos(ctx, uid, {mes: 3_000_000 for mes in TODOS_LOS_MESES})
    return filas, ()


def _caida_pos(ctx: Contexto, azar: random.Random, uid: str):
    """Septiembre al 30 por ciento de abril a agosto, con MAD cero: R2 cae en el respaldo R1."""
    mercado = azar.choice(ctx.comercios_de("alimentos_mercado"))
    filas = _gastos(ctx, uid, mercado, 200_000, DIAS_SEIS, MESES_COMPLETOS)
    filas += _gastos(ctx, uid, mercado, 200_000, DIAS_CUATRO, (OCTUBRE,))
    ingresos = {mes: 3_000_000 for mes in (4, 5, 6, 7, 8)}
    ingresos[9] = 900_000
    ingresos[OCTUBRE] = 900_000
    filas += _ingresos(ctx, uid, ingresos)
    return filas, ()


def _caida_neg(ctx: Contexto, azar: random.Random, uid: str):
    """Todos los ingresos iguales: todo falso."""
    mercado = azar.choice(ctx.comercios_de("alimentos_mercado"))
    filas = _gastos(ctx, uid, mercado, 200_000, DIAS_SEIS, MESES_COMPLETOS)
    filas += _gastos(ctx, uid, mercado, 200_000, DIAS_CUATRO, (OCTUBRE,))
    filas += _ingresos(ctx, uid, {mes: 2_000_000 for mes in TODOS_LOS_MESES})
    return filas, ()


def _pago_no_alcanza_pos(ctx: Contexto, azar: random.Random, uid: str):
    """Serie mensual de 100.000 el día 20 de abril a septiembre y saldo cero al 20 de octubre."""
    entretenimiento = azar.choice(ctx.comercios_de("entretenimiento"))
    filas = _gastos(ctx, uid, entretenimiento, 100_000, (20,), MESES_COMPLETOS)
    filas += _ingresos(ctx, uid, {mes: 100_000 for mes in MESES_COMPLETOS})
    serie = Serie(f"gasto-{uid}-serie", uid, entretenimiento.llave_destino, True, 30, "regular",
                  "Serie mensual de 100.000 el día 20, de abril a septiembre")
    return filas, (serie,)


def _cobro_nuevo_pos(ctx: Contexto, azar: random.Random, uid: str):
    """Una llave nueva con dos pagos de monto similar en los últimos 35 días (el 7 y el 15 de octubre)."""
    mercado = azar.choice(ctx.comercios_de("alimentos_mercado"))
    compras = azar.choice(ctx.comercios_de("compras"))
    filas = _gastos(ctx, uid, mercado, 200_000, DIAS_SEIS, MESES_COMPLETOS)
    filas += _gastos(ctx, uid, mercado, 200_000, DIAS_CUATRO, (OCTUBRE,))
    filas += _ingresos(ctx, uid, {mes: 3_000_000 for mes in TODOS_LOS_MESES})
    filas.extend(_pago(ctx, uid, compras, OCTUBRE, 7, 50_000))
    filas.extend(_pago(ctx, uid, compras, OCTUBRE, 15, 52_000))
    return filas, ()


def _sin_avisos(ctx: Contexto, azar: random.Random, uid: str):
    """Usuario normal, sin ninguna perturbación: todo falso."""
    mercado = azar.choice(ctx.comercios_de("alimentos_mercado"))
    filas = _gastos(ctx, uid, mercado, 200_000, DIAS_SEIS, MESES_COMPLETOS)
    filas += _gastos(ctx, uid, mercado, 200_000, DIAS_CUATRO, (OCTUBRE,))
    filas += _ingresos(ctx, uid, {mes: 3_000_000 for mes in TODOS_LOS_MESES})
    return filas, ()


def _tope_excedido(ctx: Contexto, azar: random.Random, uid: str):
    """Cuatro intenciones el 20 de octubre: el tope de tres deja fuera categoria_sobre_media (la de menor prioridad)."""
    mercado = azar.choice(ctx.comercios_de("alimentos_mercado"))
    transporte = azar.choice(ctx.comercios_de("transporte"))
    compras = azar.choice(ctx.comercios_de("compras"))
    entretenimiento = azar.choice(ctx.comercios_de("entretenimiento"))
    filas = _gastos(ctx, uid, mercado, 200_000, DIAS_SEIS, MESES_COMPLETOS)
    filas += _gastos(ctx, uid, transporte, 200_000, (10,), MESES_COMPLETOS)
    filas += _gastos(ctx, uid, mercado, 100_000, (5,), (OCTUBRE,))
    filas += _gastos(ctx, uid, transporte, 600_000, (12,), (OCTUBRE,))
    filas.extend(_pago(ctx, uid, compras, OCTUBRE, FECHA_EVALUACION.day, 4_000_000))
    filas.extend(_pago(ctx, uid, entretenimiento, OCTUBRE, 7, 50_000))
    filas.extend(_pago(ctx, uid, entretenimiento, OCTUBRE, 15, 52_000))
    ingresos = {mes: 3_000_000 for mes in (4, 5, 6, 7, 8)}
    ingresos[9] = 900_000
    ingresos[OCTUBRE] = 3_000_000
    filas += _ingresos(ctx, uid, ingresos)
    return filas, ()


CONSTRUCTORES = {
    "presupuesto_pos": _presupuesto_pos,
    "presupuesto_neg": _presupuesto_neg,
    "categoria_pos": _categoria_pos,
    "categoria_neg": _categoria_neg,
    "caida_pos": _caida_pos,
    "caida_neg": _caida_neg,
    "pago_no_alcanza_pos": _pago_no_alcanza_pos,
    "cobro_nuevo_pos": _cobro_nuevo_pos,
    "sin_avisos": _sin_avisos,
    "tope_excedido": _tope_excedido,
}


def _verdad(usuario_id: str, diseno: str) -> tuple[Aviso, ...]:
    """Cinco filas de verdad, una por tipo, con la intención de diseño en mediana y en media."""
    positivos = POSITIVO.get(diseno, ())
    return tuple(
        Aviso(usuario_id, tipo, FECHA_EVALUACION, tipo in positivos, tipo in positivos,
              f"{diseno}: {tipo} {'dispara' if tipo in positivos else 'no dispara'} por diseño")
        for tipo in TIPOS_GASTO
    )


def construir(ctx: Contexto) -> Aporte:
    """Usuarios de gasto ciclando DISENOS, con su verdad por diseño. Vacío si gasto_usuarios es 0."""
    total = ctx.parametros.gasto_usuarios
    if total <= 0:
        return Aporte()
    azar = azar_de_gasto(ctx.semilla)
    usuarios, movimientos, avisos, series = [], [], [], []
    for indice in range(total):
        diseno = DISENOS[indice % len(DISENOS)]
        usuario = crear_usuario(ctx, f"gasto-{indice:04d}")
        filas, del_usuario = CONSTRUCTORES[diseno](ctx, azar, usuario.usuario_id)
        usuarios.append(usuario)
        movimientos.extend(filas)
        series.extend(del_usuario)
        avisos.extend(_verdad(usuario.usuario_id, diseno))
    return Aporte(usuarios=tuple(usuarios), movimientos=tuple(sorted(movimientos, key=lambda m: m.ocurrido_en)),
                  avisos=tuple(avisos), series=tuple(series))
