"""Llaves de pago: solo la intersección entre la norma del Banco de la República y el código.

Patrones del backend verificados en Backend_Centinela/internal/nucleo/directorio.go:34 a :38
(allí el comercio acepta ^[A-Za-z0-9]{6,20}$, más amplio que la forma 00 más 8 dígitos que se genera aquí).
Las diferencias con la norma están documentadas en ESPECIFICACION.md y no se ejercitan aquí.
"""

import random
import re
import string
from collections.abc import Callable

ALFANUMERICOS = string.ascii_letters + string.digits
DOMINIOS_CORREO = ("gmail.com", "hotmail.com", "outlook.com", "yahoo.com", "correo.co")
MAX_LOCAL_CORREO = 30
MAX_DOMINIO_CORREO = 61
PREFIJOS_CORREO = ("juan", "maria", "carlos", "ana", "luis", "paula", "diego", "sofia", "camilo", "laura")

PATRONES = {
    "comercio": re.compile(r"^00[0-9]{8}$"),
    "celular": re.compile(r"^3[0-9]{9}$"),
    "alfanumerico": re.compile(r"^@[A-Za-z0-9]{5,20}$"),
    "documento": re.compile(r"^[0-9]{6,10}$"),
    "correo": re.compile(r"^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}$"),
}


def llave_valida(tipo: str, llave: str) -> bool:
    patron = PATRONES.get(tipo)
    if patron is None or not patron.match(llave):
        return False
    if tipo == "correo":
        local, dominio = llave.split("@")
        return len(local) <= MAX_LOCAL_CORREO and len(dominio) <= MAX_DOMINIO_CORREO
    return True


def _digitos(azar: random.Random, cantidad: int) -> str:
    return "".join(azar.choice(string.digits) for _ in range(cantidad))


def _comercio(azar: random.Random) -> str:
    return "00" + _digitos(azar, 8)


def _celular(azar: random.Random) -> str:
    return "3" + _digitos(azar, 9)


def _documento(azar: random.Random) -> str:
    return azar.choice("123456789") + _digitos(azar, azar.randint(5, 9))


def _alfanumerico(azar: random.Random) -> str:
    return "@" + "".join(azar.choice(ALFANUMERICOS) for _ in range(azar.randint(5, 20)))


def _correo(azar: random.Random) -> str:
    local = azar.choice(PREFIJOS_CORREO) + azar.choice((".", "_", "")) + _digitos(azar, azar.randint(2, 6))
    return f"{local}@{azar.choice(DOMINIOS_CORREO)}"


_FABRICAS = {
    "comercio": _comercio, "celular": _celular, "documento": _documento,
    "alfanumerico": _alfanumerico, "correo": _correo,
}


class GeneradorLlaves:
    """Entrega llaves únicas. Lleva la cuenta de las ya usadas para no repetir ninguna."""

    def __init__(self, azar: random.Random) -> None:
        self._azar = azar
        self._usadas: set[str] = set()

    def nueva(self, tipo: str, predicado: Callable[[str], bool] | None = None) -> str:
        """Llave nueva y única. El predicado opcional filtra candidatas (por ejemplo, para fijar un MCC)."""
        fabrica = _FABRICAS[tipo]
        while True:
            llave = fabrica(self._azar)
            if llave not in self._usadas and (predicado is None or predicado(llave)):
                self._usadas.add(llave)
                return llave
