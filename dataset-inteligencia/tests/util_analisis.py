"""Ayudas de las pruebas de analisis: filas de movimientos y corridas mínimas escritas en disco."""

import csv
import json
from pathlib import Path

ENCABEZADO_MOVIMIENTOS = [
    "movimiento_id", "usuario_id", "transferencia_id", "ocurrido_en", "direccion", "monto_centavos",
    "moneda", "tipo_asiento", "descripcion", "tipo_llave", "llave_destino", "nombre_comercio_crudo",
    "mcc", "canal",
]
ENCABEZADO_CORRECCIONES = [
    "correccion_id", "usuario_id", "llave_destino", "nombre_puesto", "categoria_puesta", "ocurrido_en",
]
ENCABEZADO_SERIES = ["serie_id", "usuario_id", "llave_destino", "es_recurrente", "cadencia_dias", "motivo", "detalle"]


def fila(movimiento_id: int, usuario: str, instante: str, pesos: int, llave: str = "0011111111", **extra) -> dict:
    """Una fila de movimientos.csv. Por defecto es un pago saliente a un comercio."""
    base = {
        "movimiento_id": str(movimiento_id), "usuario_id": usuario, "transferencia_id": "",
        "ocurrido_en": instante, "direccion": "salida", "monto_centavos": str(pesos * 100), "moneda": "COP",
        "tipo_asiento": "liquidacion_confirmada", "descripcion": "Pago", "tipo_llave": "comercio",
        "llave_destino": llave, "nombre_comercio_crudo": "", "mcc": "", "canal": "llave",
    }
    return {**base, **extra}


def pagos_en(usuario: str, llave: str, inicio_id: int, fechas_y_montos: list[tuple[str, int]], **extra) -> list[dict]:
    """Un pago por cada par (fecha ISO, monto en pesos), a las 16:00 UTC (11:00 en Colombia)."""
    return [
        fila(inicio_id + i, usuario, f"{fecha}T16:00:00Z", pesos, llave, **extra)
        for i, (fecha, pesos) in enumerate(fechas_y_montos)
    ]


def serie(usuario: str, llave: str, es_recurrente: bool, motivo: str, numero: int = 0) -> dict:
    return {
        "serie_id": f"serie-{numero}", "usuario_id": usuario, "llave_destino": llave,
        "es_recurrente": "true" if es_recurrente else "false",
        "cadencia_dias": "30" if es_recurrente else "", "motivo": motivo, "detalle": "prueba",
    }


def _escribir(ruta: Path, encabezado: list[str], filas: list[dict]) -> None:
    ruta.parent.mkdir(parents=True, exist_ok=True)
    with open(ruta, "w", newline="", encoding="utf-8") as f:
        w = csv.DictWriter(f, fieldnames=encabezado, lineterminator="\n")
        w.writeheader()
        w.writerows(filas)


def escribir_corrida(destino: Path, filas: list[dict], series: list[dict], fecha_fin: str = "2026-10-21",
                     semilla: int = 0) -> Path:
    """Escribe una corrida mínima con la misma forma que la del generador."""
    _escribir(destino / "entrada" / "movimientos.csv", ENCABEZADO_MOVIMIENTOS, filas)
    _escribir(destino / "entrada" / "correcciones.csv", ENCABEZADO_CORRECCIONES, [])
    _escribir(destino / "verdad" / "series.csv", ENCABEZADO_SERIES, series)
    manifiesto = {"parametros": {"fecha_fin": fecha_fin, "filas": len(filas), "semilla": semilla, "variante": "A"}}
    (destino / "manifiesto.json").write_text(json.dumps(manifiesto, indent=2, sort_keys=True), encoding="utf-8")
    return destino


# Corrida mínima con resultados calculados a mano para la configuración por defecto
# (w_monto 0.6, w_fecha 0.4, tol_monto 0.10, cadencias 7/14/30 con 2/3/4, n_min 3, umbral 0.5):
#   A regular de 5 pagos mensuales, R = 1.0, recurrente y detectada (TP).
#   B recurrente por diseño pero irregular, R = 0.2 (FN).
#   C casualidad, dos pagos iguales a 30 días, R = 2/3 (FP con umbral 0.5, TN con umbral 0.7).
#   D compra habitual irregular, R = 0.2 (TN).
#   E fuera de series.csv, dos pagos iguales a 30 días, R = 2/3 (falso positivo secundario).
#   F fuera de series.csv, un solo pago (no entra en el universo secundario).
#   G fuera de series.csv, tres pagos irregulares, R = 0.2 (negativo secundario correcto).
A = ("u1", "0011111111")
B = ("u2", "0022222222")
C = ("u3", "0033333333")
D = ("u4", "0044444444")
E = ("u5", "0055555555")
F = ("u6", "0066666666")
G = ("u7", "0077777777")


def filas_mini() -> list[dict]:
    pagos = (
        pagos_en(*A, 1, [("2026-05-05", 50000), ("2026-06-05", 50000), ("2026-07-05", 50000),
                         ("2026-08-05", 50000), ("2026-09-05", 50000)])
        + pagos_en(*B, 10, [("2026-05-03", 100000), ("2026-06-20", 200000), ("2026-07-02", 400000)])
        + pagos_en(*C, 20, [("2026-05-10", 30000), ("2026-06-09", 30000)])
        + pagos_en(*D, 30, [("2026-05-01", 10000), ("2026-05-04", 55000), ("2026-07-30", 23000)])
        + pagos_en(*E, 40, [("2026-06-01", 20000), ("2026-07-01", 20000)])
        + pagos_en(*F, 50, [("2026-06-15", 20000)])
        + pagos_en(*G, 60, [("2026-05-01", 10000), ("2026-06-02", 50000), ("2026-06-04", 12000)])
    )
    return [{**p, "movimiento_id": str(i + 1)} for i, p in enumerate(sorted(pagos, key=lambda r: r["ocurrido_en"]))]


def series_mini() -> list[dict]:
    return [
        serie(*A, True, "regular", 1), serie(*B, True, "dia_variable", 2),
        serie(*C, False, "pago_suelto", 3), serie(*D, False, "compra_habitual", 4),
    ]


def corrida_mini(destino: Path, semilla: int = 0) -> Path:
    return escribir_corrida(destino, filas_mini(), series_mini(), semilla=semilla)


# Corrida con diferencias claras entre métodos: a la mini se suman 30 series recurrentes de monto fijo y
# día muy variable (pagos de 50.000 con intervalos de 20 y 40 días). Con la configuración por defecto
# el índice las detecta (R = 0,6, mayor o igual que 0,5) y la línea base no (0 de 2 intervalos en la banda).
NUMERO_DE_SERIES_EXTRA = 30


def corrida_con_diferencias(destino: Path, semilla: int = 0) -> Path:
    filas, series = filas_mini(), series_mini()
    for k in range(NUMERO_DE_SERIES_EXTRA):
        usuario, llave = f"v{k}", f"009{k:07d}"
        filas += pagos_en(usuario, llave, 1000 + 3 * k,
                          [("2026-05-01", 50000), ("2026-05-21", 50000), ("2026-06-30", 50000)])
        series.append(serie(usuario, llave, True, "dia_variable", 100 + k))
    return escribir_corrida(destino, filas, series, semilla=semilla)
