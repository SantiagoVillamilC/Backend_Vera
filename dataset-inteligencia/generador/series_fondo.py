"""Series etiquetadas de la población de fondo, para calibrar y validar un detector de recurrencia.

es_recurrente sale de la INTENCIÓN con la que se genera cada serie, nunca de un umbral numérico.
Cada ciclo de 10 series trae 5 recurrentes por diseño (suscripción, suscripción con alza puntual,
telefonía con día variable, servicio público con monto variable hasta 30 %, arriendo con día variable)
y 5 no recurrentes (dos compras habituales, dos pagos sueltos y una casualidad que parece recurrente).
Los pagos de una serie son la única relación de ese usuario con esa llave: el fondo aleatorio no la toca.
"""

import math
import random
from collections.abc import Callable
from datetime import date, timedelta

from .contexto import Contexto
from .modelos import Destino, Movimiento, Serie
from .movimientos import envio_persona, pago_comercio, pesos
from .tiempo import dia_del_mes, instante_co, meses_hasta

PREFIJO = "Serie de fondo"
COBERTURA_DE_USUARIOS = 0.5
CICLO = ("suscripcion", "alza", "telefonia", "servicio", "arriendo",
         "compra", "compra", "suelto", "suelto", "casualidad")
MONTOS_SUSCRIPCION = (19_900, 24_900, 29_900, 39_900, 49_900, 59_900)
INICIO_VENTANA = date(2026, 4, 1)
CADENCIA_MENSUAL = 30

Constructor = Callable[[Contexto, random.Random, str, set, str, dict], tuple[tuple[Movimiento, ...], Serie]]


def _texto_pesos(valor: int) -> str:
    return f"{valor:,}".replace(",", ".")


def _instante(azar: random.Random, fecha: date):
    return instante_co(fecha, azar.randrange(7, 19), azar.randrange(60), azar.randrange(60))


def _elegir(azar: random.Random, opciones: tuple[Destino, ...], uid: str, ocupados: set) -> Destino:
    libres = [d for d in opciones if (uid, d.llave_destino) not in ocupados]
    destino = azar.choice(libres or list(opciones))
    ocupados.add((uid, destino.llave_destino))
    return destino


def _pagar(ctx: Contexto, azar: random.Random, uid: str, fecha: date, monto: int, destino: Destino,
           descripcion: str) -> tuple[Movimiento, ...]:
    instante = _instante(azar, fecha)
    if destino.es_comercio:
        return pago_comercio(uid, instante, pesos(monto), destino, ctx.grupos, descripcion)
    return envio_persona(uid, instante, pesos(monto), destino, ctx.grupos, descripcion)


def _fechas_mensuales(dia: int, fecha_fin: date, azar: random.Random, jitter: int,
                      visitas_previas: int) -> list[date]:
    """Un pago por mes. El mes de inicio rota con las series que ya visitaron la misma llave, así los
    usuarios de un comercio arrancan en meses distintos. Con día fijo hasta el 20 (el corte mínimo es el
    20 de octubre) puede arrancar en julio y aun así tiene 4 pagos. Con corrimientos, hasta junio."""
    ultimo_inicio = 7 if jitter == 0 and dia <= 20 else 6
    fechas = []
    for anio, mes in meses_hasta(fecha_fin, 4 + visitas_previas % (ultimo_inicio - 3)):
        corrido = dia + (azar.randint(-jitter, jitter) if jitter else 0)
        fecha = dia_del_mes(anio, mes, corrido)
        if fecha <= fecha_fin:
            fechas.append(fecha)
    return fechas


def _movimientos(ctx, azar, uid, destino, fechas, montos, descripcion) -> tuple[Movimiento, ...]:
    filas: list[Movimiento] = []
    for fecha, monto in zip(fechas, montos):
        filas.extend(_pagar(ctx, azar, uid, fecha, monto, destino, descripcion))
    return tuple(filas)


def _serie(clave: str, uid: str, destino: Destino, recurrente: bool, motivo: str, texto: str) -> Serie:
    return Serie(f"fondo-{clave}", uid, destino.llave_destino, recurrente,
                 CADENCIA_MENSUAL if recurrente else None, motivo, f"{PREFIJO} ({texto})")


