"""Experimento A2: efecto de agrupar por similitud los nombres votados antes de contar votos.

Corre las mismas corridas de A con tres configuraciones (ninguno como control, jaro_winkler y coseno_tfidf) sobre la
política principal, reutiliza el arnés de A y añade exposición latente y fusiones falsas. Uso:

    python3 -m analisis.convergencia_agrupacion_experimento --corridas RUTA [RUTA ...] --control-a CARPETA_A
"""

import argparse
import csv
import sys
from collections import defaultdict
from pathlib import Path

from .convergencia import Replicador
from .convergencia_agrupacion import crear_agrupador
from .convergencia_agrupacion_medidas import medidas_de_agrupacion
from .convergencia_agrupacion_resumen import texto_resumen
from .convergencia_representante_resumen import texto_resumen_representante
from .convergencia_agregado import agregar
from .convergencia_config import ConfigConvergencia, desde_dict_convergencia
from .convergencia_evaluacion import Medida, cargar_corrida_convergencia, evaluar_corrida
from .convergencia import leer_correcciones, tipos_de_llave
from .reporte import asegurar_gitignore, escribir_csv, hash_de_json, hashes_por_corrida, texto

CONFIGURACIONES = (("ninguno", "ninguno", None), ("jaro_winkler_0.97", "jaro_winkler", 0.97),
                   ("coseno_tfidf_0.78", "coseno_tfidf", 0.78))
CONFIGURACIONES_REPRESENTANTE = (
    ("ninguno", "ninguno", None), ("jaro_winkler_0.97_primero", "jaro_winkler", 0.97, "primero"),
    ("jaro_winkler_0.97_mas_votado", "jaro_winkler", 0.97, "mas_votado"),
    ("coseno_tfidf_0.78_primero", "coseno_tfidf", 0.78, "primero"),
    ("coseno_tfidf_0.78_mas_votado", "coseno_tfidf", 0.78, "mas_votado"))
POLITICA = "solo_comercio"
BASE = {"ks": [1, 2, 3], "politicas": {POLITICA: ["comercio"]}, "politica_principal": POLITICA, "k_referencia": 2}
ENCABEZADO = ("corrida", "tasa", "configuracion", "politica", "k", "ambito", "medida", "valor")
ENCABEZADO_AGREGADO = ("tasa", "configuracion", "politica", "k", "ambito", "medida", "semillas", "media", "minimo",
                       "maximo")


def configuracion(metodo: str, umbral: float | None, representante: str = "primero") -> ConfigConvergencia:
    extra = {} if representante == "primero" else {"representante": representante}
    return desde_dict_convergencia(BASE | {"metodo_similitud": metodo, "umbral_similitud": umbral} | extra)


def _medidas_nuevas(corrida, cfg: ConfigConvergencia) -> list[Medida]:
    correcciones = leer_correcciones(corrida.correcciones)
    tipos = tipos_de_llave(corrida.movimientos)
    agrupador = crear_agrupador(cfg.metodo_similitud, cfg.umbral_similitud, {c.nombre for c in correcciones},
                                cfg.representante)
    tamanos = defaultdict(int)
    for c in corrida.correcciones:
        if corrida.ruido.get(c["correccion_id"]) == "colusion":
            tamanos[c["llave_destino"]] += 1
    llaves = sorted(c["llave_destino"] for c in corrida.comercios
                    if c["es_comercio"] == "true" and tamanos.get(c["llave_destino"], 0) == 0)
    salida = []
    for k in cfg.ks:
        r = Replicador(correcciones, tipos, k, cfg.politica(POLITICA), agrupador, cfg.regla_deriva == "reciente")
        valores = medidas_de_agrupacion(r, llaves, corrida.historial, corrida.ruido, corrida.ahora)
        salida += [Medida(POLITICA, k, "sin_colusion", f"agrupacion_{n}", v) for n, v in valores.items()]
    return salida


