"""Modelos de datos inmutables del generador."""

import uuid
from dataclasses import dataclass, field
from datetime import date, datetime

from .constantes import ESPACIO_UUID


def uuid_determinista(semilla: int, clase: str, *partes: object) -> str:
    """UUID v5 sobre el espacio de nombres fijo: depende solo de la semilla y de las partes."""
    clave = "|".join([str(semilla), clase, *[str(p) for p in partes]])
    return str(uuid.uuid5(ESPACIO_UUID, clave))


@dataclass(frozen=True)
class Empresa:
    empresa_id: str
    razon_social: str
    ficticia: bool


@dataclass(frozen=True)
class Destino:
    """Llave de destino (comercio o persona) con su estado vigente al final del periodo."""

    llave_destino: str
    tipo_llave: str
    empresa_id: str
    nombre_canonico: str
    categoria: str
    es_comercio: bool
    nombre_base: str = ""
    nombre_revela_naturaleza: bool = False


@dataclass(frozen=True)
class Usuario:
    usuario_id: str
    clave: str
    llave_propia: str


@dataclass(frozen=True)
class NombreVigente:
    """Una fila del historial de nombre y categoría de un comercio (fechas locales inclusivas)."""

    llave_destino: str
    nombre_canonico: str
    categoria: str
    vigente_desde: date
    vigente_hasta: date | None


@dataclass(frozen=True)
class Movimiento:
    usuario_id: str
    ocurrido_en: datetime
    direccion: str
    monto_centavos: int
    tipo_asiento: str
    descripcion: str
    categoria_real: str
    destino: Destino | None = None
    grupo: int | None = None
    es_pago_comercio: bool = False
    es_persona: bool = False
    presentacion: str = ""
    marca: str = ""
    movimiento_id: int = 0


@dataclass(frozen=True)
class Correccion:
    usuario_id: str
    llave_destino: str
    nombre_puesto: str
    categoria_puesta: str
    ocurrido_en: datetime
    es_correcta: bool
    tipo_ruido: str
    correccion_id: str = ""


@dataclass(frozen=True)
class Aviso:
    usuario_id: str
    tipo: str
    fecha_evaluacion: date
    con_mediana: bool
    con_media: bool
    detalle: str


@dataclass(frozen=True)
class VistaEsperada:
    """Lo que un usuario debe ver en un movimiento marcado (casos 2 y 3)."""

    marca: str
    caso_id: str
    usuario_id: str
    nombre_esperado: str
    categoria_esperada: str
    origen_esperado: str


@dataclass(frozen=True)
class Serie:
    """Etiqueta de verdad sobre si los pagos de un usuario a una llave forman una serie recurrente."""

    clave: str
    usuario_id: str
    llave_destino: str
    es_recurrente: bool
    cadencia_dias: int | None
    motivo: str
    detalle: str


@dataclass(frozen=True)
class Aporte:
    """Resultado parcial de un escenario. Se combinan sin mutar ninguno."""

    usuarios: tuple[Usuario, ...] = ()
    movimientos: tuple[Movimiento, ...] = ()
    avisos: tuple[Aviso, ...] = ()
    correcciones: tuple[Correccion, ...] = ()
    vistas: tuple[VistaEsperada, ...] = ()
    historiales: tuple[NombreVigente, ...] = ()
    series: tuple[Serie, ...] = ()
    destinos: tuple[Destino, ...] = ()
    empresas: tuple[Empresa, ...] = ()
    guionadas: tuple[str, ...] = field(default=())


def combinar(aportes: list[Aporte]) -> Aporte:
    """Une varios aportes conservando el orden en que se entregan."""
    campos = ("usuarios", "movimientos", "avisos", "correcciones", "vistas",
              "historiales", "series", "destinos", "empresas", "guionadas")
    return Aporte(**{c: tuple(x for a in aportes for x in getattr(a, c)) for c in campos})
