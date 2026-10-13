"""Configuración inmutable de los avisos (experimento C), con carga desde JSON y validación estricta.

Todos los índices de las reglas viven aquí y no como constantes sueltas: meses mínimos de historial, multiplicador
del gasto, caída de ingreso y su guardia, z modificado, ventanas de pago y de cobro, tolerancia de monto, tipos de
asiento que cuentan como gasto o ingreso, reglas y líneas base a evaluar, fuentes de series y la configuración del
detector de recurrencia (el óptimo restringido del experimento B). Casi todas las reglas son propuesta sin fuente
(ver SUPUESTOS.md). La validación junta todos los problemas en un solo mensaje.
"""

import hashlib
import json
from dataclasses import dataclass
from pathlib import Path

from .avisos_estadistica import LINEAS_BASE
from .configuracion import Configuracion, ConfiguracionInvalida, desde_dict, es_entero, es_numero

RUTA_DEFECTO_AVISOS = Path(__file__).resolve().parent / "config" / "avisos.json"
REGLAS = ("R1", "R2")
FUENTES_SERIES = ("oraculo", "detectadas")
ENTEROS = ("meses_minimos", "ventana_pago_dias", "ventana_cobro_dias", "pagos_minimos_cobro", "k_categoria_inferida")
NO_NEGATIVOS = ("tope_avisos_mes",)
REALES = ("multiplicador_gasto", "caida_d", "cv_robusto_max", "z_factor", "z_umbral", "tol_monto_cobro")
BOOLEANOS = ("prorrateo_acumulado",)
TEXTOS = ("tipos_asiento_gasto", "tipos_asiento_ingreso", "descripciones_no_ingreso")
ELECCIONES = {"reglas": REGLAS, "lineas_base": LINEAS_BASE, "fuentes_series": FUENTES_SERIES}
CLAVES = ENTEROS + NO_NEGATIVOS + REALES + BOOLEANOS + TEXTOS + tuple(ELECCIONES) + ("recurrencia",)
PUEDE_ESTAR_VACIA = ("descripciones_no_ingreso",)


@dataclass(frozen=True)
class ConfiguracionAvisos:
    meses_minimos: int
    ventana_pago_dias: int
    ventana_cobro_dias: int
    pagos_minimos_cobro: int
    k_categoria_inferida: int
    multiplicador_gasto: float
    caida_d: float
    cv_robusto_max: float
    z_factor: float
    z_umbral: float
    tol_monto_cobro: float
    prorrateo_acumulado: bool
    tope_avisos_mes: int
    tipos_asiento_gasto: tuple[str, ...]
    tipos_asiento_ingreso: tuple[str, ...]
    descripciones_no_ingreso: tuple[str, ...]
    reglas: tuple[str, ...]
    lineas_base: tuple[str, ...]
    fuentes_series: tuple[str, ...]
    recurrencia: Configuracion

    def a_dict(self) -> dict:
        datos = {c: getattr(self, c) for c in CLAVES if c != "recurrencia"}
        datos = {c: list(v) if isinstance(v, tuple) else v for c, v in datos.items()}
        return {**datos, "recurrencia": self.recurrencia.a_dict()}

    def hash(self) -> str:
        texto = json.dumps(self.a_dict(), sort_keys=True, separators=(",", ":"))
        return hashlib.sha256(texto.encode("utf-8")).hexdigest()


def _problemas_de_booleanos(datos: dict) -> list[str]:
    return [f"{c} debe ser true o false (llegó {datos[c]!r})"
            for c in BOOLEANOS if c in datos and not isinstance(datos[c], bool)]


def _problemas_de_no_negativos(datos: dict) -> list[str]:
    return [f"{c} debe ser un entero mayor o igual que 0 (llegó {datos[c]!r})"
            for c in NO_NEGATIVOS if c in datos and (not es_entero(datos[c]) or datos[c] < 0)]


def _problemas_de_numeros(datos: dict) -> list[str]:
    problemas = [f"{c} debe ser un entero mayor o igual que 1 (llegó {datos[c]!r})"
                 for c in ENTEROS if c in datos and (not es_entero(datos[c]) or datos[c] < 1)]
    problemas += [f"{c} debe ser un número mayor que 0 (llegó {datos[c]!r})"
                  for c in REALES if c in datos and (not es_numero(datos[c]) or datos[c] <= 0)]
    if es_numero(datos.get("caida_d")) and datos["caida_d"] >= 1:
        problemas.append(f"caida_d debe ser menor que 1 (llegó {datos['caida_d']!r})")
    if es_numero(datos.get("tol_monto_cobro")) and datos["tol_monto_cobro"] > 1:
        problemas.append(f"tol_monto_cobro debe ser menor o igual que 1 (llegó {datos['tol_monto_cobro']!r})")
    return problemas


def _problemas_de_listas(datos: dict) -> list[str]:
    problemas = []
    for clave in (c for c in TEXTOS if c in datos):
        valor = datos[clave]
        vacia_ok = clave in PUEDE_ESTAR_VACIA
        if not isinstance(valor, list) or (not valor and not vacia_ok) or not all(isinstance(t, str) for t in valor):
            problemas.append(f"{clave} debe ser una lista {'' if vacia_ok else 'no vacía '}de textos (llegó {valor!r})")
    for clave, permitidos in ((c, p) for c, p in ELECCIONES.items() if c in datos):
        valor = datos[clave]
        if not isinstance(valor, list) or not valor or any(v not in permitidos for v in valor) \
                or len(set(valor)) != len(valor):
            problemas.append(f"{clave} debe ser una lista no vacía, sin repetidos, con valores de {list(permitidos)} "
                             f"(llegó {valor!r})")
    return problemas


def _recurrencia(datos: dict, problemas: list[str]) -> Configuracion | None:
    try:
        return desde_dict(datos["recurrencia"])
    except ConfiguracionInvalida as error:
        problemas.append("recurrencia inválida: " + str(error).replace("\n", " "))
        return None


def desde_dict_avisos(datos: object) -> ConfiguracionAvisos:
    """Construye y valida la configuración desde un diccionario con exactamente las claves esperadas."""
    if not isinstance(datos, dict):
        raise ConfiguracionInvalida("Configuración de avisos inválida: se esperaba un objeto JSON con las claves "
                                    + ", ".join(CLAVES))
    problemas = []
    faltan = [c for c in CLAVES if c not in datos]
    sobran = sorted(str(c) for c in datos if c not in CLAVES)
    if faltan:
        problemas.append("faltan las claves: " + ", ".join(faltan))
    if sobran:
        problemas.append("hay claves desconocidas: " + ", ".join(sobran))
    problemas += (_problemas_de_numeros(datos) + _problemas_de_no_negativos(datos)
                  + _problemas_de_booleanos(datos) + _problemas_de_listas(datos))
    recurrencia = _recurrencia(datos, problemas) if "recurrencia" in datos else None
    if problemas:
        raise ConfiguracionInvalida("Configuración de avisos inválida:\n- " + "\n- ".join(problemas))
    campos = {c: tuple(datos[c]) if isinstance(datos[c], list) else datos[c] for c in CLAVES if c != "recurrencia"}
    return ConfiguracionAvisos(**campos, recurrencia=recurrencia)


def cargar_configuracion_avisos(ruta: Path | str) -> ConfiguracionAvisos:
    try:
        datos = json.loads(Path(ruta).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ConfiguracionInvalida(f"No se pudo leer la configuración {ruta}: {error}") from error
    return desde_dict_avisos(datos)
