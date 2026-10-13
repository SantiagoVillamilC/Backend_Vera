"""Población de comercios envenenados por colusión: un grupo de T usuarios etiqueta mal y luego corrigen 3 honestos.

La colusión es un supuesto sintético de diseño, sin fuente de tasa real. Para cada tamaño T de
--colusion-tamanos se crean --colusion-comercios comercios nuevos. En cada uno, T usuarios nuevos pagan y
lo corrigen con el MISMO nombre y la MISMA categoría equivocada (tipo_ruido colusion), en fechas tempranas
y cercanas entre sí. Semanas después, 3 usuarios honestos distintos pagan y lo corrigen con la categoría
correcta, así que la verdad final es la categoría real. Un K menor o igual a T se envenena y un K mayor no.

--colusion-tardia-comercios añade un escenario POSTERIOR a la consolidación: por cada tamaño crea M comercios
que atacan el nombre y M que atacan la categoría. En cada uno, 3 usuarios honestos consolidan primero la
verdad y luego T usuarios colusores corrigen, en la dimensión atacada. Sin este escenario una regla de deriva
solo podría verse bien por construcción, porque los honestos siempre corregirían después de los colusores.

Usa su propio random.Random derivado de la semilla, separado del flujo principal, para no alterar el resto.
"""

import random
from datetime import date, timedelta

from .catalogo import (NOMBRES_PROPIOS, PREFIJOS_MARCA, SUFIJOS_MARCA, TIPOS_NEGOCIO, ZONAS, crear_comercio,
                       crear_empresa)
from .constantes import CATEGORIAS_COMERCIO, CATEGORIAS_ERRONEAS
from .contexto import Contexto
from .guion import Corrige, Evento, ejecutar_guion
from .modelos import Aporte, Destino, Empresa, combinar
from .nombres import canonizar
from .ruido import variante_formato

HONESTOS = 3
INICIO_COLUSION = date(2026, 4, 5)
DIAS_VENTANA_INICIO = 72
SEPARACION_COLUSORES = (1, 5)
ESPERA_HONESTOS = (21, 45)
SEPARACION_HONESTOS = (5, 20)
HORAS_PAGO = (9, 18)
MONTO_PESOS = (5_000, 90_000)
DEMORA_HORAS = (1.0, 48.0)
RUIDOS_HONESTOS = ("ninguno", "mayusculas")


def azar_de_colusion(semilla: int) -> random.Random:
    return random.Random(f"{semilla}-colusion")


def _nombre_nuevo(azar: random.Random, categoria: str, revela: bool, razones: set[str],
                  nombres: set[str]) -> tuple[str, str]:
    """(base, nombre con zona), únicos frente a las razones sociales y a los nombres canónicos ya usados."""
    while True:
        if revela:
            base = f"{azar.choice(TIPOS_NEGOCIO[categoria])} {azar.choice(NOMBRES_PROPIOS)}"
        else:
            base = (azar.choice(PREFIJOS_MARCA) + azar.choice(SUFIJOS_MARCA)).capitalize()
        completo = f"{base} {azar.choice(ZONAS)}".upper()
        if f"{base} S.A.S." not in razones and canonizar(completo) not in nombres:
            razones.add(f"{base} S.A.S.")
            nombres.add(canonizar(completo))
            return base, completo


def _comercio(ctx: Contexto, azar: random.Random, indice: int, razones: set[str],
              nombres: set[str]) -> tuple[Empresa, Destino]:
    """Comercio nuevo en una categoría con MCC. La plantilla del nombre alterna entre revelar o no la naturaleza."""
    categoria = CATEGORIAS_COMERCIO[indice % len(CATEGORIAS_COMERCIO)]
    revela = indice % 2 == 0
    base, completo = _nombre_nuevo(azar, categoria, revela, razones, nombres)
    empresa = crear_empresa(ctx.semilla, f"{base} S.A.S.", True)
    return empresa, crear_comercio(ctx.llaves, empresa, completo, categoria, revela)


def _fechas(azar: random.Random, cantidad: int, primera: date, separacion: tuple[int, int]) -> list[date]:
    fechas = [primera]
    for _ in range(cantidad - 1):
        fechas.append(fechas[-1] + timedelta(days=azar.randint(*separacion)))
    return fechas


def _evento(azar: random.Random, clave: str, destino: Destino, fecha: date, nombre: str, categoria: str,
            ruido: str) -> Evento:
    corrige = Corrige(nombre, categoria, ruido, round(azar.uniform(*DEMORA_HORAS), 2))
    return Evento(clave, destino, fecha, azar.randint(*HORAS_PAGO), azar.randrange(*MONTO_PESOS, 100), corrige)


