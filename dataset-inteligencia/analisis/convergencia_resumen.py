"""Texto de resumen.md del experimento A: titular de riesgo y cobertura, y tablas por tasa con media y rango entre semillas.

Función pura y determinista: sin horas, sin rutas absolutas. Cada celda es `media (mínimo a máximo)` entre
las semillas de la tasa, o solo la media si todas coinciden. Los valores indefinidos van `n/a`.
"""

from collections.abc import Sequence

from .bootstrap import frase
from .convergencia_agregado import FilaAgregada, FilaBootstrap
from .convergencia_bootstrap_riesgo import FilaBootstrapRiesgo
from .convergencia_config import ConfigConvergencia
from .convergencia_evaluacion import ResultadoCorrida
from .convergencia_resumen_riesgo import (ETIQUETAS, etiqueta_ambito, seccion_acierto_total, seccion_ambitos,
                                          seccion_bootstrap_riesgo, seccion_cobertura_riesgo, seccion_densidad,
                                          seccion_titular)
from .convergencia_tablas import Obtener, celda, indice, numero, tabla

AVISOS = (
    "La clave de nombre es el nombre canonizado. Eso deshace de forma trivial el ruido de mayúsculas, tildes y "
    "formato, así que la agrupación por similitud NO se mide en esta pasada. Los tipeos y las abreviaturas siguen "
    "siendo claves distintas.",
    "K no se calibra: no hay entrenamiento ni validación. Se reportan todas las semillas con media, mínimo y máximo.",
    "La regla de promoción y de relevo es una propuesta de diseño sin fuente (Waze, OSM y Google Maps no publican un K).",
    "Una persona promovida (llave con es_comercio falso que alcanza un valor global) es una falla de privacidad.",
    "Las cifras son sobre un corpus sintético. No se pueden leer como desempeño con datos reales.",
)
AMBITOS_DIAS = ("sin_colusion", "prueba_sin_colusion", "todos", "prueba")
AMBITOS_COBERTURA = ("prueba_sin_colusion", "todos", "prueba")


def _seccion_dias(tasa: str, cfg: ConfigConvergencia, ambito: str, obtener: Obtener) -> list[str]:
    p = cfg.politica_principal
    medidas = ("dias_consolidar_nombre_n", "dias_consolidar_nombre_mediana", "dias_consolidar_nombre_p90",
               "dias_consolidar_categoria_n", "dias_consolidar_categoria_mediana", "dias_consolidar_categoria_p90")
    filas = [[f"K={k}"] + [celda(obtener(tasa, p, k, ambito, m), 1 if m.endswith("_n") else 2) for m in medidas]
             for k in cfg.ks]
    encabezado = ["K", "nombre: comercios", "nombre: mediana (días)", "nombre: p90", "categoría: comercios",
                  "categoría: mediana (días)", "categoría: p90"]
    return [f"#### Días hasta consolidar, ámbito {etiqueta_ambito(ambito)}", "",
            "Mediana y p90 por corrida entre los comercios que consolidaron un valor correcto, luego media entre semillas.",
            ""] + tabla(encabezado, filas)


def _seccion_espurios_y_retraso(tasa: str, cfg: ConfigConvergencia, obtener: Obtener) -> list[str]:
    p = cfg.politica_principal
    medidas = ("espurios_nombre", "espurios_nombre_revertidos", "espurios_categoria", "espurios_categoria_revertidos",
               "retraso_nombre_cambios", "retraso_nombre_alcanzados", "retraso_nombre_mediana",
               "retraso_categoria_cambios", "retraso_categoria_alcanzados", "retraso_categoria_mediana")
    filas = [[f"K={k}"] + [celda(obtener(tasa, p, k, "todos", m), 2) for m in medidas] for k in cfg.ks]
    encabezado = ["K", "espurios nombre", "revertidos", "espurios categoría", "revertidos", "cambios de nombre",
                  "alcanzados", "retraso mediana (días)", "cambios de categoría", "alcanzados", "retraso mediana (días)"]
    return [f"#### Cambios espurios y retraso tras un cambio real, ámbito {etiqueta_ambito('todos')}", ""] + tabla(
        encabezado, filas)


