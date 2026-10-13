"""Texto de `resumen.md` del experimento C. Determinista: sin horas ni rutas, solo cifras de los resultados."""

from collections.abc import Sequence

from .avisos_config import ConfiguracionAvisos
from .avisos_evaluacion import Combinacion, FilaEvaluada, resumir, variacion
from .avisos_fondo import NOTA, agregar_fondo
from .avisos_resultado import ResultadoCorrida
from .reporte import texto

URL_Z = "https://pyod.readthedocs.io/en/latest/_modules/pyod/models/mad.html"
URL_YODLEE = "https://developer.yodlee.com/resources/yodlee/insights-details/docs/projected-low-balance"


def _coma(valor: float | int) -> str:
    return str(valor).replace(".", ",")


def _tabla(encabezado: Sequence[str], filas: Sequence[Sequence[str]]) -> str:
    lineas = ["| " + " | ".join(encabezado) + " |", "|" + "|".join("---" for _ in encabezado) + "|"]
    lineas += ["| " + " | ".join(f) + " |" for f in filas]
    return "\n".join(lineas)


def _etiqueta(c: Combinacion) -> str:
    return f"{c.regla} {c.linea_base} {c.fuente}"


def _pesos(valor) -> str:
    return "n/a" if valor is None else f"{float(valor) / 100:,.2f}"


def _introduccion(resultados: Sequence[ResultadoCorrida]) -> str:
    referencia = resultados[0].corrida
    usuarios = len({e.usuario_id for e in referencia.etiquetas})
    nombres = "\n".join(f"- {r.corrida.nombre}" for r in resultados)
    return (
        "# Avisos (experimento C): prueba de aceptación\n\n"
        f"Prueba de aceptación sobre {usuarios} usuarios y {len(referencia.etiquetas)} etiquetas escritas a mano, sin "
        "fórmula de umbral. No es una medición estadística: con tan pocas filas no hay tasas ni intervalos, solo se "
        "reporta si cada regla pasa o falla en cada fila y por qué. Ningún parámetro se ajustó a estas filas: las "
        "reglas se declararon antes de ejecutar. Todas son propuesta sin fuente, salvo el z modificado de Iglewicz y "
        f"Hoaglin con umbral 3,5 ({URL_Z}) y la ventana de 7 días de Yodlee Projected Low Balance ({URL_YODLEE}).\n\n"
        f"Corridas evaluadas, en este orden. La primera es la de referencia de las tablas por fila:\n\n{nombres}\n")


def _reglas(cfg: ConfiguracionAvisos) -> str:
    r = cfg.recurrencia
    return (
        "## Reglas declaradas de antemano\n\n"
        f"- Línea base mensual: mediana o media de los meses completos previos al mes de la fecha de evaluación que "
        f"tuvieron gasto. Un mes es completo si empieza en o después del primer movimiento del usuario. Con menos de "
        f"{cfg.meses_minimos} meses no se dispara ningún aviso (historial insuficiente).\n"
        f"- R1: el gasto acumulado del mes en curso (o el de una categoría) supera la línea base por "
        f"{_coma(cfg.multiplicador_gasto)}, "
        f"{'proyectado al cierre del mes' if cfg.prorrateo_acumulado else 'sin prorratear'}. "
        f"`caida_ingreso`: el ingreso del último mes completo queda "
        f"por debajo de (1 menos {_coma(cfg.caida_d)}) por la línea base, solo si MAD sobre mediana es menor que "
        f"{_coma(cfg.cv_robusto_max)}.\n"
        f"- R2: z modificado {_coma(cfg.z_factor)} por (x menos centro) sobre MAD mayor que {_coma(cfg.z_umbral)} "
        "(menor que su opuesto en `caida_ingreso`). Con MAD cero se usa el respaldo R1. Se califica contra la columna "
        "de la línea base que corresponde (mediana o media), con la mediana como lectura principal.\n"
        f"- `pago_no_alcanza`: una serie con pago esperado en los próximos {cfg.ventana_pago_dias} días y saldo menor "
        "que la suma de los montos esperados (mediana de los pagos de la serie).\n"
        f"- `cobro_nuevo`: una llave con primer pago en los últimos {cfg.ventana_cobro_dias} días que forma parte de "
        f"una serie o tiene al menos {cfg.pagos_minimos_cobro} pagos de monto similar (tolerancia "
        f"{_coma(cfg.tol_monto_cobro)}). Una llave con pagos anteriores a la ventana es conocida.\n"
        f"- Gasto: salidas de tipo {', '.join(cfg.tipos_asiento_gasto)}, sin retenciones ni envíos a personas. Ingreso: "
        f"entradas de tipo {', '.join(cfg.tipos_asiento_ingreso)} salvo la descripción "
        f"{', '.join(cfg.descripciones_no_ingreso)}. El reloj es el fin del día local de Colombia de la fecha de "
        "evaluación y solo cuentan movimientos anteriores o iguales.\n"
        "- Series: ORACULO usa las series de la verdad con `es_recurrente` verdadero y DETECTADAS usa el detector de "
        f"recurrencia con w_monto {_coma(r.w_monto)}, tol_monto {_coma(r.tol_monto)}, tolerancias de días "
        f"{', '.join(_coma(t) for t in r.tolerancias_dias)}, n_min {r.n_min} y umbral {_coma(r.umbral)} (el óptimo "
        "restringido del experimento B). Así el error de los avisos no se confunde con el del detector.\n")


