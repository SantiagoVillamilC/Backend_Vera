"""Utilidades compartidas por las pruebas: generación con caché y lectura de CSV."""

import atexit
import csv
import re
import shutil
import tempfile
from collections import defaultdict
from datetime import date, datetime, timedelta
from pathlib import Path

from generador.orquestador import generar_en_disco
from generador.parametros import Parametros

_RAIZ = Path(tempfile.mkdtemp(prefix="dataset-inteligencia-tests-"))
_CACHE: dict[str, Path] = {}
atexit.register(lambda: shutil.rmtree(_RAIZ, ignore_errors=True))

# Regex escritos aquí a mano, a propósito, para no depender del código bajo prueba.
PATRONES = {
    "comercio": re.compile(r"^00[0-9]{8}$"),
    "celular": re.compile(r"^3[0-9]{9}$"),
    "alfanumerico": re.compile(r"^@[A-Za-z0-9]{5,20}$"),
    "documento": re.compile(r"^[0-9]{6,10}$"),
    "correo": re.compile(r"^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}$"),
}
PATRON_CODIGO_ALFANUMERICO = re.compile(r"^@[A-Za-z0-9]{1,64}$")
PATRON_CODIGO_DOCUMENTO = re.compile(r"^[0-9]{6,10}$")
PATRON_CODIGO_COMERCIO = re.compile(r"^[A-Za-z0-9]{6,20}$")


def llave_valida(tipo: str, llave: str) -> bool:
    if tipo not in PATRONES or not PATRONES[tipo].match(llave):
        return False
    if tipo == "correo":
        local, dominio = llave.split("@")
        return len(local) <= 30 and len(dominio) <= 61
    if tipo == "alfanumerico":
        return bool(PATRON_CODIGO_ALFANUMERICO.match(llave))
    if tipo == "comercio":
        return bool(PATRON_CODIGO_COMERCIO.match(llave))
    return True


def carpeta(**kw) -> Path:
    """Genera (una sola vez por combinación de parámetros) y devuelve la carpeta."""
    params = Parametros(**{"filas": 3000, **kw})
    clave = repr(params)
    if clave not in _CACHE:
        _CACHE[clave] = generar_en_disco(params, _RAIZ / f"c{len(_CACHE)}")
    return _CACHE[clave]


def leer(base: Path, relativo: str) -> list[dict]:
    with open(base / relativo, newline="", encoding="utf-8") as f:
        return list(csv.DictReader(f))


def encabezado(base: Path, relativo: str) -> list[str]:
    with open(base / relativo, newline="", encoding="utf-8") as f:
        return next(csv.reader(f))


def instante(texto: str) -> datetime:
    return datetime.strptime(texto, "%Y-%m-%dT%H:%M:%SZ")


def local(texto: str) -> datetime:
    """Instante UTC (texto ISO con Z) llevado a hora de Colombia, UTC-5."""
    return instante(texto) - timedelta(hours=5)


def fecha_local(texto: str) -> date:
    return local(texto).date()


def correcciones_completas(base: Path) -> list[dict]:
    """Correcciones de entrada unidas con su verdad (incluye nombre_puesto_canonico)."""
    verdad = {c["correccion_id"]: c for c in leer(base, "verdad/correcciones_verdad.csv")}
    return [{**c, **verdad[c["correccion_id"]]} for c in leer(base, "entrada/correcciones.csv")]


def firmado(fila: dict) -> int:
    monto = int(fila["monto_centavos"])
    return monto if fila["direccion"] == "entrada" else -monto


def usuario_de_aviso(base: Path, tipo: str, fecha: str = "", **flags: str) -> str:
    """Devuelve el único usuario cuyo aviso `tipo` cumple los flags (y la fecha, si se indica)."""
    cols = {"mediana": "debe_disparar_con_mediana", "media": "debe_disparar_con_media"}
    filas = [
        a for a in leer(base, "verdad/avisos_esperados.csv")
        if a["tipo"] == tipo and (not fecha or a["fecha_evaluacion"] == fecha)
        and all(a[cols[k]] == v for k, v in flags.items())
    ]
    assert len(filas) == 1, f"se esperaba un aviso {tipo} {flags}, hay {len(filas)}"
    return filas[0]["usuario_id"]


def movimientos_de(base: Path, usuario_id: str) -> list[dict]:
    return [m for m in leer(base, "entrada/movimientos.csv") if m["usuario_id"] == usuario_id]


def etiquetas_por_id(base: Path) -> dict[str, dict]:
    return {e["movimiento_id"]: e for e in leer(base, "verdad/etiquetas.csv")}


def total_por_mes(filas: list[dict], etiquetas: dict, categoria: str, direccion: str) -> dict:
    """Suma de monto por mes local (año, mes) de las filas con la categoría real dada."""
    totales: dict[tuple[int, int], int] = defaultdict(int)
    for m in filas:
        if m["direccion"] == direccion and etiquetas[m["movimiento_id"]]["categoria_real"] == categoria:
            f = fecha_local(m["ocurrido_en"])
            totales[(f.year, f.month)] += int(m["monto_centavos"])
    return dict(totales)
