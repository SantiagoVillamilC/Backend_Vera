"""Formato de los archivos de salida: filas de metricas.csv y barrido.csv, JSON canónico y hashes.

Todo es determinista: sin horas, sin rutas absolutas, claves de JSON ordenadas, fin de línea "\\n" y
números con formato fijo. Los valores indefinidos se escriben `n/a`.
"""

import csv
import hashlib
import json
from collections.abc import Iterable, Mapping
from pathlib import Path

from .barrido import FilaBarrido
from .evaluacion import Evaluacion, Matriz, ResumenMotivo, ResumenOcurrencias

ENCABEZADO_METRICAS = ("corrida", "ambito", "medida", "valor")
ENCABEZADO_BARRIDO = ("indice", "w_monto", "w_fecha", "tol_monto", "factor_tolerancia_dias", "n_min", "umbral",
                      "tp", "fp", "tn", "fn", "precision", "recall", "f1", "parejas_secundarias",
                      "fp_secundarios", "tasa_fp_secundaria", "es_mejor")
SUPOSICION_SECUNDARIA = ("parejas con 2 o más pagos que no están en series.csv, tratadas como no recurrentes "
                         "(suposición: series.csv no es exhaustivo, la tasa es una cota superior)")
NOTA_OPTIMO = "óptimo sobre el corpus sintético, no óptimo real"
Fila = tuple[str, str, str, str]


def texto(valor: object) -> str:
    """Formato fijo: None es n/a, los booleanos van en minúscula y los reales con 6 decimales."""
    if valor is None:
        return "n/a"
    if isinstance(valor, bool):
        return "true" if valor else "false"
    if isinstance(valor, float):
        return f"{valor:.6f}"
    return str(valor)


def texto_mediana(valor: float | int | None) -> str:
    """Las medianas de conteos van sin ceros sobrantes: 2 y 3,5 se escriben `2` y `3.5`."""
    if valor is None:
        return "n/a"
    return str(int(valor)) if float(valor).is_integer() else str(float(valor))


def _filas_de_matriz(corrida: str, ambito: str, m: Matriz, por_motivo: bool = False) -> list[Fila]:
    """Un motivo sin positivos reales (compra_habitual, pago_suelto) no tiene precisión, recall ni F1: va n/a.

    De esos motivos solo se informa la tasa de falsos positivos, que es la medida que sí tiene sentido.
    """
    sin_positivos = por_motivo and m.tp + m.fn == 0
    razones = (("precision", m.precision), ("recall", m.recall), ("f1", m.f1))
    medidas = [("tp", m.tp), ("fp", m.fp), ("tn", m.tn), ("fn", m.fn)]
    medidas += [(nombre, None if sin_positivos else valor) for nombre, valor in razones]
    medidas.append(("tasa_falsos_positivos", m.tasa_falsos_positivos))
    return [(corrida, ambito, nombre, texto(valor)) for nombre, valor in medidas]


def _filas_secundaria(corrida: str, e: Evaluacion) -> list[Fila]:
    s = e.secundaria
    return [(corrida, "secundaria", "parejas", texto(s.parejas)),
            (corrida, "secundaria", "falsos_positivos", texto(s.falsos_positivos)),
            (corrida, "secundaria", "tasa_falsos_positivos", texto(s.tasa)),
            (corrida, "secundaria", "suposicion", SUPOSICION_SECUNDARIA)]


def _filas_de_resumen(corrida: str, ambito: str, r: ResumenOcurrencias | ResumenMotivo) -> list[Fila]:
    return [(corrida, ambito, "total", texto(r.total)), (corrida, ambito, "detectadas", texto(r.detectadas)),
            (corrida, ambito, "no_detectadas", texto(r.no_detectadas)),
            (corrida, ambito, "mediana", texto_mediana(r.mediana))]


def filas_de_evaluacion(corrida: str, e: Evaluacion, o: ResumenOcurrencias, prefijo: str = "") -> list[Fila]:
    """Filas de metricas.csv de una corrida. `prefijo` antepone el contexto (por ejemplo `mejor:validacion:val1:`)."""
    filas = _filas_de_matriz(corrida, f"{prefijo}global", e.global_)
    for motivo, matriz in e.por_motivo.items():
        filas += _filas_de_matriz(corrida, f"{prefijo}motivo:{motivo}", matriz, por_motivo=True)
    filas += [(c, f"{prefijo}{a}", m, v) for c, a, m, v in _filas_secundaria(corrida, e)]
    filas.append((corrida, f"{prefijo}global", "series_sin_pagos", texto(e.pares_sin_pagos)))
    filas += _filas_de_resumen(corrida, f"{prefijo}ocurrencias_necesarias", o)
    for motivo, resumen in o.por_motivo.items():
        filas += _filas_de_resumen(corrida, f"{prefijo}ocurrencias_necesarias:{motivo}", resumen)
    return filas


def filas_de_barrido(filas: Iterable[FilaBarrido], indice_mejor: int) -> list[list[str]]:
    salida = []
    for f in filas:
        m, s, c, p = f.matriz, f.secundaria, f.configuracion, f.parametros
        salida.append([
            str(f.indice), f"{c.w_monto:g}", f"{c.w_fecha:g}", f"{c.tol_monto:g}",
            f"{p['factor_tolerancia_dias']:g}", str(c.n_min), f"{c.umbral:g}", str(m.tp), str(m.fp), str(m.tn),
            str(m.fn), texto(m.precision), texto(m.recall), texto(m.f1), str(s.parejas),
            str(s.falsos_positivos), texto(s.tasa), texto(f.indice == indice_mejor)])
    return salida


def hash_de_json(objeto: object) -> str:
    return hashlib.sha256(json_canonico(objeto).encode("utf-8")).hexdigest()


def json_canonico(objeto: object) -> str:
    return json.dumps(objeto, sort_keys=True, indent=2, ensure_ascii=False) + "\n"


def escribir_json(ruta: Path, objeto: object) -> None:
    ruta.write_text(json_canonico(objeto), encoding="utf-8", newline="\n")


def escribir_csv(ruta: Path, encabezado: Iterable[str], filas: Iterable[Iterable[str]]) -> None:
    with open(ruta, "w", newline="", encoding="utf-8") as f:
        escritor = csv.writer(f, lineterminator="\n")
        escritor.writerow(list(encabezado))
        escritor.writerows(list(f_) for f_ in filas)


def asegurar_gitignore(raiz: Path) -> None:
    """Crea `<raiz>/.gitignore` con `*` y la excepción para sí mismo, si no existe."""
    raiz.mkdir(parents=True, exist_ok=True)
    destino = raiz / ".gitignore"
    if not destino.exists():
        destino.write_text("*\n!.gitignore\n", encoding="utf-8", newline="\n")


def hashes_por_corrida(corridas: Mapping[str, Mapping[str, str]]) -> dict[str, dict[str, str]]:
    return {nombre: dict(sorted(hashes.items())) for nombre, hashes in sorted(corridas.items())}