def _tamanos(agregado: Sequence[FilaAgregada], tasa: str) -> list[int]:
    prefijo, sufijo = "envenenamiento_T", "_comercios"
    medidas = {f.medida for f in agregado if f.tasa == tasa and f.medida.startswith(prefijo)
               and f.medida.endswith(sufijo)}
    return sorted(int(m[len(prefijo):-len(sufijo)]) for m in medidas)


def _seccion_envenenamiento(tasa: str, cfg: ConfigConvergencia, ambito: str, agregado: Sequence[FilaAgregada],
                            obtener: Obtener) -> list[str]:
    p = cfg.politica_principal
    sufijos = ("comercios", "envenenados", "fraccion", "dias_mediana", "sin_relevo")
    filas = [[f"T={t}", f"K={k}"] + [celda(obtener(tasa, p, k, ambito, f"envenenamiento_T{t}_{s}"), 2) for s in sufijos]
             for t in _tamanos(agregado, tasa) for k in cfg.ks]
    encabezado = ["T", "K", "comercios", "envenenados", "fracción envenenada", "días equivocada (mediana)",
                  "sin relevo al corte"]
    return [f"#### Envenenamiento por colusión, ámbito {ambito}", "",
            "T es el número de correcciones de colusión del comercio. Envenenado significa categoría global "
            "equivocada en algún momento. Mide justo la población de colusión, por eso solo existe en estos ámbitos.",
            ""] + tabla(encabezado, filas)


def _seccion_personas(tasa: str, cfg: ConfigConvergencia, obtener: Obtener) -> list[str]:
    nombres = [p.nombre for p in cfg.politicas]
    filas = [[f"K={k}", celda(obtener(tasa, nombres[0], k, "global", "personas_con_correcciones"), 1)]
             + [celda(obtener(tasa, n, k, "global", "personas_promovidas"), 1) for n in nombres] for k in cfg.ks]
    encabezado = ["K", "personas con correcciones"] + [f"promovidas con {n}" for n in nombres]
    return ["#### Personas promovidas (falla de privacidad)", ""] + tabla(encabezado, filas)


def _seccion_casos(tasa: str, cfg: ConfigConvergencia, resultados: Sequence[ResultadoCorrida],
                   obtener: Obtener) -> list[str]:
    p = cfg.politica_principal
    ids = sorted({d.caso_id for r in resultados if r.tasa == tasa for d in r.casos})
    medidas = ["casos_total", "casos_aprobados", "casos_columna_nombre_ok", "casos_columna_categoria_ok",
               "casos_columna_origen_ok"]
    encabezado = ["K", "casos", "aprobados", "nombre ok", "categoría ok", "origen ok"]
    filas = [[f"K={k}"] + [celda(obtener(tasa, p, k, "global", m), 1) for m in medidas]
             + [celda(obtener(tasa, p, k, "global", f"casos_{i}_aprobados"), 1) for i in ids] for k in cfg.ks]
    return ["#### Aprobación de los casos de vista", ""] + tabla(encabezado + [f"aprobados {i}" for i in ids], filas)


def _seccion_residuo(tasa: str, cfg: ConfigConvergencia, obtener: Obtener) -> list[str]:
    p = cfg.politica_principal
    medidas = ("n_comercios", "residuo_sin_categoria", "residuo_fraccion", "residuo_sin_correcciones",
               "residuo_con_correcciones_sin_consenso", "residuo_revela_naturaleza")
    filas = [[f"K={k}"] + [celda(obtener(tasa, p, k, "prueba_sin_colusion", m), 3 if m == "residuo_fraccion" else 1)
                           for m in medidas] for k in cfg.ks]
    encabezado = ["K", "comercios de prueba sin colusión", "sin categoría global", "fracción", "sin correcciones",
                  "con correcciones sin consenso", "nombre revela naturaleza"]
    return ["#### Residuo sin_categoria en prueba_sin_colusion", "",
            "Decide si hacen falta TF-IDF, MiniLM o un LLM: los comercios que el consenso no cubre y cuyo nombre "
            "revela la naturaleza son el techo de lo que un clasificador de texto podría resolver.", ""] + tabla(
                encabezado, filas)


