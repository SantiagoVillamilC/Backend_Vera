"""Etiquetas de verdad de recurrencia, definidas por la INTENCIÓN con la que se genera cada serie.

es_recurrente nunca sale de comparar contra un umbral numérico: ese umbral es un parámetro del
detector que se va a calibrar, y la verdad no puede depender de él. El campo motivo describe cómo
se generó la serie: regular, dia_variable, monto_variable_servicio_publico, compra_habitual o
pago_suelto. Solo se etiqueta la verdad, no hay detector de recurrencia en este paquete.
"""

from datetime import date, timedelta

from .contexto import Contexto
from .guion import Evento, ejecutar_guion, usuario_id_de
from .modelos import Aporte, Serie

TELEFONIA_QR = "qr_en_b"
OFFSETS_DIA_20 = (0, -2, 1, 0, 2, -1)
MONTOS_LUZ_VARIABLE = (112_000, 138_000, 118_000, 142_000, 109_000, 131_000)
MONTOS_AGUA_ESTABLE = (118_000, 121_000, 124_500, 119_500, 126_000, 122_000)
INTERVALOS_COMPRA = (7, 5, 8, 9, 6, 5, 7, 8, 6, 9, 5, 7, 6, 8, 9, 5, 6, 7, 8, 5)
MONTOS_COMPRA = (
    64_300, 112_900, 38_500, 87_200, 141_000, 52_700, 96_400, 29_800, 73_100, 125_600,
    45_900, 81_700, 58_200, 104_300, 33_400, 92_800, 67_500, 118_700, 49_100, 77_400)
CANTIDAD_COMPRAS = 20


def _telefonia_suelta(d) -> tuple[Evento, ...]:
    pagos = ((1, 10, 20_000), (2, 20, 25_000), (3, 20, 50_000))
    return tuple(Evento("tel-neg", d, date(2026, m, dia), 11, monto, presentacion=TELEFONIA_QR,
                        descripcion="Plan móvil") for m, dia, monto in pagos)


def _telefonia_dia_variable(d) -> tuple[Evento, ...]:
    return tuple(Evento("tel-pos", d, date(2026, mes, 20 + off), 11, 59_900, presentacion=TELEFONIA_QR,
                        descripcion="Plan móvil")
                 for mes, off in zip(range(4, 10), OFFSETS_DIA_20))


def _servicio_publico(d, clave: str, montos: tuple[int, ...], descripcion: str) -> tuple[Evento, ...]:
    return tuple(Evento(clave, d, date(2026, mes, 12), 10, monto, descripcion=descripcion)
                 for mes, monto in zip(range(4, 10), montos))


def _compras_habituales(d) -> tuple[Evento, ...]:
    """Supermercado cada 5 a 9 días con montos distintos: hábito de compra, no cobro recurrente."""
    eventos, dia = [], date(2026, 4, 3)
    for i in range(CANTIDAD_COMPRAS):
        eventos.append(Evento("compra-h", d, dia, 17, MONTOS_COMPRA[i],
                              descripcion="Mercado"))
        dia += timedelta(days=INTERVALOS_COMPRA[i])
    return tuple(eventos)


def _series(aporte: Aporte, r) -> tuple[Serie, ...]:
    def uid(clave: str) -> str:
        return usuario_id_de(aporte, clave)

    return (
        Serie("telefonia-suelta", uid("tel-neg"), r.telefonia.llave_destino, False, None, "pago_suelto",
              "Tres pagos a la misma llave de telefonía en días distintos: 20.000 el 10 de enero, 25.000 el "
              "20 de febrero y 50.000 el 20 de marzo. Se generaron como pagos sueltos, no como un plan"),
        Serie("telefonia-dia-variable", uid("tel-pos"), r.telefonia.llave_destino, True, 30, "dia_variable",
              "Plan de 59.900 que se paga alrededor del día 20 con corrimientos de más o menos 2 días"),
        Serie("luz-variable", uid("luz"), r.energia.llave_destino, True, 30, "monto_variable_servicio_publico",
              "Servicio público (luz) el día 12 de cada mes con monto que sube y baja hasta cerca de "
              "25 % entre meses consecutivos. Es recurrente por diseño aunque el monto cambie bastante"),
        Serie("agua-estable", uid("agua"), r.energia.llave_destino, True, 30, "monto_variable_servicio_publico",
              "Servicio público (agua) el día 12 de cada mes con monto casi igual. Recurrente por diseño"),
        Serie("mercado-habitual", uid("compra-h"), r.supermercado.llave_destino, False, None, "compra_habitual",
              "Compras en el mismo supermercado cada 5 a 9 días con montos aleatorios. Es un hábito de "
              "compra, no una serie recurrente"),
    )


def construir(ctx: Contexto) -> Aporte:
    r = ctx.reservados
    eventos = (_telefonia_suelta(r.telefonia) + _telefonia_dia_variable(r.telefonia)
               + _servicio_publico(r.energia, "luz", MONTOS_LUZ_VARIABLE, "Luz")
               + _servicio_publico(r.energia, "agua", MONTOS_AGUA_ESTABLE, "Agua")
               + _compras_habituales(r.supermercado))
    aporte = ejecutar_guion(ctx, eventos)
    return Aporte(usuarios=aporte.usuarios, movimientos=aporte.movimientos,
                  correcciones=aporte.correcciones, series=_series(aporte, r))
