"""Ayudas de las pruebas de convergencia: filas de correcciones y una corrida diminuta con verdad calculada a mano.

Todos los instantes de la corrida diminuta caen a las 05:00 UTC, que es la medianoche de Colombia, para que
las diferencias en días sean enteras y se puedan calcular a mano.

Comercios de la corrida diminuta (el `conjunto` va entre paréntesis):
  C1 0011111111 TIENDA-UNO, compras (prueba). u1 el 05-01 y u2 el 05-11, nombres equivalentes tras canonizar.
  C2 0022222222 FERRETERIA-DOS, vivienda_servicios (entrenamiento). u5 con tipeo el 05-02, u3 el 05-03, u4 el 05-06.
  C3 0033333333 DROGUERIA-TRES, salud (prueba). Colusores u6 (04-10) y u7 (04-12) con entretenimiento. Honestos
     u8 (06-01), u9 (06-05) y u10 (06-10) con salud.
  C5 0055555555 cambia el 2026-06-15 de CAFETERIA-CINCO (restaurantes_domicilios) a LIBRERIA-CINCO (educacion)
     (entrenamiento). u13 el 07-01 y u14 el 07-10 corrigen al valor nuevo.
  C6 0066666666 KIOSCO-SEIS, compras (prueba), sin correcciones, nombre_revela_naturaleza verdadero.
  C8 0088888888 PANADERIA-OCHO, alimentos_mercado (entrenamiento). u16 con tipeo el 05-05 y u15 el 05-06.
  P4 3001234567 persona (celular). u11 y u12 la etiquetan igual como vivienda_servicios el 08-01 y el 08-02.
"""

import json
from pathlib import Path

from .util_analisis import ENCABEZADO_CORRECCIONES, ENCABEZADO_MOVIMIENTOS, _escribir, fila

FECHA_FIN = "2026-10-21"
ENCABEZADO_COMERCIOS = ["llave_destino", "clase_llave", "empresa_id", "nombre_canonico", "categoria",
                        "mcc_categoria", "es_comercio", "nombre_revela_naturaleza", "conjunto"]
ENCABEZADO_HISTORIAL = ["llave_destino", "nombre_canonico", "categoria", "vigente_desde", "vigente_hasta"]
ENCABEZADO_VERDAD_CORRECCIONES = ["correccion_id", "es_correcta", "tipo_ruido", "nombre_puesto_canonico"]
ENCABEZADO_CASOS = ["caso_id", "usuario_id", "movimiento_id", "ocurrido_en", "nombre_esperado",
                    "categoria_esperada", "origen_esperado"]


def t(mes_dia: str) -> str:
    """Instante a las 05:00 UTC (medianoche en Colombia) de un día de 2026, por ejemplo `05-01`."""
    return f"2026-{mes_dia}T05:00:00Z"


def corr(numero: int, usuario: str, llave: str, nombre: str, categoria: str, mes_dia: str) -> dict:
    """Una fila de correcciones.csv. El id es `c<numero con ceros>` para que el orden de ids sea el numérico."""
    return {"correccion_id": f"c{numero:03d}", "usuario_id": usuario, "llave_destino": llave,
            "nombre_puesto": nombre, "categoria_puesta": categoria, "ocurrido_en": t(mes_dia)}


CORRECCIONES_DIMINUTAS = [
    corr(1, "u1", "0011111111", "Tienda Uno", "compras", "05-01"),
    corr(2, "u5", "0022222222", "Ferretria Dos", "vivienda_servicios", "05-02"),
    corr(3, "u3", "0022222222", "Ferreteria Dos", "vivienda_servicios", "05-03"),
    corr(4, "u16", "0088888888", "Panadria Ocho", "alimentos_mercado", "05-05"),
    corr(5, "u4", "0022222222", "Ferreteria Dos", "vivienda_servicios", "05-06"),
    corr(6, "u15", "0088888888", "Panaderia Ocho", "alimentos_mercado", "05-06"),
    corr(7, "u2", "0011111111", "tienda_uno", "compras", "05-11"),
    corr(8, "u6", "0033333333", "Drogueria Tres", "entretenimiento", "04-10"),
    corr(9, "u7", "0033333333", "Drogueria Tres", "entretenimiento", "04-12"),
    corr(10, "u8", "0033333333", "Drogueria Tres", "salud", "06-01"),
    corr(11, "u9", "0033333333", "DROGUERIA TRES", "salud", "06-05"),
    corr(12, "u10", "0033333333", "Drogueria Tres", "salud", "06-10"),
    corr(13, "u13", "0055555555", "Libreria Cinco", "educacion", "07-01"),
    corr(14, "u14", "0055555555", "Libreria Cinco", "educacion", "07-10"),
    corr(15, "u11", "3001234567", "Arrendadora Ana", "vivienda_servicios", "08-01"),
    corr(16, "u12", "3001234567", "Arrendadora Ana", "vivienda_servicios", "08-02"),
]
RUIDO = {"c008": "colusion", "c009": "colusion", "c002": "tipeo", "c004": "tipeo"}