def _tabla_resumen(filas: Sequence[FilaEvaluada]) -> str:
    filas_tabla = [[s.combinacion.regla, s.combinacion.linea_base, s.combinacion.fuente,
                    f"{s.aprobadas} de {s.total}", f"{s.falsos_positivos} de {s.negativas}",
                    f"{s.falsos_negativos} de {s.positivas}"] for s in resumir(filas)]
    return _tabla(["Regla", "Línea base", "Series", "Filas aprobadas", "Falsos positivos (sobre negativas)",
                   "Falsos negativos (sobre positivas)"], filas_tabla)


def _seccion_resumen(resultados: Sequence[ResultadoCorrida]) -> str:
    referencia = resultados[0]
    cobro = ("`cobro_nuevo` solo tiene una etiqueta negativa en los datos: solo puede medir falsos positivos y no dice "
             "nada de su sensibilidad.")
    return (f"## Tabla resumen ({referencia.corrida.nombre})\n\n{_tabla_resumen(referencia.filas)}\n\n{cobro}\n\n"
            f"### Filas aprobadas por corrida\n\n{_aprobadas_por_corrida(resultados)}\n")


def _aprobadas_por_corrida(resultados: Sequence[ResultadoCorrida]) -> str:
    combos = [s.combinacion for s in resumir(resultados[0].filas)]
    filas = []
    for r in resultados:
        por_combo = {s.combinacion: s for s in resumir(r.filas)}
        filas.append([r.corrida.nombre] + [f"{por_combo[c].aprobadas} de {por_combo[c].total}" for c in combos])
    return _tabla(["Corrida"] + [_etiqueta(c) for c in combos], filas)


def _celda(f: FilaEvaluada) -> str:
    return f"{'SI' if f.dispara else 'NO'} dispara, {'pasa' if f.aprobada else 'FALLA'}"


def _por_fila(filas: Sequence[FilaEvaluada]) -> str:
    combos = list(dict.fromkeys(f.combinacion for f in filas))
    encabezado = ["Fila", "Usuario", "Tipo", "Fecha", "Esperado (mediana, media)"] + [_etiqueta(c) for c in combos]
    cuerpo = []
    for n in sorted({f.fila for f in filas}):
        propias = {f.combinacion: f for f in filas if f.fila == n}
        primera = propias[combos[0]]
        esperado = ", ".join("si" if propias[next(c for c in combos if c.linea_base == b)].esperado else "no"
                             for b in ("mediana", "media"))
        cuerpo.append([str(n), primera.usuario_id[:8], primera.tipo, primera.fecha_evaluacion.isoformat(), esperado]
                      + [_celda(propias[c]) for c in combos])
    return _tabla(encabezado, cuerpo)


def _fallas(filas: Sequence[FilaEvaluada]) -> str:
    falladas = [f for f in filas if not f.aprobada]
    if not falladas:
        return "Ninguna fila falla en la corrida de referencia."
    cuerpo = [[str(f.fila), f.tipo, _etiqueta(f.combinacion), "si" if f.esperado else "no", "si" if f.dispara else "no",
               _pesos(f.referencia), _pesos(f.valor), "n/a" if f.estadistico is None else texto(float(f.estadistico)),
               f.causa] for f in falladas]
    return _tabla(["Fila", "Tipo", "Combinación", "Esperado", "Dispara", "Base usada", "Valor usado", "Estadístico",
                   "Causa"], cuerpo)


