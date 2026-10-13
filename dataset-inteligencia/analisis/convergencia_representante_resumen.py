"""Texto de resumen.md del experimento A3: política de representante primero frente a mas_votado."""

from collections.abc import Mapping, Sequence

from .convergencia_agrupacion_resumen import AMBITO, COLUMNAS, NUEVAS, POLITICA, TASAS_CLAVE, _indice, _tabla
from .convergencia_agregado import FilaAgregada

CONFIG_AGRUPADAS = (("jaro_winkler_0.97", "jaro_winkler 0,97"), ("coseno_tfidf_0.78", "coseno_tfidf 0,78"))


def _media(agregados: Mapping[str, Sequence[FilaAgregada]], etiqueta: str, tasa: str, k: int, medida: str):
    fila = _indice(agregados[etiqueta]).get((tasa, k, medida))
    return None if fila is None else fila.media


def _veredicto(control: float, votado: float) -> str:
    if votado < control:
        return "queda por debajo del control"
    return "iguala al control" if votado == control else "no recupera el control, sigue por encima"


def _lectura(agregados: Mapping[str, Sequence[FilaAgregada]]) -> list[str]:
    lineas = []
    for tasa in TASAS_CLAVE:
        for base, nombre in CONFIG_AGRUPADAS:
            c, p, v = (_media(agregados, e, tasa, 2, "errores_visibles_nombre")
                       for e in ("ninguno", f"{base}_primero", f"{base}_mas_votado"))
            if None in (c, p, v):
                lineas.append(f"- K=2, tasa {tasa}, {nombre}: sin dato.")
                continue
            lineas.append(f"- Medido, K=2, tasa {tasa}, {nombre}: errores visibles de nombre control {c:.3f}, "
                          f"primero {p:.3f}, mas_votado {v:.3f}. Con la media, mas_votado {_veredicto(c, v)} "
                          f"({v - c:+.3f} frente al control).")
    return lineas


def texto_resumen_representante(agregados: Mapping[str, Sequence[FilaAgregada]], corridas: int,
                                control: tuple[int, int] | None) -> str:
    tasas = sorted({f.tasa for f in agregados["ninguno"]}, key=float)
    ks = sorted({f.k for f in agregados["ninguno"]})
    comparacion = ("No se comparó contra A (no se pasó la carpeta de A)." if control is None else
                   f"Medido: el control se comparó contra el `agregado.csv` de A, política {POLITICA}: {control[0]} "
                   f"medidas comparadas, {control[1]} difieren.")
    partes = [
        "# Experimento A3: representante primero frente a mas_votado", "",
        f"Corridas: {corridas} (tasas 0.1, 0.35 y 0.6 por semillas 2026, 1, 7 y 11). Política {POLITICA}, ámbito "
        f"{AMBITO}. Configuraciones: control (ninguno) y, para jaro_winkler 0,97 y coseno_tfidf 0,78, representante "
        "primero (el comportamiento de A2) y mas_votado. Cada celda es la media entre semillas con su rango (mínimo "
        "a máximo).", "",
        "Etiquetas: toda cifra de este archivo es **Medido** sobre el corpus sintético. Lo **Probado en código** son "
        "las propiedades de las pruebas: el default `primero` es idéntico al comportamiento anterior, el hash de la "
        "configuración no cambia con el default, determinismo con filas barajadas y el reetiquetado de los votos ya "
        "contados en casos a mano (`tests/test_analisis_convergencia_representante.py`).", "",
        "## Política mas_votado (Probado en código)", "",
        "El conteo de votos usa el ancla del grupo (el primer nombre que lo creó), que nunca cambia, así que "
        "promover o relevar no depende de la política. Lo que cambia es el nombre visible del grupo: el de más "
        "votantes distintos vigentes, con desempate por el voto que primero alcanzó ese conteo. Cuando cambia, los "
        "votos ya contados se reetiquetan con el nombre nuevo y el cambio del global se registra como un `Cambio` "
        "(antes y después). Las correcciones efectivas usan la etiqueta final del grupo.", "",
        "## Control frente a A", "", comparacion, "",
        "## Titular por tasa, K y configuración (Medido)", ""]
    partes += _tabla(agregados, COLUMNAS, tasas, ks, 3)
    partes += ["", "## Exposición latente y fusiones falsas (Medido)", "",
               "Exposición latente: comercios con un nombre equivocado que llegó a 2 o más votantes distintos "
               "(medido sobre la etiqueta final del grupo). Fusión falsa: grupo por llave con intenciones distintas "
               "según la verdad. Conteos de comercios.", ""]
    partes += _tabla(agregados, NUEVAS, tasas, ks, 2)
    partes += ["", "## Tabla K=2, tasas 0.35 y 0.6 (Medido, media y rango entre semillas)", ""]
    partes += _tabla(agregados, COLUMNAS[:1] + NUEVAS, TASAS_CLAVE, (2,), 3)
    partes += ["", "## Lectura (Medido, calculada de las medias)", ""] + _lectura(agregados)
    partes += ["", "## Limitaciones", "",
               "- Corpus sintético: las cifras no se extrapolan a nombres reales.",
               "- La abreviatura es determinista y el tipeo es independiente entre usuarios.",
               "- Los umbrales (0,97 y 0,78) se calibraron sobre el mismo generador: posible circularidad.",
               "- Solo política `solo_comercio` y ámbito sin colusión: no se mide el efecto frente a atacantes.",
               "- Sin bootstrap: el rango es mínimo y máximo entre 4 semillas por tasa, no un intervalo de confianza.",
               "- Los grupos son por llave y el ancla depende del orden de llegada.", ""]
    return "\n".join(partes)
