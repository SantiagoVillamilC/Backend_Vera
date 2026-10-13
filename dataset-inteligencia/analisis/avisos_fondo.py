"""Avisos emitidos a usuarios SIN etiqueta: el conteo es un dato descriptivo y no una tasa de falsos positivos.

No hay verdad para esos usuarios: un aviso emitido puede ser correcto o no y el corpus no lo dice. Por eso solo se
cuenta cuántos avisos salen por usuario y mes, rotulado "sin verdad, no es tasa de falsos". Cada usuario se evalúa
al final de cada mes desde el de su primer movimiento y en la fecha final de la corrida. Los usuarios que sí tienen
etiquetas (los perfiles) se evalúan también en las fechas que NO están etiquetadas, en un grupo aparte: ahí se ve
cómo se comporta cada regla fuera del día para el que se escribió la etiqueta.
"""

import calendar
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from datetime import date

from .avisos import TIPOS, contexto_en, evaluar, fin_del_dia_local
from .avisos_config import ConfiguracionAvisos
from .avisos_corrida import CorridaAvisos
from .avisos_datos import Historial, construir_historiales, mes_de, mes_siguiente
from .avisos_evaluacion import Combinacion, combinaciones, series_de_la_fuente
from .avisos_fuentes import Categorias, series_oraculo
from .pagos import fecha_local_colombia

GRUPO_FONDO = "fondo"
GRUPO_PERFIL = "perfil_etiquetado"
GRUPOS = (GRUPO_FONDO, GRUPO_PERFIL)
NOTA = "sin verdad, no es tasa de falsos"
NO_EVALUABLES = ("historial_insuficiente", "sin_categoria_evaluable")


@dataclass(frozen=True)
class FilaFondo:
    corrida: str
    usuario_id: str
    grupo: str
    fecha_evaluacion: date
    mes: str
    combinacion: Combinacion
    tipo: str
    dispara: bool
    causa: str


@dataclass(frozen=True)
class ResumenFondo:
    grupo: str
    combinacion: Combinacion
    tipo: str
    usuarios: int
    evaluaciones: int
    evaluables: int
    avisos: int


def fechas_de_evaluacion(primero: date, fecha_fin: date) -> tuple[date, ...]:
    """Fin de cada mes desde el del primer movimiento hasta la fecha final, que cierra la lista."""
    fechas, mes = [], mes_de(primero)
    while mes <= mes_de(fecha_fin):
        fin = date(mes[0], mes[1], calendar.monthrange(*mes)[1])
        if fin < fecha_fin:
            fechas.append(fin)
        mes = mes_siguiente(mes)
    return tuple(fechas) + (fecha_fin,)


def _filas_de_fecha(corrida: CorridaAvisos, historial: Historial, fecha: date, grupo: str, contextos: Mapping,
                    combos: Sequence[Combinacion], etiquetadas: frozenset) -> list[FilaFondo]:
    filas = []
    for tipo in TIPOS:
        if (historial.usuario_id, tipo, fecha) in etiquetadas:
            continue
        for c in combos:
            d = evaluar(contextos[c.fuente], tipo, c.regla, c.linea_base)
            filas.append(FilaFondo(corrida.nombre, historial.usuario_id, grupo, fecha, f"{fecha:%Y-%m}", c, tipo,
                                   d.dispara, d.causa))
    return filas


def filas_sin_etiqueta(corrida: CorridaAvisos, cfg: ConfiguracionAvisos) -> tuple[FilaFondo, ...]:
    """Decisión de cada tipo y combinación para cada usuario en cada fecha, salvo las (usuario, tipo, fecha) etiquetadas."""
    historiales = construir_historiales(corrida.movimientos, cfg)
    oraculo = series_oraculo(corrida.series)
    categorias = Categorias(corrida, cfg, False)
    combos = combinaciones(cfg)
    etiquetadas = frozenset((e.usuario_id, e.tipo, e.fecha_evaluacion) for e in corrida.etiquetas)
    con_etiqueta = {e.usuario_id for e in corrida.etiquetas}
    filas: list[FilaFondo] = []
    for usuario, historial in historiales.items():
        grupo = GRUPO_PERFIL if usuario in con_etiqueta else GRUPO_FONDO
        for fecha in fechas_de_evaluacion(fecha_local_colombia(historial.primero), corrida.fecha_fin):
            ahora = fin_del_dia_local(fecha)
            contextos = {f: contexto_en(historial, series_de_la_fuente(f, historial, oraculo, cfg, ahora),
                                        categorias.en(ahora), ahora, cfg) for f in cfg.fuentes_series}
            filas += _filas_de_fecha(corrida, historial, fecha, grupo, contextos, combos, etiquetadas)
    return tuple(filas)


def agregar_fondo(filas: Sequence[FilaFondo]) -> tuple[ResumenFondo, ...]:
    """Conteo por (grupo, combinación, tipo): usuarios distintos, evaluaciones, evaluables (con historial) y avisos."""
    grupos: dict[tuple, list[FilaFondo]] = {}
    for f in filas:
        grupos.setdefault((f.grupo, f.combinacion, f.tipo), []).append(f)
    orden_grupo = {g: i for i, g in enumerate(GRUPOS)}
    orden_combinacion = {c: i for i, c in enumerate(dict.fromkeys(f.combinacion for f in filas))}
    resumenes = [ResumenFondo(g, c, t, len({f.usuario_id for f in propias}), len(propias),
                              sum(f.causa not in NO_EVALUABLES for f in propias), sum(f.dispara for f in propias))
                 for (g, c, t), propias in grupos.items()]
    return tuple(sorted(resumenes, key=lambda r: (orden_grupo[r.grupo], orden_combinacion[r.combinacion],
                                                  TIPOS.index(r.tipo))))
