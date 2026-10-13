"""Secciones de resumen.md sobre riesgo y cobertura: tabla titular, ámbitos, densidad, bootstrap y etiquetas.

El titular es el ámbito `sin_colusion`. Se usa el marco de clasificación selectiva (El-Yaniv y Wiener, 2010): la
cobertura es la masa aceptada (comercios con valor global) y el riesgo es el error sobre lo aceptado.
"""

from collections.abc import Sequence

from .bootstrap import frase
from .convergencia_bootstrap_riesgo import FilaBootstrapRiesgo
from .convergencia_config import ConfigConvergencia
from .convergencia_medidas import AMBITOS_CONTAMINADOS, KS_COTA
from .convergencia_tablas import Obtener, celda, numero, tabla

AMBITO_TITULAR = "sin_colusion"
MARCA_CONTAMINADO = "contaminado por la población de colusión"
MEDIDAS_TITULAR = ("cobertura_nombre", "riesgo_nombre", "errores_visibles_nombre", "cobertura_categoria",
                   "riesgo_categoria", "errores_visibles_categoria")
ENCABEZADO_TITULAR = ["comercios", "cobertura nombre", "riesgo nombre", "errores visibles nombre",
                      "cobertura categoría", "riesgo categoría", "errores visibles categoría"]


def etiqueta_ambito(ambito: str) -> str:
    if ambito in AMBITOS_CONTAMINADOS:
        return f"{ambito} ({MARCA_CONTAMINADO})"
    return f"{ambito} (titular)" if ambito == AMBITO_TITULAR else ambito


def _filas_de_riesgo(tasa: str, cfg: ConfigConvergencia, ambito: str, obtener: Obtener, con_tasa: bool) -> list[list[str]]:
    p = cfg.politica_principal
    return [([tasa] if con_tasa else []) + [f"K={k}", celda(obtener(tasa, p, k, ambito, "n_comercios"), 1)]
            + [celda(obtener(tasa, p, k, ambito, m)) for m in MEDIDAS_TITULAR] for k in cfg.ks]


def seccion_titular(cfg: ConfigConvergencia, tasas: Sequence[str], obtener: Obtener) -> list[str]:
    filas = [f for tasa in tasas for f in _filas_de_riesgo(tasa, cfg, AMBITO_TITULAR, obtener, True)]
    return ["## Titular: riesgo y cobertura en sin_colusion", "",
            "Cada celda es la media entre semillas con su rango (mínimo a máximo). Cobertura es la fracción de comercios con "
            "valor global. Riesgo es el error entre los comercios consolidados (1 menos el acierto entre consolidados). "
            "Errores visibles es la fracción de todos los comercios cuyo valor global es distinto del vigente verdadero, así "
            "que errores visibles es riesgo por cobertura y la abstención no cuenta como error.", ""] + tabla(
                ["tasa", "K"] + ENCABEZADO_TITULAR, filas)


def seccion_cobertura_riesgo(tasa: str, cfg: ConfigConvergencia, ambito: str, obtener: Obtener) -> list[str]:
    return [f"#### Cobertura, riesgo y errores visibles, ámbito {etiqueta_ambito(ambito)}", ""] + tabla(
        ["K"] + ENCABEZADO_TITULAR, _filas_de_riesgo(tasa, cfg, ambito, obtener, False))


def seccion_acierto_total(tasa: str, cfg: ConfigConvergencia, obtener: Obtener) -> list[str]:
    p = cfg.politica_principal
    filas = [[etiqueta_ambito(a), f"K={k}", celda(obtener(tasa, p, k, a, "acierto_total_nombre")),
              celda(obtener(tasa, p, k, a, "acierto_total_categoria"))]
             for a in ("sin_colusion", "prueba_sin_colusion", "todos", "prueba") for k in cfg.ks]
    return ["#### Acierto total (secundario)", "",
            "El acierto total cuenta cada comercio sin valor global como fallo, así que castiga la abstención y mezcla "
            "dos efectos distintos (cobertura y riesgo). Se conserva solo como referencia y no es el titular.", ""] + tabla(
                ["ámbito", "K", "acierto total nombre", "acierto total categoría"], filas)


def _poblacion_colusion(cfg: ConfigConvergencia, tasa: str, obtener: Obtener) -> str:
    p, k = cfg.politica_principal, cfg.k_referencia
    total, libres = obtener(tasa, p, k, "todos", "n_comercios"), obtener(tasa, p, k, "sin_colusion", "n_comercios")
    if total is None or libres is None or total.media is None or libres.media is None or not total.media:
        return ""
    colusion, todos = round(total.media - libres.media), round(total.media)
    return f" (alrededor de {colusion} de {todos} comercios, {round(100 * colusion / todos)} %)"


def seccion_ambitos(cfg: ConfigConvergencia, tasas: Sequence[str], obtener: Obtener) -> list[str]:
    poblacion = _poblacion_colusion(cfg, tasas[0], obtener) if tasas else ""
    return ["## Ámbitos", "",
            "- `sin_colusion` es el ámbito titular: excluye los comercios de la población de colusión y el caso a mano de "
            "la droguería.",
            "- `prueba_sin_colusion` es la intersección del conjunto `prueba` con `sin_colusion`.",
            "- `todos` y `prueba` quedan marcados como contaminados por la población de colusión inyectada" + poblacion
            + ", que consolida el nombre con todos los K y deja categorías equivocadas sin relevo cuando T=3. Se conservan "
            "para comparar con la primera ejecución y no son titulares.",
            ""]


