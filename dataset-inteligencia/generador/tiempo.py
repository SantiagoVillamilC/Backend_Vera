"""Conversión entre hora de Colombia (UTC-5, sin horario de verano) y UTC."""

import calendar
from datetime import date, datetime, timedelta, timezone

from .constantes import ZONA_COLOMBIA


def instante_co(fecha: date, hora: int = 12, minuto: int = 0, segundo: int = 0) -> datetime:
    """Instante UTC (con zona) de una fecha y hora locales de Colombia."""
    local = datetime(fecha.year, fecha.month, fecha.day, hora, minuto, segundo, tzinfo=ZONA_COLOMBIA)
    return local.astimezone(timezone.utc)


def a_texto_utc(instante: datetime) -> str:
    return instante.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def fecha_co(instante: datetime) -> date:
    return instante.astimezone(ZONA_COLOMBIA).date()


def ultimo_dia(anio: int, mes: int) -> int:
    return calendar.monthrange(anio, mes)[1]


def dia_del_mes(anio: int, mes: int, dia: int) -> date:
    """Fecha del mes pedido, recortando el día al último válido."""
    return date(anio, mes, min(dia, ultimo_dia(anio, mes)))


def fin_de_mes(anio: int, mes: int) -> date:
    return date(anio, mes, ultimo_dia(anio, mes))


def meses_hasta(fecha_fin: date, desde_mes: int = 4, anio: int = 2026) -> tuple[tuple[int, int], ...]:
    """Pares (año, mes) desde `desde_mes` hasta el mes de `fecha_fin`."""
    resultado = []
    actual = (anio, desde_mes)
    while actual <= (fecha_fin.year, fecha_fin.month):
        resultado.append(actual)
        actual = (actual[0] + (actual[1] // 12), actual[1] % 12 + 1)
    return tuple(resultado)


def sumar_horas(instante: datetime, horas: float) -> datetime:
    return instante + timedelta(hours=horas)
