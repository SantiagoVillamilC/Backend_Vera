"""Guiones de eventos: pagos con fecha exacta y, opcionalmente, la corrección que dispara cada uno."""

from dataclasses import dataclass
from datetime import date, timedelta

from .constantes import INICIO_PERIODO
from .contexto import Contexto, crear_usuario
from .modelos import Aporte, Correccion, Destino, Movimiento, NombreVigente, Usuario
from .movimientos import deposito, envio_persona, pago_comercio, pesos
from .tiempo import fecha_co, instante_co

SALDO_INICIAL_PESOS = 20_000_000


@dataclass(frozen=True)
class Corrige:
    """Corrección escrita por el usuario tras el pago. El nombre es el crudo, tal como lo tecleó."""

    nombre: str
    categoria: str
    tipo_ruido: str = "ninguno"
    demora_horas: float = 1.0


@dataclass(frozen=True)
class Evento:
    clave_usuario: str
    destino: Destino
    fecha: date
    hora: int
    monto_pesos: int
    corrige: Corrige | None = None
    marca: str = ""
    presentacion: str = ""
    descripcion: str = "Pago"


def categoria_vigente(destino: Destino, fecha: date, historiales: tuple[NombreVigente, ...]) -> str:
    """Categoría real del destino en una fecha local, según el historial (o la actual si no hay)."""
    for h in historiales:
        if h.llave_destino == destino.llave_destino and h.vigente_desde <= fecha and (
                h.vigente_hasta is None or fecha <= h.vigente_hasta):
            return h.categoria
    return destino.categoria


def _fecha_saldo(eventos: list[Evento]) -> date:
    return min(INICIO_PERIODO, min(e.fecha for e in eventos) - timedelta(days=5))


def _crear_usuarios(ctx: Contexto, eventos: tuple[Evento, ...]) -> dict[str, Usuario]:
    usuarios: dict[str, Usuario] = {}
    for e in eventos:
        if e.clave_usuario not in usuarios:
            usuarios[e.clave_usuario] = crear_usuario(ctx, e.clave_usuario)
    return usuarios


def _movimientos_del_evento(ctx: Contexto, evento: Evento, usuario: Usuario,
                            historiales: tuple[NombreVigente, ...]) -> tuple[Movimiento, ...]:
    instante = instante_co(evento.fecha, evento.hora)
    monto = pesos(evento.monto_pesos)
    if not evento.destino.es_comercio:
        return envio_persona(usuario.usuario_id, instante, monto, evento.destino, ctx.grupos, evento.descripcion)
    categoria = categoria_vigente(evento.destino, evento.fecha, historiales)
    return pago_comercio(usuario.usuario_id, instante, monto, evento.destino, ctx.grupos,
                         evento.descripcion, categoria, evento.marca, evento.presentacion)


def _correccion_del_evento(evento: Evento, usuario: Usuario,
                           historiales: tuple[NombreVigente, ...]) -> Correccion:
    c = evento.corrige
    instante = instante_co(evento.fecha, evento.hora) + timedelta(hours=c.demora_horas)
    real = categoria_vigente(evento.destino, fecha_co(instante), historiales)
    return Correccion(usuario.usuario_id, evento.destino.llave_destino, c.nombre, c.categoria,
                      instante, c.categoria == real, c.tipo_ruido)


def ejecutar_guion(ctx: Contexto, eventos: tuple[Evento, ...],
                   historiales: tuple[NombreVigente, ...] = ()) -> Aporte:
    """Crea los usuarios del guion (con un depósito inicial), sus pagos y sus correcciones."""
    usuarios = _crear_usuarios(ctx, eventos)
    movimientos: list[Movimiento] = []
    correcciones: list[Correccion] = []
    for clave, usuario in usuarios.items():
        propios = [e for e in eventos if e.clave_usuario == clave]
        instante = instante_co(_fecha_saldo(propios), 8)
        movimientos.append(deposito(usuario.usuario_id, instante, pesos(SALDO_INICIAL_PESOS),
                                    "otros_ingresos", "Saldo inicial"))
    for evento in eventos:
        usuario = usuarios[evento.clave_usuario]
        movimientos.extend(_movimientos_del_evento(ctx, evento, usuario, historiales))
        if evento.corrige is not None:
            correcciones.append(_correccion_del_evento(evento, usuario, historiales))
    return Aporte(usuarios=tuple(usuarios.values()), movimientos=tuple(movimientos),
                  correcciones=tuple(correcciones))


def usuario_id_de(aporte: Aporte, clave: str) -> str:
    return next(u.usuario_id for u in aporte.usuarios if u.clave == clave)
