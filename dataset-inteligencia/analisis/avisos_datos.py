"""Datos de los avisos: historial de un usuario, series declaradas, decisiones y calendario de meses.

Solo ve filas de `entrada/` ya cargadas. Todo cálculo de fechas usa la fecha local de Colombia (UTC menos 5), igual
que el detector de recurrencia. Un mes es "completo" para un usuario cuando empieza en o después de su primer
movimiento y ya terminó antes del instante de consulta. Puro: no abre archivos ni mira la hora del sistema.
"""

from collections import defaultdict
from collections.abc import Iterable, Mapping
from dataclasses import dataclass
from datetime import date, datetime, time, timedelta, timezone
from fractions import Fraction

from .avisos_config import ConfiguracionAvisos
from .pagos import DESFASE_COLOMBIA, Identidad, Pago, extraer_pagos, fecha_local_colombia, parsear_instante

Mes = tuple[int, int]
CATEGORIA_DESCONOCIDA = "sin_categoria"


@dataclass(frozen=True)
class Ingreso:
    ocurrido_en: datetime
    fecha_local: date
    monto_centavos: int


@dataclass(frozen=True)
class Flujo:
    """Movimiento de saldo: positivo si entra dinero y negativo si sale (incluye retenciones y devoluciones)."""

    ocurrido_en: datetime
    monto_centavos: int


@dataclass(frozen=True)
class Historial:
    usuario_id: str
    pagos: tuple[Pago, ...]
    gastos: tuple[Pago, ...]
    ingresos: tuple[Ingreso, ...]
    flujos: tuple[Flujo, ...]
    primero: datetime | None


@dataclass(frozen=True)
class SerieDeclarada:
    """Una serie recurrente que alguien declara: su identidad y su cadencia en días. Sin más datos."""

    identidad: Identidad
    cadencia_dias: int


@dataclass(frozen=True)
class Decision:
    """Resultado de un aviso. Los montos van en centavos. `causa` dice por qué se decidió así."""

    tipo: str
    dispara: bool
    causa: str
    valor: Fraction | None = None
    referencia: Fraction | None = None
    estadistico: Fraction | None = None
    detalle: str = ""


def fin_del_dia_local(fecha: date) -> datetime:
    """Último segundo de una fecha de Colombia como instante UTC: el día siguiente a las 04:59:59Z."""
    medianoche = datetime.combine(fecha + timedelta(days=1), time.min, tzinfo=timezone.utc) - DESFASE_COLOMBIA
    return medianoche - timedelta(seconds=1)


def mes_de(fecha: date) -> Mes:
    return fecha.year, fecha.month


def mes_siguiente(mes: Mes) -> Mes:
    return (mes[0] + 1, 1) if mes[1] == 12 else (mes[0], mes[1] + 1)


def mes_anterior(mes: Mes) -> Mes:
    return (mes[0] - 1, 12) if mes[1] == 1 else (mes[0], mes[1] - 1)


def meses_completos_antes(primero_local: date | None, limite: Mes) -> tuple[Mes, ...]:
    """Meses completos del usuario anteriores a `limite` (excluido), en orden. Sin movimientos no hay ninguno."""
    if primero_local is None:
        return ()
    mes = mes_de(primero_local)
    if primero_local.day != 1:
        mes = mes_siguiente(mes)
    meses = []
    while mes < limite:
        meses.append(mes)
        mes = mes_siguiente(mes)
    return tuple(meses)


def totales_por_mes(items: Iterable[tuple[date, int]]) -> dict[Mes, Fraction]:
    acumulado: dict[Mes, Fraction] = defaultdict(Fraction)
    for fecha, monto in items:
        acumulado[mes_de(fecha)] += monto
    return dict(acumulado)


def _flujo(fila: Mapping[str, str]) -> Flujo:
    monto = int(fila["monto_centavos"])
    return Flujo(parsear_instante(fila["ocurrido_en"]), monto if fila["direccion"] == "entrada" else -monto)


def _es_ingreso(fila: Mapping[str, str], cfg: ConfiguracionAvisos) -> bool:
    return (fila["direccion"] == "entrada" and fila["tipo_asiento"] in cfg.tipos_asiento_ingreso
            and fila.get("descripcion", "") not in cfg.descripciones_no_ingreso)


def _ingreso(fila: Mapping[str, str]) -> Ingreso:
    instante = parsear_instante(fila["ocurrido_en"])
    return Ingreso(instante, fecha_local_colombia(instante), int(fila["monto_centavos"]))


def _historial(usuario: str, filas: list[Mapping[str, str]], pagos: tuple[Pago, ...],
               cfg: ConfiguracionAvisos) -> Historial:
    tipos = {int(f["movimiento_id"]): f["tipo_asiento"] for f in filas}
    gastos = tuple(p for p in pagos if tipos[p.movimiento_id] in cfg.tipos_asiento_gasto)
    flujos = sorted((_flujo(f) for f in filas), key=lambda x: x.ocurrido_en)
    ingresos = sorted((_ingreso(f) for f in filas if _es_ingreso(f, cfg)), key=lambda x: x.ocurrido_en)
    primero = flujos[0].ocurrido_en if flujos else None
    return Historial(usuario, pagos, gastos, tuple(ingresos), tuple(flujos), primero)


def construir_historiales(filas: Iterable[Mapping[str, str]], cfg: ConfiguracionAvisos) -> dict[str, Historial]:
    """Un historial por usuario a partir de las filas de movimientos. No modifica la entrada."""
    todas = tuple(filas)
    por_usuario: dict[str, list[Mapping[str, str]]] = defaultdict(list)
    for fila in todas:
        por_usuario[fila["usuario_id"]].append(fila)
    pagos: dict[str, list[Pago]] = defaultdict(list)
    for p in extraer_pagos(todas):
        pagos[p.usuario_id].append(p)
    return {u: _historial(u, por_usuario[u], tuple(pagos[u]), cfg) for u in sorted(por_usuario)}
