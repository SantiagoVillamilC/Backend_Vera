"""Partición entrenamiento y prueba de los comercios, estratificada por nombre_revela_naturaleza.

El 30 % de los comercios va a prueba. Se elige con un generador derivado de la semilla, no por
posición, y por separado dentro de cada estrato (nombre_revela_naturaleza verdadero o falso), de modo
que la proporción de cada valor sea casi igual en prueba y en entrenamiento.
"""

import random

from .modelos import Destino

FRACCION_PRUEBA = 0.30
ENTRENAMIENTO = "entrenamiento"
PRUEBA = "prueba"


def _cupos(tamanos: dict[bool, int]) -> dict[bool, int]:
    """Reparte el total de prueba entre estratos por el método del mayor resto."""
    total = sum(tamanos.values())
    objetivo = round(FRACCION_PRUEBA * total)
    base = {k: int(FRACCION_PRUEBA * n) for k, n in tamanos.items()}
    restos = sorted(tamanos, key=lambda k: (-(FRACCION_PRUEBA * tamanos[k] - base[k]), k))
    for k in restos[: objetivo - sum(base.values())]:
        base[k] += 1
    return base


def asignar_conjuntos(destinos: tuple[Destino, ...], azar: random.Random) -> dict[str, str]:
    """llave_destino a conjunto, solo para comercios. Las personas no se asignan."""
    estratos: dict[bool, list[str]] = {True: [], False: []}
    for d in sorted(destinos, key=lambda x: x.llave_destino):
        if d.es_comercio:
            estratos[d.nombre_revela_naturaleza].append(d.llave_destino)
    cupos = _cupos({k: len(v) for k, v in estratos.items()})
    conjuntos: dict[str, str] = {}
    for clave in (True, False):
        llaves = list(estratos[clave])
        azar.shuffle(llaves)
        for i, llave in enumerate(llaves):
            conjuntos[llave] = PRUEBA if i < cupos[clave] else ENTRENAMIENTO
    return conjuntos
