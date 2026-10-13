"""Evaluación del experimento A (convergencia por multitud) contra la carpeta de comparación del generador.

Aquí sí se carga la verdad (comercios, historial de nombres, ruido de las correcciones y casos de vista).
Este módulo es el evaluador y nunca lo importa el replicador (`convergencia.py`).

Una corrida produce, para cada política y cada K: las medidas por ámbito (`todos`, `prueba`, `sin_colusion` y `global` para las
que no dependen del conjunto), el detalle de los casos de vista y las filas por comercio que luego usa el bootstrap.
"""

from collections import defaultdict
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from datetime import date, datetime, timedelta, timezone
from pathlib import Path

from .cargar import cargar_entrada, cargar_manifiesto, cargar_verdad, sha256_de
from .convergencia import Cambio, Replicador, leer_correcciones, tipos_de_llave
from .convergencia_agrupacion import Agrupador, crear_agrupador
from .convergencia_config import ConfigConvergencia, Politica
from .convergencia_medidas import AMBITOS_DE_FILAS, FilaComercio, en_ambito, medidas_de_comercios
from .evaluacion import ahora_de_corrida
from .pagos import fecha_local_colombia, parsear_instante

TABLAS = ("comercios", "nombres_historial", "correcciones_verdad", "casos_vista")
ARCHIVOS_HASHEADOS = ("entrada/correcciones.csv", "entrada/movimientos.csv", "manifiesto.json",
                      "verdad/casos_vista.csv", "verdad/comercios.csv", "verdad/correcciones_verdad.csv",
                      "verdad/nombres_historial.csv")
DESFASE_COLOMBIA = timedelta(hours=5)
SEGUNDOS_POR_DIA = 86400
TIPO_RUIDO_COLUSION = "colusion"


@dataclass(frozen=True)
class CorridaConvergencia:
    nombre: str
    tasa: str
    semilla: int
    ahora: datetime
    movimientos: tuple[dict, ...]
    correcciones: tuple[dict, ...]
    comercios: tuple[dict, ...]
    historial: Mapping[str, tuple[dict, ...]]
    ruido: Mapping[str, str]
    casos: tuple[dict, ...]
    hashes: Mapping[str, str]


@dataclass(frozen=True)
class Medida:
    politica: str
    k: int
    ambito: str
    medida: str
    valor: int | float | None


@dataclass(frozen=True)
class DetalleCaso:
    politica: str
    k: int
    caso_id: str
    movimiento_id: str
    ocurrido_en: str
    nombre_esperado: str
    categoria_esperada: str
    origen_esperado: str
    nombre_obtenido: str
    categoria_obtenida: str
    origen_obtenido: str

    @property
    def nombre_ok(self) -> bool:
        return self.nombre_esperado == self.nombre_obtenido

    @property
    def categoria_ok(self) -> bool:
        return self.categoria_esperada == self.categoria_obtenida

    @property
    def origen_ok(self) -> bool:
        return self.origen_esperado == self.origen_obtenido

    @property
    def aprobado(self) -> bool:
        return self.nombre_ok and self.categoria_ok and self.origen_ok


@dataclass(frozen=True)
class ResultadoCorrida:
    nombre: str
    tasa: str
    semilla: int
    ahora: datetime
    hashes: Mapping[str, str]
    medidas: tuple[Medida, ...]
    casos: tuple[DetalleCaso, ...]
    comercios: Mapping[tuple[str, int], tuple[FilaComercio, ...]]


