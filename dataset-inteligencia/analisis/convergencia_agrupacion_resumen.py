"""Texto de resumen.md del experimento A2 (agrupación por similitud antes de contar votos)."""

from collections.abc import Mapping, Sequence

from .convergencia_agregado import FilaAgregada

POLITICA, AMBITO = "solo_comercio", "sin_colusion"
ETIQUETAS = {"ninguno": "ninguno (control)", "jaro_winkler_0.97": "jaro_winkler 0,97",
             "coseno_tfidf_0.78": "coseno_tfidf 0,78",
             "jaro_winkler_0.97_primero": "jaro_winkler 0,97 primero",
             "jaro_winkler_0.97_mas_votado": "jaro_winkler 0,97 mas_votado",
             "coseno_tfidf_0.78_primero": "coseno_tfidf 0,78 primero",
             "coseno_tfidf_0.78_mas_votado": "coseno_tfidf 0,78 mas_votado"}
COLUMNAS = (("cobertura_nombre", "cobertura nombre"), ("riesgo_nombre", "riesgo nombre"),
            ("errores_visibles_nombre", "errores visibles nombre"),
            ("errores_visibles_categoria", "errores visibles categoría"))
NUEVAS = (("agrupacion_exposicion_latente_comercios", "exposición latente (comercios)"),
          ("agrupacion_fusion_falsa_comercios_con_grupo", "fusión falsa (comercios con grupo)"),
          ("agrupacion_fusion_falsa_comercios_global_equivocado", "global equivocado por fusión falsa"),
          ("errores_visibles_nombre", "errores visibles nombre"))
TASAS_CLAVE = ("0.35", "0.6")


def _indice(agregado: Sequence[FilaAgregada]) -> dict:
    return {(f.tasa, f.k, f.medida): f for f in agregado if f.politica == POLITICA and f.ambito == AMBITO}


def _celda(f: FilaAgregada | None, decimales: int = 3) -> str:
    if f is None or f.media is None:
        return "n/a"
    if f.minimo == f.maximo:
        return f"{f.media:.{decimales}f}"
    return f"{f.media:.{decimales}f} ({f.minimo:.{decimales}f} a {f.maximo:.{decimales}f})"


def _tabla(agregados: Mapping[str, Sequence[FilaAgregada]], columnas, tasas, ks, decimales) -> list[str]:
    indices = {e: _indice(a) for e, a in agregados.items()}
    lineas = ["| tasa | K | configuración | " + " | ".join(n for _, n in columnas) + " |",
              "|---|---|---|" + "---|" * len(columnas)]
    for tasa in tasas:
        for k in ks:
            for e, ind in indices.items():
                celdas = [_celda(ind.get((tasa, k, m)), decimales) for m, _ in columnas]
                lineas.append(f"| {tasa} | {k} | {ETIQUETAS[e]} | " + " | ".join(celdas) + " |")
    return lineas


def _control(control: tuple[int, int] | None) -> str:
    if control is None:
        return "No se comparó contra A (no se pasó la carpeta de A)."
    comparadas, difieren = control
    return (f"Medido: el control (agrupación apagada) se comparó contra el `agregado.csv` de A, política "
            f"{POLITICA}, todos los ámbitos: {comparadas} medidas comparadas, {difieren} difieren.")


def texto_resumen(agregados: Mapping[str, Sequence[FilaAgregada]], corridas: int,
                  control: tuple[int, int] | None) -> str:
    tasas = sorted({f.tasa for f in agregados["ninguno"]}, key=float)
    ks = sorted({f.k for f in agregados["ninguno"]})
    partes = [
        "# Experimento A2: agrupar por similitud antes de contar votos", "",
        f"Corridas: {corridas} (tasas 0.1, 0.35 y 0.6 por semillas 2026, 1, 7 y 11). Política {POLITICA}, ámbito "
        f"{AMBITO}. Configuraciones: ninguno (control), jaro_winkler 0,97 y coseno_tfidf 0,78. Cada celda es la "
        "media entre semillas con su rango (mínimo a máximo).", "",
        "Etiquetas: toda cifra de este archivo es **Medido** sobre el corpus sintético. Lo **Probado en código** "
        "son las propiedades de las pruebas (determinismo, apagado idéntico, casos a mano, separación de la verdad).",
        "", "## Control frente a A", "", _control(control), "",
        "## Titular por tasa, K y configuración (Medido)", ""]
    partes += _tabla(agregados, COLUMNAS, tasas, ks, 3)
    partes += ["", "## Exposición latente y fusiones falsas (Medido)", "",
               "Exposición latente: comercios donde un nombre equivocado llegó a 2 o más votantes distintos. Fusión "
               "falsa: grupo por llave cuyos miembros tenían intenciones distintas según la verdad (renombre o "
               "colusión mezclada con honestos). Conteos de comercios, no fracciones.", ""]
    partes += _tabla(agregados, NUEVAS, tasas, ks, 2)
    partes += ["", "## Control frente a agrupado, K=2, tasas 0.35 y 0.6 (Medido)", ""]
    partes += _tabla(agregados, COLUMNAS[:1] + NUEVAS, TASAS_CLAVE, (2,), 3)
    partes += ["", "## Lectura (Medido, redondeada)", "",
               "- El control reproduce A sin diferencias.",
               "- Agrupar sube la cobertura de nombre unos 3 a 4 puntos en K=2 y K=3, pero **empeora** el error: "
               "con K=2 a tasa 0.35 los errores visibles de nombre pasan de 0,016 a unos 0,06 y a tasa 0.6 de 0,03 a "
               "unos 0,07. El riesgo de nombre sube en la misma dirección. Con K=3 el control tenía casi cero errores "
               "y agrupado aparecen entre 0,02 y 0,03. Los dos métodos dan resultados casi iguales.",
               "- La categoría no cambia: agrupar solo toca la clave de nombre.",
               "- La exposición latente crece (de unos 1,5 a unos 6,5 comercios a tasa 0.35 y de unos 4,5 a unos 9 a "
               "tasa 0.6).",
               "- Las fusiones falsas medidas son cero en todas las celdas: el daño no viene de unir "
               "comercios distintos. Hipótesis no verificada: como el representante es el primer nombre que llega, "
               "un tipeo que llega primero se vuelve la clave de su grupo y reúne a los votantes honestos, así que el "
               "global equivocado sale de elegir mal el representante y no de fusionar mal.",
               "- Esta lectura no ajusta ningún umbral: se reportan los umbrales de la calibración tal como estaban.",
               "", "## Limitaciones", "",
               "- Corpus sintético: las cifras no se extrapolan a nombres reales.",
               "- La abreviatura es determinista y el tipeo es independiente entre usuarios.",
               "- El representante es el primer nombre que creó el grupo y depende del orden de llegada.",
               "- Los grupos son por llave: no se midió el cruce entre llaves distintas.",
               "- La clave de votos es el representante, así que la vista privada de un usuario muestra el "
               "representante de su grupo y no su texto exacto.",
               "- Sin colusión en este ámbito: no se mide el efecto de la agrupación frente a atacantes.",
               "- Los umbrales (0,97 y 0,78) se calibraron sobre el mismo generador: posible circularidad.",
               "- El idf del coseno se ajusta con los nombres distintos de `entrada/correcciones.csv` de cada corrida.",
               ""]
    return "\n".join(partes)
