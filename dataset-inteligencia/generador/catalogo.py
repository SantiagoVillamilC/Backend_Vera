"""Catálogo de empresas, sucursales (llaves de comercio) y personas del mundo sintético."""

import random
from collections.abc import Callable

from .constantes import CATEGORIAS_COMERCIO, SIN_CATEGORIA
from .llaves import GeneradorLlaves
from .nombres import canonizar
from .modelos import Destino, Empresa, uuid_determinista

# Únicas empresas reales: cadenas de retail con fuente pública (sitio oficial de cada cadena).
# La razón social es el nombre comercial público, no la razón social legal.
# Cada sucursal lleva su plantilla de construcción: True si el nombre antepone una palabra que declara
# la naturaleza del negocio (TIENDAS, SUPERTIENDAS), False si es solo marca más zona.
CADENAS_REALES = (
    ("Tiendas D1", "alimentos_mercado",
     (("D1 CHAPINERO", False), ("TIENDAS D1 SUBA", True), ("D1 USAQUÉN", False), ("TIENDAS D1 KENNEDY", True))),
    ("Almacenes Éxito", "alimentos_mercado",
     (("ÉXITO CALLE 80", False), ("ÉXITO USAQUÉN", False), ("ÉXITO SUBA", False), ("ÉXITO AMÉRICAS", False))),
    ("Tiendas Ara", "alimentos_mercado",
     (("ARA CHAPINERO", False), ("TIENDAS ARA ENGATIVÁ", True), ("ARA BOSA", False))),
    ("Alkosto", "compras",
     (("ALKOSTO CALLE 13", False), ("ALKOSTO AV 68", False), ("ALKOSTO SUBA", False))),
    ("Supermercados Olímpica", "alimentos_mercado",
     (("OLÍMPICA CALLE 170", False), ("SUPERTIENDAS OLÍMPICA IBAGUÉ", True), ("OLÍMPICA KENNEDY", False))),
)

# Marcas inventadas (sin pistas de la naturaleza del negocio): prefijo más sufijo.
PREFIJOS_MARCA = ("Ka", "Lu", "Mi", "No", "Ta", "Ve", "Zo", "Ri", "Pe", "Su", "Da", "Fi", "Be", "Ga", "Jo", "Xe")
SUFIJOS_MARCA = ("limba", "vex", "bela", "vira", "meo", "xon", "dara", "lor", "nika", "pia", "quen", "tul",
                 "zuro", "mira", "dax", "fono")

TIPOS_NEGOCIO = {
    "alimentos_mercado": ("Supermercado", "Minimercado", "Panadería", "Fruver", "Carnicería"),
    "restaurantes_domicilios": ("Restaurante", "Asadero", "Cafetería", "Pizzería", "Heladería"),
    "transporte": ("Taxis", "Parqueadero", "Estación de servicio", "Transportes", "Lavadero"),
    "vivienda_servicios": ("Ferretería", "Lavandería", "Telecomunicaciones", "Servicios", "Cerrajería"),
    "salud": ("Droguería", "Clínica", "Laboratorio", "Consultorio", "Óptica"),
    "educacion": ("Colegio", "Academia", "Papelería", "Instituto", "Librería"),
    "entretenimiento": ("Cine", "Boliche", "Gimnasio", "Videojuegos", "Teatro"),
    "compras": ("Almacén", "Boutique", "Tecnología", "Calzado", "Muebles"),
}
NOMBRES_PROPIOS = (
    "Aurora", "Zafiro", "Quimbaya", "Nogal", "Ceiba", "Alborada", "Cumbre", "Marfil",
    "Palmira", "Sabana", "Tulipán", "Vendaval",
)
ZONAS = (
    "Chapinero", "Usaquén", "Suba", "Kennedy", "Fontibón", "Teusaquillo", "Engativá", "Bosa",
    "Centro", "Salitre", "Cedritos", "Modelia",
)
MAX_FICTICIAS = len(CATEGORIAS_COMERCIO) * 5 * len(NOMBRES_PROPIOS)

NOMBRES_PERSONA = (
    "Andrea", "Carlos", "Lucía", "Mateo", "Valentina", "Santiago", "Camila", "Julián", "Daniela",
    "Sebastián", "Paola", "Andrés", "Natalia", "Felipe", "Marcela", "Esteban",
)
APELLIDOS = (
    "Pérez", "Gómez", "Rodríguez", "Martínez", "Ríos", "Castaño", "Herrera", "Londoño", "Vargas",
    "Ospina", "Mejía", "Cardona", "Quintero", "Salazar", "Ortiz", "Beltrán",
)
TIPOS_PERSONA = ("celular", "celular", "celular", "celular", "correo", "alfanumerico", "documento")


