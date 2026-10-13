"""Experimento A: convergencia por multitud. Replica cómo un nombre y una categoría pasan de votos privados a globales.

Es el detector y solo ve `entrada/`: recibe filas ya cargadas de correcciones.csv y movimientos.csv, y nunca
importa el cargador de la carpeta de comparación (lo comprueba una prueba estática). Función pura: el reloj se
inyecta en `estado_en(ahora)`, que solo mira correcciones con `ocurrido_en <= ahora`.

Reglas (todas son propuesta de diseño sin fuente, ver SUPUESTOS.md puntos 43 a 56):

- Las correcciones se reproducen en orden cronológico por (ocurrido_en, correccion_id). Así barajar las filas
  que comparten instante no cambia el resultado.
- La clave de nombre es el nombre del usuario canonizado. Esto deshace de forma trivial el ruido de
  mayúsculas, tildes y formato. Si hay un agrupador (opcional, apagado por defecto), la clave pasa a ser el
  representante del grupo por similitud (ver `convergencia_agrupacion.py`).
- Un usuario tiene un voto vigente por (llave, dimensión). Si corrige de nuevo, su voto anterior se reemplaza.
- Un valor se promueve a global al reunir K usuarios distintos. Con la regla de relevo `estricta` el candidato debe
  superar estrictamente al global actual y reunir al menos K, así que el empate conserva al incumbente. Con la regla
  de deriva `reciente` un empate se decide por recencia: releva el candidato cuyo voto que completa K es posterior a
  la promoción del incumbente. El global nunca se retira por sí solo.
- En la vista del propio usuario gana su voto privado, luego el global y por último el crudo.
"""

from collections import Counter
from collections.abc import Iterable, Mapping
from dataclasses import dataclass, replace
from datetime import datetime

from .convergencia_agrupacion import Agrupador
from .convergencia_config import Politica
from .convergencia_representante import Etiquetador
from .pagos import normalizar_nombre, parsear_instante

DIMENSIONES = ("nombre", "categoria")
SIN_CATEGORIA = "sin_categoria"
ORIGEN_PRIVADO, ORIGEN_GLOBAL, ORIGEN_CRUDO = "privado", "global", "crudo"


@dataclass(frozen=True)
class Correccion:
    correccion_id: str
    usuario_id: str
    llave: str
    nombre: str
    categoria: str
    ocurrido_en: datetime

    def valor(self, dimension: str) -> str:
        return self.nombre if dimension == "nombre" else self.categoria


@dataclass(frozen=True)
class Cambio:
    """Una promoción o un relevo del valor global de una llave en una dimensión."""

    instante: datetime
    llave: str
    dimension: str
    antes: str | None
    despues: str
    correccion_id: str


@dataclass(frozen=True)
class EstadoDimension:
    votos: Mapping[str, str]
    global_: str | None
    promocionado_en: datetime | None = None


@dataclass(frozen=True)
class EstadoLlave:
    nombre: EstadoDimension
    categoria: EstadoDimension

    def de(self, dimension: str) -> EstadoDimension:
        return self.nombre if dimension == "nombre" else self.categoria


VACIA = EstadoDimension({}, None, None)


@dataclass(frozen=True)
class Estado:
    llaves: Mapping[str, EstadoLlave]

    def global_de(self, llave: str, dimension: str) -> str | None:
        estado = self.llaves.get(llave)
        return estado.de(dimension).global_ if estado else None

    def voto_de(self, llave: str, usuario: str, dimension: str) -> str | None:
        estado = self.llaves.get(llave)
        return estado.de(dimension).votos.get(usuario) if estado else None


@dataclass(frozen=True)
class Vista:
    nombre: str
    categoria: str
    origen: str


def leer_correcciones(filas: Iterable[Mapping[str, str]]) -> tuple[Correccion, ...]:
    """Correcciones tipadas y ordenadas por (ocurrido_en, correccion_id). No modifica la entrada."""
    correcciones = [
        Correccion(f["correccion_id"], f["usuario_id"], f["llave_destino"], normalizar_nombre(f["nombre_puesto"]),
                   f["categoria_puesta"].strip(), parsear_instante(f["ocurrido_en"]))
        for f in filas]
    return tuple(sorted(correcciones, key=lambda c: (c.ocurrido_en, c.correccion_id)))