def cargar_corrida_convergencia(ruta: Path | str) -> CorridaConvergencia:
    """Carga una corrida una sola vez: entrada, verdad, reloj de evaluación y hashes."""
    ruta = Path(ruta)
    manifiesto = cargar_manifiesto(ruta)
    entrada = cargar_entrada(ruta)
    verdad = cargar_verdad(ruta, TABLAS).tablas
    historial: dict[str, list[dict]] = defaultdict(list)
    for fila in verdad["nombres_historial"]:
        historial[fila["llave_destino"]].append(fila)
    ordenado = {llave: tuple(sorted(filas, key=lambda f: f["vigente_desde"])) for llave, filas in historial.items()}
    parametros = manifiesto["parametros"]
    return CorridaConvergencia(
        f"{ruta.parent.name}/{ruta.name}", str(parametros.get("tasa_correccion", "n/a")), int(parametros["semilla"]),
        ahora_de_corrida(manifiesto), entrada.movimientos, entrada.correcciones, verdad["comercios"], ordenado,
        {f["correccion_id"]: f["tipo_ruido"] for f in verdad["correcciones_verdad"]}, verdad["casos_vista"],
        sha256_de(ruta, ARCHIVOS_HASHEADOS))


def instante_de_fecha_local(texto: str) -> datetime:
    """Medianoche de una fecha de Colombia (UTC-5) como instante UTC."""
    return datetime.combine(date.fromisoformat(texto), datetime.min.time(), tzinfo=timezone.utc) + DESFASE_COLOMBIA


def valor_verdadero_en(historial: Sequence[Mapping[str, str]], instante: datetime, dimension: str) -> str | None:
    """Nombre o categoría vigente en un instante, según fechas locales de Colombia con extremos inclusivos."""
    dia = fecha_local_colombia(instante)
    campo = "nombre_canonico" if dimension == "nombre" else "categoria"
    for fila in historial:
        hasta = fila["vigente_hasta"]
        if date.fromisoformat(fila["vigente_desde"]) <= dia and (not hasta or dia <= date.fromisoformat(hasta)):
            return fila[campo]
    return None


def _dias(desde: datetime, hasta: datetime) -> float:
    return (hasta - desde).total_seconds() / SEGUNDOS_POR_DIA


def _dias_hasta_correcta(eventos: Sequence[Cambio], historial: Sequence[Mapping], dimension: str,
                         primera: datetime | None) -> float | None:
    for e in eventos:
        if primera is not None and e.despues == valor_verdadero_en(historial, e.instante, dimension):
            return _dias(primera, e.instante)
    return None


def _espurios(eventos: Sequence[Cambio], historial: Sequence[Mapping], dimension: str) -> tuple[int, int]:
    """(promociones a un valor distinto del vigente verdadero, de esas las que luego se revierten)."""
    erradas = [i for i, e in enumerate(eventos) if e.despues != valor_verdadero_en(historial, e.instante, dimension)]
    return len(erradas), sum(1 for i in erradas if i < len(eventos) - 1)


def _tiempo_equivocada(eventos: Sequence[Cambio], historial: Sequence[Mapping], corte: datetime
                       ) -> tuple[float, bool, bool]:
    """(días totales con la categoría global equivocada, si lo estuvo alguna vez, si sigue equivocada al corte).

    La verdad se mira en el instante en que cada valor se hizo global, sin segmentar por cambios de verdad
    dentro del intervalo. En los comercios de colusión la verdad de la categoría no cambia.
    """
    total, alguna, equivocada = 0.0, False, False
    for i, e in enumerate(eventos):
        equivocada = e.despues != valor_verdadero_en(historial, e.instante, "categoria")
        alguna = alguna or equivocada
        if equivocada:
            fin = eventos[i + 1].instante if i + 1 < len(eventos) else corte
            total += _dias(e.instante, fin)
    return total, alguna, equivocada


def _retraso(eventos: Sequence[Cambio], historial: Sequence[Mapping], dimension: str) -> tuple[bool, float | None]:
    """(hubo cambio real en la dimensión, días desde la vigencia del valor nuevo hasta que el global lo adopta)."""
    campo = "nombre_canonico" if dimension == "nombre" else "categoria"
    if len(historial) != 2 or historial[0][campo] == historial[1][campo]:
        return False, None
    inicio = instante_de_fecha_local(historial[1]["vigente_desde"])
    for e in eventos:
        if e.instante >= inicio and e.despues == historial[1][campo]:
            return True, _dias(inicio, e.instante)
    return True, None


