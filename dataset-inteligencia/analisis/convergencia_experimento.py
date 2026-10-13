"""Experimento A (convergencia por multitud): evaluar las corridas y escribir los resultados.

Salida: `<resultados>/convergencia/convergencia-<id>/`, donde `<id>` sale de un hash de la configuración, de los
sha256 de los archivos de entrada y de los parámetros del bootstrap. Misma entrada y misma configuración dan los
mismos archivos, byte a byte: el tiempo de ejecución solo se imprime por stderr.
"""

from collections.abc import Sequence
from pathlib import Path

from .bootstrap import frase
from .convergencia_agregado import FilaAgregada, FilaBootstrap, agregar, diferencias_bootstrap
from .convergencia_bootstrap_riesgo import FilaBootstrapRiesgo, bootstrap_visibles
from .convergencia_config import ConfigConvergencia
from .convergencia_evaluacion import (DetalleCaso, Medida, ResultadoCorrida, cargar_corrida_convergencia,
                                      evaluar_corrida)
from .convergencia_resumen import texto_resumen
from .reporte import asegurar_gitignore, escribir_csv, escribir_json, hash_de_json, hashes_por_corrida, texto

VERSION = "1.1.0"
CARPETA_EXPERIMENTO = "convergencia"
LARGO_ID = 12
ENCABEZADO_MEDIDAS = ("corrida", "tasa", "politica", "k", "ambito", "medida", "valor")
ENCABEZADO_AGREGADO = ("tasa", "politica", "k", "ambito", "medida", "semillas", "media", "minimo", "maximo")
ENCABEZADO_CASOS = ("corrida", "politica", "k", "caso_id", "movimiento_id", "ocurrido_en", "nombre_esperado",
                    "nombre_obtenido", "nombre_ok", "categoria_esperada", "categoria_obtenida", "categoria_ok",
                    "origen_esperado", "origen_obtenido", "origen_ok", "aprobado")
ENCABEZADO_BOOTSTRAP = ("tasa", "ambito", "dimension", "comparacion", "comercios", "diferencia_observada",
                        "media_bootstrap", "inferior", "superior", "lectura")
ENCABEZADO_BOOTSTRAP_RIESGO = ("tasa", "ambito", "medida", "dimension", "comparacion", "comercios",
                               "diferencia_observada", "media_bootstrap", "inferior", "superior", "lectura")
NOTA = ("clave de nombre canonizada, la agrupacion por similitud no se mide. K no se calibra. "
        "Regla de promocion y relevo: propuesta de diseno sin fuente. Corpus sintetico")


def _filas_de_medidas(resultados: Sequence[ResultadoCorrida]) -> list[list[str]]:
    return [[r.nombre, r.tasa, m.politica, str(m.k), m.ambito, m.medida, texto(m.valor)]
            for r in resultados for m in _ordenadas(r.medidas)]


def _ordenadas(medidas: Sequence[Medida]) -> list[Medida]:
    return sorted(medidas, key=lambda m: (m.politica, m.k, m.ambito, m.medida))


def _filas_de_agregado(agregado: Sequence[FilaAgregada]) -> list[list[str]]:
    return [[f.tasa, f.politica, str(f.k), f.ambito, f.medida, str(f.semillas), texto(f.media), texto(f.minimo),
             texto(f.maximo)] for f in agregado]


def _fila_de_caso(corrida: str, d: DetalleCaso) -> list[str]:
    return [corrida, d.politica, str(d.k), d.caso_id, d.movimiento_id, d.ocurrido_en, d.nombre_esperado,
            d.nombre_obtenido, texto(d.nombre_ok), d.categoria_esperada, d.categoria_obtenida,
            texto(d.categoria_ok), d.origen_esperado, d.origen_obtenido, texto(d.origen_ok), texto(d.aprobado)]


def _filas_de_casos(resultados: Sequence[ResultadoCorrida]) -> list[list[str]]:
    return [_fila_de_caso(r.nombre, d) for r in resultados
            for d in sorted(r.casos, key=lambda d: (d.politica, d.k, d.caso_id, int(d.movimiento_id)))]