def _suscripcion(ctx, azar, uid, ocupados, clave, visitas):
    destino = _elegir(azar, ctx.comercios_de("entretenimiento"), uid, ocupados)
    dia, monto = azar.randint(3, 20), azar.choice(MONTOS_SUSCRIPCION)
    fechas = _fechas_mensuales(dia, ctx.parametros.fecha_fin, azar, 0, visitas.get(destino.llave_destino, 0))
    filas = _movimientos(ctx, azar, uid, destino, fechas, [monto] * len(fechas), "Suscripción")
    texto = f"suscripción: cobro mensual de {_texto_pesos(monto)} el día {dia}. Recurrente por diseño"
    return filas, _serie(clave, uid, destino, True, "regular", texto)


def _suscripcion_con_alza(ctx, azar, uid, ocupados, clave, visitas):
    destino = _elegir(azar, ctx.comercios_de("entretenimiento"), uid, ocupados)
    dia, base = azar.randint(3, 20), azar.choice(MONTOS_SUSCRIPCION)
    fechas = _fechas_mensuales(dia, ctx.parametros.fecha_fin, azar, 0, visitas.get(destino.llave_destino, 0))
    desde = azar.randint(2, len(fechas) - 1)
    nuevo = round(base * azar.uniform(1.05, 1.12) / 100) * 100
    montos = [base if i < desde else nuevo for i in range(len(fechas))]
    filas = _movimientos(ctx, azar, uid, destino, fechas, montos, "Suscripción")
    texto = (f"suscripción con alza puntual: de {_texto_pesos(base)} a {_texto_pesos(nuevo)} desde el "
             f"cobro número {desde + 1}, día {dia}. Recurrente por diseño")
    return filas, _serie(clave, uid, destino, True, "regular", texto)


def _telefonia(ctx, azar, uid, ocupados, clave, visitas):
    destino = _elegir(azar, ctx.comercios_de("vivienda_servicios"), uid, ocupados)
    dia, jitter, monto = azar.randint(8, 22), azar.randint(2, 4), azar.randrange(30_000, 95_000, 100)
    fechas = _fechas_mensuales(dia, ctx.parametros.fecha_fin, azar, jitter, visitas.get(destino.llave_destino, 0))
    filas = _movimientos(ctx, azar, uid, destino, fechas, [monto] * len(fechas), "Plan móvil")
    texto = (f"telefonía: plan de {_texto_pesos(monto)} alrededor del día {dia} con corrimientos de más o "
             f"menos {jitter} días. Recurrente por diseño")
    return filas, _serie(clave, uid, destino, True, "dia_variable", texto)


def _montos_servicio(azar: random.Random, cantidad: int) -> list[int]:
    """Factura que sube y baja: cada mes cambia entre -22 % y +29 % respecto al anterior."""
    montos = [azar.randrange(50_000, 76_000, 100)]
    for _ in range(cantidad - 1):
        siguiente = round(montos[-1] * azar.uniform(0.78, 1.29) / 100) * 100
        montos.append(min(200_000, max(30_000, siguiente)))
    return montos


def _servicio_publico(ctx, azar, uid, ocupados, clave, visitas):
    destino = _elegir(azar, ctx.comercios_de("vivienda_servicios"), uid, ocupados)
    dia = azar.randint(8, 20)
    fechas = _fechas_mensuales(dia, ctx.parametros.fecha_fin, azar, 0, visitas.get(destino.llave_destino, 0))
    filas = _movimientos(ctx, azar, uid, destino, fechas, _montos_servicio(azar, len(fechas)), "Servicio público")
    texto = (f"servicio público: factura mensual el día {dia} con monto que cambia hasta cerca de 30 % entre "
             "meses. Recurrente por diseño aunque el monto cambie bastante")
    return filas, _serie(clave, uid, destino, True, "monto_variable_servicio_publico", texto)


def _arriendo(ctx, azar, uid, ocupados, clave, visitas):
    destino = _elegir(azar, ctx.personas, uid, ocupados)
    dia, jitter, monto = azar.randint(8, 22), azar.randint(2, 4), azar.randrange(400_000, 1_200_001, 10_000)
    fechas = _fechas_mensuales(dia, ctx.parametros.fecha_fin, azar, jitter, visitas.get(destino.llave_destino, 0))
    filas = _movimientos(ctx, azar, uid, destino, fechas, [monto] * len(fechas), "Arriendo")
    texto = (f"arriendo: pago de {_texto_pesos(monto)} a una persona alrededor del día {dia} con corrimientos "
             f"de más o menos {jitter} días. Recurrente por diseño")
    return filas, _serie(clave, uid, destino, True, "dia_variable", texto)


