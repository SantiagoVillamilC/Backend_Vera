"""Constructores de filas de movimiento: depósitos, pagos, envíos con devolución y GMF."""

import itertools
from datetime import datetime

from .constantes import (CATEGORIA_GMF, CENTAVOS_POR_PESO, MONTO_EXENTO_CENTAVOS, SIN_CATEGORIA)
from .modelos import Destino, Movimiento

DESCRIPCION_GMF = "GMF 4x1000"


def calcular_gmf(monto_centavos: int) -> int:
    """4 por mil sobre el envío completo si supera el monto exento por envío (redondeo hacia arriba en .5)."""
    if monto_centavos <= MONTO_EXENTO_CENTAVOS:
        return 0
    return (monto_centavos * 4 + 500) // 1000


def pesos(valor: int) -> int:
    return valor * CENTAVOS_POR_PESO


def particionar(total_pesos: int, tope_pesos: int = 100_000) -> tuple[int, ...]:
    """Reparte un total en pagos de a lo sumo `tope_pesos`, lo más parejos posible."""
    cantidad = -(-total_pesos // tope_pesos)
    base, resto = divmod(total_pesos, cantidad)
    return tuple(base + 1 if i < resto else base for i in range(cantidad))


def deposito(usuario_id: str, instante: datetime, monto: int, categoria: str, descripcion: str,
             marca: str = "") -> Movimiento:
    return Movimiento(usuario_id, instante, "entrada", monto, "deposito", descripcion, categoria, marca=marca)


def _gmf(usuario_id: str, instante: datetime, monto: int, grupo: int) -> tuple[Movimiento, ...]:
    gmf = calcular_gmf(monto)
    if gmf == 0:
        return ()
    return (Movimiento(usuario_id, instante, "salida", gmf, "retencion", DESCRIPCION_GMF,
                       CATEGORIA_GMF, grupo=grupo),)


def pago_comercio(usuario_id: str, instante: datetime, monto: int, destino: Destino,
                  grupos: itertools.count, descripcion: str = "Pago", categoria_real: str = "",
                  marca: str = "", presentacion: str = "") -> tuple[Movimiento, ...]:
    """Pago confirmado a un comercio: una fila, más el GMF si el monto pasa del exento."""
    grupo = next(grupos)
    principal = Movimiento(
        usuario_id, instante, "salida", monto, "liquidacion_confirmada", descripcion,
        categoria_real or destino.categoria, destino, grupo, es_pago_comercio=True,
        marca=marca, presentacion=presentacion,
    )
    return (principal,) + _gmf(usuario_id, instante, monto, grupo)


def tipo_asiento_de_envio(destino: Destino) -> str:
    return "transferencia_interna" if destino.tipo_llave in ("celular", "correo") else "despacho_riel"


def envio_persona(usuario_id: str, instante: datetime, monto: int, destino: Destino,
                  grupos: itertools.count, descripcion: str = "Envío a persona") -> tuple[Movimiento, ...]:
    grupo = next(grupos)
    principal = Movimiento(usuario_id, instante, "salida", monto, tipo_asiento_de_envio(destino),
                           descripcion, SIN_CATEGORIA, destino, grupo, es_persona=True)
    return (principal,) + _gmf(usuario_id, instante, monto, grupo)


def envio_con_devolucion(usuario_id: str, instante: datetime, instante_devolucion: datetime, monto: int,
                         destino: Destino, grupos: itertools.count, tipo_asiento: str,
                         descripcion: str) -> tuple[Movimiento, ...]:
    """Envío que no se completa: sale (retención o despacho), cobra GMF y se compensa por el total."""
    grupo = next(grupos)
    principal = Movimiento(usuario_id, instante, "salida", monto, tipo_asiento, descripcion,
                           SIN_CATEGORIA, destino, grupo, es_persona=True)
    gmf = _gmf(usuario_id, instante, monto, grupo)
    devuelto = monto + sum(m.monto_centavos for m in gmf)
    compensacion = Movimiento(usuario_id, instante_devolucion, "entrada", devuelto, "compensacion",
                              "Devolución de envío no completado", SIN_CATEGORIA, destino, grupo,
                              es_persona=True)
    return (principal,) + gmf + (compensacion,)


def saldo(movimientos: tuple[Movimiento, ...] | list[Movimiento]) -> int:
    return sum(m.monto_centavos if m.direccion == "entrada" else -m.monto_centavos for m in movimientos)


def monto_que_deja_saldo(disponible: int) -> int:
    """Mayor monto en pesos exactos tal que monto más su GMF no supera `disponible` (centavos)."""
    monto = disponible * 1000 // 1004
    while monto + calcular_gmf(monto) > disponible:
        monto -= 1
    while monto + 1 + calcular_gmf(monto + 1) <= disponible:
        monto += 1
    return monto // CENTAVOS_POR_PESO * CENTAVOS_POR_PESO