def _fila_densidad(tasa: str, cfg: ConfigConvergencia, obtener: Obtener, medidas: Sequence[str]) -> list[str]:
    p, k = cfg.politica_principal, cfg.ks[0]
    return [tasa] + [celda(obtener(tasa, p, k, AMBITO_TITULAR, m), 2) for m in medidas]


def _filas_de_cota(tasa: str, cfg: ConfigConvergencia, obtener: Obtener) -> list[list[str]]:
    p = cfg.politica_principal
    return [[tasa, f"K={k}", celda(obtener(tasa, p, k, AMBITO_TITULAR, f"correctores_fraccion_ge{k}"))]
            + [celda(obtener(tasa, p, k, AMBITO_TITULAR, f"cobertura_{d}")) for d in ("nombre", "categoria")]
            for k in cfg.ks if k in KS_COTA]


def seccion_densidad(cfg: ConfigConvergencia, tasas: Sequence[str], obtener: Obtener) -> list[str]:
    cinco = ("min", "p25", "mediana", "p75", "max")
    pagadores = [f"pagadores_{c}" for c in cinco] + ["pagadores_fraccion_un_solo"]
    correctores = [f"correctores_{c}" for c in cinco] + [f"correctores_fraccion_ge{k}" for k in KS_COTA]
    cabeza = ["mín", "p25", "mediana", "p75", "máx"]
    return (["## Densidad de pagadores y correctores", "",
             "Comercios de sin_colusion. Cada cifra se calcula por corrida y luego se promedia entre semillas. No depende de "
             "K ni de la política. La cobertura depende de cuántos usuarios distintos pagan y corrigen a cada comercio, "
             "así que esta tabla sirve para trasladar el resultado a un volumen real de usuarios. Un mínimo de 0 pagadores "
             "es un comercio del catálogo sin ningún pago en la corrida.", "",
             "#### Pagadores distintos por comercio", ""]
            + tabla(["tasa"] + cabeza + ["fracción con un solo pagador"], [_fila_densidad(t, cfg, obtener, pagadores) for t in tasas])
            + ["#### Correctores distintos por comercio", ""]
            + tabla(["tasa"] + cabeza + [f"fracción con al menos {k}" for k in KS_COTA],
                    [_fila_densidad(t, cfg, obtener, correctores) for t in tasas])
            + ["#### Cobertura frente a su cota", "",
               "La fracción de comercios con al menos K correctores distintos es la cota superior de la cobertura, porque un "
               "valor solo se consolida con K usuarios distintos. La diferencia entre la cota y la cobertura la explican los "
               "desacuerdos entre las correcciones.", ""]
            + tabla(["tasa", "K", "cota superior de la cobertura", "cobertura nombre", "cobertura categoría"],
                    [f for t in tasas for f in _filas_de_cota(t, cfg, obtener)]))


def seccion_bootstrap_riesgo(filas: Sequence[FilaBootstrapRiesgo], b: int, semilla: int) -> list[str]:
    cuerpo = [[f.tasa, f.ambito, f.medida, f.dimension, f.comparacion, str(f.n), numero(f.intervalo.observada, 4),
               f"{numero(f.intervalo.inferior, 4)} a {numero(f.intervalo.superior, 4)}",
               frase(f.intervalo, f.comparacion.split()[-1])] for f in filas]
    encabezado = ["tasa", "ámbito", "medida", "dimensión", "comparación", "comercios", "diferencia observada", "IC 95 %",
                  "lectura"]
    return ["## Bootstrap pareado de errores visibles y cobertura", "",
            f"Remuestreo de comercios con reemplazo sobre las cuatro semillas de cada tasa, en los ámbitos sin colusión. "
            f"B = {b}, semilla {semilla}, los mismos remuestreos para todos los K. Diferencia entre dos K sobre los mismos "
            "comercios. La columna comercios es el n de cada comparación: en prueba_sin_colusion es pequeño y los "
            "intervalos son anchos.", ""] + tabla(encabezado, cuerpo)


ETIQUETAS = (
    "## Etiquetas de verificación", "",
    "- **Medido**: cobertura, riesgo, errores visibles, días hasta consolidar y residuo, en el ámbito sin_colusion. "
    "Salen de las corridas contra la verdad del generador y valen para este corpus sintético.",
    "- **Probado en código** (consecuencias de la regla y del generador, fijadas con pruebas, no son una medición):",
    "  - Envenenamiento: un comercio queda envenenado exactamente cuando K es menor o igual a T, el número de "
    "correcciones de colusión. Que el relevo corrija el error depende de la razón de 3 honestos contra T colusores y de "
    "los empates, que conservan al incumbente.",
    "  - Personas promovidas: las llaves de comercio del generador son todas de clase `comercio` y las de personas son "
    "celular, correo, alfanumérico o documento, así que el filtro por tipo de llave es perfecto por construcción del "
    "generador. Que la política solo_comercio no promueva personas no dice nada sobre datos reales, donde el tipo de "
    "llave puede no separar comercios de personas.",
    "  - casos_vista: son 13 casos escritos a mano, no una muestra.", "",
    "## Hallazgo: cambio_nombre", "",
    "La regla de relevo estricto no puede seguir un cambio real. El nombre viejo y el nuevo empatan 2 contra 2, y el "
    "empate conserva al incumbente. Por eso 11 de 13 casos de vista aprueban (fallan los dos posteriores al cambio con "
    "K=1 y K=2) y el retraso tras un cambio real queda vacío. Con K=3 aprueban solo 3 de 13.", "",
    "Limitación a declarar: cualquier regla que deje a K votos recientes reemplazar un nombre consolidado también deja a "
    "K colusores sobrescribir uno correcto. El generador solo tiene colusores antes de los honestos, así que una regla de "
    "deriva solo podría verse bien en el generador actual. No se probó ninguna.", "")