def tipos_de_llave(movimientos: Iterable[Mapping[str, str]]) -> dict[str, str]:
    """Tipo de llave observable en movimientos.csv, indexado por llave. Se queda con el primero que aparece."""
    tipos: dict[str, str] = {}
    for m in movimientos:
        llave, tipo = (m.get("llave_destino") or ""), (m.get("tipo_llave") or "")
        if llave and tipo and llave not in tipos:
            tipos[llave] = tipo
    return tipos


def _global_tras_el_voto(votos: Mapping[str, str], actual: str | None, promocion: datetime | None, k: int,
                         elegible: bool, deriva: bool, instante: datetime) -> tuple[str | None, datetime | None]:
    """(global, instante de su promoción) después de un voto.

    Con la regla `estricta` el candidato supera estrictamente al actual. Con `reciente` un empate lo decide la
    recencia: releva el candidato cuyo voto que completa K es posterior a la promoción del incumbente.
    """
    if not elegible:
        return actual, promocion
    conteo = Counter(votos.values())
    apoyo_actual = conteo.get(actual, 0) if actual is not None else 0

    def releva(cantidad: int) -> bool:
        return cantidad > apoyo_actual or (
            deriva and cantidad == apoyo_actual and promocion is not None and instante > promocion)

    candidatos = [v for v, c in conteo.items() if v != actual and c >= k and releva(c)]
    if not candidatos:
        return actual, promocion
    return min(candidatos, key=lambda v: (-conteo[v], v)), instante


def _votar(dim: EstadoDimension, usuario: str, valor: str, k: int, elegible: bool, deriva: bool,
           instante: datetime) -> EstadoDimension:
    votos = {**dim.votos, usuario: valor}
    global_, promocion = _global_tras_el_voto(votos, dim.global_, dim.promocionado_en, k, elegible, deriva, instante)
    return EstadoDimension(votos, global_, promocion)


def _aplicar(estado: EstadoLlave | None, c: Correccion, k: int, elegible: bool, deriva: bool
             ) -> tuple[EstadoLlave, list[Cambio]]:
    previo = estado or EstadoLlave(VACIA, VACIA)
    nuevas = {d: _votar(previo.de(d), c.usuario_id, c.valor(d), k, elegible, deriva, c.ocurrido_en)
              for d in DIMENSIONES}
    cambios = [Cambio(c.ocurrido_en, c.llave, d, previo.de(d).global_, nuevas[d].global_, c.correccion_id)
               for d in DIMENSIONES if nuevas[d].global_ != previo.de(d).global_]
    return EstadoLlave(nuevas["nombre"], nuevas["categoria"]), cambios


def _cambios_visibles(nuevos: list[Cambio], estado: EstadoLlave, c: Correccion, et: Etiquetador,
                      visible: dict[str, str | None]) -> list[Cambio]:
    """Con `mas_votado` el cambio de nombre es el de la etiqueta visible del global, no el del ancla.

    Un cambio de etiqueta con el mismo grupo global (el representante pasa a otro nombre) también cuenta.
    """
    resultado = [x for x in nuevos if x.dimension != "nombre"]
    ancla = estado.nombre.global_
    if ancla is not None:
        ahora_visible = et.etiqueta(c.llave, ancla)
        antes = visible.get(c.llave)
        if ahora_visible != antes:
            visible[c.llave] = ahora_visible
            resultado.insert(0, Cambio(c.ocurrido_en, c.llave, "nombre", antes, ahora_visible, c.correccion_id))
    return resultado


def _con_etiquetas(estado: EstadoLlave, llave: str, et: Etiquetador) -> EstadoLlave:
    """Estado con los votos y el global de nombre mostrados con la etiqueta actual de su grupo."""
    nombre = estado.nombre
    votos = {u: et.etiqueta(llave, ancla) for u, ancla in nombre.votos.items()}
    global_ = None if nombre.global_ is None else et.etiqueta(llave, nombre.global_)
    return EstadoLlave(EstadoDimension(votos, global_, nombre.promocionado_en), estado.categoria)


