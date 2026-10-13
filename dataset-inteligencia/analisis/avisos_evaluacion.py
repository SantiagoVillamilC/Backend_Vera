"""Evaluación de los avisos contra las etiquetas de avisos_esperados.csv. Es una PRUEBA DE ACEPTACIÓN, no una medición.

Son 13 etiquetas escritas a mano de 5 usuarios, sin fórmula de umbral: no hay muestra para una tasa. Por cada fila de
etiquetas y por cada combinación (regla R1 o R2) x (línea base mediana o media) x (series del oráculo o detectadas) se
guarda si el aviso dispara, si debía, si pasa o falla, la línea base y el valor usados y la causa. Los falsos positivos
cuentan sobre las etiquetas negativas y los falsos negativos sobre las positivas de la columna que corresponde a la
línea base. `cobro_nuevo` solo tiene una etiqueta negativa: mide falsos positivos y nada más.
"""

from collections import defaultdict
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, replace
from datetime import date
from fractions import Fraction
from itertools import product

from .avisos import contexto_en, evaluar, fin_del_dia_local
from .avisos_config import ConfiguracionAvisos
from .avisos_corrida import CorridaAvisos, Etiqueta, cargar_corrida_avisos
from .avisos_datos import Historial, SerieDeclarada, construir_historiales
from .avisos_fuentes import Categorias, series_detectadas, series_oraculo

__all__ = ["Combinacion", "FilaEvaluada", "ResumenCombinacion", "FilaVariacion", "cargar_corrida_avisos",
           "combinaciones", "evaluar_corrida", "resumir", "variacion", "series_de_la_fuente", "aplicar_tope",
           "PRIORIDAD"]

TODOS = "todos"
PRIORIDAD = ("pago_no_alcanza", "caida_ingreso", "cobro_nuevo", "presupuesto_anticipado", "categoria_sobre_media")


@dataclass(frozen=True)
class Combinacion:
    regla: str
    linea_base: str
    fuente: str


@dataclass(frozen=True)
class FilaEvaluada:
    corrida: str
    fila: int
    usuario_id: str
    tipo: str
    fecha_evaluacion: date
    combinacion: Combinacion
    esperado: bool
    dispara: bool
    valor: Fraction | None
    referencia: Fraction | None
    estadistico: Fraction | None
    causa: str
    detalle: str
    categoria_fuente: str

    @property
    def aprobada(self) -> bool:
        return self.dispara == self.esperado


@dataclass(frozen=True)
class ResumenCombinacion:
    """Conteos de una combinación. Cada tupla es (aprobadas, total, falsos_positivos, negativas, falsos_negativos,
    positivas)."""

    combinacion: Combinacion
    aprobadas: int
    total: int
    falsos_positivos: int
    negativas: int
    falsos_negativos: int
    positivas: int
    por_tipo: Mapping[str, tuple[int, int, int, int, int, int]]


@dataclass(frozen=True)
class FilaVariacion:
    fila: int
    tipo: str
    fecha_evaluacion: date
    combinacion: Combinacion
    corridas: int
    aprobadas: int

    @property
    def varia(self) -> bool:
        return 0 < self.aprobadas < self.corridas


def combinaciones(cfg: ConfiguracionAvisos) -> tuple[Combinacion, ...]:
    return tuple(Combinacion(r, b, f) for r, b, f in product(cfg.reglas, cfg.lineas_base, cfg.fuentes_series))


def series_de_la_fuente(fuente: str, historial: Historial, oraculo: Mapping[str, tuple[SerieDeclarada, ...]],
                        cfg: ConfiguracionAvisos, ahora) -> tuple[SerieDeclarada, ...]:
    """Las series que se le pasan a la lógica de avisos según la fuente elegida."""
    if fuente == "oraculo":
        return oraculo.get(historial.usuario_id, ())
    return series_detectadas(historial, cfg, ahora)


def _recorte(filas: Sequence[FilaEvaluada], elegida, tope: int) -> set[str]:
    """Tipos que sobreviven al tope dentro de un grupo: los primeros por prioridad que cumplan `elegida`."""
    posicion = {tipo: i for i, tipo in enumerate(PRIORIDAD)}
    ordenadas = sorted((f for f in filas if elegida(f)), key=lambda f: posicion.get(f.tipo, len(PRIORIDAD)))
    return {f.tipo for f in ordenadas[:tope]}


