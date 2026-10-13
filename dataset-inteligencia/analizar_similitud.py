#!/usr/bin/env python3
"""Calibra el umbral de similitud para "mismo comercio" con una semilla de entrenamiento y varias de validación.

    python3 analizar_similitud.py [--entrenamiento RUTA] [--validacion RUTA [RUTA ...]] [--resultados RUTA]

Salida en resultados/similitud/<id>/ (ignorado por git): resumen.md y medidas.csv. Este arnés es el evaluador:
lee la verdad con `cargar_verdad` y la entrada con `cargar_entrada`, y entrega filas ya cargadas a las funciones
puras de `analisis/`. Si falta una corrida, informa y se detiene sin inventar datos.
"""

import argparse
import statistics
import sys
from functools import lru_cache, partial
from pathlib import Path

from analisis.cargar import cargar_entrada, cargar_verdad, sha256_de
from analisis.pagos import normalizar_nombre
from analisis.reporte import asegurar_gitignore, escribir_csv, hash_de_json, texto
from analisis.similitud import ajustar_idf, coseno_tfidf, jaro_winkler
from analisis.similitud_calibracion import (CRITERIO_MAX_F1, CRITERIO_RECALL_CON_PRECISION, CRITERIOS, Medida,
                                            PRECISION_MINIMA, UMBRALES, elegir_umbral, medir, puntuar)
from analisis.similitud_pares import Par, construir_pares

RAIZ = Path(__file__).resolve().parent
BASE = RAIZ / "salida" / "multitud" / "tasa-0.35"
ENTRENAMIENTO = BASE / "2026-3000-A"
VALIDACION = tuple(BASE / f"{s}-3000-A" for s in (1, 7, 11))
RESULTADOS_POR_DEFECTO = RAIZ / "resultados"
TABLAS = ("comercios", "correcciones_verdad")
ARCHIVOS = ("entrada/correcciones.csv", "verdad/comercios.csv", "verdad/correcciones_verdad.csv")
BASES = ("todos", "sin_abreviatura", "solo_tipeo")
SIN_IDENTICOS = "+sin_identicos"
ANTERIOR = "similitud-bcb6b8d59eba"
CORTES = BASES + tuple(b + SIN_IDENTICOS for b in BASES)
ENCABEZADO = ("metodo", "criterio", "corrida", "rol", "corte", "umbral", "tp", "fp", "fn", "precision", "recall", "f1")
# Pares explícitos: (texto a, texto b, grupo). El abreviado es el ruido de RUTA VELOZ COMIDAS, no un canónico.
PARES_RUTA = (("RUTA-VELOZ", "RUTA-VELOZ-COMIDAS", "ruta_veloz canónico, negativo"),
              ("RUTA-VELOZ-COM", "RUTA-VELOZ-COMIDAS", "ruta_veloz abreviado contra su comercio, positivo"),
              ("RUTA-VELOZ-COM", "RUTA-VELOZ", "ruta_veloz abreviado contra el otro, negativo difícil"))


def _pares(ruta: Path) -> tuple[tuple[Par, ...], list[str]]:
    verdad = cargar_verdad(ruta, TABLAS)
    entrada = cargar_entrada_correcciones(ruta)
    comercios = verdad.tablas["comercios"]
    pares = construir_pares(comercios, entrada, verdad.tablas["correcciones_verdad"])
    return pares, [normalizar_nombre(f["nombre_canonico"]) for f in comercios]


def cargar_entrada_correcciones(ruta: Path):
    return cargar_entrada(ruta).correcciones


def es_identico(p: Par) -> bool:
    """Negativo con canónico idéntico en llaves distintas: irreducible para cualquier puntaje de texto."""
    return (not p.misma_llave) and (not p.ambiguo) and normalizar_nombre(p.texto_a) == normalizar_nombre(p.texto_b)


def _corte(pares, corte: str):
    base = corte.removesuffix(SIN_IDENTICOS)
    out = [p for p in pares if not p.ambiguo]
    if base == "sin_abreviatura":
        out = [p for p in out if not (p.misma_llave and p.tipo_ruido == "abreviatura")]
    elif base == "solo_tipeo":
        out = [p for p in out if not (p.misma_llave and p.tipo_ruido != "tipeo")]
    if corte.endswith(SIN_IDENTICOS):
        out = [p for p in out if not es_identico(p)]
    return out


def _es_dificil_d1(p: Par) -> bool:
    return (not p.misma_llave) and (not p.ambiguo) and p.misma_empresa and "D1" in normalizar_nombre(p.texto_a)


def _metodos(idf):
    # Memoizar no cambia el resultado: los puntajes son funciones puras de los dos textos.
    return {"jaro_winkler": lru_cache(maxsize=None)(jaro_winkler),
            "coseno_tfidf": lru_cache(maxsize=None)(partial(coseno_tfidf, idf=idf))}