COMERCIOS = [
    ("0011111111", "comercio", "TIENDA-UNO", "compras", "true", "false", "prueba"),
    ("0022222222", "comercio", "FERRETERIA-DOS", "vivienda_servicios", "true", "true", "entrenamiento"),
    ("0033333333", "comercio", "DROGUERIA-TRES", "salud", "true", "true", "prueba"),
    ("0055555555", "comercio", "LIBRERIA-CINCO", "educacion", "true", "false", "entrenamiento"),
    ("0066666666", "comercio", "KIOSCO-SEIS", "compras", "true", "true", "prueba"),
    ("0088888888", "comercio", "PANADERIA-OCHO", "alimentos_mercado", "true", "false", "entrenamiento"),
    ("3001234567", "celular", "ARRENDADORA-ANA", "sin_categoria", "false", "false", ""),
]
HISTORIAL = [
    ("0011111111", "TIENDA-UNO", "compras", "2026-01-01", ""),
    ("0022222222", "FERRETERIA-DOS", "vivienda_servicios", "2026-01-01", ""),
    ("0033333333", "DROGUERIA-TRES", "salud", "2026-01-01", ""),
    ("0055555555", "CAFETERIA-CINCO", "restaurantes_domicilios", "2026-01-01", "2026-06-14"),
    ("0055555555", "LIBRERIA-CINCO", "educacion", "2026-06-15", ""),
    ("0066666666", "KIOSCO-SEIS", "compras", "2026-01-01", ""),
    ("0088888888", "PANADERIA-OCHO", "alimentos_mercado", "2026-01-01", ""),
]
# Casos de vista sobre C1: privado (u1 ve lo suyo), crudo (antes de cualquier corrección), global (u21 después del 05-11).
CASOS = [
    ("privado", "u1", 3, t("05-20"), "TIENDA-UNO", "compras", "privado"),
    ("crudo", "u20", 1, t("04-25"), "", "sin_categoria", "crudo"),
    ("global", "u21", 2, t("05-20"), "TIENDA-UNO", "compras", "global"),
]


def _tipo_llave(llave: str) -> str:
    return "celular" if llave.startswith("3") else "comercio"


def movimientos_diminutos() -> list[dict]:
    """Un pago por cada comercio, más los tres pagos de los casos de vista (ids 1 a 3 sobre C1)."""
    filas = [
        fila(1, "u20", t("04-25"), 100, "0011111111", nombre_comercio_crudo=""),
        fila(2, "u21", t("05-20"), 100, "0011111111", nombre_comercio_crudo=""),
        fila(3, "u1", t("05-20"), 100, "0011111111", nombre_comercio_crudo=""),
    ]
    otras = [c[0] for c in COMERCIOS if c[0] != "0011111111"]
    for i, llave in enumerate(otras):
        filas.append(fila(10 + i, f"p{i}", t("04-01"), 100, llave, tipo_llave=_tipo_llave(llave)))
    return filas


def escribir_corrida_diminuta(destino: Path, tasa: float = 0.35, semilla: int = 0) -> Path:
    """Corrida diminuta con entrada y verdad escritas en disco, con la misma forma que la del generador."""
    _escribir(destino / "entrada" / "movimientos.csv", ENCABEZADO_MOVIMIENTOS, movimientos_diminutos())
    _escribir(destino / "entrada" / "correcciones.csv", ENCABEZADO_CORRECCIONES, CORRECCIONES_DIMINUTAS)
    verdad = destino / "verdad"
    _escribir(verdad / "comercios.csv", ENCABEZADO_COMERCIOS, [
        dict(zip(ENCABEZADO_COMERCIOS, (c[0], c[1], "e", c[2], c[3], "5411", c[4], c[5], c[6])))
        for c in COMERCIOS])
    _escribir(verdad / "nombres_historial.csv", ENCABEZADO_HISTORIAL,
              [dict(zip(ENCABEZADO_HISTORIAL, h)) for h in HISTORIAL])
    _escribir(verdad / "correcciones_verdad.csv", ENCABEZADO_VERDAD_CORRECCIONES, [
        {"correccion_id": c["correccion_id"], "es_correcta": "true",
         "tipo_ruido": RUIDO.get(c["correccion_id"], "ninguno"), "nombre_puesto_canonico": ""}
        for c in CORRECCIONES_DIMINUTAS])
    _escribir(verdad / "casos_vista.csv", ENCABEZADO_CASOS,
              [dict(zip(ENCABEZADO_CASOS, c)) for c in CASOS])
    manifiesto = {"parametros": {"fecha_fin": FECHA_FIN, "semilla": semilla, "tasa_correccion": tasa,
                                 "variante": "A", "filas": 0}}
    (destino / "manifiesto.json").write_text(json.dumps(manifiesto, indent=2, sort_keys=True), encoding="utf-8")
    return destino
