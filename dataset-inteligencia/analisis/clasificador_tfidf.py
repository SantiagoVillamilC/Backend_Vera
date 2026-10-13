"""Clasificador TF-IDF de la categoria de un comercio a partir de su nombre canonico y su MCC.

Mide el techo de un clasificador de texto sobre el residuo `sin_categoria` del experimento A: entrena
con los comercios de `conjunto == "entrenamiento"` y evalua sobre los de `conjunto == "prueba"` con
`es_comercio == "true"`. El texto del vector es el nombre canonico mas, cuando la fila trae
`mcc_categoria`, un token propio `mcc:<codigo>`. Asi el MCC participa del entrenamiento y de la
prediccion. El vectorizador no se reimplementa: extiende el de `analisis.similitud` con
`ajustar_idf_ampliado` y `vector_tfidf_ampliado`, que combinan tokens de palabra (`w:`) con los
trigramas de caracter (`c:`) del texto ya limpio de sufijos y genericos legales, y aplican tf
sublineal `1 + log(tf)`. El `vector_tfidf` de trigramas puros queda intacto para las pruebas de
similitud. Un centroide por categoria es el
promedio de los vectores TF-IDF de sus textos de entrenamiento, y la prediccion es la categoria del
centroide de coseno mayor. Si el mejor coseno queda por debajo de `UMBRAL_COSENO` (parametro
`umbral` de `predecir`), el clasificador se abstiene y devuelve None en vez de forzar una categoria.
Es determinista: mismo corpus, mismos numeros.

Sobre esa base, un lexico semilla de palabras a categoria refuerza la prediccion cuando el coseno no
es concluyente: si el mejor coseno no alcanza `UMBRAL_COSENO_FUERTE`, las palabras del nombre se
comparan con las semillas via Jaro-Winkler (`analisis.similitud.jaro_winkler`) y, si alguna coincide
con `UMBRAL_JW_LEXICO` o mas, la categoria de esa semilla gana. Un coseno alto (>= `UMBRAL_COSENO_FUERTE`)
nunca se sobreescribe: el lexico solo entra cuando el coseno es debil. La semilla solo puede devolver
una categoria que exista entre los centroides del modelo. El lexico es dato editable: el built-in vive
en `LEXICO_SEMILLA` y `cargar_lexico` lo puede extender desde un archivo JSON de configuracion, que
`predecir` acepta por el parametro `lexico`.
"""

from __future__ import annotations

import json
from collections.abc import Iterable, Mapping
from dataclasses import dataclass
from pathlib import Path
from types import MappingProxyType

from .cargar import cargar_verdad
from .similitud import (
    ajustar_idf_ampliado,
    coseno_vectores,
    jaro_winkler,
    palabras,
    vector_tfidf_ampliado,
)

TABLAS = ("comercios", "empresas")

# Coseno minimo para aceptar la categoria del centroide mas cercano. Por debajo, `predecir` se
# abstiene (None) en vez de forzar una categoria. El valor es conservador: solo descarta textos casi
# ortogonales a todos los centroides, y se puede sobrescribir con el parametro `umbral`.
UMBRAL_COSENO = 0.1

# Coseno a partir del cual la prediccion del centroide es concluyente: el lexico semilla no la
# sobreescribe. Por debajo de este valor el coseno es debil y el lexico puede reforzar la prediccion.
UMBRAL_COSENO_FUERTE = 0.5

# Similitud Jaro-Winkler minima entre una palabra del nombre y una semilla para que la semilla vote.
# Alto a proposito: solo variantes morfologicas (plurales, tildes de una letra) deben disparar la
# semilla, no cualquier parecido lejano.
UMBRAL_JW_LEXICO = 0.85

# Lexico semilla palabra (en mayusculas, como las devuelve `analisis.similitud.palabras`) a categoria.
# Es dato editable: agregar una palabra nueva es anadir una entrada aqui, sin tocar la logica. Solo
# se consideran las semillas cuya categoria este entre los centroides del modelo.
LEXICO_SEMILLA: Mapping[str, str] = MappingProxyType(
    {
        "FARMACIA": "salud",
        "DROGUERIA": "salud",
        "CLINICA": "salud",
        "HOSPITAL": "salud",
        "OPTICA": "salud",
        "LABORATORIO": "salud",
        "CONSULTORIO": "salud",
        "ODONTOLOGIA": "salud",
        "MERCADO": "alimentos_mercado",
        "SUPERMERCADO": "alimentos_mercado",
        "MINIMERCADO": "alimentos_mercado",
        "PLAZA": "alimentos_mercado",
        "ABASTOS": "alimentos_mercado",
        "RESTAURANTE": "restaurantes_domicilios",
        "PIZZERIA": "restaurantes_domicilios",
        "ASADERO": "restaurantes_domicilios",
        "CAFETERIA": "restaurantes_domicilios",
        "HELADERIA": "restaurantes_domicilios",
        "UNIVERSIDAD": "educacion",
        "COLEGIO": "educacion",
        "ACADEMIA": "educacion",
        "ESCUELA": "educacion",
        "INSTITUTO": "educacion",
        "LICEO": "educacion",
        "BOUTIQUE": "compras",
        "ZAPATERIA": "compras",
        "JOYERIA": "compras",
        "CALZADO": "compras",
        "TRANSPORTE": "transporte",
        "TAXI": "transporte",
        "GASOLINERA": "transporte",
        "FLOTA": "transporte",
        "CINE": "entretenimiento",
        "TEATRO": "entretenimiento",
        "GIMNASIO": "entretenimiento",
        "DISCOTECA": "entretenimiento",
        "INMOBILIARIA": "vivienda_servicios",
        "CONSTRUCTORA": "vivienda_servicios",
        "ARRENDAMIENTO": "vivienda_servicios",
    }
)