def _fila(metodo, criterio, corrida, rol, corte, m: Medida) -> list[str]:
    return [metodo, criterio, corrida, rol, corte, texto(m.umbral), str(m.tp), str(m.fp), str(m.fn),
            texto(m.precision), texto(m.recall), texto(m.f1)]


def _puntuados_por_corte(pares, puntaje):
    """Puntúa cada par una sola vez y reparte los puntajes entre los cortes."""
    utiles = [p for p in pares if not p.ambiguo]
    por_par = dict(zip(map(id, utiles), puntuar(utiles, puntaje)))
    return {c: tuple(por_par[id(p)] for p in _corte(utiles, c)) for c in CORTES}


def calibrar(entrenamiento: Path, validacion: list[Path]):
    pares_ent, corpus = _pares(entrenamiento)
    pares_val = {r.name: _pares(r)[0] for r in validacion}
    metodos = _metodos(ajustar_idf(corpus))
    filas, resultados = [], {}
    for metodo, puntaje in metodos.items():
        ent = _puntuados_por_corte(pares_ent, puntaje)
        val = {n: _puntuados_por_corte(p, puntaje) for n, p in pares_val.items()}
        barridos = {"": tuple(medir(ent["todos"], u) for u in UMBRALES),
                    SIN_IDENTICOS: tuple(medir(ent["todos" + SIN_IDENTICOS], u) for u in UMBRALES)}
        for sufijo, barrido in barridos.items():
            filas += [_fila(metodo, "barrido" + sufijo, entrenamiento.name, "entrenamiento", "todos", m) for m in barrido]
        for criterio, sufijo in [(c, x) for c in CRITERIOS for x in barridos]:
            elegido = elegir_umbral(barridos[sufijo], criterio)
            criterio += sufijo
            res = {"umbral": elegido.umbral, "entrenamiento": {}, "validacion": {}}
            for corte in CORTES:
                m = medir(ent[corte], elegido.umbral)
                res["entrenamiento"][corte] = m
                filas.append(_fila(metodo, criterio, entrenamiento.name, "entrenamiento", corte, m))
                for nombre in pares_val:
                    m = medir(val[nombre][corte], elegido.umbral)
                    res["validacion"].setdefault(corte, {})[nombre] = m
                    filas.append(_fila(metodo, criterio, nombre, "validacion", corte, m))
            resultados[(metodo, criterio)] = res
    return filas, resultados, metodos, pares_ent, pares_val


def _u(umbral) -> str:
    return "ninguno (se abstiene siempre)" if umbral is None else f"{umbral:.2f}"


def _rango(valores) -> str:
    return f"{statistics.mean(valores):.3f} ({min(valores):.3f} a {max(valores):.3f})"


def _tabla_corte(resultados, corte: str) -> list[str]:
    out = ["| método | criterio | umbral | precisión entrenamiento | recall entrenamiento | precisión validación | recall validación |",
           "|---|---|---|---|---|---|---|"]
    for (metodo, criterio), r in resultados.items():
        e, v = r["entrenamiento"][corte], list(r["validacion"][corte].values())
        out.append(f"| {metodo} | {criterio} | {_u(r['umbral'])} | {e.precision:.3f} | {e.recall:.3f} | "
                   f"{_rango([m.precision for m in v])} | {_rango([m.recall for m in v])} |")
    return out


def _tabla_validacion(resultados, corte: str) -> list[str]:
    out = ["| método | criterio | semilla | umbral | TP | FP | FN | precisión | recall |", "|---|---|---|---|---|---|---|---|---|"]
    for (metodo, criterio), r in resultados.items():
        for nombre, m in r["validacion"][corte].items():
            out.append(f"| {metodo} | {criterio} | {nombre} | {_u(m.umbral)} | {m.tp} | {m.fp} | {m.fn} | "
                       f"{m.precision:.3f} | {m.recall:.3f} |")
    return out


def _tabla_dificil(resultados, metodos, pares_todos: dict[str, tuple[Par, ...]]) -> list[str]:
    filas_par = [(normalizar_nombre(a), normalizar_nombre(b), g) for a, b, g in PARES_RUTA]
    vistos = {}
    for pares in pares_todos.values():
        for p in pares:
            if _es_dificil_d1(p):
                vistos.setdefault((normalizar_nombre(p.texto_a), normalizar_nombre(p.texto_b)), p)
    filas_par += [(a, b, "tiendas_d1 sucursal, negativo") for (a, b), _ in sorted(vistos.items())]
    cab = ["| par (canonizado) | grupo | " + " | ".join(f"{m} {c} ({_u(r['umbral'])})" for (m, c), r in resultados.items()) + " |",
           "|---|---|" + "---|" * len(resultados)]
    filas, fusiones = [], {k: 0 for k in resultados}
    negativos = [x for x in filas_par if "positivo" not in x[2].split(",")[-1] or "negativo" in x[2]]
    for a, b, grupo in filas_par:
        celdas = []
        for (m, c), r in resultados.items():
            s = metodos[m](a, b)
            fusiona = r["umbral"] is not None and s >= r["umbral"]
            if (a, b, grupo) in negativos:
                fusiones[(m, c)] += fusiona
            celdas.append(f"{s:.3f} {'FUSIONA' if fusiona else 'separa'}")
        filas.append(f"| {a} / {b} | {grupo} | " + " | ".join(celdas) + " |")
    resumen = ("| negativos fusionados | " + str(len(negativos)) + " negativos | "
               + " | ".join(f"{fusiones[k]} de {len(negativos)}" for k in resultados) + " |")
    return cab + filas + [resumen]


