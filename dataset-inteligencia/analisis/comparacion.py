"""Experimento de comparación: línea base citable, óptimo restringido al rango de práctica y óptimo libre.

Los dos óptimos se buscan sobre la corrida de entrenamiento. Se evalúan los tres métodos en las corridas
de validación (que el barrido usa solo para reportar) y de verificación (que nunca entran al barrido).
Las diferencias de F1 llevan intervalo bootstrap percentil pareado sobre las mismas series.
"""

from collections.abc import Mapping, Sequence
from pathlib import Path

from .barrido import EJES, Rejilla, barrer
from .bootstrap import frase, generar_remuestreos, intervalo_pareado
from .configuracion import Configuracion, ConfiguracionInvalida
from .evaluacion import CorridaPreparada, Evaluacion, evaluar, evaluar_con, preparar_corrida
from .experimentos import VERSION, _ahora, _carpeta_de_salida, _nombres_unicos
from .linea_base import REGLA_PRACTICA, detectar_linea_base_agrupado
from .reporte import NOTA_OPTIMO, escribir_csv, escribir_json, hash_de_json, hashes_por_corrida, texto

BOOTSTRAP_B_DEFECTO = 2000
SEMILLA_BOOTSTRAP_DEFECTO = 2026
METODOS = ("linea_base", "restringido", "libre")
COMPARACIONES = (("restringido_vs_linea_base", "restringido", "linea_base", "la línea base"),
                 ("libre_vs_linea_base", "libre", "linea_base", "la línea base"),
                 ("libre_vs_restringido", "libre", "restringido", "el restringido"))
ENCABEZADO_COMPARACION = ("corrida", "rol", "semilla", "metodo", "ambito", "tp", "fp", "tn", "fn", "precision",
                          "recall", "f1", "tasa_falsos_positivos", "nota")
ENCABEZADO_IC = ("corrida", "rol", "comparacion", "n_series", "diferencia_observada",
                 "diferencia_media_bootstrap", "ic95_inferior", "ic95_superior", "bootstrap_b",
                 "semilla_bootstrap", "frase")
NOTA_SIN_POSITIVOS = ("sin positivos reales: precision, recall y F1 no estan definidos, "
                      "se reporta la tasa de falsos positivos")
AGREGADA = "validacion_agregada"


def validar_restringida(rejilla: Rejilla, base: Configuracion) -> None:
    """La rejilla restringida fija factor 1,0 y n_min 3, y la base conserva las tolerancias 2, 3 y 4."""
    problemas = []
    if rejilla.valores["factor_tolerancia_dias"] != (1.0,):
        problemas.append("factor_tolerancia_dias debe ser exactamente [1.0]")
    if rejilla.valores["n_min"] != (3,):
        problemas.append("n_min debe ser exactamente [3]")
    if base.tolerancias_dias != (2, 3, 4) or base.cadencias_dias != (7, 14, 30):
        problemas.append("la configuración base debe tener cadencias 7, 14, 30 y tolerancias 2, 3, 4")
    if problemas:
        raise ConfiguracionInvalida("Rejilla restringida inválida:\n- " + "\n- ".join(problemas))


def _validar_semillas(todas: Sequence[CorridaPreparada]) -> None:
    semillas = [c.semilla for c in todas]
    if len(set(semillas)) != len(semillas):
        raise ValueError(f"Las semillas de entrenamiento, validación y verificación deben ser distintas ({semillas}).")


def _exigir_mismas_series(evaluaciones: Mapping[str, Evaluacion]) -> None:
    ordenes = {tuple(d.serie_id for d in e.por_serie) for e in evaluaciones.values()}
    if len(ordenes) != 1:
        raise ValueError("Los métodos deben evaluar las mismas series en el mismo orden.")


def _filas_de(corrida: CorridaPreparada, rol: str, metodo: str, e: Evaluacion) -> list[list[str]]:
    ambitos = [("global", e.global_, False)] + [(f"motivo:{m}", x, True) for m, x in e.por_motivo.items()]
    filas = []
    for ambito, m, por_motivo in ambitos:
        sin_positivos = por_motivo and m.tp + m.fn == 0
        razones = ["n/a"] * 3 if sin_positivos else [texto(m.precision), texto(m.recall), texto(m.f1)]
        filas.append([corrida.nombre, rol, str(corrida.semilla), metodo, ambito, str(m.tp), str(m.fp), str(m.tn),
                      str(m.fn), *razones, texto(m.tasa_falsos_positivos), NOTA_SIN_POSITIVOS if sin_positivos else ""])
    return filas


def _filas_ic(nombre: str, rol: str, por_metodo: Mapping[str, Sequence], b: int, semilla: int) -> list[list[str]]:
    n = len(por_metodo["linea_base"])
    remuestreos = generar_remuestreos(n, b, semilla, nombre)
    filas = []
    for etiqueta, a, c, referencia in COMPARACIONES:
        i = intervalo_pareado(por_metodo[a], por_metodo[c], remuestreos)
        filas.append([nombre, rol, etiqueta, str(n), texto(i.observada), texto(i.media), texto(i.inferior),
                      texto(i.superior), str(b), str(semilla), frase(i, referencia)])
    return filas


def _resultados(e: Evaluacion) -> list[tuple[bool, bool]]:
    return [(d.predicho, d.es_recurrente) for d in e.por_serie]