@dataclass(frozen=True)
class Replicador:
    """Reproduce las correcciones con un K y una política de elegibilidad. El reloj lo pone quien consulta."""

    correcciones: tuple[Correccion, ...]
    tipos: Mapping[str, str]
    k: int
    politica: Politica
    agrupador: Agrupador | None = None
    deriva: bool = False

    @classmethod
    def desde_filas(cls, filas_correcciones: Iterable[Mapping[str, str]], filas_movimientos: Iterable[Mapping[str, str]],
                    k: int, politica: Politica, agrupador: Agrupador | None = None, deriva: bool = False) -> "Replicador":
        return cls(leer_correcciones(filas_correcciones), tipos_de_llave(filas_movimientos), k, politica, agrupador,
                   deriva)

    def _con_ancla(self) -> tuple[Correccion, ...]:
        """Las correcciones con el nombre reemplazado por el ancla de su grupo (online, por llave)."""
        if self.agrupador is None:
            return self.correcciones
        anclas: dict[str, tuple[str, ...]] = {}
        efectivas = []
        for c in self.correcciones:
            ancla, nuevos = self.agrupador.asignar(anclas.get(c.llave, ()), c.nombre)
            anclas[c.llave] = anclas.get(c.llave, ()) + nuevos
            efectivas.append(replace(c, nombre=ancla))
        return tuple(efectivas)

    def _mas_votado(self) -> bool:
        return self.agrupador is not None and self.agrupador.representante == "mas_votado"

    def correcciones_efectivas(self) -> tuple[Correccion, ...]:
        """Las correcciones con el nombre reemplazado por el representante de su grupo (online, por llave).

        Sin agrupador son las mismas correcciones. El orden cronológico se mantiene. Con `primero` el representante
        es el ancla. Con `mas_votado` es la etiqueta final del grupo, con todas las correcciones ya contadas.
        """
        anclas = self._con_ancla()
        if not self._mas_votado():
            return anclas
        et = Etiquetador()
        for i, (original, c) in enumerate(zip(self.correcciones, anclas)):
            et.votar(i, c.llave, c.usuario_id, c.nombre, original.nombre)
        return tuple(replace(c, nombre=et.etiqueta(c.llave, c.nombre)) for c in anclas)

    def _reproducir(self, ahora: datetime | None) -> tuple[Estado, tuple[Cambio, ...]]:
        llaves: dict[str, EstadoLlave] = {}
        cambios: list[Cambio] = []
        mas_votado = self._mas_votado()
        et, visible = Etiquetador(), {}
        for i, (original, c) in enumerate(zip(self.correcciones, self._con_ancla())):
            if ahora is not None and c.ocurrido_en > ahora:
                break
            elegible = self.politica.admite(self.tipos.get(c.llave, ""))
            llaves[c.llave], nuevos = _aplicar(llaves.get(c.llave), c, self.k, elegible, self.deriva)
            if mas_votado:
                et.votar(i, c.llave, c.usuario_id, c.nombre, original.nombre)
                nuevos = _cambios_visibles(nuevos, llaves[c.llave], c, et, visible)
            cambios += nuevos
        if mas_votado:
            llaves = {llave: _con_etiquetas(e, llave, et) for llave, e in llaves.items()}
        return Estado(llaves), tuple(cambios)

    def cambios(self) -> tuple[Cambio, ...]:
        """Historia completa de promociones y relevos, en orden cronológico."""
        return self._reproducir(None)[1]

    def estado_en(self, ahora: datetime) -> Estado:
        """Estado con las correcciones de `ocurrido_en <= ahora`. Rechaza un instante sin zona horaria."""
        if ahora.tzinfo is None:
            raise ValueError("`ahora` debe llevar zona horaria.")
        return self._reproducir(ahora)[0]

    def vista_en(self, ahora: datetime, usuario: str, llave: str, nombre_crudo: str) -> Vista:
        """Lo que ve `usuario` de `llave` en `ahora`: su voto privado, si no el global, si no el crudo."""
        estado = self.estado_en(ahora)
        privado_nombre = estado.voto_de(llave, usuario, "nombre")
        if privado_nombre is not None:
            return Vista(privado_nombre, estado.voto_de(llave, usuario, "categoria") or SIN_CATEGORIA, ORIGEN_PRIVADO)
        global_nombre, global_categoria = estado.global_de(llave, "nombre"), estado.global_de(llave, "categoria")
        if global_nombre is None and global_categoria is None:
            return Vista(normalizar_nombre(nombre_crudo), SIN_CATEGORIA, ORIGEN_CRUDO)
        return Vista(global_nombre if global_nombre is not None else normalizar_nombre(nombre_crudo),
                     global_categoria if global_categoria is not None else SIN_CATEGORIA, ORIGEN_GLOBAL)