def _conteos_corrida(pares) -> str:
    pos = sum(p.misma_llave for p in pares)
    amb = sum(p.ambiguo for p in pares)
    ident = sum(es_identico(p) for p in pares)
    neg = len(pares) - pos - amb
    return f"{pos} | {neg} | {ident} | {amb}"


def _tabla_conteos(pares_todos) -> list[str]:
    out = ["| corrida | positivos | negativos | de ellos canónico idéntico en llaves distintas | ambiguo (colusión o cambio real) |",
           "|---|---|---|---|---|"]
    out += [f"| {n} | {_conteos_corrida(p)} |" for n, p in pares_todos.items()]
    return out


def _tabla_piso(resultados, pares_todos) -> list[str]:
    """Techo de precisión por los negativos idénticos: con el umbral más permisivo que los fusiona."""
    out = ["| corrida | negativos idénticos (FP mínimos con cualquier umbral hasta 0.99) | positivos | techo de precisión con todos los positivos |", "|---|---|---|---|"]
    for n, p in pares_todos.items():
        ident, pos = sum(es_identico(x) for x in p), sum(x.misma_llave for x in p)
        out.append(f"| {n} | {ident} | {pos} | {pos / (pos + ident):.3f} |")
    return out


def _texto_resumen(entrenamiento, validacion, resultados, metodos, pares_todos, anterior) -> str:
    cortes_texto = {
        "todos": "Corte i: todos los positivos",
        "sin_abreviatura": "Corte ii: sin abreviatura (la abreviatura del generador es determinista)",
        "solo_tipeo": "Corte iii: solo ruido de tipeo entre los positivos"}
    L = [f"> Nota: este resumen reemplaza a `{anterior}` por un error de etiquetado. Aquella calibración comparaba el "
         "nombre_puesto_canonico (que ya traía el ruido) con el canónico del destino, así que tipeo, abreviatura y "
         "mayúsculas quedaron como negativos y los positivos eran todos texto idéntico. La carpeta anterior se conserva sin cambios.", "",
         "# Calibración del umbral de similitud para mismo comercio", "",
         f"Entrenamiento: {entrenamiento.name} (tasa 0.35). Validación: {', '.join(r.name for r in validacion)}. "
         "Umbrales de 0.50 a 0.99 en pasos de 0.01. El idf se ajusta solo con los nombres canónicos de entrenamiento.", "",
         "Resultado: óptimo sobre el corpus sintético, no óptimo real. La etiqueta sale de la identidad de la verdad, nunca de un puntaje.", "",
         "## Regla de etiquetado", "",
         "- Una corrección apunta a `llave_destino` por construcción. Con ruido de escritura (tipeo, abreviatura, mayúsculas, tildes, con `es_correcta` verdadero) el par es positivo aunque el texto difiera.",
         "- Sin ruido y con el mismo nombre canonizado: positivo.",
         "- Sin ruido de escritura y con nombre distinto (colusión, etiqueta errónea o cambio real): ambiguo, fuera de la calibración, sin etiqueta.",
         "- Negativos: pares de nombre canónico entre llaves distintas (`misma_empresa` aparte para sucursales).", "",
         "## Conteos por corrida", ""] + _tabla_conteos(pares_todos) + [
         "", "## Piso de falsos positivos irreducible", "",
         "Los negativos con canónico idéntico en llaves distintas tienen puntaje 1.0 con cualquier método de texto, así que "
         "ningún umbral de la rejilla los separa. Son un piso de falsos positivos que no depende del método.", ""] + _tabla_piso(resultados, pares_todos) + [
         "", f"Criterios: `{CRITERIO_MAX_F1}` (máximo F1) y `{CRITERIO_RECALL_CON_PRECISION}` (mayor recall con precisión de al menos "
         f"{PRECISION_MINIMA}). Cada criterio se ajusta dos veces: con los negativos idénticos y con sufijo `{SIN_IDENTICOS}` sin ellos, "
         "para que el criterio de precisión no quede vacío por una causa que no es del método. Empates por el umbral más alto. "
         "Cada celda de validación es media (mínimo a máximo) entre semillas.", ""]
    for base in BASES:
        for sufijo, titulo in (("", "con los negativos idénticos"), (SIN_IDENTICOS, "sin los negativos idénticos")):
            L += [f"## {cortes_texto[base]}, {titulo}", ""] + _tabla_corte(resultados, base + sufijo) + [""]
    L += ["## Negativo difícil", "",
          "RUTA VELOZ frente a RUTA VELOZ COMIDAS, el nombre abreviado RUTA VELOZ COM frente a su comercio (control positivo) y frente a RUTA VELOZ, y las sucursales de TIENDAS D1. "
          "Los pares de RUTA VELOZ son explícitos, no pares canónicos del corpus. FUSIONA significa que el umbral declararía mismo comercio.", ""] + \
         _tabla_dificil(resultados, metodos, pares_todos) + [
         "", "## Detalle por semilla de validación (corte i, con idénticos)", ""] + _tabla_validacion(resultados, "todos") + [
         "", "## Etiquetas de verificación", "",
         "- **Medido (sobre este corpus sintético)**: conteos, umbrales, TP, FP, FN, precisión, recall, F1, el piso de falsos positivos y las decisiones sobre el negativo difícil. Salen de `medidas.csv` y de las tablas de este resumen.",
         "- **Probado en código**: la regla de etiquetado (tipeo, abreviatura, mayúsculas positivos, colusión ambigua), la exclusión de ambiguos, la rejilla, la monotonía del recall, el respeto de la precisión mínima y el determinismo (`tests/test_analisis_similitud_pares.py`, `tests/test_analisis_similitud_calibracion.py`).", "",
         "## Limitaciones", "",
         "- Corpus sintético: ninguna cifra es desempeño con datos reales.",
         "- La abreviatura del generador es determinista, por eso existe el corte sin abreviatura y el corte con todos los positivos está inflado.",
         "- El tipeo es independiente entre usuarios en el generador, algo que datos reales no garantizan.",
         "- Una sola tasa de corrección (0.35), así que no se sabe cómo cambia el umbral con otra tasa.",
         "- Calibración posiblemente circular: el generador produce el ruido que el método detecta, y el umbral puede quedar ajustado a ese ruido.",
         "- Los ambiguos (colusión o cambio real) no se calibran: no se sabe qué umbral los resolvería.",
         "- El idf se ajusta con pocos cientos de nombres canónicos de una sola corrida.",
         "- La precisión sin predicciones se define como 1.0 (vacua), así que el criterio b puede elegir un umbral que se abstiene siempre.", ""]
    return "\n".join(L)


