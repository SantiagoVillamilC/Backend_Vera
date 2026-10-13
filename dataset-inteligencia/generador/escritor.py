"""Escritura de los CSV y del manifiesto. Salida determinista: UTF-8, saltos \\n, sin horas del sistema."""

import csv
import hashlib
import json
from pathlib import Path

from .constantes import VERSION_GENERADOR
from .dataset import Dataset
from .mcc import mcc_de
from .modelos import uuid_determinista
from .nombres import canonizar
from .presentacion import presentar
from .tiempo import a_texto_utc

COLUMNAS = {
    "entrada/movimientos.csv": [
        "movimiento_id", "usuario_id", "transferencia_id", "ocurrido_en", "direccion", "monto_centavos",
        "moneda", "tipo_asiento", "descripcion", "tipo_llave", "llave_destino", "nombre_comercio_crudo",
        "mcc", "canal"],
    "entrada/correcciones.csv": [
        "correccion_id", "usuario_id", "llave_destino", "nombre_puesto", "categoria_puesta", "ocurrido_en"],
    "verdad/empresas.csv": ["empresa_id", "razon_social", "ficticia"],
    "verdad/comercios.csv": [
        "llave_destino", "clase_llave", "empresa_id", "nombre_canonico", "categoria", "mcc_categoria",
        "es_comercio", "nombre_revela_naturaleza", "conjunto"],
    "verdad/etiquetas.csv": ["movimiento_id", "categoria_real", "llave_destino", "es_persona"],
    "verdad/correcciones_verdad.csv": [
        "correccion_id", "es_correcta", "tipo_ruido", "nombre_puesto_canonico"],
    "verdad/avisos_esperados.csv": [
        "usuario_id", "tipo", "fecha_evaluacion", "debe_disparar_con_mediana", "debe_disparar_con_media",
        "detalle"],
    "verdad/nombres_historial.csv": [
        "llave_destino", "nombre_canonico", "categoria", "vigente_desde", "vigente_hasta"],
    "verdad/casos_vista.csv": [
        "caso_id", "usuario_id", "movimiento_id", "ocurrido_en", "nombre_esperado", "categoria_esperada",
        "origen_esperado"],
    "verdad/series.csv": [
        "serie_id", "usuario_id", "llave_destino", "es_recurrente", "cadencia_dias", "motivo", "detalle"],
}


def _bool(valor: bool) -> str:
    return "true" if valor else "false"


def _movimientos(d: Dataset) -> list[list]:
    razones = {e.empresa_id: e.razon_social for e in d.empresas}
    historial: dict[str, list] = {}
    for h in d.historial:
        historial.setdefault(h.llave_destino, []).append(h)
    filas = []
    for m in d.movimientos:
        p = presentar(m, d.parametros, d.base_presentacion, razones, historial)
        transferencia = "" if m.grupo is None else _transferencia(d, m.grupo)
        filas.append([m.movimiento_id, m.usuario_id, transferencia, a_texto_utc(m.ocurrido_en), m.direccion,
                      m.monto_centavos, "COP", m.tipo_asiento, m.descripcion, p.tipo_llave, p.llave_destino,
                      p.nombre_comercio_crudo, p.mcc, p.canal])
    return filas


def _transferencia(d: Dataset, grupo: int) -> str:
    return uuid_determinista(d.parametros.semilla, "transferencia", grupo)


def _correcciones(d: Dataset) -> list[list]:
    return [[c.correccion_id, c.usuario_id, c.llave_destino, c.nombre_puesto, c.categoria_puesta,
             a_texto_utc(c.ocurrido_en)] for c in d.correcciones]


def _comercios(d: Dataset) -> list[list]:
    return [[x.llave_destino, x.tipo_llave, x.empresa_id, x.nombre_canonico, x.categoria,
             mcc_de(x.categoria, x.llave_destino) if x.es_comercio else "", _bool(x.es_comercio),
             _bool(x.nombre_revela_naturaleza), d.conjuntos.get(x.llave_destino, "")]
            for x in d.destinos]


def _etiquetas(d: Dataset) -> list[list]:
    return [[m.movimiento_id, m.categoria_real, m.destino.llave_destino if m.destino else "",
             _bool(m.es_persona)] for m in d.movimientos]


def _verdad_correcciones(d: Dataset) -> list[list]:
    return [[c.correccion_id, _bool(c.es_correcta), c.tipo_ruido, canonizar(c.nombre_puesto)]
            for c in d.correcciones]


def _avisos(d: Dataset) -> list[list]:
    return [[a.usuario_id, a.tipo, a.fecha_evaluacion.isoformat(), _bool(a.con_mediana), _bool(a.con_media),
             a.detalle] for a in d.avisos]


def _historial(d: Dataset) -> list[list]:
    return [[h.llave_destino, h.nombre_canonico, h.categoria, h.vigente_desde.isoformat(),
             h.vigente_hasta.isoformat() if h.vigente_hasta else ""] for h in d.historial]


def _vistas(d: Dataset) -> list[list]:
    return [[v.caso_id, v.usuario_id, m.movimiento_id, a_texto_utc(m.ocurrido_en), v.nombre_esperado,
             v.categoria_esperada, v.origen_esperado] for v, m in d.vistas]


def _series(d: Dataset) -> list[list]:
    return [[sid, s.usuario_id, s.llave_destino, _bool(s.es_recurrente),
             "" if s.cadencia_dias is None else s.cadencia_dias, s.motivo, s.detalle] for sid, s in d.series]


def _filas_por_archivo(d: Dataset) -> dict[str, list[list]]:
    return {
        "entrada/movimientos.csv": _movimientos(d),
        "entrada/correcciones.csv": _correcciones(d),
        "verdad/empresas.csv": [[e.empresa_id, e.razon_social, _bool(e.ficticia)] for e in d.empresas],
        "verdad/comercios.csv": _comercios(d),
        "verdad/etiquetas.csv": _etiquetas(d),
        "verdad/correcciones_verdad.csv": _verdad_correcciones(d),
        "verdad/avisos_esperados.csv": _avisos(d),
        "verdad/nombres_historial.csv": _historial(d),
        "verdad/casos_vista.csv": _vistas(d),
        "verdad/series.csv": _series(d),
    }


def _escribir_csv(ruta: Path, columnas: list[str], filas: list[list]) -> None:
    ruta.parent.mkdir(parents=True, exist_ok=True)
    with open(ruta, "w", newline="", encoding="utf-8") as f:
        escritor = csv.writer(f, lineterminator="\n")
        escritor.writerow(columnas)
        escritor.writerows(filas)


def _manifiesto(d: Dataset, archivos: dict[str, dict]) -> str:
    cuerpo = {
        "generador": {"nombre": "dataset-inteligencia", "version": VERSION_GENERADOR},
        "parametros": d.parametros.a_dict(),
        "archivos": archivos,
    }
    return json.dumps(cuerpo, ensure_ascii=False, indent=2, sort_keys=True) + "\n"


def escribir_dataset(d: Dataset, carpeta: Path) -> None:
    archivos = {}
    for relativo, filas in _filas_por_archivo(d).items():
        ruta = carpeta / relativo
        _escribir_csv(ruta, COLUMNAS[relativo], filas)
        archivos[relativo] = {"filas": len(filas), "sha256": hashlib.sha256(ruta.read_bytes()).hexdigest()}
    (carpeta / "manifiesto.json").write_text(_manifiesto(d, archivos), encoding="utf-8", newline="\n")