def aplicar_tope(filas: Sequence[FilaEvaluada], tope: int) -> tuple[FilaEvaluada, ...]:
    """Aplica el tope de avisos por usuario y mes, con la misma prioridad a la intención y al disparo.

    Agrupa por corrida, usuario, fecha de evaluación y combinación. Con `tope` 0 o menor no cambia nada.
    """
    if tope <= 0:
        return tuple(filas)
    grupos: dict[tuple, list[FilaEvaluada]] = defaultdict(list)
    for f in filas:
        grupos[(f.corrida, f.usuario_id, f.fecha_evaluacion, f.combinacion)].append(f)
    resultado: list[FilaEvaluada] = []
    for propias in grupos.values():
        intencion = _recorte(propias, lambda f: f.esperado, tope)
        disparo = _recorte(propias, lambda f: f.dispara, tope)
        resultado.extend(replace(f, esperado=f.tipo in intencion, dispara=f.tipo in disparo) for f in propias)
    return tuple(resultado)


def _historial_de(historiales: Mapping[str, Historial], usuario: str) -> Historial:
    return historiales.get(usuario) or Historial(usuario, (), (), (), (), None)


def _filas_de_etiqueta(corrida: CorridaAvisos, e: Etiqueta, contextos: Mapping, combos: Sequence[Combinacion],
                       categoria_fuente: str) -> list[FilaEvaluada]:
    filas = []
    for c in combos:
        d = evaluar(contextos[c.fuente], e.tipo, c.regla, c.linea_base)
        filas.append(FilaEvaluada(corrida.nombre, e.fila, e.usuario_id, e.tipo, e.fecha_evaluacion, c,
                                  e.esperado(c.linea_base), d.dispara, d.valor, d.referencia, d.estadistico,
                                  d.causa, d.detalle, categoria_fuente))
    return filas


def evaluar_corrida(corrida: CorridaAvisos, cfg: ConfiguracionAvisos,
                    categoria_inferida: bool = False) -> tuple[FilaEvaluada, ...]:
    """Una fila por etiqueta y combinación. La categoría sale del oráculo o, aparte, de la inferida con K=2."""
    historiales = construir_historiales(corrida.movimientos, cfg)
    oraculo = series_oraculo(corrida.series)
    categorias = Categorias(corrida, cfg, categoria_inferida)
    combos = combinaciones(cfg)
    fuente = "inferida" if categoria_inferida else "oraculo"
    filas: list[FilaEvaluada] = []
    for e in corrida.etiquetas:
        historial = _historial_de(historiales, e.usuario_id)
        ahora = fin_del_dia_local(e.fecha_evaluacion)
        contextos = {f: contexto_en(historial, series_de_la_fuente(f, historial, oraculo, cfg, ahora),
                                    categorias.en(ahora), ahora, cfg) for f in cfg.fuentes_series}
        filas += _filas_de_etiqueta(corrida, e, contextos, combos, fuente)
    return aplicar_tope(filas, cfg.tope_avisos_mes)


def _conteos(filas: Sequence[FilaEvaluada]) -> tuple[int, int, int, int, int, int]:
    positivas = [f for f in filas if f.esperado]
    negativas = [f for f in filas if not f.esperado]
    return (sum(f.aprobada for f in filas), len(filas), sum(f.dispara for f in negativas), len(negativas),
            sum(not f.dispara for f in positivas), len(positivas))


def resumir(filas: Sequence[FilaEvaluada]) -> tuple[ResumenCombinacion, ...]:
    """Conteos por combinación, en el orden en que aparecen, con el desglose por tipo de aviso."""
    grupos: dict[Combinacion, list[FilaEvaluada]] = {}
    for f in filas:
        grupos.setdefault(f.combinacion, []).append(f)
    resumenes = []
    for combinacion, propias in grupos.items():
        tipos = sorted({f.tipo for f in propias})
        por_tipo = {t: _conteos([f for f in propias if f.tipo == t]) for t in tipos}
        resumenes.append(ResumenCombinacion(combinacion, *_conteos(propias), por_tipo))
    return tuple(resumenes)


def variacion(corridas: Sequence[Sequence[FilaEvaluada]]) -> tuple[FilaVariacion, ...]:
    """Cuántas corridas aprueban cada (fila, combinación). Una fila `varia` si aprueba en unas corridas y en otras no."""
    acumulado: dict[tuple, list[int]] = {}
    for filas in corridas:
        for f in filas:
            clave = (f.fila, f.tipo, f.fecha_evaluacion, f.combinacion)
            cuenta = acumulado.setdefault(clave, [0, 0])
            cuenta[0] += 1
            cuenta[1] += f.aprobada
    return tuple(FilaVariacion(k[0], k[1], k[2], k[3], c[0], c[1]) for k, c in sorted(
        acumulado.items(), key=lambda kc: (kc[0][0], kc[0][3].regla, kc[0][3].linea_base, kc[0][3].fuente)))
