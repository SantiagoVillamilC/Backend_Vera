"""Lógica de los avisos (experimento C). Solo ve `entrada/` y recibe las series como parámetro.

Cinco tipos: `presupuesto_anticipado`, `categoria_sobre_media`, `caida_ingreso`, `pago_no_alcanza` y `cobro_nuevo`.
Solo `pago_no_alcanza` y `cobro_nuevo` usan series: las recibe como lista de `SerieDeclarada` y no sabe de dónde
vienen (el arnés de evaluación le pasa las del oráculo o las del detector). Los otros tres usan totales mensuales.

Línea base mensual: mediana o media de los meses COMPLETOS previos al mes de `ahora` que tuvieron gasto (igual que la
regla de referencia de la especificación). Con menos de `meses_minimos` meses no se dispara ningún aviso.

- R1: el acumulado del mes en curso supera la línea base (multiplicador 1,0). Con `prorrateo_acumulado` el acumulado se
  proyecta al cierre del mes antes de compararlo, para avisar cuando el ritmo y no solo el total supera la base. En
  `caida_ingreso`, el ingreso del mes completo queda por debajo de (1 menos d) x línea base, solo si MAD / mediana es
  menor que `cv_robusto_max`.
- R2: z modificado de Iglewicz y Hoaglin, 0,6745 x (x menos centro) / MAD mayor que 3,5 (menor que menos 3,5 en la
  caída de ingreso). Con MAD cero cae en el respaldo R1.

Todas las reglas son propuesta sin fuente salvo el z modificado y la ventana de 7 días (SUPUESTOS.md). El reloj es
el parámetro `ahora`: solo cuentan los movimientos con `ocurrido_en` menor o igual que él.
"""

from collections import defaultdict
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from datetime import date, datetime, timedelta
from fractions import Fraction

import calendar

from .avisos_config import ConfiguracionAvisos
from .avisos_datos import (CATEGORIA_DESCONOCIDA, Decision, Historial, Mes, SerieDeclarada, fin_del_dia_local,
                           meses_completos_antes, mes_anterior, mes_de, mes_siguiente, totales_por_mes)
from .avisos_estadistica import linea_base, mad, mediana, z_modificado
from .configuracion import exacto
from .pagos import Identidad, Pago, fecha_local_colombia

__all__ = ["SerieDeclarada", "Decision", "ContextoAviso", "contexto_en", "evaluar", "fin_del_dia_local", "TIPOS"]

TIPOS = ("categoria_sobre_media", "presupuesto_anticipado", "caida_ingreso", "pago_no_alcanza", "cobro_nuevo")
INSUFICIENTE = "historial_insuficiente"


@dataclass(frozen=True)
class ContextoAviso:
    """Lo que el usuario tenía hasta `ahora`: totales mensuales, saldo, pagos y series. Se arma una vez por instante."""

    cfg: ConfiguracionAvisos
    ahora: datetime
    hoy: date
    mes_actual: Mes
    primero_local: date | None
    completos: tuple[Mes, ...]
    gasto_mes: Mapping[Mes, Fraction]
    gasto_categoria: Mapping[str, Mapping[Mes, Fraction]]
    ingreso_mes: Mapping[Mes, Fraction]
    saldo: Fraction
    pagos: tuple[Pago, ...]
    series: tuple[SerieDeclarada, ...]


def _categoria_de(pago: Pago, categorias: Mapping[str, str]) -> str:
    tipo, valor = pago.identidad
    return categorias.get(valor, CATEGORIA_DESCONOCIDA) if tipo == "llave" else CATEGORIA_DESCONOCIDA


def _gasto_por_categoria(gastos: Sequence[Pago], categorias: Mapping[str, str]) -> dict[str, dict[Mes, Fraction]]:
    grupos: dict[str, list[tuple[date, int]]] = defaultdict(list)
    for p in gastos:
        grupos[_categoria_de(p, categorias)].append((p.fecha_local, p.monto_centavos))
    return {c: totales_por_mes(items) for c, items in sorted(grupos.items())}


