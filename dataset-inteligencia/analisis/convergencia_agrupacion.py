"""Agrupación determinista y online de nombres votados por similitud, previa al conteo de votos.

Solo ve nombres canonizados de `entrada/`: nunca importa el cargador de la verdad. Cada llave tiene sus propios
grupos. Un nombre nuevo se asigna al grupo existente cuyo representante (el primer nombre que lo creó) tenga puntaje
mayor o igual al umbral. Si hay varios gana el de mayor puntaje y el empate lo decide el orden de creación.
El idf del coseno se ajusta una sola vez con los nombres de entrada, nunca con la verdad.
"""

from collections.abc import Callable, Iterable
from dataclasses import dataclass

from .similitud import ajustar_idf, coseno_tfidf, jaro_winkler

METODOS = ("ninguno", "jaro_winkler", "coseno_tfidf")
REPRESENTANTES = ("primero", "mas_votado")


@dataclass(frozen=True)
class Agrupador:
    """`representante` solo cambia el nombre visible del grupo (ver `convergencia_representante.py`).

    El grupo (su ancla) lo sigue fijando el primer nombre que lo creó con cualquier política.
    """

    puntaje: Callable[[str, str], float]
    umbral: float
    representante: str = "primero"

    def asignar(self, representantes: tuple[str, ...], nombre: str) -> tuple[str, tuple[str, ...]]:
        """(representante del nombre, representantes nuevos que hay que añadir al final)."""
        mejor, mejor_puntaje = None, -1.0
        for rep in representantes:
            p = self.puntaje(nombre, rep)
            if p >= self.umbral and p > mejor_puntaje:
                mejor, mejor_puntaje = rep, p
        return (mejor, ()) if mejor is not None else (nombre, (nombre,))


def crear_agrupador(metodo: str, umbral: float | None, nombres: Iterable[str],
                    representante: str = "primero") -> Agrupador | None:
    """None cuando el método es `ninguno`. El idf se ajusta con los nombres distintos dados, en orden fijo."""
    if representante not in REPRESENTANTES:
        raise ValueError(f"Política de representante desconocida: {representante}")
    if metodo == "ninguno":
        return None
    if metodo == "jaro_winkler":
        return Agrupador(jaro_winkler, umbral, representante)
    if metodo == "coseno_tfidf":
        idf = ajustar_idf(sorted(set(nombres)))
        return Agrupador(lambda a, b: coseno_tfidf(a, b, idf), umbral, representante)
    raise ValueError(f"Método de similitud desconocido: {metodo}")
