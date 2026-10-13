"""Presentación de cada movimiento según la variante: nombre crudo, canal y MCC.

Variante A (realidad de hoy): canal llave, MCC vacío, nombre enmascarado, genérico o vacío.
Variante B (QR): una fracción de los pagos a comercio llega por QR con nombre de 25 caracteres
y MCC derivado de la categoría real. La verdad nunca depende de la variante: cada fila usa su
propio generador de azar, con las mismas tres primeras tiradas en A y en B.
"""

import random
from dataclasses import dataclass

from .constantes import PROBABILIDAD_NOMBRE_GENERICO, PROBABILIDAD_NOMBRE_VACIO, TEXTO_GENERICO
from .mcc import mcc_de
from .modelos import Movimiento, NombreVigente
from .nombres import mostrar
from .parametros import Parametros
from .ruido import enmascarar_nombre, nombre_qr
from .tiempo import fecha_co


@dataclass(frozen=True)
class Presentacion:
    tipo_llave: str
    llave_destino: str
    nombre_comercio_crudo: str
    mcc: str
    canal: str


def _sin_destino() -> Presentacion:
    return Presentacion("", "", "", "", "llave")


def _persona(m: Movimiento) -> Presentacion:
    d = m.destino
    nombre = "" if m.tipo_asiento == "compensacion" else enmascarar_nombre(d.nombre_base)
    return Presentacion(d.tipo_llave, d.llave_destino, nombre, "", "llave")


def nombre_vigente(m: Movimiento, historial: dict[str, tuple[NombreVigente, ...]]) -> str:
    """Nombre legible que el comercio tenía en la fecha del movimiento (no el de fin de periodo)."""
    fecha = fecha_co(m.ocurrido_en)
    for h in historial.get(m.destino.llave_destino, ()):
        if h.vigente_desde <= fecha and (h.vigente_hasta is None or fecha <= h.vigente_hasta):
            return mostrar(h.nombre_canonico)
    return m.destino.nombre_base


def _comercio(m: Movimiento, params: Parametros, azar: random.Random, razon_social: str,
              historial: dict[str, tuple[NombreVigente, ...]]) -> Presentacion:
    d = m.destino
    u_qr, u_vacio, u_generico = azar.random(), azar.random(), azar.random()
    if m.presentacion == "sin_nombre":
        return Presentacion(d.tipo_llave, d.llave_destino, "", "", "llave")
    hay_qr = params.variante == "B" and params.fraccion_qr > 0
    es_qr = hay_qr and (u_qr < params.fraccion_qr or m.presentacion == "qr_en_b")
    if es_qr:
        return Presentacion(d.tipo_llave, d.llave_destino, nombre_qr(nombre_vigente(m, historial), azar),
                            mcc_de(m.categoria_real, d.llave_destino), "qr")
    if u_vacio < PROBABILIDAD_NOMBRE_VACIO:
        nombre = ""
    elif u_generico < PROBABILIDAD_NOMBRE_GENERICO:
        nombre = TEXTO_GENERICO
    else:
        nombre = enmascarar_nombre(razon_social)
    return Presentacion(d.tipo_llave, d.llave_destino, nombre, "", "llave")


def presentar(m: Movimiento, params: Parametros, base_azar: int, razones: dict[str, str],
              historial: dict[str, tuple[NombreVigente, ...]]) -> Presentacion:
    if m.destino is None:
        return _sin_destino()
    if not m.es_pago_comercio:
        return _persona(m)
    azar = random.Random(f"{base_azar}-{m.movimiento_id}")
    return _comercio(m, params, azar, razones[m.destino.empresa_id], historial)