def contexto_en(historial: Historial, series: Sequence[SerieDeclarada], categorias: Mapping[str, str],
                ahora: datetime, cfg: ConfiguracionAvisos) -> ContextoAviso:
    """Contexto con solo los movimientos de `ocurrido_en <= ahora`. Rechaza un `ahora` sin zona horaria."""
    if ahora.tzinfo is None or ahora.utcoffset() is None:
        raise ValueError("ahora debe llevar zona horaria (por ejemplo UTC): el reloj se inyecta, no se adivina")
    vigentes = tuple(p for p in historial.pagos if p.ocurrido_en <= ahora)
    gastos = tuple(p for p in historial.gastos if p.ocurrido_en <= ahora)
    ingresos = [(i.fecha_local, i.monto_centavos) for i in historial.ingresos if i.ocurrido_en <= ahora]
    hoy = fecha_local_colombia(ahora)
    primero = historial.primero
    primero_local = fecha_local_colombia(primero) if primero is not None and primero <= ahora else None
    return ContextoAviso(
        cfg, ahora, hoy, mes_de(hoy), primero_local, meses_completos_antes(primero_local, mes_de(hoy)),
        totales_por_mes((p.fecha_local, p.monto_centavos) for p in gastos), _gasto_por_categoria(gastos, categorias),
        totales_por_mes(ingresos), Fraction(sum(f.monto_centavos for f in historial.flujos if f.ocurrido_en <= ahora)),
        vigentes, tuple(series))


def _superior(x: Fraction, valores: Sequence[Fraction], regla: str, base: str,
              cfg: ConfiguracionAvisos) -> tuple[bool, Fraction, Fraction | None, str]:
    """(dispara, línea base, z o None, causa) para una regla de "x supera lo habitual"."""
    centro, desviacion = linea_base(valores, base), mad(valores)
    if regla == "R2" and desviacion > 0:
        z = z_modificado(x, centro, desviacion, exacto(cfg.z_factor))
        dispara = z > exacto(cfg.z_umbral)
        return dispara, centro, z, "z_sobre_el_umbral" if dispara else "z_bajo_el_umbral"
    causa = "respaldo_r1_mad_cero" if regla == "R2" else "r1"
    return x > exacto(cfg.multiplicador_gasto) * centro, centro, None, causa


def _con_gasto(ctx: ContextoAviso, por_mes: Mapping[Mes, Fraction]) -> list[Fraction]:
    return [por_mes[m] for m in ctx.completos if por_mes.get(m, 0) > 0]


def _proyectado(ctx: ContextoAviso, x: Fraction) -> Fraction:
    """Proyecta el acumulado del mes en curso al cierre del mes, para avisar cuando el ritmo supera la base."""
    if not ctx.cfg.prorrateo_acumulado or x == 0:
        return x
    dias = calendar.monthrange(ctx.hoy.year, ctx.hoy.month)[1]
    return x * Fraction(dias, ctx.hoy.day)


def _presupuesto(ctx: ContextoAviso, regla: str, base: str) -> Decision:
    x = _proyectado(ctx, ctx.gasto_mes.get(ctx.mes_actual, Fraction(0)))
    valores = _con_gasto(ctx, ctx.gasto_mes)
    if len(ctx.completos) < ctx.cfg.meses_minimos or len(valores) < ctx.cfg.meses_minimos:
        return Decision("presupuesto_anticipado", False, INSUFICIENTE, x)
    dispara, centro, z, causa = _superior(x, valores, regla, base, ctx.cfg)
    return Decision("presupuesto_anticipado", dispara, causa, x, centro, z)