def _filas_de_bootstrap(filas: Sequence[FilaBootstrap]) -> list[list[str]]:
    return [[f.tasa, f.ambito, f.dimension, f.comparacion, str(f.n), texto(f.intervalo.observada),
             texto(f.intervalo.media), texto(f.intervalo.inferior), texto(f.intervalo.superior),
             frase(f.intervalo, f.comparacion.split()[-1])] for f in filas]


def _filas_de_bootstrap_riesgo(filas: Sequence[FilaBootstrapRiesgo]) -> list[list[str]]:
    return [[f.tasa, f.ambito, f.medida, f.dimension, f.comparacion, str(f.n), texto(f.intervalo.observada),
             texto(f.intervalo.media), texto(f.intervalo.inferior), texto(f.intervalo.superior),
             frase(f.intervalo, f.comparacion.split()[-1])] for f in filas]


def _manifiesto(cfg: ConfigConvergencia, resultados: Sequence[ResultadoCorrida], b: int, semilla: int) -> dict:
    return {
        "version": VERSION, "experimento": "convergencia", "configuracion_sha256": cfg.hash(),
        "bootstrap": {"b": b, "semilla": semilla},
        "entradas": hashes_por_corrida({r.nombre: dict(r.hashes) for r in resultados}),
        "semillas": {r.nombre: r.semilla for r in resultados}, "tasas": {r.nombre: r.tasa for r in resultados},
        "ahora": {r.nombre: r.ahora.strftime("%Y-%m-%dT%H:%M:%SZ") for r in resultados}, "nota": NOTA}


def _nombres_unicos(nombres: Sequence[str]) -> None:
    if len(set(nombres)) != len(nombres):
        raise ValueError(f"Las corridas deben tener nombres distintos (carpeta padre y carpeta). Llegaron {sorted(nombres)}.")


def correr_convergencia(rutas: Sequence[Path | str], cfg: ConfigConvergencia, resultados: Path | str, b: int,
                        semilla_bootstrap: int) -> tuple[Path, list[ResultadoCorrida]]:
    """Evalúa cada corrida, agrega por tasa y escribe medidas, agregado, casos, bootstrap y resumen."""
    corridas = [cargar_corrida_convergencia(ruta) for ruta in rutas]
    _nombres_unicos([c.nombre for c in corridas])
    evaluadas = [evaluar_corrida(c, cfg) for c in corridas]
    agregado = agregar(evaluadas)
    bootstrap = diferencias_bootstrap(evaluadas, cfg, b, semilla_bootstrap)
    bootstrap_riesgo = bootstrap_visibles(evaluadas, cfg, b, semilla_bootstrap)
    manifiesto = _manifiesto(cfg, evaluadas, b, semilla_bootstrap)
    asegurar_gitignore(Path(resultados))
    identificador = hash_de_json({"configuracion": manifiesto["configuracion_sha256"],
                                  "entradas": manifiesto["entradas"], "bootstrap": manifiesto["bootstrap"]})
    carpeta = Path(resultados) / CARPETA_EXPERIMENTO / f"convergencia-{identificador[:LARGO_ID]}"
    carpeta.mkdir(parents=True, exist_ok=True)
    escribir_csv(carpeta / "medidas.csv", ENCABEZADO_MEDIDAS, _filas_de_medidas(evaluadas))
    escribir_csv(carpeta / "agregado.csv", ENCABEZADO_AGREGADO, _filas_de_agregado(agregado))
    escribir_csv(carpeta / "casos_vista.csv", ENCABEZADO_CASOS, _filas_de_casos(evaluadas))
    escribir_csv(carpeta / "bootstrap.csv", ENCABEZADO_BOOTSTRAP, _filas_de_bootstrap(bootstrap))
    escribir_csv(carpeta / "bootstrap_riesgo.csv", ENCABEZADO_BOOTSTRAP_RIESGO,
                 _filas_de_bootstrap_riesgo(bootstrap_riesgo))
    escribir_json(carpeta / "configuracion.json", cfg.a_dict())
    escribir_json(carpeta / "manifiesto.json", manifiesto)
    resumen = texto_resumen(cfg, evaluadas, agregado, bootstrap, bootstrap_riesgo, b, semilla_bootstrap)
    (carpeta / "resumen.md").write_text(resumen, encoding="utf-8", newline="\n")
    return carpeta, evaluadas