def _fila_de_comercio(comercio: Mapping, eventos: Mapping[str, list[Cambio]], historial: Sequence[Mapping],
                      primera: datetime | None, colusion_t: int, corte: datetime, global_final: Mapping,
                      pagadores: int = 0, correctores: int = 0) -> FilaComercio:
    ok = {d: global_final[d] is not None and global_final[d] == valor_verdadero_en(historial, corte, d)
          for d in ("nombre", "categoria")}
    erradas = {d: _espurios(eventos[d], historial, d) for d in ("nombre", "categoria")}
    dias_mal, mal_alguna, mal_al_corte = _tiempo_equivocada(eventos["categoria"], historial, corte)
    cambio_n, retraso_n = _retraso(eventos["nombre"], historial, "nombre")
    cambio_c, retraso_c = _retraso(eventos["categoria"], historial, "categoria")
    return FilaComercio(
        comercio["llave_destino"], comercio["conjunto"] == "prueba", comercio["nombre_revela_naturaleza"] == "true",
        colusion_t, primera is not None, global_final["nombre"], global_final["categoria"], ok["nombre"],
        ok["categoria"], _dias_hasta_correcta(eventos["nombre"], historial, "nombre", primera),
        _dias_hasta_correcta(eventos["categoria"], historial, "categoria", primera), *erradas["nombre"],
        *erradas["categoria"], mal_alguna, dias_mal, mal_al_corte, cambio_n, retraso_n,
        cambio_c, retraso_c, pagadores, correctores)


def _usuarios_distintos(filas: Sequence[Mapping[str, str]]) -> dict[str, int]:
    """Usuarios distintos por llave de destino. Sirve para pagadores (movimientos) y correctores (correcciones)."""
    usuarios: dict[str, set[str]] = defaultdict(set)
    for f in filas:
        usuarios[f["llave_destino"]].add(f["usuario_id"])
    return {llave: len(u) for llave, u in usuarios.items()}


def _filas_de_comercios(corrida: CorridaConvergencia, r: Replicador, tipos_colusion: Mapping[str, int]
                        ) -> tuple[FilaComercio, ...]:
    estado = r.estado_en(corrida.ahora)
    eventos: dict[str, dict[str, list[Cambio]]] = defaultdict(lambda: {"nombre": [], "categoria": []})
    for c in r.cambios():
        eventos[c.llave][c.dimension].append(c)
    primeras: dict[str, datetime] = {}
    for c in r.correcciones:
        primeras.setdefault(c.llave, c.ocurrido_en)
    comercios = sorted((c for c in corrida.comercios if c["es_comercio"] == "true"), key=lambda c: c["llave_destino"])
    pagadores, correctores = _usuarios_distintos(corrida.movimientos), _usuarios_distintos(corrida.correcciones)
    filas = []
    for c in comercios:
        llave = c["llave_destino"]
        global_final = {d: estado.global_de(llave, d) for d in ("nombre", "categoria")}
        filas.append(_fila_de_comercio(c, eventos[llave], corrida.historial.get(llave, ()), primeras.get(llave),
                                       tipos_colusion.get(llave, 0), corrida.ahora, global_final,
                                       pagadores.get(llave, 0), correctores.get(llave, 0)))
    return tuple(filas)


def _tamanos_de_colusion(corrida: CorridaConvergencia) -> dict[str, int]:
    """T de cada llave: número de correcciones de tipo colusión que recibió."""
    tamanos: dict[str, int] = defaultdict(int)
    for c in corrida.correcciones:
        if corrida.ruido.get(c["correccion_id"]) == TIPO_RUIDO_COLUSION:
            tamanos[c["llave_destino"]] += 1
    return dict(tamanos)


def _medidas_de_personas(corrida: CorridaConvergencia, r: Replicador) -> dict:
    personas = {c["llave_destino"] for c in corrida.comercios if c["es_comercio"] != "true"}
    con_correcciones = personas & {c.llave for c in r.correcciones}
    promovidas = personas & {c.llave for c in r.cambios()}
    return {"personas_con_correcciones": len(con_correcciones), "personas_promovidas": len(promovidas)}


