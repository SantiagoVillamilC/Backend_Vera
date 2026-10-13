"""Experimento C (avisos): evaluar las corridas y escribir los resultados.

Salida: `<resultados>/avisos/avisos-<id>/`, donde `<id>` sale de un hash de la configuración y de los sha256 de los
archivos de entrada. Misma entrada y misma configuración dan los mismos archivos, byte a byte: el tiempo de ejecución
solo se imprime por stderr. Es una prueba de aceptación sobre pocas etiquetas escritas a mano, no una medición.
"""

from collections.abc import Sequence
from pathlib import Path

from .avisos_config import ConfiguracionAvisos
from .avisos_corrida import cargar_corrida_avisos
from .avisos_evaluacion import FilaEvaluada, FilaVariacion, resumir, variacion
from .avisos_fondo import NOTA, ResumenFondo, agregar_fondo
from .avisos_resultado import ResultadoCorrida, evaluar_resultado
from .avisos_resumen import texto_resumen
from .reporte import asegurar_gitignore, escribir_csv, escribir_json, hash_de_json, hashes_por_corrida, texto

VERSION = "1.0.0"
CARPETA_EXPERIMENTO = "avisos"
LARGO_ID = 12
TODOS = "todos"
ENCABEZADO_FILAS = ("corrida", "fila", "usuario_id", "tipo", "fecha_evaluacion", "regla", "linea_base", "fuente_series",
                    "categoria_fuente", "esperado", "dispara", "pasa", "base_usada", "valor_usado", "estadistico",
                    "causa", "detalle")
ENCABEZADO_RESUMEN = ("corrida", "regla", "linea_base", "fuente", "tipo", "aprobadas", "total", "falsos_positivos",
                      "negativas", "falsos_negativos", "positivas")
ENCABEZADO_VARIACION = ("fila", "tipo", "fecha_evaluacion", "regla", "linea_base", "fuente", "corridas", "aprobadas",
                        "varia")
ENCABEZADO_FONDO = ("corrida", "grupo", "regla", "linea_base", "fuente", "tipo", "usuarios", "evaluaciones",
                    "evaluables", "avisos", "avisos_por_evaluable", "nota")
ENCABEZADO_DETALLE = ("corrida", "grupo", "usuario_id", "mes", "fecha_evaluacion", "regla", "linea_base", "fuente",
                      "tipo", "causa", "nota")
NOTA_MANIFIESTO = ("prueba de aceptacion sobre etiquetas escritas a mano, no una medicion estadistica. "
                   "Reglas propuestas sin fuente salvo el z modificado y la ventana de 7 dias")


def pesos(valor) -> str:
    """Centavos exactos a pesos con 2 decimales. None es n/a."""
    return "n/a" if valor is None else f"{float(valor) / 100:.2f}"


def _estadistico(valor) -> str:
    return "n/a" if valor is None else texto(float(valor))


def _fila_de_evaluacion(f: FilaEvaluada) -> list[str]:
    c = f.combinacion
    return [f.corrida, str(f.fila), f.usuario_id, f.tipo, f.fecha_evaluacion.isoformat(), c.regla, c.linea_base,
            c.fuente, f.categoria_fuente, texto(f.esperado), texto(f.dispara), texto(f.aprobada), pesos(f.referencia),
            pesos(f.valor), _estadistico(f.estadistico), f.causa, f.detalle]


def _filas_de_resumen(resultados: Sequence[ResultadoCorrida]) -> list[list[str]]:
    filas = []
    for r in resultados:
        for s in resumir(r.filas):
            c = s.combinacion
            filas.append([r.corrida.nombre, c.regla, c.linea_base, c.fuente, TODOS, *map(str, (
                s.aprobadas, s.total, s.falsos_positivos, s.negativas, s.falsos_negativos, s.positivas))])
            filas += [[r.corrida.nombre, c.regla, c.linea_base, c.fuente, tipo, *map(str, conteo)]
                      for tipo, conteo in s.por_tipo.items()]
    return filas


def _filas_de_variacion(filas: Sequence[FilaVariacion]) -> list[list[str]]:
    return [[str(v.fila), v.tipo, v.fecha_evaluacion.isoformat(), v.combinacion.regla, v.combinacion.linea_base,
             v.combinacion.fuente, str(v.corridas), str(v.aprobadas), texto(v.varia)] for v in filas]


