"""Comercios y personas reservados para los escenarios fijos y los casos de multitud.

Ninguna es una marca real: todas las empresas son ficticias. Los comercios de los casos
no entran al fondo aleatorio ni a las correcciones aleatorias, para conservar su guion.
"""

from dataclasses import dataclass
from datetime import date

from .catalogo import crear_comercio, crear_empresa, crear_persona
from .constantes import INICIO_HISTORIAL
from .llaves import GeneradorLlaves
from .modelos import Destino, Empresa, NombreVigente
from .nombres import canonizar

FECHA_CAMBIO_NOMBRE = date(2026, 7, 15)
FECHA_CAMBIO_CATEGORIA = date(2026, 8, 10)
NOMBRE_LUCY_ANTES = "Minimercado La Esquina"
NOMBRE_LUCY_AHORA = "Tienda Doña Lucy"
NOMBRE_ROBLE_ANTES = "Cafetería El Roble"
NOMBRE_ROBLE_AHORA = "Librería El Roble"


@dataclass(frozen=True)
class Reservados:
    internet: Destino
    telefonia: Destino
    energia: Destino
    supermercado: Destino
    panaderia_centro: Destino
    panaderia_suba: Destino
    drogueria: Destino
    ruta: Destino
    ruta_comidas: Destino
    arrendadora: Destino
    pepe: Destino
    lucy: Destino
    roble: Destino
    ferreteria: Destino
    empresas: tuple[Empresa, ...]
    historiales: tuple[NombreVigente, ...]

    def destinos(self) -> tuple[Destino, ...]:
        return (self.internet, self.telefonia, self.energia, self.supermercado, self.panaderia_centro, self.panaderia_suba,
                self.drogueria, self.ruta, self.ruta_comidas, self.arrendadora, self.pepe, self.lucy,
                self.roble, self.ferreteria)

    def llaves_guionadas(self) -> tuple[str, ...]:
        """Comercios y personas de los casos de multitud: sus correcciones se escriben a mano."""
        sueltos = (self.internet, self.telefonia, self.energia, self.supermercado)
        return tuple(d.llave_destino for d in self.destinos() if d not in sueltos)


def _historial_con_cambio(destino: Destino, antes: tuple[str, str], fecha: date) -> tuple[NombreVigente, ...]:
    """Dos filas: el nombre anterior hasta el día previo a `fecha` y el vigente desde `fecha`."""
    previo = date.fromordinal(fecha.toordinal() - 1)
    return (
        NombreVigente(destino.llave_destino, canonizar(antes[0]), antes[1], INICIO_HISTORIAL, previo),
        NombreVigente(destino.llave_destino, destino.nombre_canonico, destino.categoria, fecha, None),
    )


def _mcc_4814(llave: str) -> bool:
    """Llaves cuyo MCC derivado de la categoría vivienda_servicios es 4814 (primer código de la lista)."""
    return int(llave[-3:]) % 3 == 0


def construir_reservados(semilla: int, llaves: GeneradorLlaves) -> Reservados:
    nombres = ("Redes Hogar Andinas", "Telefonía Móvil Cóndor", "Energía Cumbre", "Supermercado Mil Frutas", "Panadería Trigal Real",
               "Droguería Nube Azul", "Ruta Veloz", "Ruta Veloz Comidas", "Supermercado Don Pepe",
               NOMBRE_LUCY_ANTES, NOMBRE_ROBLE_ANTES, "Ferretería Los Andes")
    emp = {n: crear_empresa(semilla, f"{n} S.A.S.", True) for n in nombres}

    def comercio(clave: str, nombre: str, categoria: str, revela: bool, predicado=None) -> Destino:
        """revela: el nombre se escribió con plantilla de tipo de negocio (True) o como marca (False)."""
        return crear_comercio(llaves, emp[clave], nombre, categoria, revela, predicado)

    lucy = comercio(NOMBRE_LUCY_ANTES, NOMBRE_LUCY_AHORA, "alimentos_mercado", True)
    roble = comercio(NOMBRE_ROBLE_ANTES, NOMBRE_ROBLE_AHORA, "educacion", True)
    historiales = (
        _historial_con_cambio(lucy, (NOMBRE_LUCY_ANTES, "alimentos_mercado"), FECHA_CAMBIO_NOMBRE)
        + _historial_con_cambio(roble, (NOMBRE_ROBLE_ANTES, "restaurantes_domicilios"), FECHA_CAMBIO_CATEGORIA)
    )
    return Reservados(
        internet=comercio("Redes Hogar Andinas", "REDES HOGAR ANDINAS", "vivienda_servicios", False),
        telefonia=comercio("Telefonía Móvil Cóndor", "TELEFONÍA MÓVIL CÓNDOR", "vivienda_servicios", True, _mcc_4814),
        energia=comercio("Energía Cumbre", "ENERGÍA CUMBRE", "vivienda_servicios", True),
        supermercado=comercio("Supermercado Mil Frutas", "SUPERMERCADO MIL FRUTAS", "alimentos_mercado", True),
        panaderia_centro=comercio("Panadería Trigal Real", "PANADERÍA TRIGAL REAL CENTRO", "alimentos_mercado", True),
        panaderia_suba=comercio("Panadería Trigal Real", "PANADERÍA TRIGAL REAL SUBA", "alimentos_mercado", True),
        drogueria=comercio("Droguería Nube Azul", "DROGUERÍA NUBE AZUL", "salud", True),
        ruta=comercio("Ruta Veloz", "RUTA VELOZ", "transporte", False),
        ruta_comidas=comercio("Ruta Veloz Comidas", "RUTA VELOZ COMIDAS", "restaurantes_domicilios", True),
        arrendadora=crear_persona(llaves, "celular", "Marta Lucía Ocampo"),
        pepe=comercio("Supermercado Don Pepe", "Supermercado Don Pepe", "alimentos_mercado", True),
        lucy=lucy,
        roble=roble,
        ferreteria=comercio("Ferretería Los Andes", "Ferretería Los Andes", "vivienda_servicios", True),
        empresas=tuple(emp.values()),
        historiales=historiales,
    )
