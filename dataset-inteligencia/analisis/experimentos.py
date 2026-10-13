"""Experimento B (recurrencia): evaluar una configuración y correr el barrido, escribiendo los resultados.

Salida: `<resultados>/recurrencia/<id>/`, donde `<id>` sale de un hash de la configuración y de los
sha256 de los archivos de entrada. Misma entrada y misma configuración dan los mismos archivos, byte a byte.
"""

from collections.abc import Sequence
from pathlib import Path

from .barrido import EJES, Rejilla, ResultadoBarrido, barrer
from .configuracion import Configuracion
from .evaluacion import CorridaPreparada, Evaluacion, evaluar, ocurrencias_necesarias, preparar_corrida
from .reporte import (ENCABEZADO_BARRIDO, ENCABEZADO_METRICAS, NOTA_OPTIMO, Fila, asegurar_gitignore,
                      escribir_csv, escribir_json, filas_de_barrido, filas_de_evaluacion, hash_de_json,
                      hashes_por_corrida)

VERSION = "1.0.0"
CARPETA_EXPERIMENTO = "recurrencia"
LARGO_ID = 12
CRITERIO_DESEMPATE = "mayor F1, mayor precisión, menor tasa de falsos positivos secundaria, menor índice de la rejilla"


def _nombres_unicos(corridas: Sequence[CorridaPreparada]) -> None:
    nombres = [c.nombre for c in corridas]
    if len(set(nombres)) != len(nombres):
        raise ValueError(f"Las corridas deben tener carpetas con nombres distintos (llegaron {sorted(nombres)}).")


def _carpeta_de_salida(resultados: Path, prefijo: str, contenido_del_id: object) -> Path:
    asegurar_gitignore(resultados)
    carpeta = resultados / CARPETA_EXPERIMENTO / f"{prefijo}-{hash_de_json(contenido_del_id)[:LARGO_ID]}"
    carpeta.mkdir(parents=True, exist_ok=True)
    return carpeta


def _ahora(corrida: CorridaPreparada) -> str:
    return corrida.ahora.strftime("%Y-%m-%dT%H:%M:%SZ")


def _filas_de(corrida: CorridaPreparada, e: Evaluacion, cfg: Configuracion, prefijo: str = "") -> list[Fila]:
    return filas_de_evaluacion(corrida.nombre, e, ocurrencias_necesarias(corrida, cfg), prefijo)


def correr_evaluacion(ruta_corrida: Path | str, cfg: Configuracion, resultados: Path | str) -> tuple[Path, Evaluacion]:
    """Evalúa una configuración sobre una corrida. Escribe metricas.csv, configuracion.json y manifiesto.json."""
    corrida = preparar_corrida(ruta_corrida)
    evaluacion = evaluar(corrida, cfg)
    entradas = hashes_por_corrida({corrida.nombre: corrida.hashes})
    carpeta = _carpeta_de_salida(Path(resultados), "evaluacion",
                                 {"configuracion": cfg.hash(), "entradas": entradas})
    escribir_csv(carpeta / "metricas.csv", ENCABEZADO_METRICAS, _filas_de(corrida, evaluacion, cfg))
    escribir_json(carpeta / "configuracion.json", cfg.a_dict())
    escribir_json(carpeta / "manifiesto.json", {
        "version": VERSION, "experimento": "recurrencia", "configuracion_sha256": cfg.hash(),
        "semillas": {corrida.nombre: corrida.semilla}, "entradas": entradas,
        "ahora": {corrida.nombre: _ahora(corrida)}})
    return carpeta, evaluacion


def _filas_del_barrido(r: ResultadoBarrido, entrenamiento: CorridaPreparada,
                       validaciones: Sequence[CorridaPreparada], base: Configuracion) -> list[Fila]:
    cfg = r.mejor.configuracion
    filas = _filas_de(entrenamiento, r.entrenamiento, cfg, "mejor:entrenamiento:")
    for corrida, (_, evaluacion) in zip(validaciones, r.validacion):
        filas += _filas_de(corrida, evaluacion, cfg, f"mejor:validacion:{corrida.nombre}:")
    filas += _filas_de(entrenamiento, r.referencia_entrenamiento, base, "referencia:entrenamiento:")
    for corrida, (_, evaluacion) in zip(validaciones, r.referencia_validacion):
        filas += _filas_de(corrida, evaluacion, base, f"referencia:validacion:{corrida.nombre}:")
    filas.append(("todas", "resumen", "optimo_sobre_el_corpus_sintetico", NOTA_OPTIMO))
    return filas


def _manifiesto_del_barrido(r: ResultadoBarrido, entrenamiento: CorridaPreparada,
                            validaciones: Sequence[CorridaPreparada], rejilla: Rejilla, base: Configuracion) -> dict:
    ejes = {eje: list(rejilla.valores[eje]) for eje in EJES}
    return {
        "version": VERSION, "experimento": "recurrencia-barrido", "configuracion_sha256": r.mejor.configuracion.hash(),
        "base_sha256": base.hash(), "rejilla_sha256": hash_de_json(ejes),
        "rejilla": {"tamano": len(r.filas), "ejes": ejes},
        "semillas": {"entrenamiento": {entrenamiento.nombre: entrenamiento.semilla},
                     "validacion": {c.nombre: c.semilla for c in validaciones}},
        "entradas": {"entrenamiento": hashes_por_corrida({entrenamiento.nombre: entrenamiento.hashes}),
                     "validacion": hashes_por_corrida({c.nombre: c.hashes for c in validaciones})},
        "ahora": {c.nombre: _ahora(c) for c in (entrenamiento, *validaciones)},
        "mejor": {"indice": r.mejor.indice, "parametros": r.mejor.parametros},
        "criterio": "maximizar F1 sobre la corrida de entrenamiento", "desempate": CRITERIO_DESEMPATE,
        "nota": NOTA_OPTIMO}


def correr_barrido(ruta_entrenamiento: Path | str, rutas_validacion: Sequence[Path | str], rejilla: Rejilla,
                   base: Configuracion, resultados: Path | str) -> tuple[Path, ResultadoBarrido]:
    """Barrido sobre el entrenamiento y evaluación de la mejor configuración en cada validación."""
    entrenamiento = preparar_corrida(ruta_entrenamiento)
    validaciones = tuple(preparar_corrida(ruta) for ruta in rutas_validacion)
    _nombres_unicos((entrenamiento, *validaciones))
    resultado = barrer(entrenamiento, validaciones, rejilla, base)
    manifiesto = _manifiesto_del_barrido(resultado, entrenamiento, validaciones, rejilla, base)
    carpeta = _carpeta_de_salida(Path(resultados), "barrido", {
        "base": manifiesto["base_sha256"], "rejilla": manifiesto["rejilla_sha256"], "entradas": manifiesto["entradas"]})
    escribir_csv(carpeta / "metricas.csv", ENCABEZADO_METRICAS,
                 _filas_del_barrido(resultado, entrenamiento, validaciones, base))
    escribir_csv(carpeta / "barrido.csv", ENCABEZADO_BARRIDO, filas_de_barrido(resultado.filas, resultado.mejor.indice))
    escribir_json(carpeta / "mejor_configuracion.json", resultado.mejor.configuracion.a_dict())
    escribir_json(carpeta / "manifiesto.json", manifiesto)
    return carpeta, resultado