def crear_empresa(semilla: int, razon_social: str, ficticia: bool) -> Empresa:
    return Empresa(uuid_determinista(semilla, "empresa", razon_social), razon_social, ficticia)


def crear_comercio(llaves: GeneradorLlaves, empresa: Empresa, nombre_base: str, categoria: str,
                   revela_naturaleza: bool, predicado: Callable[[str], bool] | None = None) -> Destino:
    """Comercio con llave nueva. nombre_base es el nombre legible, nombre_canonico se guarda canonizado.

    revela_naturaleza dice con qué plantilla se construyó el nombre, no se deduce del texto.
    """
    return Destino(llaves.nueva("comercio", predicado), "comercio", empresa.empresa_id,
                   canonizar(nombre_base), categoria, True, nombre_base, revela_naturaleza)


def _cadenas_reales(semilla: int, llaves: GeneradorLlaves) -> tuple[list[Empresa], list[Destino]]:
    empresas, destinos = [], []
    for razon, categoria, sucursales in CADENAS_REALES:
        empresa = crear_empresa(semilla, razon, False)
        empresas.append(empresa)
        destinos.extend(crear_comercio(llaves, empresa, nombre, categoria, revela)
                        for nombre, revela in sucursales)
    return empresas, destinos


def _marcas(azar: random.Random) -> list[str]:
    marcas = [p + s for p in PREFIJOS_MARCA for s in SUFIJOS_MARCA]
    azar.shuffle(marcas)
    return marcas


def _combinaciones(azar: random.Random) -> dict[str, list[tuple[str, str]]]:
    resultado = {}
    for categoria in CATEGORIAS_COMERCIO:
        combos = [(t, p) for t in TIPOS_NEGOCIO[categoria] for p in NOMBRES_PROPIOS]
        azar.shuffle(combos)
        resultado[categoria] = combos
    return resultado


def _nombre_de_empresa(revela: bool, categoria: str, combos: dict, marcas: list[str]) -> str:
    """Plantilla 'tipo de negocio más nombre propio' (revela) o 'marca inventada' (no revela)."""
    if revela:
        tipo, propio = combos[categoria].pop()
        return f"{tipo} {propio}"
    return marcas.pop().capitalize()


def _ficticias(semilla: int, azar: random.Random, llaves: GeneradorLlaves,
               cantidad: int) -> tuple[list[Empresa], list[Destino]]:
    """Cada empresa nueva usa la plantilla con menos sucursales hasta ahora, y así queda cerca de la mitad."""
    combos, marcas = _combinaciones(azar), _marcas(azar)
    cuenta = {True: 0, False: 0}
    empresas, destinos = [], []
    for i in range(min(cantidad, MAX_FICTICIAS)):
        categoria = CATEGORIAS_COMERCIO[i % len(CATEGORIAS_COMERCIO)]
        revela = cuenta[True] <= cuenta[False] or not marcas
        base = _nombre_de_empresa(revela, categoria, combos, marcas)
        empresa = crear_empresa(semilla, f"{base} S.A.S.", True)
        empresas.append(empresa)
        for zona in azar.sample(ZONAS, azar.randint(1, 3)):
            destinos.append(crear_comercio(llaves, empresa, f"{base} {zona}".upper(), categoria, revela))
            cuenta[revela] += 1
    return empresas, destinos


def construir_catalogo(semilla: int, azar: random.Random, llaves: GeneradorLlaves,
                       ficticias: int) -> tuple[tuple[Empresa, ...], tuple[Destino, ...]]:
    e1, d1 = _cadenas_reales(semilla, llaves)
    e2, d2 = _ficticias(semilla, azar, llaves, ficticias)
    return tuple(e1 + e2), tuple(d1 + d2)


def crear_persona(llaves: GeneradorLlaves, tipo: str, nombre: str) -> Destino:
    return Destino(llaves.nueva(tipo), tipo, "", canonizar(nombre), SIN_CATEGORIA, False, nombre)


def construir_personas(azar: random.Random, llaves: GeneradorLlaves, cantidad: int) -> tuple[Destino, ...]:
    personas = []
    for i in range(cantidad):
        nombre = f"{azar.choice(NOMBRES_PERSONA)} {azar.choice(APELLIDOS)}"
        personas.append(crear_persona(llaves, TIPOS_PERSONA[i % len(TIPOS_PERSONA)], nombre))
    return tuple(personas)