def correr(entrenamiento: Path, validacion: list[Path], resultados_raiz: Path) -> Path:
    faltan = [str(r) for r in [entrenamiento, *validacion] if not (r / "verdad" / "comercios.csv").is_file()]
    if faltan:
        raise FileNotFoundError(f"No existen las corridas: {', '.join(faltan)}. No se inventa nada.")
    filas, resultados, metodos, pares_ent, pares_val = calibrar(entrenamiento, validacion)
    pares_todos = {entrenamiento.name: pares_ent, **pares_val}
    hashes = {r.name: sha256_de(r, ARCHIVOS) for r in [entrenamiento, *validacion]}
    identificador = hash_de_json({"entradas": hashes, "umbrales": "0.50-0.99", "criterios": CRITERIOS,
                                  "regla": "identidad-v2"})[:12]
    carpeta = Path(resultados_raiz) / "similitud" / f"similitud-{identificador}"
    asegurar_gitignore(Path(resultados_raiz))
    carpeta.mkdir(parents=True, exist_ok=True)
    escribir_csv(carpeta / "medidas.csv", ENCABEZADO, filas)
    (carpeta / "resumen.md").write_text(
        _texto_resumen(entrenamiento, validacion, resultados, metodos, pares_todos, ANTERIOR),
        encoding="utf-8", newline="\n")
    for (metodo, criterio), r in resultados.items():
        print(f"{metodo} {criterio}: umbral {_u(r['umbral'])}")
    return carpeta


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(description="Calibración del umbral de similitud.")
    p.add_argument("--entrenamiento", type=Path, default=ENTRENAMIENTO)
    p.add_argument("--validacion", type=Path, nargs="+", default=list(VALIDACION))
    p.add_argument("--resultados", type=Path, default=RESULTADOS_POR_DEFECTO)
    a = p.parse_args(argv)
    try:
        carpeta = correr(a.entrenamiento, a.validacion, a.resultados)
    except (FileNotFoundError, ValueError) as error:
        print(f"Error: {error}", file=sys.stderr)
        return 2
    print(f"resultados: {carpeta}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