def _seccion_bootstrap(bootstrap: Sequence[FilaBootstrap], b: int, semilla: int) -> list[str]:
    filas = [[f.tasa, f.ambito, f.dimension, f.comparacion, str(f.n), numero(f.intervalo.observada, 4),
              f"{numero(f.intervalo.inferior, 4)} a {numero(f.intervalo.superior, 4)}",
              frase(f.intervalo, f.comparacion.split()[-1])] for f in bootstrap]
    encabezado = ["tasa", "ámbito", "dimensión", "comparación", "comercios", "diferencia observada", "IC 95 %", "lectura"]
    return ["## Bootstrap pareado del acierto total (secundario)", "",
            f"Remuestreo de comercios con reemplazo sobre las cuatro semillas de cada tasa. B = {b}, semilla {semilla}. "
            "Diferencia de acierto total entre dos K sobre los mismos comercios. Los ámbitos todos y prueba están "
            "contaminados por la población de colusión.", ""] + tabla(encabezado, filas)


def _seccion_de_tasa(tasa: str, cfg: ConfigConvergencia, resultados: Sequence[ResultadoCorrida],
                     agregado: Sequence[FilaAgregada], obtener: Obtener) -> list[str]:
    semillas = sorted(r.semilla for r in resultados if r.tasa == tasa)
    lineas = [f"## Tasa de corrección {tasa}", "", f"Semillas: {', '.join(str(s) for s in semillas)}. "
              f"Política principal: {cfg.politica_principal}. La cobertura y el riesgo de sin_colusion están en el titular.",
              ""]
    for ambito in AMBITOS_COBERTURA:
        lineas += seccion_cobertura_riesgo(tasa, cfg, ambito, obtener)
    for ambito in AMBITOS_DIAS:
        lineas += _seccion_dias(tasa, cfg, ambito, obtener)
    lineas += _seccion_espurios_y_retraso(tasa, cfg, obtener)
    for ambito in ("todos", "prueba"):
        lineas += _seccion_envenenamiento(tasa, cfg, ambito, agregado, obtener)
    return lineas + (_seccion_personas(tasa, cfg, obtener) + _seccion_casos(tasa, cfg, resultados, obtener)
                     + _seccion_residuo(tasa, cfg, obtener) + seccion_acierto_total(tasa, cfg, obtener))


def texto_resumen(cfg: ConfigConvergencia, resultados: Sequence[ResultadoCorrida], agregado: Sequence[FilaAgregada],
                  bootstrap: Sequence[FilaBootstrap], bootstrap_riesgo: Sequence[FilaBootstrapRiesgo], b: int,
                  semilla: int) -> str:
    obtener = indice(agregado)
    tasas = sorted({r.tasa for r in resultados})
    lineas = ["# Experimento A: convergencia por multitud", "",
              f"K probados: {', '.join(str(k) for k in cfg.ks)}. Políticas de elegibilidad: "
              + ", ".join(p.nombre for p in cfg.politicas) + ". Corridas: " + str(len(resultados)) + ".", ""]
    lineas += seccion_titular(cfg, tasas, obtener) + seccion_ambitos(cfg, tasas, obtener)
    lineas += ["## Avisos", ""] + [f"- {aviso}" for aviso in AVISOS] + [""] + list(ETIQUETAS)
    for tasa in tasas:
        lineas += _seccion_de_tasa(tasa, cfg, resultados, agregado, obtener)
    lineas += seccion_densidad(cfg, tasas, obtener) + seccion_bootstrap_riesgo(bootstrap_riesgo, b, semilla)
    lineas += _seccion_bootstrap(bootstrap, b, semilla)
    return "\n".join(lineas).rstrip("\n") + "\n"
