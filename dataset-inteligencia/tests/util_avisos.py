"""Usuarios diminutos para las pruebas de avisos, con resultados calculados a mano, y corridas mínimas en disco."""

import csv
from datetime import date
from pathlib import Path

from .util_analisis import ENCABEZADO_CORRECCIONES, escribir_corrida, fila, serie

ENCABEZADO_COMERCIOS = ["llave_destino", "clase_llave", "empresa_id", "nombre_canonico", "categoria",
                        "mcc_categoria", "es_comercio", "nombre_revela_naturaleza", "conjunto"]
ENCABEZADO_ETIQUETAS = ["usuario_id", "tipo", "fecha_evaluacion", "debe_disparar_con_mediana",
                        "debe_disparar_con_media", "detalle"]
MERCADO = "0056988451"
RESTAURANTE = "0077777777"
INTERNET = "0019870620"
OTRA = "0033333333"
CLARO = "0044444444"


class Contador:
    """Genera movimiento_id consecutivos para que cada fila de una prueba tenga el suyo."""

    def __init__(self) -> None:
        self.siguiente = 0

    def __call__(self) -> int:
        self.siguiente += 1
        return self.siguiente


def deposito(ids: Contador, usuario: str, instante: str, pesos: int, descripcion: str = "Salario") -> dict:
    return fila(ids(), usuario, instante, pesos, llave="", direccion="entrada", tipo_asiento="deposito",
                descripcion=descripcion, tipo_llave="")


def retencion(ids: Contador, usuario: str, instante: str, pesos: int) -> dict:
    return fila(ids(), usuario, instante, pesos, llave="", tipo_asiento="retencion", descripcion="GMF 4x1000",
                tipo_llave="")


def pago(ids: Contador, usuario: str, instante: str, pesos: int, llave: str = MERCADO) -> dict:
    return fila(ids(), usuario, instante, pesos, llave)


def pagos_del_mes(ids: Contador, usuario: str, mes: int, cantidad: int, pesos: int = 100_000,
                  llave: str = MERCADO) -> list[dict]:
    """`cantidad` pagos de `pesos` repartidos desde el día 1 del mes, a las 18:00 UTC (13:00 en Colombia)."""
    return [pago(ids, usuario, f"2026-{mes:02d}-{1 + 2 * i:02d}T18:00:00Z", pesos, llave) for i in range(cantidad)]


def usuario_p4(ids: Contador, usuario: str = "u4") -> list[dict]:
    """Perfil 4: 10, 13, 11, 13, 12 y 13 pagos de 100.000 de abril a septiembre y 13 hasta el 20 de octubre.

    Totales mensuales en pesos: 1.000.000, 1.300.000, 1.100.000, 1.300.000, 1.200.000 y 1.300.000.
    Mediana 1.250.000, media 1.200.000, MAD 50.000 y octubre acumulado 1.300.000.
    """
    filas = [deposito(ids, usuario, "2026-04-01T14:00:00Z", 3_000_000)]
    for mes, cantidad in zip(range(4, 10), (10, 13, 11, 13, 12, 13)):
        filas += pagos_del_mes(ids, usuario, mes, cantidad)
    filas += [pago(ids, usuario, f"2026-10-{1 + i:02d}T18:00:00Z", 100_000) for i in range(12)]
    filas.append(pago(ids, usuario, "2026-10-20T18:00:00Z", 100_000))
    return filas


def usuario_p1(ids: Contador, usuario: str = "u1", octubre: int = 6) -> list[dict]:
    """Perfil 1: 300.000 al mes salvo julio con 2.400.000. Mediana 300.000, media 650.000, MAD 0."""
    filas = [deposito(ids, usuario, "2026-04-01T14:00:00Z", 4_000_000)]
    for mes in (4, 5, 6, 8, 9):
        filas += pagos_del_mes(ids, usuario, mes, 3, llave=RESTAURANTE)
    filas.append(pago(ids, usuario, "2026-07-19T01:00:00Z", 2_400_000, RESTAURANTE))
    filas += pagos_del_mes(ids, usuario, 10, octubre, llave=RESTAURANTE)
    return filas