def _fila_de_fondo(corrida: str, r: ResumenFondo) -> list[str]:
    por_evaluable = r.avisos / r.evaluables if r.evaluables else None
    c = r.combinacion
    return [corrida, r.grupo, c.regla, c.linea_base, c.fuente, r.tipo, str(r.usuarios), str(r.evaluaciones),
            str(r.evaluables), str(r.avisos), texto(por_evaluable), NOTA]


def _filas_de_fondo(resultados: Sequence[ResultadoCorrida]) -> list[list[str]]:
    return [_fila_de_fondo(r.corrida.nombre, s) for r in resultados for s in agregar_fondo(r.fondo)]


def _filas_de_detalle(resultados: Sequence[ResultadoCorrida]) -> list[list[str]]:
    return [[f.corrida, f.grupo, f.usuario_id, f.mes, f.fecha_evaluacion.isoformat(), f.combinacion.regla,
             f.combinacion.linea_base, f.combinacion.fuente, f.tipo, f.causa, NOTA]
            for r in resultados for f in r.fondo if f.dispara]


def _manifiesto(cfg: ConfiguracionAvisos, resultados: Sequence[ResultadoCorrida]) -> dict:
    corridas = [r.corrida for r in resultados]
    return {
        "version": VERSION, "experimento": "avisos", "configuracion_sha256": cfg.hash(),
        "entradas": hashes_por_corrida({c.nombre: c.hashes for c in corridas}),
        "semillas": {c.nombre: c.semilla for c in corridas},
        "fecha_fin": {c.nombre: c.fecha_fin.isoformat() for c in corridas}, "nota": NOTA_MANIFIESTO}


def _nombres_unicos(nombres: Sequence[str]) -> None:
    if len(set(nombres)) != len(nombres):
        raise ValueError(f"Las corridas deben tener nombres distintos (carpeta padre y carpeta). Llegaron {sorted(nombres)}.")


def _escribir(carpeta: Path, cfg: ConfiguracionAvisos, resultados: Sequence[ResultadoCorrida], manifiesto: dict) -> None:
    filas = [f for r in resultados for f in r.filas]
    inferidas = [f for r in resultados for f in r.inferidas]
    escribir_csv(carpeta / "filas.csv", ENCABEZADO_FILAS, [_fila_de_evaluacion(f) for f in filas])
    escribir_csv(carpeta / "filas_categoria_inferida.csv", ENCABEZADO_FILAS, [_fila_de_evaluacion(f) for f in inferidas])
    escribir_csv(carpeta / "resumen.csv", ENCABEZADO_RESUMEN, _filas_de_resumen(resultados))
    escribir_csv(carpeta / "variacion.csv", ENCABEZADO_VARIACION,
                 _filas_de_variacion(variacion([r.filas for r in resultados])))
    escribir_csv(carpeta / "fondo_resumen.csv", ENCABEZADO_FONDO, _filas_de_fondo(resultados))
    escribir_csv(carpeta / "fondo_detalle.csv", ENCABEZADO_DETALLE, _filas_de_detalle(resultados))
    escribir_json(carpeta / "configuracion.json", cfg.a_dict())
    escribir_json(carpeta / "manifiesto.json", manifiesto)
    (carpeta / "resumen.md").write_text(texto_resumen(cfg, resultados), encoding="utf-8", newline="\n")


def correr_avisos(rutas: Sequence[Path | str], cfg: ConfiguracionAvisos,
                  resultados: Path | str) -> tuple[Path, list[ResultadoCorrida]]:
    """Evalúa cada corrida (etiquetas, categoría inferida y fondo) y escribe un CSV por tabla y el resumen."""
    corridas = [cargar_corrida_avisos(ruta) for ruta in rutas]
    _nombres_unicos([c.nombre for c in corridas])
    evaluadas = [evaluar_resultado(c, cfg) for c in corridas]
    manifiesto = _manifiesto(cfg, evaluadas)
    asegurar_gitignore(Path(resultados))
    identificador = hash_de_json({"configuracion": manifiesto["configuracion_sha256"], "entradas": manifiesto["entradas"]})
    carpeta = Path(resultados) / CARPETA_EXPERIMENTO / f"avisos-{identificador[:LARGO_ID]}"
    carpeta.mkdir(parents=True, exist_ok=True)
    _escribir(carpeta, cfg, evaluadas, manifiesto)
    return carpeta, evaluadas
