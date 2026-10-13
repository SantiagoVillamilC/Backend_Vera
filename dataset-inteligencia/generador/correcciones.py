"""Correcciones aleatorias de la multitud: cada usuario que pagó en un comercio corrige con probabilidad p.

El nombre_puesto es el nombre crudo que teclea el usuario, con ruido de formato, tildes,
abreviaturas y tipeo según --ruido-nombre. Con --tasa-etiqueta-erronea pone una categoría equivocada.
"""

import random
from datetime import datetime, timedelta, timezone

from .constantes import CATEGORIAS_ERRONEAS
from .modelos import Correccion, Movimiento
from .parametros import Parametros
from .ruido import variar_nombre

DEMORA_MINIMA_HORAS = 1.0
DEMORA_MAXIMA_HORAS = 72.0


def _primeros_pagos(movimientos: tuple[Movimiento, ...], guionadas: frozenset[str]) -> list[Movimiento]:
    vistos: dict[tuple[str, str], Movimiento] = {}
    for m in movimientos:
        if m.es_pago_comercio and m.direccion == "salida" and m.destino.llave_destino not in guionadas:
            vistos.setdefault((m.usuario_id, m.destino.llave_destino), m)
    return list(vistos.values())


def _categoria_equivocada(azar: random.Random, real: str) -> str:
    return azar.choice([c for c in CATEGORIAS_ERRONEAS if c != real])


def _una(pago: Movimiento, azar: random.Random, params: Parametros, limite) -> Correccion | None:
    """Corrección de un pago, o None si no corrige o si el corte no deja tiempo para hacerlo."""
    if azar.random() >= params.tasa_correccion:
        return None
    instante = pago.ocurrido_en + timedelta(hours=azar.uniform(DEMORA_MINIMA_HORAS, DEMORA_MAXIMA_HORAS))
    if pago.ocurrido_en + timedelta(hours=DEMORA_MINIMA_HORAS) > limite:
        return None
    instante = min(instante, limite)
    destino = pago.destino
    nombre, ruido = variar_nombre(destino.nombre_base, azar, params.ruido_nombre)
    erronea = azar.random() < params.tasa_etiqueta_erronea
    categoria = _categoria_equivocada(azar, destino.categoria) if erronea else destino.categoria
    return Correccion(pago.usuario_id, destino.llave_destino, nombre, categoria, instante,
                      categoria == destino.categoria, "etiqueta_erronea" if erronea else ruido)


def generar_correcciones(params: Parametros, azar: random.Random, movimientos: tuple[Movimiento, ...],
                         guionadas: frozenset[str]) -> tuple[Correccion, ...]:
    limite = datetime(params.fecha_fin.year, params.fecha_fin.month, params.fecha_fin.day, 23, 59, 59,
                      tzinfo=timezone.utc)
    resultado = (_una(p, azar, params, limite) for p in _primeros_pagos(movimientos, guionadas))
    return tuple(c for c in resultado if c is not None)