def cargar_lexico(ruta: Path | str | None = None) -> Mapping[str, str]:
    """Lexico semilla: el built-in (`LEXICO_SEMILLA`) o uno extendido desde un archivo JSON.

    Con `ruta` None devuelve el `LEXICO_SEMILLA` del modulo, que es el comportamiento por defecto.
    Con una ruta lee un objeto JSON `{"PALABRA": "categoria"}` cuyas claves se normalizan a
    mayusculas y se fusionan SOBRE el built-in: agregar o corregir una semilla no exige tocar la
    logica, solo el archivo. Una ruta ausente devuelve el built-in en vez de fallar, para que la
    configuracion sea opcional.
    """
    if ruta is None:
        return LEXICO_SEMILLA
    camino = Path(ruta)
    if not camino.exists():
        return LEXICO_SEMILLA
    datos = json.loads(camino.read_text(encoding="utf-8"))
    fusion = dict(LEXICO_SEMILLA)
    fusion.update({str(palabra).upper(): str(categoria) for palabra, categoria in datos.items()})
    return MappingProxyType(fusion)


def _texto(nombre: str, mcc: str | None = None) -> str:
    """Texto del vector: el nombre mas un token propio `mcc:<codigo>` cuando hay MCC.

    Un MCC vacio o ausente deja el texto en el nombre solo, de modo que las filas sin
    `mcc_categoria` se entrenan y predicen exactamente como antes.
    """
    return f"{nombre} mcc:{mcc}" if mcc else nombre


@dataclass(frozen=True)
class Modelo:
    """Idf de los textos de entrenamiento y centroide TF-IDF por categoria."""

    idf: Mapping[str, float]
    centroides: Mapping[str, Mapping[str, float]]


def entrenar(filas: Iterable[Mapping[str, str]]) -> Modelo:
    """Entrena con filas de comercios (`nombre_canonico`, `categoria`, `mcc_categoria`) y devuelve el modelo.

    `mcc_categoria` es opcional: si la fila no la trae, el texto del vector es solo el nombre.
    """
    pares = [
        (fila["nombre_canonico"], fila.get("mcc_categoria"), fila["categoria"])
        for fila in filas
    ]
    idf = ajustar_idf_ampliado([_texto(nombre, mcc) for nombre, mcc, _ in pares])
    sumas: dict[str, dict[str, float]] = {}
    conteos: dict[str, int] = {}
    for nombre, mcc, categoria in pares:
        acumulado = sumas.setdefault(categoria, {})
        for gram, peso in vector_tfidf_ampliado(_texto(nombre, mcc), idf).items():
            acumulado[gram] = acumulado.get(gram, 0.0) + peso
        conteos[categoria] = conteos.get(categoria, 0) + 1
    centroides = {
        categoria: {gram: total / conteos[categoria] for gram, total in acumulado.items()}
        for categoria, acumulado in sumas.items()
    }
    return Modelo(idf, centroides)


def _categoria_lexico(
    nombre: str, categorias: Iterable[str], lexico: Mapping[str, str] = LEXICO_SEMILLA
) -> str | None:
    """Categoria de la semilla mas parecida a alguna palabra del nombre, o None si ninguna coincide.

    Compara cada palabra del nombre con cada semilla del `lexico` (el `LEXICO_SEMILLA` por defecto, o
    el que devuelva `cargar_lexico`) usando Jaro-Winkler y se queda con la semilla de mayor similitud
    que alcance `UMBRAL_JW_LEXICO`. Las semillas cuya categoria no este en `categorias` (los
    centroides del modelo) se ignoran: el lexico no inventa una categoria que el modelo no conozca.
    El empate entre categorias se rompe por orden alfabetico.
    """
    conocidas = set(categorias)
    votos: dict[str, float] = {}
    for palabra in palabras(nombre):
        for semilla, categoria in lexico.items():
            if categoria not in conocidas:
                continue
            similitud = jaro_winkler(palabra, semilla)
            if similitud >= UMBRAL_JW_LEXICO and similitud > votos.get(categoria, 0.0):
                votos[categoria] = similitud
    if not votos:
        return None
    return max(sorted(votos), key=lambda categoria: votos[categoria])


