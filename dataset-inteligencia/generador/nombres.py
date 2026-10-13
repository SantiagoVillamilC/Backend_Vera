"""Normalización de nombres de comercio: canonizar (clave guardada) y mostrar (solo interfaz)."""

import re
import unicodedata

LARGO_MAXIMO = 40
SIGNOS_ELIMINADOS = "'.&"
_SEPARADORES = re.compile(r"[\s_\-]+")


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
    return texto[:LARGO_MAXIMO]


def canonizar(nombre: str) -> str:
    """Mayúsculas, separadores a un guion, sin guiones en los bordes, sin tildes (salvo Ñ),
    sin los signos ' . & y recortado a 40. Se repite hasta estabilizar para ser idempotente
    (por ejemplo, quitar un punto entre dos guiones deja un guion doble que el paso siguiente une)."""
    actual = _un_paso(nombre)
    while True:
        siguiente = _un_paso(actual)
        if siguiente == actual:
            return actual
        actual = siguiente


def mostrar(nombre_canonico: str) -> str:
    """Versión para la interfaz: guiones por espacios. No se guarda en ninguna parte."""
    return nombre_canonico.replace("-", " ")
