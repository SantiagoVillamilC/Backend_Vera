"""Medidas del arnés para la agrupación por similitud: exposición latente y fusiones falsas.

Este módulo es evaluador: lee la verdad (historial de nombres y tipo de ruido) y nunca lo importa el replicador
ni el agrupador. Todas las medidas se cuentan por comercio sobre las llaves que se le pasan.

- Exposición latente: comercio donde un nombre distinto del vigente verdadero (en el instante del voto) llegó a
  2 o más votantes distintos, haya o no llegado a ser global.
- Fusión falsa: grupo de una llave cuyos miembros tenían intenciones distintas. La intención de una corrección es el
  nombre verdadero vigente en su instante, salvo que sea de ruido `colusion`, cuya intención es el nombre deliberado.
  Se mide por llave (los grupos no cruzan llaves), así que la fusión falsa solo puede mezclar versiones de un mismo
  comercio (renombre) o colusión con honestos.
"""

from collections import defaultdict
from collections.abc import Mapping, Sequence

from .convergencia import Correccion, Replicador
from .convergencia_evaluacion import TIPO_RUIDO_COLUSION, valor_verdadero_en


def _intencion(c: Correccion, original: Correccion, historial: Sequence[Mapping], ruido: Mapping[str, str]) -> str:
    if ruido.get(c.correccion_id) == TIPO_RUIDO_COLUSION:
        return "COLUSION:" + original.nombre
    return valor_verdadero_en(historial, c.ocurrido_en, "nombre") or "?"


def _por_llave(r: Replicador, historial: Mapping[str, Sequence[Mapping]], ruido: Mapping[str, str]) -> dict:
    """Por llave: usuarios por (nombre equivocado), intenciones por grupo."""
    usuarios_malos: dict[str, dict[str, set[str]]] = defaultdict(lambda: defaultdict(set))
    intenciones: dict[str, dict[str, set[str]]] = defaultdict(lambda: defaultdict(set))
    for original, c in zip(r.correcciones, r.correcciones_efectivas()):
        hist = historial.get(c.llave, ())
        if c.nombre != valor_verdadero_en(hist, c.ocurrido_en, "nombre"):
            usuarios_malos[c.llave][c.nombre].add(c.usuario_id)
        intenciones[c.llave][c.nombre].add(_intencion(c, original, hist, ruido))
    return {"malos": usuarios_malos, "intenciones": intenciones}


def medidas_de_agrupacion(r: Replicador, llaves: Sequence[str], historial: Mapping[str, Sequence[Mapping]],
                          ruido: Mapping[str, str], corte) -> dict:
    """Conteos sobre `llaves` (los comercios del ámbito) con el estado al instante `corte`."""
    datos = _por_llave(r, historial, ruido)
    estado = r.estado_en(corte)
    expuestos = con_grupo_falso = global_falso = grupos_falsos = 0
    for llave in llaves:
        malos = datos["malos"].get(llave, {})
        expuestos += any(len(u) >= 2 for u in malos.values())
        falsos = {rep for rep, i in datos["intenciones"].get(llave, {}).items() if len(i) > 1}
        grupos_falsos += len(falsos)
        con_grupo_falso += bool(falsos)
        global_ = estado.global_de(llave, "nombre")
        verdad = valor_verdadero_en(historial.get(llave, ()), corte, "nombre")
        global_falso += global_ in falsos and global_ != verdad
    return {"n_comercios": len(llaves), "exposicion_latente_comercios": expuestos,
            "fusion_falsa_grupos": grupos_falsos, "fusion_falsa_comercios_con_grupo": con_grupo_falso,
            "fusion_falsa_comercios_global_equivocado": global_falso}
