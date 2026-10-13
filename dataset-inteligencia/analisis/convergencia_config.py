"""Configuración inmutable del replicador de convergencia por multitud (experimento A), con validación estricta.

Todos los índices viven aquí: la lista de K, las políticas de elegibilidad por tipo de llave, la política
principal del reporte, el K de referencia para las comparaciones y la agrupación por similitud
(`metodo_similitud` opcional, por defecto `ninguno`, con su `umbral_similitud`, que es null si el método es ninguno).
La validación junta todos los problemas en un solo mensaje.
"""

import hashlib
import json
from dataclasses import dataclass
from pathlib import Path

from .configuracion import ConfiguracionInvalida, es_entero

RUTA_DEFECTO_CONVERGENCIA = Path(__file__).resolve().parent / "config" / "convergencia.json"
CLAVES = ("ks", "politicas", "politica_principal", "k_referencia", "umbral_similitud")
CLAVES_OPCIONALES = ("metodo_similitud", "representante", "regla_deriva")
REPRESENTANTES = ("primero", "mas_votado")
METODOS_SIMILITUD = ("ninguno", "jaro_winkler", "coseno_tfidf")
REGLAS_DERIVA = ("estricta", "reciente")


@dataclass(frozen=True)
class Politica:
    """Quién es elegible para consolidarse. `tipos_elegibles` None significa que toda llave lo es."""

    nombre: str
    tipos_elegibles: tuple[str, ...] | None

    def admite(self, tipo_llave: str) -> bool:
        return self.tipos_elegibles is None or tipo_llave in self.tipos_elegibles


@dataclass(frozen=True)
class ConfigConvergencia:
    ks: tuple[int, ...]
    politicas: tuple[Politica, ...]
    politica_principal: str
    k_referencia: int
    umbral_similitud: float | None
    metodo_similitud: str = "ninguno"
    representante: str = "primero"
    regla_deriva: str = "estricta"

    def politica(self, nombre: str) -> Politica:
        return next(p for p in self.politicas if p.nombre == nombre)

    def a_dict(self) -> dict:
        """Con el método apagado el diccionario (y su hash) es idéntico al de antes de existir `metodo_similitud`."""
        datos = {
            "ks": list(self.ks),
            "politicas": {p.nombre: None if p.tipos_elegibles is None else list(p.tipos_elegibles)
                          for p in self.politicas},
            "politica_principal": self.politica_principal, "k_referencia": self.k_referencia,
            "umbral_similitud": self.umbral_similitud}
        if self.metodo_similitud != "ninguno":
            datos = datos | {"metodo_similitud": self.metodo_similitud}
        if self.representante != "primero":
            datos = datos | {"representante": self.representante}
        if self.regla_deriva != "estricta":
            datos = datos | {"regla_deriva": self.regla_deriva}
        return datos

    def hash(self) -> str:
        texto = json.dumps(self.a_dict(), sort_keys=True, separators=(",", ":"))
        return hashlib.sha256(texto.encode("utf-8")).hexdigest()


def _problemas_de_ks(ks: object, k_referencia: object) -> list[str]:
    problemas = []
    ks_ok = isinstance(ks, list) and len(ks) > 0 and all(es_entero(k) and k >= 1 for k in ks)
    if not ks_ok:
        problemas.append(f"ks debe ser una lista no vacía de enteros mayores o iguales que 1 (llegó {ks!r})")
    elif len(set(ks)) != len(ks):
        problemas.append(f"ks tiene valores repetidos ({ks})")
    if not es_entero(k_referencia) or (isinstance(ks, list) and k_referencia not in ks):
        problemas.append(f"k_referencia debe ser un entero que esté en ks (llegó {k_referencia!r})")
    return problemas