def usuario_p5(ids: Contador, usuario: str = "u5") -> list[dict]:
    """Perfil 5: creado el 15 de agosto, solo septiembre queda completo."""
    filas = [deposito(ids, usuario, "2026-08-15T14:00:00Z", 2_000_000)]
    filas += [pago(ids, usuario, "2026-08-18T18:00:00Z", 100_000, RESTAURANTE),
              pago(ids, usuario, "2026-08-27T18:00:00Z", 100_000, RESTAURANTE)]
    filas += pagos_del_mes(ids, usuario, 9, 3, llave=RESTAURANTE)
    filas += pagos_del_mes(ids, usuario, 10, 6, llave=RESTAURANTE)
    return filas


def usuario_internet(ids: Contador, usuario: str, deposito_pesos: int, hasta_mes: int = 9) -> list[dict]:
    """Un depósito el 1 de abril e internet de 89.900 el día 5 de cada mes, de abril a `hasta_mes`."""
    filas = [deposito(ids, usuario, "2026-04-01T14:00:00Z", deposito_pesos)]
    filas += [pago(ids, usuario, f"2026-{mes:02d}-05T15:00:00Z", 89_900, INTERNET) for mes in range(4, hasta_mes + 1)]
    return filas


def usuario_caida(ids: Contador, usuario: str, ingresos: tuple[int, ...]) -> list[dict]:
    """Un saldo inicial de 800.000 el 1 de abril y un depósito el último día de cada mes (21:30 en Colombia)."""
    instantes = ("2026-05-01T02:30:00Z", "2026-06-01T02:30:00Z", "2026-07-01T02:30:00Z", "2026-08-01T02:30:00Z",
                 "2026-09-01T02:30:00Z", "2026-10-01T02:30:00Z")
    filas = [deposito(ids, usuario, "2026-04-01T14:00:00Z", 800_000, "Saldo inicial")]
    filas += [deposito(ids, usuario, instante, monto) for instante, monto in zip(instantes, ingresos)]
    return filas


def _escribir(ruta: Path, encabezado: list[str], filas: list[dict]) -> None:
    with open(ruta, "w", newline="", encoding="utf-8") as f:
        w = csv.DictWriter(f, fieldnames=encabezado, lineterminator="\n")
        w.writeheader()
        w.writerows(filas)


def etiqueta(usuario: str, tipo: str, fecha: str, mediana: bool, media: bool, detalle: str = "prueba") -> dict:
    texto = {True: "true", False: "false"}
    return {"usuario_id": usuario, "tipo": tipo, "fecha_evaluacion": fecha,
            "debe_disparar_con_mediana": texto[mediana], "debe_disparar_con_media": texto[media], "detalle": detalle}


def comercio(llave: str, categoria: str) -> dict:
    return {"llave_destino": llave, "clase_llave": "comercio", "empresa_id": "e", "nombre_canonico": "X",
            "categoria": categoria, "mcc_categoria": "5411", "es_comercio": "true",
            "nombre_revela_naturaleza": "false", "conjunto": "prueba"}


def corrida_avisos(destino: Path, filas: list[dict], etiquetas: list[dict], series: list[dict] | None = None,
                   categorias: dict[str, str] | None = None, fecha_fin: str = "2026-10-21",
                   correcciones: list[dict] | None = None) -> Path:
    """Corrida mínima con avisos_esperados.csv y comercios.csv, con la misma forma que la del generador."""
    escribir_corrida(destino, filas, series or [], fecha_fin=fecha_fin, semilla=2026)
    mapa = categorias or {MERCADO: "alimentos_mercado", RESTAURANTE: "restaurantes_domicilios"}
    _escribir(destino / "verdad" / "comercios.csv", ENCABEZADO_COMERCIOS, [comercio(k, c) for k, c in mapa.items()])
    _escribir(destino / "verdad" / "avisos_esperados.csv", ENCABEZADO_ETIQUETAS, etiquetas)
    _escribir(destino / "entrada" / "correcciones.csv", ENCABEZADO_CORRECCIONES, correcciones or [])
    return destino


def serie_de(usuario: str, llave: str, motivo: str = "regular", numero: int = 1, recurrente: bool = True) -> dict:
    return serie(usuario, llave, recurrente, motivo, numero)


def fecha_de(texto: str) -> date:
    return date.fromisoformat(texto)