def predecir(
    modelo: Modelo,
    nombre: str,
    mcc: str | None = None,
    umbral: float = UMBRAL_COSENO,
    lexico: Mapping[str, str] = LEXICO_SEMILLA,
) -> str | None:
    """Categoria del comercio por coseno; el lexico semilla refuerza cuando el coseno es debil.

    Con un coseno alto (>= `UMBRAL_COSENO_FUERTE`) manda el centroide mas cercano. Con un coseno debil
    el `lexico` decide si alguna palabra del nombre coincide (Jaro-Winkler >= `UMBRAL_JW_LEXICO`) con
    una semilla de una categoria conocida. Si no hay semilla aplicable, se conserva el comportamiento
    por coseno: se devuelve la mejor categoria si supera `umbral` y None (abstencion) en caso
    contrario. Un nombre vacio no produce vector y da None. El MCC, cuando se pasa, se agrega al texto
    con el mismo token `mcc:<codigo>` del entrenamiento. El `lexico` por defecto es el
    `LEXICO_SEMILLA` del modulo; `cargar_lexico` produce uno extendido desde un archivo JSON. Los
    empates se rompen por orden alfabetico de la categoria, para que la salida sea determinista.
    """
    vector = vector_tfidf_ampliado(_texto(nombre, mcc), modelo.idf)
    mejor, mejor_puntaje = None, -1.0
    if vector:
        for categoria in sorted(modelo.centroides):
            puntaje = coseno_vectores(vector, modelo.centroides[categoria])
            if puntaje > mejor_puntaje:
                mejor, mejor_puntaje = categoria, puntaje
    # Coseno concluyente: la prediccion del centroide manda y el lexico no la sobreescribe.
    if mejor is not None and mejor_puntaje >= UMBRAL_COSENO_FUERTE:
        return mejor
    # Coseno debil: el lexico semilla puede reforzar la prediccion con una palabra conocida.
    categoria_lexico = _categoria_lexico(nombre, modelo.centroides, lexico)
    if categoria_lexico is not None:
        return categoria_lexico
    # Sin semilla aplicable: abstencion por umbral o la mejor categoria del coseno.
    if mejor is None or mejor_puntaje < umbral:
        return None
    return mejor


def _tasa(aciertos: int, total: int) -> float:
    """Razon aciertos/total. Sin casos evaluados la tasa es 0.0 (no hay nada que acertar)."""
    return aciertos / total if total else 0.0


def medir(ruta_corpus: Path | str, umbral: float = UMBRAL_COSENO) -> dict[str, int | float]:
    """Entrena con la verdad de entrenamiento y evalua sobre la de prueba. Devuelve los conteos.

    `exactitud` se calcula sobre las predicciones no abstenidas, `cobertura` es la fraccion de la
    prueba que no se abstuvo y `abstenciones` su conteo. Los bloques (`revela`, `ficticia`, `real`)
    conservan su `n_*` como conteo de filas del subconjunto y calculan su exactitud sobre las
    predicciones no abstenidas de ese subconjunto.
    """
    tablas = cargar_verdad(ruta_corpus, TABLAS).tablas
    comercios, empresas = tablas["comercios"], tablas["empresas"]
    ficticia = {empresa["empresa_id"]: empresa["ficticia"] for empresa in empresas}

    modelo = entrenar(fila for fila in comercios if fila["conjunto"] == "entrenamiento")
    prueba = [fila for fila in comercios
              if fila["conjunto"] == "prueba" and fila["es_comercio"] == "true"]
    predichos = [(predecir(modelo, fila["nombre_canonico"], fila.get("mcc_categoria"), umbral), fila)
                 for fila in prueba]
    evaluadas = [(pred, fila) for pred, fila in predichos if pred is not None]
    abstenciones = len(predichos) - len(evaluadas)

    def bloque(filtro) -> tuple[int, float]:
        n = sum(1 for _, fila in predichos if filtro(fila))
        del_bloque = [(pred, fila) for pred, fila in evaluadas if filtro(fila)]
        aciertos = sum(1 for pred, fila in del_bloque if pred == fila["categoria"])
        return n, _tasa(aciertos, len(del_bloque))

    n_prueba = len(predichos)
    aciertos = sum(1 for pred, fila in evaluadas if pred == fila["categoria"])
    n_revela, exactitud_revela = bloque(lambda fila: fila["nombre_revela_naturaleza"] == "true")
    n_ficticia, exactitud_ficticia = bloque(lambda fila: ficticia.get(fila["empresa_id"]) == "true")
    n_real, exactitud_real = bloque(lambda fila: ficticia.get(fila["empresa_id"]) == "false")
    return {
        "n_prueba": n_prueba,
        "exactitud": _tasa(aciertos, len(evaluadas)),
        "cobertura": _tasa(len(evaluadas), n_prueba),
        "abstenciones": abstenciones,
        "n_revela": n_revela,
        "exactitud_revela": exactitud_revela,
        "n_ficticia": n_ficticia,
        "exactitud_ficticia": exactitud_ficticia,
        "n_real": n_real,
        "exactitud_real": exactitud_real,
    }
