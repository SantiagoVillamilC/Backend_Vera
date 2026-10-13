"""Configuración inmutable del detector de recurrencia, con carga desde JSON y validación estricta.

Todos los índices del detector (pesos, tolerancias, cadencias, ocurrencias mínimas y umbral) viven aquí
y nunca como constantes sueltas en el código, para poder barrerlos. La validación junta todos los
problemas en un solo mensaje, de modo que un archivo con varios errores se corrige de una vez.
"""

import hashlib
import json
import math
from dataclasses import dataclass
from fractions import Fraction
from pathlib import Path

RUTA_DEFECTO = Path(__file__).resolve().parent / "config" / "recurrencia.json"
CLAVES = ("w_monto", "w_fecha", "tol_monto", "cadencias_dias", "tolerancias_dias", "n_min", "umbral")
DECIMALES_FACTOR = 6


class ConfiguracionInvalida(ValueError):
    """La configuración (o la rejilla) no cumple las reglas. El mensaje lista cada problema."""


def es_numero(valor: object) -> bool:
    """Número real finito. Los booleanos se rechazan a propósito porque son enteros en Python."""
    return isinstance(valor, (int, float)) and not isinstance(valor, bool) and math.isfinite(valor)


def es_entero(valor: object) -> bool:
    return isinstance(valor, int) and not isinstance(valor, bool)


def exacto(valor: int | float) -> Fraction:
    """Fracción exacta del decimal que el usuario escribió (0.1 es 1/10 y no 0.1000000000000000055...)."""
    return Fraction(valor) if es_entero(valor) else Fraction(repr(float(valor)))


def _canonico(valor):
    """Forma canónica para escribir: 3.0 se escribe 3, para que el hash no dependa de cómo se tecleó."""
    if isinstance(valor, (tuple, list)):
        return [_canonico(v) for v in valor]
    if isinstance(valor, float) and valor.is_integer():
        return int(valor)
    return valor


def _problemas_de_pesos(w_monto: object, w_fecha: object) -> list[str]:
    problemas = []
    for nombre, valor in (("w_monto", w_monto), ("w_fecha", w_fecha)):
        if not es_numero(valor) or valor < 0:
            problemas.append(f"{nombre} debe ser un número mayor o igual que 0 (llegó {valor!r})")
    if not problemas and not math.isclose(w_monto + w_fecha, 1.0, abs_tol=1e-9):
        problemas.append(f"los pesos w_monto y w_fecha deben sumar 1 (suman {w_monto + w_fecha})")
    return problemas


def _problemas_de_cadencias(cadencias: object, tolerancias: object) -> list[str]:
    problemas = []
    cadencias_ok = isinstance(cadencias, tuple) and len(cadencias) > 0 and all(
        es_entero(c) and c > 0 for c in cadencias)
    if not cadencias_ok:
        problemas.append(f"cadencias_dias debe ser una lista no vacía de enteros positivos (llegó {cadencias!r})")
    elif len(set(cadencias)) != len(cadencias):
        problemas.append(f"cadencias_dias tiene cadencias repetidas ({list(cadencias)})")
    if not (isinstance(tolerancias, tuple) and tolerancias and all(es_numero(t) and t > 0 for t in tolerancias)):
        problemas.append(f"tolerancias_dias debe ser una lista no vacía de números positivos (llegó {tolerancias!r})")
    elif cadencias_ok and len(tolerancias) != len(cadencias):
        problemas.append("cadencias_dias y tolerancias_dias deben tener la misma longitud "
                         f"({len(cadencias)} frente a {len(tolerancias)})")
    return problemas