def _compra_habitual(ctx, azar, uid, ocupados, clave, visitas):
    destino = _elegir(azar, ctx.comercios_de("alimentos_mercado"), uid, ocupados)
    fecha, fechas = INICIO_VENTANA + timedelta(days=azar.randrange(2, 90)), []
    for _ in range(azar.randint(8, 12)):
        if fecha > ctx.parametros.fecha_fin:
            break
        fechas.append(fecha)
        fecha += timedelta(days=azar.randint(5, 9))
    montos = azar.sample(range(15_000, 95_100, 100), len(fechas))
    filas = _movimientos(ctx, azar, uid, destino, fechas, montos, "Mercado")
    texto = "compra habitual: compras en el mismo supermercado cada 5 a 9 días con montos distintos. No es una serie recurrente"
    return filas, _serie(clave, uid, destino, False, "compra_habitual", texto)


def _pago_suelto(ctx, azar, uid, ocupados, clave, visitas):
    destino = _elegir(azar, ctx.catalogo, uid, ocupados)
    fecha, fechas = INICIO_VENTANA + timedelta(days=azar.randrange(0, 100)), []
    for _ in range(3):
        fechas.append(fecha)
        fecha += timedelta(days=azar.randint(18, 70))
    fechas = [f for f in fechas if f <= ctx.parametros.fecha_fin]
    montos = azar.sample(range(15_000, 95_100, 100), len(fechas))
    filas = _movimientos(ctx, azar, uid, destino, fechas, montos, "Pago")
    texto = "pago suelto: pocos pagos al mismo comercio en días y montos sin patrón. No es una serie"
    return filas, _serie(clave, uid, destino, False, "pago_suelto", texto)


def _casualidad(ctx, azar, uid, ocupados, clave, visitas):
    destino = _elegir(azar, ctx.catalogo, uid, ocupados)
    primera = INICIO_VENTANA + timedelta(days=azar.randrange(0, 150))
    fechas = [primera, primera + timedelta(days=azar.randint(28, 31))]
    monto = azar.randrange(20_000, 90_000, 100)
    filas = _movimientos(ctx, azar, uid, destino, fechas, [monto, monto], "Pago")
    texto = ("pago suelto por casualidad: dos pagos del mismo monto con unos 30 días de diferencia que parecen "
             "una serie mensual pero fueron coincidencia. No es recurrente")
    return filas, _serie(clave, uid, destino, False, "pago_suelto", texto)


CONSTRUCTORES: dict[str, Constructor] = {
    "suscripcion": _suscripcion, "alza": _suscripcion_con_alza, "telefonia": _telefonia,
    "servicio": _servicio_publico, "arriendo": _arriendo, "compra": _compra_habitual,
    "suelto": _pago_suelto, "casualidad": _casualidad,
}


def construir_series(ctx: Contexto, azar: random.Random, usuarios: list, presupuesto: int,
                     ocupados: set, visitas: dict[str, int]) -> tuple[tuple[Movimiento, ...], tuple[Serie, ...]]:
    """Series hasta agotar `presupuesto` filas.

    `ocupados` recibe las parejas (usuario, llave) usadas y `visitas` cuenta una visita por serie a cada
    llave, para que los pagos aleatorios posteriores a ese comercio sigan rotando por la ventana.
    """
    if presupuesto < 4 or not usuarios:
        return (), ()
    cubiertos = usuarios[: max(1, math.ceil(COBERTURA_DE_USUARIOS * len(usuarios)))]
    filas: list[Movimiento] = []
    series: list[Serie] = []
    cola: list[str] = []
    while True:
        if not cola:
            cola = list(CICLO)
            azar.shuffle(cola)
        usuario = cubiertos[len(series) % len(cubiertos)]
        propios = set(ocupados)
        movimientos, serie = CONSTRUCTORES[cola.pop()](ctx, azar, usuario.usuario_id, propios, str(len(series)), visitas)
        if len(filas) + len(movimientos) > presupuesto:
            return tuple(filas), tuple(series)
        ocupados.update(propios)
        visitas[serie.llave_destino] = visitas.get(serie.llave_destino, 0) + 1
        filas.extend(movimientos)
        series.append(serie)
