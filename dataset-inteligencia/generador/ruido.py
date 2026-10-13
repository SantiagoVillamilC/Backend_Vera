"""Enmascarado de nombres y variantes con ruido (mayúsculas, tildes, abreviaturas, tipeo)."""

import random
import unicodedata

from .constantes import CIUDAD_QR, LIMITE_NOMBRE_QR, TIPOS_RUIDO_NOMBRE

ABREVIATURAS = {
    "SUPERMERCADO": "SUPER", "DROGUERÍA": "DROG", "RESTAURANTE": "REST", "TIENDAS": "TDAS",
    "TIENDA": "TDA", "CALLE": "CL", "AVENIDA": "AV", "ALMACÉN": "ALM", "PANADERÍA": "PAN",
    "CLÍNICA": "CLIN", "MINIMERCADO": "MINIMERC", "FERRETERÍA": "FERRET", "CAFETERÍA": "CAFET",
    "LIBRERÍA": "LIBR", "CHAPINERO": "CHAPI", "USAQUÉN": "USAQ", "KENNEDY": "KENN",
    "ENGATIVÁ": "ENGA", "SUPERTIENDAS": "SUPERTDAS", "LOS": "L.", "LAS": "L.", "DON": "D.",
    "ESTACIÓN": "EST", "LABORATORIO": "LAB", "COMIDAS": "COM",
}


def quitar_tildes(texto: str) -> str:
    descompuesto = unicodedata.normalize("NFD", texto)
    return unicodedata.normalize("NFC", "".join(c for c in descompuesto if unicodedata.category(c) != "Mn"))


def _enmascarar_palabra(palabra: str) -> str:
    largo = len(palabra)
    if largo <= 2:
        return palabra
    inicio = 2 if largo >= 6 else 1
    fin = 1 if largo >= 6 else (2 if largo == 5 else 1)
    return palabra[:inicio] + "X" * (largo - inicio - fin) + palabra[largo - fin:]


def enmascarar_nombre(nombre: str) -> str:
    """Conserva las letras de los extremos de cada palabra y reemplaza el medio por X."""
    return " ".join(_enmascarar_palabra(p) for p in nombre.split(" "))


def _con_caso(palabra: str, caso: str, azar: random.Random) -> str:
    if caso == "lower":
        return palabra.lower()
    if caso == "title":
        return palabra.capitalize()
    if caso == "mixto":
        return "".join(c.upper() if azar.random() < 0.5 else c.lower() for c in palabra)
    return palabra.upper()


def variante_formato(nombre: str, azar: random.Random) -> str:
    """Variante de formato tecleada por un usuario: minúsculas, mixto, guion bajo, guion o espacios.

    Todas se colapsan a la misma clave con canonizar(), por eso cuentan como ruido de 'mayusculas'.
    """
    palabras = nombre.split(" ")
    while True:
        caso = azar.choice(("lower", "title", "mixto", "upper"))
        separador = azar.choice((" ", "_", "-"))
        if (caso, separador) != ("upper", " "):
            break
    return separador.join(_con_caso(p, caso, azar) for p in palabras)


_ABREVIATURAS_SIN_TILDES = {quitar_tildes(k): v for k, v in ABREVIATURAS.items()}


def abreviar(nombre: str) -> str:
    """Abrevia palabras conocidas, con o sin tildes (los nombres canonizados no las traen)."""
    return " ".join(_ABREVIATURAS_SIN_TILDES.get(quitar_tildes(p.upper()), p) for p in nombre.split(" "))


def _tipeo(nombre: str, azar: random.Random) -> str:
    posiciones = [i for i, c in enumerate(nombre) if c.isalpha()]
    for _ in range(12):
        i = azar.choice(posiciones[:-1] or posiciones)
        opcion = azar.randrange(3)
        if opcion == 0 and i + 1 < len(nombre):
            candidato = nombre[:i] + nombre[i + 1] + nombre[i] + nombre[i + 2:]
        elif opcion == 1:
            candidato = nombre[:i] + nombre[i + 1:]
        else:
            candidato = nombre[:i] + nombre[i] + nombre[i:]
        if candidato != nombre:
            return candidato
    return nombre


def _aplicar(tipo: str, nombre: str, azar: random.Random) -> str:
    if tipo == "mayusculas":
        return variante_formato(nombre, azar)
    if tipo == "tildes":
        return quitar_tildes(nombre)
    if tipo == "abreviatura":
        return abreviar(nombre)
    return _tipeo(nombre, azar)


def variar_nombre(nombre: str, azar: random.Random, probabilidad: float) -> tuple[str, str]:
    """Devuelve (texto, tipo_ruido). Si ningún ruido cambia el texto, devuelve (nombre, 'ninguno')."""
    if azar.random() >= probabilidad:
        return nombre, "ninguno"
    tipos = list(TIPOS_RUIDO_NOMBRE)
    azar.shuffle(tipos)
    for tipo in tipos:
        candidato = _aplicar(tipo, nombre, azar)
        if candidato != nombre:
            return candidato, tipo
    return nombre, "ninguno"


def nombre_qr(nombre: str, azar: random.Random, ciudad: str = CIUDAD_QR) -> str:
    """Campo 59 del QR: mayúsculas sin tildes, a veces con ciudad pegada, truncado a 25."""
    base = abreviar(nombre) if azar.random() < 0.25 else nombre
    base = quitar_tildes(base).upper()
    if azar.random() < 0.35:
        base = base + azar.choice(("", " ")) + ciudad
    return base[:LIMITE_NOMBRE_QR].rstrip()
