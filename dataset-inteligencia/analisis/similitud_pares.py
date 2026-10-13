"""Pares de nombres etiquetados para calibrar un umbral de similitud.

Regla de etiquetado (por diseño de la verdad): una corrección apunta a `llave_destino` por construcción,
así que su nombre_puesto es de ese comercio. Con ruido de escritura (tipo distinto de ninguno, colusion y etiqueta_erronea, con es_correcta verdadero) el par es
positivo aunque el texto difiera. Sin ruido y con el mismo texto canonizado también es positivo. Sin ruido y
con texto distinto (colusión o cambio real de nombre) el par es ambiguo: no es positivo ni negativo y la
calibración lo excluye.

Función pura: recibe filas ya cargadas y no lee archivos. Las etiquetas salen siempre de la
identidad (llave y empresa), nunca de una puntuación de similitud, así el umbral que se calibre
después no puede contaminar sus propias etiquetas.
"""

from dataclasses import dataclass
from itertools import combinations
from typing import Mapping, Sequence

from .pagos import normalizar_nombre

NINGUNO = "ninguno"
# Tipos de la verdad que no son ruido de escritura: el nombre puede ser de otro comercio.
NO_ESCRITURA = frozenset((NINGUNO, "colusion", "etiqueta_erronea"))


@dataclass(frozen=True)
class Par:
    texto_a: str
    texto_b: str
    misma_llave: bool
    misma_empresa: bool
    origen: str  # "correccion" o "canonico"
    tipo_ruido: str  # tipo de ruido o "ninguno"
    ambiguo: bool = False  # colusión o cambio real: sin etiqueta, fuera de la calibración


def _ordenar(a: str, b: str) -> tuple[str, str]:
    return (a, b) if a <= b else (b, a)


def _pares_canonicos(comercios: Sequence[Mapping[str, str]]) -> list[Par]:
    filas = sorted(comercios, key=lambda f: f["llave_destino"])
    pares = []
    for x, y in combinations(filas, 2):
        if x["llave_destino"] == y["llave_destino"]:
            continue
        a, b = _ordenar(x["nombre_canonico"], y["nombre_canonico"])
        pares.append(Par(a, b, False, x["empresa_id"] == y["empresa_id"], "canonico", NINGUNO))
    return pares


def _pares_correcciones(comercios, correcciones, correcciones_verdad) -> list[Par]:
    por_llave = {f["llave_destino"]: f for f in comercios}
    empresa_por_nombre: dict[str, str] = {}
    for f in sorted(comercios, key=lambda f: f["llave_destino"]):
        empresa_por_nombre.setdefault(f["nombre_canonico"], f["empresa_id"])
    por_id = {f["correccion_id"]: f for f in correcciones_verdad}
    pares = []
    for c in sorted(correcciones, key=lambda f: f["correccion_id"]):
        destino = por_llave.get(c["llave_destino"])
        real = por_id.get(c["correccion_id"])
        if destino is None or real is None:
            continue
        canonico = destino["nombre_canonico"]
        tipo = real["tipo_ruido"] or NINGUNO
        correcta = real.get("es_correcta", "true").strip().lower() in ("1", "true")
        con_ruido = tipo not in NO_ESCRITURA and correcta
        mismo_texto = normalizar_nombre(c["nombre_puesto"]) == normalizar_nombre(canonico)
        misma_llave = con_ruido or mismo_texto
        ambiguo = not misma_llave
        empresa_real = empresa_por_nombre.get(real["nombre_puesto_canonico"])
        misma_empresa = misma_llave or empresa_real == destino["empresa_id"]
        a, b = _ordenar(c["nombre_puesto"], canonico)
        pares.append(Par(a, b, misma_llave, misma_empresa, "correccion", tipo, ambiguo))
    return pares


def construir_pares(
    comercios: Sequence[Mapping[str, str]],
    correcciones: Sequence[Mapping[str, str]],
    correcciones_verdad: Sequence[Mapping[str, str]],
) -> tuple[Par, ...]:
    """Pares sin duplicar (no ordenados) y en orden determinista: correcciones y luego canónicos."""
    vistos = set()
    salida = []
    for p in _pares_correcciones(comercios, correcciones, correcciones_verdad) + _pares_canonicos(comercios):
        clave = (p.origen, p.texto_a, p.texto_b, p.misma_llave, p.misma_empresa, p.tipo_ruido, p.ambiguo)
        if clave not in vistos:
            vistos.add(clave)
            salida.append(p)
    return tuple(salida)
