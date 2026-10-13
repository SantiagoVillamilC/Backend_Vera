"""De filas de movimientos a pagos con identidad. Función pura: no abre archivos ni mira el reloj.

Criterio de qué es un pago (decisión documentada en SUPUESTOS.md):

- La fila es una salida de dinero.
- Su `tipo_asiento` es `liquidacion_confirmada` (comercio), `transferencia_interna` o `despacho_riel`
  (personas). Quedan fuera los depósitos, la retención (incluido el GMF, que no trae llave) y la
  compensación, que es la devolución.
- Su `transferencia_id` no tiene una compensación. Un envío devuelto no es un pago que se completó.
- Tiene llave de destino. Si no la tiene, solo entra cuando el nombre crudo sirve de identidad débil.

Identidad: `("llave", llave_destino)` es el nivel fuerte. `("nombre", nombre_canonico)` es el nivel
degradado y solo se usa sin llave. Las dos formas llevan una etiqueta distinta, así que un nombre
nunca puede chocar con una llave. El MCC no interviene nunca: es una categoría y no una identidad.
"""

import re
import unicodedata
from collections.abc import Iterable, Mapping
from dataclasses import dataclass
from datetime import date, datetime, timedelta, timezone

TIPOS_DE_PAGO = frozenset({"liquidacion_confirmada", "transferencia_interna", "despacho_riel"})
TIPO_DEVOLUCION = "compensacion"
NOMBRES_GENERICOS = frozenset({"PAGO-A-COMERCIO"})
DESFASE_COLOMBIA = timedelta(hours=-5)
LARGO_MAXIMO_NOMBRE = 40
SIGNOS_ELIMINADOS = "'.&"
_SEPARADORES = re.compile(r"[\s_\-]+")

Identidad = tuple[str, str]


@dataclass(frozen=True)
class Pago:
    usuario_id: str
    identidad: Identidad
    ocurrido_en: datetime
    fecha_local: date
    monto_centavos: int
    movimiento_id: int


def parsear_instante(texto: str) -> datetime:
    """Instante UTC de un texto ISO 8601 con Z."""
    return datetime.strptime(texto, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)


def fecha_local_colombia(instante: datetime) -> date:
    """Fecha calendario en Colombia (UTC-5, sin horario de verano). Los intervalos se miden en estas fechas."""
    return (instante + DESFASE_COLOMBIA).date()


def _quitar_tildes_salvo_enie(texto: str) -> str:
    salida = []
    for caracter in texto:
        if caracter == "Ñ":
            salida.append(caracter)
            continue
        descompuesto = unicodedata.normalize("NFD", caracter)
        salida.append("".join(c for c in descompuesto if unicodedata.category(c) != "Mn"))
    return "".join(salida)


def _un_paso(nombre: str) -> str:
    texto = unicodedata.normalize("NFC", nombre).upper()
    texto = _SEPARADORES.sub("-", texto).strip("-")
    texto = _quitar_tildes_salvo_enie(texto)
    texto = "".join(c for c in texto if c not in SIGNOS_ELIMINADOS)
    return texto[:LARGO_MAXIMO_NOMBRE]


def normalizar_nombre(nombre: str) -> str:
    """Nombre canónico según la sección 9 de la especificación (mismas reglas que `canonizar`).

    Se reimplementa aquí para que el análisis no dependa del paquete del generador.
    """
    actual = _un_paso(nombre)
    while True:
        siguiente = _un_paso(actual)
        if siguiente == actual:
            return actual
        actual = siguiente


def identidad_de(fila: Mapping[str, str]) -> Identidad | None:
    """Identidad de la contraparte de un pago, o None si la fila no permite identificarla."""
    llave = (fila.get("llave_destino") or "").strip()
    if llave:
        return ("llave", llave)
    nombre = normalizar_nombre(fila.get("nombre_comercio_crudo") or "")
    if nombre and nombre not in NOMBRES_GENERICOS:
        return ("nombre", nombre)
    return None


def _grupos_devueltos(movimientos: Iterable[Mapping[str, str]]) -> frozenset[str]:
    return frozenset(
        m["transferencia_id"] for m in movimientos
        if m["tipo_asiento"] == TIPO_DEVOLUCION and m.get("transferencia_id"))


def _es_pago_saliente(fila: Mapping[str, str], devueltos: frozenset[str]) -> bool:
    return (fila["direccion"] == "salida" and fila["tipo_asiento"] in TIPOS_DE_PAGO
            and fila.get("transferencia_id", "") not in devueltos)


def extraer_pagos(movimientos: Iterable[Mapping[str, str]]) -> tuple[Pago, ...]:
    """Pagos de las filas dadas, ordenados por (ocurrido_en, movimiento_id). No modifica la entrada."""
    filas = tuple(movimientos)
    devueltos = _grupos_devueltos(filas)
    pagos = []
    for fila in filas:
        if not _es_pago_saliente(fila, devueltos):
            continue
        identidad = identidad_de(fila)
        if identidad is None:
            continue
        instante = parsear_instante(fila["ocurrido_en"])
        pagos.append(Pago(fila["usuario_id"], identidad, instante, fecha_local_colombia(instante),
                          int(fila["monto_centavos"]), int(fila["movimiento_id"])))
    return tuple(sorted(pagos, key=lambda p: (p.ocurrido_en, p.movimiento_id)))