def _problemas_de_politicas(politicas: object, principal: object) -> list[str]:
    valida = isinstance(politicas, dict) and len(politicas) > 0 and all(
        tipos is None or (isinstance(tipos, list) and tipos and all(isinstance(t, str) and t for t in tipos))
        for tipos in politicas.values())
    problemas = []
    if not valida:
        problemas.append("politicas debe ser un objeto no vacío cuyos valores son null (toda llave es elegible) "
                         f"o una lista no vacía de tipos de llave (llegó {politicas!r})")
    if not isinstance(principal, str) or (valida and principal not in politicas):
        problemas.append(f"politica_principal debe ser el nombre de una política declarada (llegó {principal!r})")
    return problemas


def _problemas_de_umbral(metodo: object, umbral: object) -> list[str]:
    if metodo not in METODOS_SIMILITUD:
        return [f"metodo_similitud debe ser uno de {', '.join(METODOS_SIMILITUD)} (llegó {metodo!r})"]
    if metodo == "ninguno":
        return [] if umbral is None else [
            f"umbral_similitud debe ser null cuando metodo_similitud es ninguno (llegó {umbral!r})"]
    if isinstance(umbral, bool) or not isinstance(umbral, (int, float)) or not 0 < umbral <= 1:
        return [f"umbral_similitud debe ser un número en (0, 1] cuando metodo_similitud es {metodo} (llegó {umbral!r})"]
    return []


def _problemas_de_representante(metodo: object, representante: object) -> list[str]:
    if representante not in REPRESENTANTES:
        return [f"representante debe ser uno de {', '.join(REPRESENTANTES)} (llegó {representante!r})"]
    if representante != "primero" and metodo == "ninguno":
        return [f"representante {representante} exige un metodo_similitud distinto de ninguno"]
    return []


def _problemas_de_deriva(regla: object) -> list[str]:
    if regla not in REGLAS_DERIVA:
        return [f"regla_deriva debe ser una de {', '.join(REGLAS_DERIVA)} (llegó {regla!r})"]
    return []


def desde_dict_convergencia(datos: object) -> ConfigConvergencia:
    """Construye y valida la configuración desde un diccionario con exactamente las cinco claves."""
    if not isinstance(datos, dict):
        raise ConfiguracionInvalida("Configuración inválida: se esperaba un objeto JSON con las claves "
                                    + ", ".join(CLAVES))
    problemas = []
    faltan = [c for c in CLAVES if c not in datos]
    sobran = sorted(str(c) for c in datos if c not in CLAVES + CLAVES_OPCIONALES)
    if faltan:
        problemas.append("faltan las claves: " + ", ".join(faltan))
    if sobran:
        problemas.append("hay claves desconocidas: " + ", ".join(sobran))
    if not faltan:
        problemas += (_problemas_de_ks(datos["ks"], datos["k_referencia"])
                      + _problemas_de_politicas(datos["politicas"], datos["politica_principal"])
                      + _problemas_de_umbral(datos.get("metodo_similitud", "ninguno"), datos["umbral_similitud"])
                      + _problemas_de_representante(datos.get("metodo_similitud", "ninguno"),
                                                    datos.get("representante", "primero"))
                      + _problemas_de_deriva(datos.get("regla_deriva", "estricta")))
    if problemas:
        raise ConfiguracionInvalida("Configuración inválida:\n- " + "\n- ".join(problemas))
    politicas = tuple(Politica(nombre, None if tipos is None else tuple(tipos))
                      for nombre, tipos in datos["politicas"].items())
    return ConfigConvergencia(tuple(datos["ks"]), politicas, datos["politica_principal"], datos["k_referencia"],
                              datos["umbral_similitud"], datos.get("metodo_similitud", "ninguno"),
                              datos.get("representante", "primero"), datos.get("regla_deriva", "estricta"))


def cargar_configuracion_convergencia(ruta: Path | str) -> ConfigConvergencia:
    try:
        datos = json.loads(Path(ruta).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ConfiguracionInvalida(f"No se pudo leer la configuración {ruta}: {error}") from error
    return desde_dict_convergencia(datos)
