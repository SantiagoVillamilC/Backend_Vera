"""Similitud de cadenas: Jaro-Winkler y coseno TF-IDF de trigramas.

Funciones puras sobre la biblioteca estandar. Reciben texto ya normalizado:
no se cambian mayusculas ni tildes aqui, eso es responsabilidad de quien llama.
Este modulo no fija ningun umbral de similitud: se calibra aparte.
"""

from __future__ import annotations

import math
import re
from collections import Counter
from collections.abc import Mapping, Sequence
from types import MappingProxyType

# Winkler solo aplica la bonificacion de prefijo si la similitud Jaro supera este valor.
_UMBRAL_BONIFICACION = 0.7

# Sufijos y genericos legales que no aportan a la identidad del comercio: se descartan de las
# caracteristicas (palabras y n-gramas de caracter) antes de vectorizar.
PALABRAS_GENERICAS = frozenset({"SAS", "LTDA", "SA", "SUCURSAL", "TIENDA", "ALMACEN"})

_PALABRA = re.compile(r"[^\W_]+", re.UNICODE)
_GENERICO = re.compile(
    r"\b(?:SAS|LTDA|SA|SUCURSAL|TIENDA|ALMACEN)\b", re.IGNORECASE
)


def _coincidencias(a: str, b: str) -> tuple[list[str], list[str]]:
    """Caracteres coincidentes de a y de b, cada uno en su orden de aparicion."""
    ventana = max(max(len(a), len(b)) // 2 - 1, 0)
    usado_b = [False] * len(b)
    de_a: list[int] = []
    for i, car in enumerate(a):
        inicio, fin = max(0, i - ventana), min(len(b), i + ventana + 1)
        for j in range(inicio, fin):
            if not usado_b[j] and b[j] == car:
                usado_b[j] = True
                de_a.append(i)
                break
    return [a[i] for i in de_a], [b[j] for j, u in enumerate(usado_b) if u]


def jaro(a: str, b: str) -> float:
    """Similitud de Jaro en [0, 1]. Cadena vacia da 0."""
    if not a or not b:
        return 0.0
    ca, cb = _coincidencias(a, b)
    m = len(ca)
    if m == 0:
        return 0.0
    transposiciones = sum(x != y for x, y in zip(ca, cb)) // 2
    return (m / len(a) + m / len(b) + (m - transposiciones) / m) / 3


def jaro_winkler(a: str, b: str, p: float = 0.1, prefijo_max: int = 4) -> float:
    """Jaro-Winkler: Jaro mas bonificacion por prefijo comun (hasta prefijo_max)."""
    if not 0.0 <= p <= 0.25:
        raise ValueError("p debe estar en [0, 0.25] para que el resultado no pase de 1")
    base = jaro(a, b)
    if base <= _UMBRAL_BONIFICACION:
        return base
    largo = 0
    for x, y in zip(a[:prefijo_max], b[:prefijo_max]):
        if x != y:
            break
        largo += 1
    return base + largo * p * (1.0 - base)


def trigramas(texto: str, n: int = 3) -> tuple[str, ...]:
    """N-gramas de caracteres con un espacio de relleno a cada lado, con repetidos."""
    if n < 1:
        raise ValueError("n debe ser al menos 1")
    relleno = f" {texto} "
    return tuple(relleno[i : i + n] for i in range(len(relleno) - n + 1))


def quitar_genericos(texto: str) -> str:
    """Texto sin los genericos legales como palabras completas, con espacios colapsados.

    Solo elimina la palabra exacta (frontera de palabra), de modo que un nombre como
    `ALMACENES` o `TIENDAS` no pierde su token por contener un generico.
    """
    return re.sub(r"\s+", " ", _GENERICO.sub(" ", texto)).strip()


def palabras(texto: str) -> tuple[str, ...]:
    """Palabras alfanumericas en mayusculas, sin genericos legales, en su orden de aparicion."""
    return tuple(p for p in _PALABRA.findall(texto.upper()) if p not in PALABRAS_GENERICAS)


def caracteristicas(texto: str, n: int = 3) -> Counter[str]:
    """Bolsa combinada de palabras (`w:`) y n-gramas de caracter (`c:`) del texto limpio."""
    conteo: Counter[str] = Counter(f"w:{p}" for p in palabras(texto))
    conteo.update(f"c:{g}" for g in trigramas(quitar_genericos(texto), n))
    return conteo


def ajustar_idf(corpus: Sequence[str], n: int = 3) -> Mapping[str, float]:
    """IDF suavizado log((1+N)/(1+df))+1 por n-grama. Mapeo de solo lectura."""
    total = len(corpus)
    df: Counter[str] = Counter()
    for texto in corpus:
        df.update(set(trigramas(texto, n)))
    idf = {g: math.log((1 + total) / (1 + c)) + 1.0 for g, c in df.items()}
    return MappingProxyType(idf)


def vector_tfidf(texto: str, idf: Mapping[str, float], n: int = 3) -> dict[str, float]:
    """Vector TF-IDF disperso de n-gramas del texto. Un n-grama ausente del idf usa el idf maximo.

    Es el unico vectorizador del modulo: `coseno_tfidf` y quien necesite un vector lo reutilizan.
    """
    idf_max = max(idf.values(), default=1.0)
    conteo = Counter(trigramas(texto, n))
    return {g: tf * idf.get(g, idf_max) for g, tf in conteo.items()}


def ajustar_idf_ampliado(corpus: Sequence[str], n: int = 3) -> Mapping[str, float]:
    """IDF suavizado log((1+N)/(1+df))+1 sobre palabras y n-gramas de caracter del texto limpio."""
    total = len(corpus)
    df: Counter[str] = Counter()
    for texto in corpus:
        df.update(caracteristicas(texto, n).keys())
    idf = {g: math.log((1 + total) / (1 + c)) + 1.0 for g, c in df.items()}
    return MappingProxyType(idf)


def vector_tfidf_ampliado(texto: str, idf: Mapping[str, float], n: int = 3) -> dict[str, float]:
    """Vector TF-IDF de palabras y n-gramas con tf sublineal `1 + log(tf)`, no la cuenta cruda.

    Una caracteristica ausente del idf usa el idf maximo, igual que `vector_tfidf`. El tf sublineal
    amortigua la repeticion: una aparicion pesa 1.0 y dos pesan `1 + log(2)`.
    """
    idf_max = max(idf.values(), default=1.0)
    return {
        g: (1.0 + math.log(tf)) * idf.get(g, idf_max)
        for g, tf in caracteristicas(texto, n).items()
    }


def coseno_vectores(a: Mapping[str, float], b: Mapping[str, float]) -> float:
    """Coseno entre dos vectores dispersos ya construidos, en [0, 1]. Vector nulo da 0."""
    norma = math.sqrt(sum(w * w for w in a.values())) * math.sqrt(
        sum(w * w for w in b.values())
    )
    if norma == 0.0:
        return 0.0
    punto = sum(w * b[g] for g, w in a.items() if g in b)
    return min(1.0, max(0.0, punto / norma))


def coseno_tfidf(a: str, b: str, idf: Mapping[str, float], n: int = 3) -> float:
    """Coseno entre vectores TF-IDF de n-gramas. Un n-grama ausente usa el idf maximo."""
    return coseno_vectores(vector_tfidf(a, idf, n), vector_tfidf(b, idf, n))