def _seccion_filas(resultados: Sequence[ResultadoCorrida]) -> str:
    referencia = resultados[0]
    return (f"## Fila por fila ({referencia.corrida.nombre})\n\nCada celda dice si el aviso dispara y si pasa o falla "
            f"frente a la etiqueta de su columna (mediana o media).\n\n{_por_fila(referencia.filas)}\n\n### Filas que fallan\n\n"
            f"{_fallas(referencia.filas)}\n")


def _seccion_variacion(resultados: Sequence[ResultadoCorrida]) -> str:
    cambian = [v for v in variacion([r.filas for r in resultados]) if v.varia]
    if not cambian:
        cuerpo = f"Ninguna fila cambia de resultado entre las {len(resultados)} corridas."
    else:
        cuerpo = _tabla(["Fila", "Tipo", "Combinación", "Corridas que aprueban"],
                        [[str(v.fila), v.tipo, _etiqueta(v.combinacion), f"{v.aprobadas} de {v.corridas}"]
                         for v in cambian])
    return ("## Variación entre corridas\n\nLos usuarios de los perfiles son iguales entre corridas salvo el comercio "
            f"elegido al azar. Filas y combinaciones cuyo resultado cambia:\n\n{cuerpo}\n")


def _seccion_inferida(resultados: Sequence[ResultadoCorrida]) -> str:
    referencia = resultados[0]
    oraculo = {s.combinacion: s for s in resumir(referencia.filas)}
    filas = [[_etiqueta(s.combinacion), f"{oraculo[s.combinacion].aprobadas} de {oraculo[s.combinacion].total}",
              f"{s.aprobadas} de {s.total}"] for s in resumir(referencia.inferidas)]
    return ("## Categoría inferida por el experimento A con K=2 (fuente secundaria)\n\n"
            "La categoría de cada llave sale del consenso de K=2 usuarios a la fecha de evaluación y una llave sin "
            "consenso queda como `sin_categoria`. Solo cambia `categoria_sobre_media`. Se reporta aparte y no "
            f"reemplaza la fuente principal.\n\n{_tabla(['Combinación', 'Con categoría del oráculo', 'Con categoría inferida'], filas)}\n")


def _seccion_fondo(resultados: Sequence[ResultadoCorrida]) -> str:
    referencia = resultados[0]
    filas = [[r.grupo, r.combinacion.regla, r.combinacion.linea_base, r.combinacion.fuente, r.tipo, str(r.usuarios),
              str(r.evaluables), str(r.avisos), texto(r.avisos / r.evaluables) if r.evaluables else "n/a"]
             for r in agregar_fondo(referencia.fondo)]
    return (f"## Avisos emitidos a usuarios sin etiqueta ({referencia.corrida.nombre})\n\n"
            f"{NOTA}. Se evalúa a cada usuario al final de cada mes y en la fecha final de la corrida. `fondo` son los "
            "usuarios sin ninguna etiqueta y `perfil_etiquetado` son los perfiles en fechas que no tienen etiqueta. "
            "Los avisos por evaluable son avisos emitidos sobre evaluaciones con historial suficiente. Las demás "
            "corridas están en `fondo_resumen.csv` y el detalle por usuario y mes en `fondo_detalle.csv`.\n\n"
            + _tabla(["Grupo", "Regla", "Línea base", "Series", "Tipo", "Usuarios", "Evaluables", "Avisos",
                      "Avisos por evaluable"], filas) + "\n")


def _limitaciones() -> str:
    return (
        "## Limitaciones\n\n"
        "- Son pocas etiquetas escritas a mano: sirven para ver si una regla puede reproducir los casos que se "
        "describieron y no para estimar un error.\n"
        "- Las reglas no se calibraron. Un cambio de umbral que haga pasar más filas sería ajustar a las etiquetas.\n"
        "- El acumulado del mes no se prorratea: al final de un mes casi cualquier usuario supera la mediana de sus "
        "meses previos en cerca de la mitad de los casos, por eso el conteo del fondo no es una tasa de falsos.\n"
        "- La categoría de cada llave sale de la verdad (oráculo) y no de lo que el sistema sabría.\n")


def texto_resumen(cfg: ConfiguracionAvisos, resultados: Sequence[ResultadoCorrida]) -> str:
    """Resumen legible. La corrida de referencia de las tablas es la primera de la lista."""
    return "\n".join([_introduccion(resultados), _reglas(cfg), _seccion_resumen(resultados),
                      _seccion_filas(resultados), _seccion_variacion(resultados), _seccion_inferida(resultados),
                      _seccion_fondo(resultados), _limitaciones()])