def _resumen(corridas: Sequence[CorridaPreparada], evals: Mapping[str, Mapping[str, Evaluacion]]) -> list[str]:
    lineas = []
    for c in corridas:
        for metodo in METODOS:
            e = evals[c.nombre][metodo]
            tasas = ", ".join(f"{m} {texto(x.tasa_falsos_positivos)} (FP {x.fp}, TN {x.tn})"
                              for m, x in e.por_motivo.items() if x.tp + x.fn == 0)
            lineas.append(f"{c.nombre} {metodo}: F1 {texto(e.global_.f1)}. tasa de falsos positivos por motivo: {tasas}")
    return lineas


def correr_comparacion(ruta_entrenamiento: Path | str, rutas_validacion: Sequence[Path | str],
                       rutas_verificacion: Sequence[Path | str], rejilla_libre: Rejilla,
                       rejilla_restringida: Rejilla, base: Configuracion, resultados: Path | str, b: int,
                       semilla: int) -> tuple[Path, list[str]]:
    validar_restringida(rejilla_restringida, base)
    ent = preparar_corrida(ruta_entrenamiento)
    vals = tuple(preparar_corrida(r) for r in rutas_validacion)
    vers = tuple(preparar_corrida(r) for r in rutas_verificacion)
    _nombres_unicos((ent, *vals, *vers))
    _validar_semillas((ent, *vals, *vers))
    libre, restr = barrer(ent, vals, rejilla_libre, base), barrer(ent, vals, rejilla_restringida, base)
    cfgs = {"restringido": restr.mejor.configuracion, "libre": libre.mejor.configuracion}
    roles = [(ent, "entrenamiento"), *((c, "validacion") for c in vals), *((c, "verificacion") for c in vers)]
    evals = {c.nombre: {"linea_base": evaluar_con(c, lambda g, a: detectar_linea_base_agrupado(g, REGLA_PRACTICA, a)),
                        **{m: evaluar(c, cfg) for m, cfg in cfgs.items()}} for c, _ in roles}
    for c, _ in roles:
        _exigir_mismas_series(evals[c.nombre])
    filas = [f for c, rol in roles for m in METODOS for f in _filas_de(c, rol, m, evals[c.nombre][m])]
    ic = []
    for c, rol in roles[1:]:
        ic += _filas_ic(c.nombre, rol, {m: _resultados(evals[c.nombre][m]) for m in METODOS}, b, semilla)
        if c is vals[-1]:
            pool = {m: [r for v in vals for r in _resultados(evals[v.nombre][m])] for m in METODOS}
            ic += _filas_ic(AGREGADA, "validacion", pool, b, semilla)
    manifiesto = _manifiesto(ent, vals, vers, rejilla_libre, rejilla_restringida, base, cfgs, libre, restr, b, semilla)
    carpeta = _carpeta_de_salida(Path(resultados), "comparacion", {
        k: manifiesto[k] for k in ("base_sha256", "rejilla_libre_sha256", "rejilla_restringida_sha256", "entradas",
                                   "bootstrap_b", "semilla_bootstrap")})
    escribir_csv(carpeta / "comparacion.csv", ENCABEZADO_COMPARACION, filas)
    escribir_csv(carpeta / "comparacion_ic.csv", ENCABEZADO_IC, ic)
    escribir_json(carpeta / "mejor_libre.json", cfgs["libre"].a_dict())
    escribir_json(carpeta / "mejor_restringida.json", cfgs["restringido"].a_dict())
    escribir_json(carpeta / "manifiesto.json", manifiesto)
    return carpeta, _resumen([c for c, _ in roles], evals)


def _manifiesto(ent, vals, vers, rejilla_libre, rejilla_restringida, base, cfgs, libre, restr, b, semilla) -> dict:
    def ejes(r):
        return {e: list(r.valores[e]) for e in EJES}
    return {
        "version": VERSION, "experimento": "recurrencia-comparacion", "bootstrap_b": b, "semilla_bootstrap": semilla,
        "intervalo": "percentil pareado de 95 por ciento sobre las mismas series",
        "base_sha256": base.hash(), "libre_sha256": cfgs["libre"].hash(),
        "restringida_sha256": cfgs["restringido"].hash(),
        "rejilla_libre_sha256": hash_de_json(ejes(rejilla_libre)),
        "rejilla_restringida_sha256": hash_de_json(ejes(rejilla_restringida)),
        "mejor_indice": {"libre": libre.mejor.indice, "restringido": restr.mejor.indice},
        "linea_base": "regla de practica de lyndonkl con la cadencia mas cercana a la mediana",
        "semillas": {"entrenamiento": {ent.nombre: ent.semilla}, "validacion": {c.nombre: c.semilla for c in vals},
                     "verificacion": {c.nombre: c.semilla for c in vers}},
        "entradas": {"entrenamiento": hashes_por_corrida({ent.nombre: ent.hashes}),
                     "validacion": hashes_por_corrida({c.nombre: c.hashes for c in vals}),
                     "verificacion": hashes_por_corrida({c.nombre: c.hashes for c in vers})},
        "ahora": {c.nombre: _ahora(c) for c in (ent, *vals, *vers)}, "nota": NOTA_OPTIMO}