def _por_categoria(ctx: ContextoAviso, regla: str, base: str) -> list[tuple[str, Fraction, bool, Decision]]:
    candidatas = []
    for categoria, por_mes in ctx.gasto_categoria.items():
        valores = _con_gasto(ctx, por_mes)
        if len(valores) < ctx.cfg.meses_minimos:
            continue
        x = _proyectado(ctx, por_mes.get(ctx.mes_actual, Fraction(0)))
        dispara, centro, z, causa = _superior(x, valores, regla, base, ctx.cfg)
        decision = Decision("categoria_sobre_media", dispara, causa, x, centro, z, f"categoria {categoria}")
        candidatas.append((categoria, x / centro, dispara, decision))
    return candidatas


def _categoria(ctx: ContextoAviso, regla: str, base: str) -> Decision:
    if len(ctx.completos) < ctx.cfg.meses_minimos:
        return Decision("categoria_sobre_media", False, INSUFICIENTE)
    candidatas = _por_categoria(ctx, regla, base)
    if not candidatas:
        return Decision("categoria_sobre_media", False, "sin_categoria_evaluable")
    disparan = [c for c in candidatas if c[2]] or candidatas
    return max(disparan, key=lambda c: c[1])[3]


def _mes_evaluado(ctx: ContextoAviso) -> Mes:
    """El último mes completo: el de `hoy` si hoy es su último día, si no el anterior."""
    es_ultimo_dia = mes_de(ctx.hoy + timedelta(days=1)) != ctx.mes_actual
    return ctx.mes_actual if es_ultimo_dia else mes_anterior(ctx.mes_actual)


def _decision_caida(ctx: ContextoAviso, x: Fraction, valores: Sequence[Fraction], regla: str, base: str) -> Decision:
    cfg = ctx.cfg
    centro, desviacion, mediana_ = linea_base(valores, base), mad(valores), mediana(valores)
    if regla == "R2" and desviacion > 0:
        z = z_modificado(x, centro, desviacion, exacto(cfg.z_factor))
        dispara = z < -exacto(cfg.z_umbral)
        return Decision("caida_ingreso", dispara, "z_sobre_el_umbral" if dispara else "z_bajo_el_umbral", x, centro, z)
    cv = desviacion / mediana_ if mediana_ > 0 else None
    if cv is None or cv >= exacto(cfg.cv_robusto_max):
        return Decision("caida_ingreso", False, "guardia_cv_robusto", x, centro, cv)
    dispara = x < (1 - exacto(cfg.caida_d)) * centro
    return Decision("caida_ingreso", dispara, "respaldo_r1_mad_cero" if regla == "R2" else "r1", x, centro, cv)


def _caida(ctx: ContextoAviso, regla: str, base: str) -> Decision:
    evaluado = _mes_evaluado(ctx)
    hasta_evaluado = meses_completos_antes(ctx.primero_local, mes_siguiente(evaluado))
    previos = [m for m in hasta_evaluado if m < evaluado]
    if evaluado not in hasta_evaluado or len(previos) < ctx.cfg.meses_minimos or len(ctx.completos) < ctx.cfg.meses_minimos:
        return Decision("caida_ingreso", False, INSUFICIENTE)
    valores = [ctx.ingreso_mes.get(m, Fraction(0)) for m in previos]
    return _decision_caida(ctx, ctx.ingreso_mes.get(evaluado, Fraction(0)), valores, regla, base)