def _detalle_de_casos(corrida: CorridaConvergencia, r: Replicador, politica: str) -> tuple[DetalleCaso, ...]:
    movimientos = {m["movimiento_id"]: m for m in corrida.movimientos}
    detalle = []
    for caso in corrida.casos:
        mov = movimientos[caso["movimiento_id"]]
        v = r.vista_en(parsear_instante(caso["ocurrido_en"]), caso["usuario_id"], mov["llave_destino"],
                       mov["nombre_comercio_crudo"])
        detalle.append(DetalleCaso(politica, r.k, caso["caso_id"], caso["movimiento_id"], caso["ocurrido_en"],
                                   caso["nombre_esperado"], caso["categoria_esperada"], caso["origen_esperado"],
                                   v.nombre, v.categoria, v.origen))
    return tuple(detalle)


def _medidas_de_casos(detalle: Sequence[DetalleCaso]) -> dict:
    aprobados = sum(1 for d in detalle if d.aprobado)
    medidas: dict = {"casos_total": len(detalle), "casos_aprobados": aprobados,
                     "casos_aprobacion": aprobados / len(detalle) if detalle else None}
    for columna in ("nombre", "categoria", "origen"):
        medidas[f"casos_columna_{columna}_ok"] = sum(1 for d in detalle if getattr(d, f"{columna}_ok"))
    for caso_id in sorted({d.caso_id for d in detalle}):
        propios = [d for d in detalle if d.caso_id == caso_id]
        medidas[f"casos_{caso_id}_total"] = len(propios)
        medidas[f"casos_{caso_id}_aprobados"] = sum(1 for d in propios if d.aprobado)
    return medidas


def _medidas_de_una_combinacion(corrida: CorridaConvergencia, politica: Politica, k: int, tipos: Mapping[str, str],
                                correcciones: tuple, tamanos: Mapping[str, int],
                                agrupador: Agrupador | None = None, deriva: bool = False) -> tuple:
    r = Replicador(correcciones, tipos, k, politica, agrupador, deriva)
    filas = _filas_de_comercios(corrida, r, tamanos)
    detalle = _detalle_de_casos(corrida, r, politica.nombre)
    medidas = []
    for ambito in AMBITOS_DE_FILAS:
        subconjunto = tuple(f for f in filas if en_ambito(f, ambito))
        medidas += [Medida(politica.nombre, k, ambito, nombre, valor)
                    for nombre, valor in medidas_de_comercios(subconjunto).items()]
    globales = _medidas_de_personas(corrida, r) | _medidas_de_casos(detalle)
    medidas += [Medida(politica.nombre, k, "global", nombre, valor) for nombre, valor in globales.items()]
    return medidas, detalle, filas


def evaluar_corrida(corrida: CorridaConvergencia, config: ConfigConvergencia) -> ResultadoCorrida:
    """Evalúa todas las combinaciones de política y K sobre una corrida, con el reloj en el corte de la corrida."""
    correcciones = leer_correcciones(corrida.correcciones)
    tipos = tipos_de_llave(corrida.movimientos)
    tamanos = _tamanos_de_colusion(corrida)
    agrupador = crear_agrupador(config.metodo_similitud, config.umbral_similitud, {c.nombre for c in correcciones},
                                config.representante)
    deriva = config.regla_deriva == "reciente"
    medidas: list[Medida] = []
    casos: list[DetalleCaso] = []
    comercios: dict[tuple[str, int], tuple[FilaComercio, ...]] = {}
    for politica in config.politicas:
        for k in config.ks:
            m, detalle, filas = _medidas_de_una_combinacion(corrida, politica, k, tipos, correcciones, tamanos,
                                                            agrupador, deriva)
            medidas += m
            casos += detalle
            comercios[(politica.nombre, k)] = filas
    return ResultadoCorrida(corrida.nombre, corrida.tasa, corrida.semilla, corrida.ahora, corrida.hashes,
                            tuple(medidas), tuple(casos), comercios)