def _eventos(azar: random.Random, destino: Destino, tamano: int, indice: int) -> tuple[Evento, ...]:
    """T colusores tempranos y cercanos, y tres honestos semanas después."""
    prefijo = f"colusion-{tamano}-{indice}"
    equivocada = azar.choice([c for c in CATEGORIAS_ERRONEAS if c != destino.categoria])
    primera = INICIO_COLUSION + timedelta(days=azar.randrange(DIAS_VENTANA_INICIO))
    fechas_c = _fechas(azar, tamano, primera, SEPARACION_COLUSORES)
    colusores = tuple(_evento(azar, f"{prefijo}-c{j}", destino, f, destino.nombre_base, equivocada, "colusion")
                      for j, f in enumerate(fechas_c))
    inicio_h = fechas_c[-1] + timedelta(days=azar.randint(*ESPERA_HONESTOS))
    fechas_h = _fechas(azar, HONESTOS, inicio_h, SEPARACION_HONESTOS)
    honestos = tuple(_honesto(azar, f"{prefijo}-h{j}", destino, f) for j, f in enumerate(fechas_h))
    return colusores + honestos


def _honesto(azar: random.Random, clave: str, destino: Destino, fecha: date) -> Evento:
    """Corrección correcta, con el nombre tal cual o con ruido de formato que canoniza al mismo nombre."""
    ruido = azar.choice(RUIDOS_HONESTOS)
    nombre = variante_formato(destino.nombre_base, azar) if ruido == "mayusculas" else destino.nombre_base
    return _evento(azar, clave, destino, fecha, nombre, destino.categoria, ruido)


def _eventos_tardios(azar: random.Random, destino: Destino, tamano: int, indice: int, dimension: str,
                     nombre_equivocado: str | None) -> tuple[Evento, ...]:
    """Escenario posterior: 3 honestos consolidan primero y T colusores corrigen después.

    En `nombre` los colusores escriben un nombre equivocado y la categoría correcta. En `categoria`
    escriben el nombre correcto y una categoría equivocada, como la población temprana.
    """
    prefijo = f"colusion-tardia-{dimension}-{tamano}-{indice}"
    primera = INICIO_COLUSION + timedelta(days=azar.randrange(DIAS_VENTANA_INICIO))
    fechas_h = _fechas(azar, HONESTOS, primera, SEPARACION_HONESTOS)
    honestos = tuple(_honesto(azar, f"{prefijo}-h{j}", destino, f) for j, f in enumerate(fechas_h))
    inicio_c = fechas_h[-1] + timedelta(days=azar.randint(*ESPERA_HONESTOS))
    fechas_c = _fechas(azar, tamano, inicio_c, SEPARACION_COLUSORES)
    if dimension == "nombre":
        colusores = tuple(_evento(azar, f"{prefijo}-c{j}", destino, f, nombre_equivocado, destino.categoria,
                                  "colusion") for j, f in enumerate(fechas_c))
    else:
        equivocada = azar.choice([c for c in CATEGORIAS_ERRONEAS if c != destino.categoria])
        colusores = tuple(_evento(azar, f"{prefijo}-c{j}", destino, f, destino.nombre_base, equivocada, "colusion")
                          for j, f in enumerate(fechas_c))
    return honestos + colusores


def _aporte(guion, destino: Destino, empresa: Empresa) -> Aporte:
    return Aporte(usuarios=guion.usuarios, movimientos=guion.movimientos, correcciones=guion.correcciones,
                  destinos=(destino,), empresas=(empresa,), guionadas=(destino.llave_destino,))


def _comercio_tardio(ctx: Contexto, azar: random.Random, tamano: int, indice: int, dimension: str,
                     razones: set[str], nombres: set[str]) -> Aporte:
    """Un comercio del escenario posterior. En `nombre` genera además el nombre equivocado que imponen."""
    empresa, destino = _comercio(ctx, azar, indice, razones, nombres)
    equivocado = None
    if dimension == "nombre":
        _, equivocado = _nombre_nuevo(azar, destino.categoria, False, razones, nombres)
    return _aporte(ejecutar_guion(ctx, _eventos_tardios(azar, destino, tamano, indice, dimension, equivocado)),
                   destino, empresa)


def construir(ctx: Contexto, empresas: tuple[Empresa, ...]) -> Aporte:
    """Aporte con los comercios envenenados, sus usuarios, pagos y correcciones. Vacío si los dos conteos son 0."""
    p = ctx.parametros
    if p.colusion_comercios == 0 and p.colusion_tardia_comercios == 0:
        return Aporte()
    azar = azar_de_colusion(p.semilla)
    razones = {e.razon_social for e in empresas} | {e.razon_social for e in ctx.reservados.empresas}
    nombres = {d.nombre_canonico for d in ctx.catalogo + ctx.reservados.destinos()}
    aportes, indice = [], 0
    for tamano in p.colusion_tamanos:
        for _ in range(p.colusion_comercios):
            empresa, destino = _comercio(ctx, azar, indice, razones, nombres)
            guion = ejecutar_guion(ctx, _eventos(azar, destino, tamano, indice))
            aportes.append(_aporte(guion, destino, empresa))
            indice += 1
    indice_tardio = 0
    for tamano in p.colusion_tamanos:
        for _ in range(p.colusion_tardia_comercios):
            for dimension in ("nombre", "categoria"):
                aportes.append(_comercio_tardio(ctx, azar, tamano, indice_tardio, dimension, razones, nombres))
                indice_tardio += 1
    return combinar(aportes)