def _proximo_pago(ultimo: date, cadencia: int, hoy: date) -> date:
    """Último pago más k cadencias, con el menor k mayor o igual que 1 que no queda antes de hoy."""
    transcurridos = (hoy - ultimo).days
    return ultimo + timedelta(days=cadencia * max(1, -(-transcurridos // cadencia)))


def _pagos_de(ctx: ContextoAviso, identidad: Identidad) -> list[Pago]:
    return [p for p in ctx.pagos if p.identidad == identidad]


def _esperados_en_la_ventana(ctx: ContextoAviso) -> list[tuple[SerieDeclarada, Fraction]]:
    limite = ctx.hoy + timedelta(days=ctx.cfg.ventana_pago_dias)
    esperados = []
    for serie in ctx.series:
        pagos = _pagos_de(ctx, serie.identidad)
        if pagos and ctx.hoy <= _proximo_pago(max(p.fecha_local for p in pagos), serie.cadencia_dias, ctx.hoy) <= limite:
            esperados.append((serie, mediana([p.monto_centavos for p in pagos])))
    return esperados


def _pago_no_alcanza(ctx: ContextoAviso, regla: str, base: str) -> Decision:
    if len(ctx.completos) < ctx.cfg.meses_minimos:
        return Decision("pago_no_alcanza", False, INSUFICIENTE, ctx.saldo)
    if not ctx.series:
        return Decision("pago_no_alcanza", False, "sin_series", ctx.saldo)
    esperados = _esperados_en_la_ventana(ctx)
    if not esperados:
        return Decision("pago_no_alcanza", False, "sin_pago_en_la_ventana", ctx.saldo)
    suma = sum((monto for _, monto in esperados), Fraction(0))
    detalle = ", ".join(f"{serie.identidad[1]}" for serie, _ in esperados)
    return Decision("pago_no_alcanza", ctx.saldo < suma, "pago_en_la_ventana", ctx.saldo, suma, None, detalle)


def _similares(pagos: Sequence[Pago], tolerancia: Fraction) -> int:
    centro = mediana([p.monto_centavos for p in pagos])
    return sum(1 for p in pagos if abs(p.monto_centavos - centro) <= tolerancia * centro)


def _llaves_nuevas(ctx: ContextoAviso) -> tuple[list[Identidad], int]:
    """(identidades nuevas que ya son recurrentes, identidades nuevas en total). Nueva: primer pago en la ventana."""
    corte = ctx.hoy - timedelta(days=ctx.cfg.ventana_cobro_dias)
    grupos: dict[Identidad, list[Pago]] = defaultdict(list)
    for p in ctx.pagos:
        grupos[p.identidad].append(p)
    declaradas = {s.identidad for s in ctx.series}
    nuevas = {i: g for i, g in sorted(grupos.items()) if g[0].fecha_local >= corte}
    tolerancia = exacto(ctx.cfg.tol_monto_cobro)
    recurrentes = [i for i, g in nuevas.items()
                   if i in declaradas or _similares(g, tolerancia) >= ctx.cfg.pagos_minimos_cobro]
    return recurrentes, len(nuevas)


def _cobro_nuevo(ctx: ContextoAviso, regla: str, base: str) -> Decision:
    if len(ctx.completos) < ctx.cfg.meses_minimos:
        return Decision("cobro_nuevo", False, INSUFICIENTE)
    recurrentes, nuevas = _llaves_nuevas(ctx)
    if recurrentes:
        return Decision("cobro_nuevo", True, "llave_nueva", detalle=", ".join(i[1] for i in recurrentes))
    return Decision("cobro_nuevo", False, "llave_nueva_sin_serie" if nuevas else "sin_llave_nueva",
                    detalle=f"llaves nuevas sin recurrencia: {nuevas}")


AVISOS = {"categoria_sobre_media": _categoria, "presupuesto_anticipado": _presupuesto, "caida_ingreso": _caida,
          "pago_no_alcanza": _pago_no_alcanza, "cobro_nuevo": _cobro_nuevo}


def evaluar(ctx: ContextoAviso, tipo: str, regla: str, base: str) -> Decision:
    """Decisión de un tipo de aviso con una regla (R1 o R2) y una línea base (mediana o media)."""
    if tipo not in AVISOS:
        raise ValueError(f"tipo de aviso desconocido {tipo!r}: se esperaba uno de {TIPOS}")
    if regla not in ("R1", "R2"):
        raise ValueError(f"regla desconocida {regla!r}: se esperaba R1 o R2")
    return AVISOS[tipo](ctx, regla, base)
