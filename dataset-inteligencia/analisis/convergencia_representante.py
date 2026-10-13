"""Política de representante `mas_votado` de la agrupación por similitud.

El conteo de votos usa siempre el ancla del grupo (el primer nombre que lo creó), que es estable y no cambia nunca.
Lo que cambia es el nombre visible del grupo (su etiqueta): el nombre con más votantes distintos vigentes, con
desempate por el voto que primero alcanzó ese conteo. Los votos ya contados se reetiquetan: no se pierde ninguno,
porque el conteo no depende de la etiqueta. El resultado depende solo del orden cronológico (ocurrido_en,
correccion_id) y no del orden de iteración de ningún diccionario.

Para cada nombre de un grupo se guarda una pila con el índice del voto en que su conteo subió a 1, a 2, a 3... Si un
usuario corrige y su voto sale del nombre, se descarta el último escalón de esa pila. El desempate compara el escalón
del conteo actual: gana el índice menor, que es único porque cada voto sube un solo conteo.
"""

from collections import defaultdict


class Etiquetador:
    """Estado mutable y local a una sola reproducción. Nunca se comparte entre reproducciones."""

    def __init__(self) -> None:
        self._votos: dict[tuple[str, str], tuple[str, str]] = {}
        self._pilas: dict[tuple[str, str], dict[str, list[int]]] = defaultdict(lambda: defaultdict(list))

    def votar(self, indice: int, llave: str, usuario: str, ancla: str, nombre: str) -> None:
        previo = self._votos.get((llave, usuario))
        if previo is not None:
            self._pilas[(llave, previo[0])][previo[1]].pop()
        self._votos[(llave, usuario)] = (ancla, nombre)
        self._pilas[(llave, ancla)][nombre].append(indice)

    def etiqueta(self, llave: str, ancla: str) -> str:
        """Nombre visible del grupo ahora. Un grupo sin votos vigentes se muestra con su ancla."""
        pilas = {n: p for n, p in self._pilas.get((llave, ancla), {}).items() if p}
        if not pilas:
            return ancla
        return min(pilas, key=lambda n: (-len(pilas[n]), pilas[n][-1]))