def _problemas_escalares(tol_monto: object, n_min: object, umbral: object) -> list[str]:
    problemas = []
    if not es_numero(tol_monto) or not 0 < tol_monto <= 1:
        problemas.append(f"tol_monto debe ser un número mayor que 0 y menor o igual que 1 (llegó {tol_monto!r})")
    if not es_entero(n_min) or n_min < 1:
        problemas.append(f"n_min debe ser un entero mayor o igual que 1 (llegó {n_min!r})")
    if not es_numero(umbral) or not 0 <= umbral <= 1:
        problemas.append(f"umbral debe ser un número entre 0 y 1 (llegó {umbral!r})")
    return problemas


@dataclass(frozen=True)
class Configuracion:
    """Parámetros del índice R = N x (w_monto x Monto + w_fecha x Fecha) y de la decisión por umbral.

    `cadencias_dias` y `tolerancias_dias` se emparejan por posición: la cadencia i admite una
    desviación de `tolerancias_dias[i]` días, de forma inclusiva.
    """

    w_monto: float
    w_fecha: float
    tol_monto: float
    cadencias_dias: tuple[int, ...]
    tolerancias_dias: tuple[float, ...]
    n_min: int
    umbral: float

    def __post_init__(self) -> None:
        for campo in ("cadencias_dias", "tolerancias_dias"):
            valor = getattr(self, campo)
            if isinstance(valor, list):
                object.__setattr__(self, campo, tuple(valor))
        problemas = (_problemas_de_pesos(self.w_monto, self.w_fecha)
                     + _problemas_de_cadencias(self.cadencias_dias, self.tolerancias_dias)
                     + _problemas_escalares(self.tol_monto, self.n_min, self.umbral))
        if problemas:
            raise ConfiguracionInvalida("Configuración inválida:\n- " + "\n- ".join(problemas))

    @classmethod
    def por_defecto(cls) -> "Configuracion":
        return cargar_configuracion(RUTA_DEFECTO)

    def a_dict(self) -> dict:
        return {
            "w_monto": _canonico(self.w_monto), "w_fecha": _canonico(self.w_fecha),
            "tol_monto": _canonico(self.tol_monto), "cadencias_dias": _canonico(self.cadencias_dias),
            "tolerancias_dias": _canonico(self.tolerancias_dias), "n_min": self.n_min,
            "umbral": _canonico(self.umbral),
        }

    def hash(self) -> str:
        texto = json.dumps(self.a_dict(), sort_keys=True, separators=(",", ":"))
        return hashlib.sha256(texto.encode("utf-8")).hexdigest()

    def con_factor_tolerancia(self, factor: float) -> "Configuracion":
        """Copia con las tolerancias de días multiplicadas por `factor` (la original no cambia)."""
        escaladas = tuple(round(t * factor, DECIMALES_FACTOR) for t in self.tolerancias_dias)
        return Configuracion(self.w_monto, self.w_fecha, self.tol_monto, self.cadencias_dias, escaladas,
                             self.n_min, self.umbral)


def desde_dict(datos: object) -> Configuracion:
    """Construye y valida una configuración desde un diccionario con exactamente las siete claves."""
    if not isinstance(datos, dict):
        raise ConfiguracionInvalida("Configuración inválida: se esperaba un objeto JSON con las claves "
                                    + ", ".join(CLAVES))
    faltan = [c for c in CLAVES if c not in datos]
    sobran = sorted(str(c) for c in datos if c not in CLAVES)
    problemas = []
    if faltan:
        problemas.append("faltan las claves: " + ", ".join(faltan))
    if sobran:
        problemas.append("hay claves desconocidas: " + ", ".join(sobran))
    if problemas:
        raise ConfiguracionInvalida("Configuración inválida:\n- " + "\n- ".join(problemas))
    return Configuracion(**{c: _como_tupla(datos[c]) for c in CLAVES})


def _como_tupla(valor: object) -> object:
    return tuple(valor) if isinstance(valor, list) else valor


def cargar_configuracion(ruta: Path | str) -> Configuracion:
    try:
        datos = json.loads(Path(ruta).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ConfiguracionInvalida(f"No se pudo leer la configuración {ruta}: {error}") from error
    return desde_dict(datos)