def correr(rutas: list[Path], carpeta_salida: Path, configuraciones=CONFIGURACIONES):
    """Cada configuración es (etiqueta, método, umbral) o (etiqueta, método, umbral, representante)."""
    corridas = [cargar_corrida_convergencia(r) for r in rutas]
    resultados = {}
    for etiqueta, metodo, umbral, *resto in configuraciones:
        cfg = configuracion(metodo, umbral, *resto)
        evaluadas = []
        for c in corridas:
            e = evaluar_corrida(c, cfg)
            evaluadas.append(type(e)(e.nombre, e.tasa, e.semilla, e.ahora, e.hashes,
                                     e.medidas + tuple(_medidas_nuevas(c, cfg)), e.casos, e.comercios))
        resultados[etiqueta] = (cfg, evaluadas)
        print(f"{etiqueta}: {len(evaluadas)} corridas", file=sys.stderr)
    return corridas, resultados


def _escribir(corridas, resultados, carpeta_a: Path | None, raiz: Path, redactar=texto_resumen) -> Path:
    agregados = {etiqueta: agregar(ev) for etiqueta, (_, ev) in resultados.items()}
    identificador = hash_de_json({"configuraciones": {e: cfg.hash() for e, (cfg, _) in resultados.items()},
                                  "entradas": hashes_por_corrida({c.nombre: dict(c.hashes) for c in corridas})})
    carpeta = raiz / "convergencia" / f"convergencia-{identificador[:12]}"
    carpeta.mkdir(parents=True, exist_ok=True)
    asegurar_gitignore(raiz)
    filas = [[r.nombre, r.tasa, etiqueta, m.politica, str(m.k), m.ambito, m.medida, texto(m.valor)]
             for etiqueta, (_, ev) in resultados.items() for r in ev for m in r.medidas]
    escribir_csv(carpeta / "medidas.csv", ENCABEZADO, filas)
    escribir_csv(carpeta / "agregado.csv", ENCABEZADO_AGREGADO,
                 [[f.tasa, e, f.politica, str(f.k), f.ambito, f.medida, str(f.semillas), texto(f.media),
                   texto(f.minimo), texto(f.maximo)] for e, ag in agregados.items() for f in ag])
    control = _comparar_con_a(agregados["ninguno"], carpeta_a)
    (carpeta / "resumen.md").write_text(redactar(agregados, len(corridas), control), encoding="utf-8",
                                        newline="\n")
    return carpeta


def _comparar_con_a(control, carpeta_a: Path | None) -> tuple[int, int] | None:
    """(medidas comparadas, medidas que difieren) del control contra el agregado de A, política principal."""
    if carpeta_a is None:
        return None
    with open(carpeta_a / "agregado.csv", encoding="utf-8", newline="") as f:
        de_a = {(r["tasa"], r["k"], r["ambito"], r["medida"]): (r["media"], r["minimo"], r["maximo"])
                for r in csv.DictReader(f) if r["politica"] == POLITICA}
    comparadas = difieren = 0
    for f in control:
        clave = (f.tasa, str(f.k), f.ambito, f.medida)
        if clave in de_a:
            comparadas += 1
            difieren += de_a[clave] != (texto(f.media), texto(f.minimo), texto(f.maximo))
    return comparadas, difieren


def main() -> None:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--corridas", type=Path, nargs="+", required=True)
    p.add_argument("--control-a", type=Path, default=None)
    p.add_argument("--resultados", type=Path, default=Path("resultados"))
    p.add_argument("--representante", action="store_true",
                   help="compara las políticas de representante primero y mas_votado (A3)")
    a = p.parse_args()
    if a.representante:
        corridas, resultados = correr(a.corridas, a.resultados, CONFIGURACIONES_REPRESENTANTE)
        print(_escribir(corridas, resultados, a.control_a, a.resultados, texto_resumen_representante))
        return
    corridas, resultados = correr(a.corridas, a.resultados)
    print(_escribir(corridas, resultados, a.control_a, a.resultados))


if __name__ == "__main__":
    main()
