"""Pruebas de la población de comercios envenenados por colusión (--colusion-comercios, --colusion-tamanos)."""

import hashlib
import io
import json
import tempfile
import unittest
from collections import defaultdict
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path

import generar
from generador.orquestador import generar_en_disco
from generador.parametros import Parametros

from .util import carpeta, correcciones_completas, leer

LINEA_BASE = json.loads((Path(__file__).parent / "baseline_n0.json").read_text(encoding="utf-8"))
DROGUERIA_A_MANO = "DROGUERIA-NUBE-AZUL"
N = 5


def _poblacion(**kw):
    return carpeta(colusion_comercios=N, colusion_tamanos=(2, 3), **kw)


def _grupos(base):
    """llave a correcciones de colusión de los comercios generados (excluye el caso escrito a mano)."""
    comercios = {c["llave_destino"]: c for c in leer(base, "verdad/comercios.csv")}
    grupos = defaultdict(list)
    for c in correcciones_completas(base):
        if c["tipo_ruido"] == "colusion" and comercios[c["llave_destino"]]["nombre_canonico"] != DROGUERIA_A_MANO:
            grupos[c["llave_destino"]].append(c)
    return grupos, comercios


def _honestas(base, llave):
    return [c for c in correcciones_completas(base) if c["llave_destino"] == llave and c["tipo_ruido"] != "colusion"]


def _orden(base):
    """Comercios con colusión separados por el orden: los colusores antes de los honestos, o después.

    Excluye el caso escrito a mano de la droguería. Devuelve (tempranos, tardios, comercios), cada uno
    un diccionario llave a las correcciones de colusión, y la tabla de comercios de la verdad.
    """
    comercios = {c["llave_destino"]: c for c in leer(base, "verdad/comercios.csv")}
    por_llave = defaultdict(list)
    for c in correcciones_completas(base):
        if comercios[c["llave_destino"]]["nombre_canonico"] != DROGUERIA_A_MANO:
            por_llave[c["llave_destino"]].append(c)
    tempranos, tardios = {}, {}
    for llave, filas in por_llave.items():
        colusoras = [c for c in filas if c["tipo_ruido"] == "colusion"]
        honestas = [c for c in filas if c["tipo_ruido"] != "colusion"]
        if not colusoras or not honestas:
            continue
        if max(c["ocurrido_en"] for c in colusoras) < min(c["ocurrido_en"] for c in honestas):
            tempranos[llave] = colusoras
        elif min(c["ocurrido_en"] for c in colusoras) > max(c["ocurrido_en"] for c in honestas):
            tardios[llave] = colusoras
    return tempranos, tardios, comercios


def _firma(filas):
    """Identidad comparable de un grupo de correcciones: usuario, nombre crudo, categoría e instante."""
    return sorted((c["usuario_id"], c["nombre_puesto"], c["categoria_puesta"], c["ocurrido_en"]) for c in filas)


def _manifiesto_sin_opciones_nuevas(texto: str) -> str:
    cuerpo = json.loads(texto)
    for clave in ("colusion_comercios", "colusion_tamanos", "colusion_tardia_comercios", "gasto_usuarios"):
        cuerpo["parametros"].pop(clave, None)
    return json.dumps(cuerpo, ensure_ascii=False, indent=2, sort_keys=True) + "\n"


class PruebasIdenticoConCero(unittest.TestCase):
    def test_con_cero_la_salida_es_byte_a_byte_la_de_antes(self):
        base = carpeta(semilla=2026, variante="A", colusion_comercios=0)
        for relativo, esperado in LINEA_BASE.items():
            if relativo == "manifiesto.json":
                continue
            real = hashlib.sha256((base / relativo).read_bytes()).hexdigest()
            self.assertEqual(real, esperado, relativo)

    def test_con_cero_el_manifiesto_solo_agrega_las_opciones_de_colusion(self):
        base = carpeta(semilla=2026, variante="A", colusion_comercios=0)
        texto = (base / "manifiesto.json").read_text(encoding="utf-8")
        restante = _manifiesto_sin_opciones_nuevas(texto)
        self.assertEqual(hashlib.sha256(restante.encode("utf-8")).hexdigest(), LINEA_BASE["manifiesto.json"])
        parametros = json.loads(texto)["parametros"]
        self.assertEqual(parametros["colusion_comercios"], 0)
        self.assertEqual(parametros["colusion_tamanos"], [2, 3])
        self.assertEqual(parametros["colusion_tardia_comercios"], 0)


class PruebasPoblacion(unittest.TestCase):
    def test_hay_exactamente_n_comercios_envenenados_por_tamano(self):
        grupos, _ = _grupos(_poblacion())
        tamanos = sorted(len(filas) for filas in grupos.values())
        self.assertEqual(tamanos, [2] * N + [3] * N)

    def test_cada_comercio_tiene_t_colusores_con_mismo_nombre_y_categoria_equivocada(self):
        base = _poblacion()
        grupos, comercios = _grupos(base)
        for llave, filas in grupos.items():
            self.assertEqual(len({c["usuario_id"] for c in filas}), len(filas))
            self.assertEqual(len({c["nombre_puesto_canonico"] for c in filas}), 1)
            self.assertEqual(filas[0]["nombre_puesto_canonico"], comercios[llave]["nombre_canonico"])
            self.assertEqual(len({c["categoria_puesta"] for c in filas}), 1)
            self.assertNotEqual(filas[0]["categoria_puesta"], comercios[llave]["categoria"])
            self.assertTrue(all(c["es_correcta"] == "false" for c in filas))

    def test_despues_hay_al_menos_tres_honestos_con_la_categoria_correcta(self):
        base = _poblacion()
        grupos, comercios = _grupos(base)
        for llave, filas in grupos.items():
            honestas = _honestas(base, llave)
            self.assertGreaterEqual(len(honestas), 3)
            self.assertGreaterEqual(len({c["usuario_id"] for c in honestas}), 3)
            ultimo_colusor = max(c["ocurrido_en"] for c in filas)
            for c in honestas:
                self.assertEqual(c["categoria_puesta"], comercios[llave]["categoria"])
                self.assertEqual(c["es_correcta"], "true")
                self.assertIn(c["tipo_ruido"], ("ninguno", "mayusculas", "tildes"))
                self.assertEqual(c["nombre_puesto_canonico"], comercios[llave]["nombre_canonico"])
                self.assertGreater(c["ocurrido_en"], ultimo_colusor)
            self.assertTrue({c["usuario_id"] for c in honestas}.isdisjoint({c["usuario_id"] for c in filas}))

    def test_los_comercios_nuevos_son_comercios_completos_de_la_verdad(self):
        base = _poblacion()
        grupos, comercios = _grupos(base)
        empresas = {e["empresa_id"]: e for e in leer(base, "verdad/empresas.csv")}
        historial = {h["llave_destino"] for h in leer(base, "verdad/nombres_historial.csv")}
        for llave in grupos:
            c = comercios[llave]
            self.assertEqual(c["es_comercio"], "true")
            self.assertNotEqual(c["mcc_categoria"], "")
            self.assertIn(c["conjunto"], ("entrenamiento", "prueba"))
            self.assertIn(c["nombre_revela_naturaleza"], ("true", "false"))
            self.assertEqual(empresas[c["empresa_id"]]["ficticia"], "true")
            self.assertIn(llave, historial)
        nombres = [comercios[l]["nombre_canonico"] for l in grupos]
        self.assertEqual(len(nombres), len(set(nombres)))
        self.assertEqual(len(empresas), len({e["razon_social"] for e in empresas.values()}))

    def test_los_colusores_son_tempranos_y_cercanos_entre_si(self):
        base = _poblacion()
        grupos, _ = _grupos(base)
        for llave, filas in grupos.items():
            instantes = sorted(c["ocurrido_en"] for c in filas)
            dias = (_dia(instantes[-1]) - _dia(instantes[0]))
            self.assertLessEqual(dias, 12, llave)
            honestas = _honestas(base, llave)
            self.assertGreaterEqual(_dia(min(c["ocurrido_en"] for c in honestas)) - _dia(instantes[-1]), 14)

    def test_ninguna_fila_posterior_a_fecha_fin_y_correcciones_despues_del_pago(self):
        base = _poblacion()
        movimientos = leer(base, "entrada/movimientos.csv")
        primero = {}
        for m in movimientos:
            if m["direccion"] == "salida" and m["llave_destino"]:
                primero.setdefault((m["usuario_id"], m["llave_destino"]), m["ocurrido_en"])
        for c in leer(base, "entrada/correcciones.csv"):
            self.assertGreater(c["ocurrido_en"], primero[(c["usuario_id"], c["llave_destino"])])
            self.assertLess(c["ocurrido_en"], "2026-10-22")
        self.assertLess(max(m["ocurrido_en"] for m in movimientos), "2026-10-22")

    def test_un_k_menor_o_igual_a_t_se_envenena_y_uno_mayor_no(self):
        base = _poblacion()
        grupos, comercios = _grupos(base)
        for llave, filas in grupos.items():
            todas = sorted(correcciones_completas(base), key=lambda c: c["ocurrido_en"])
            propias = [c for c in todas if c["llave_destino"] == llave]
            for k in (1, 2, 3):
                self.assertEqual(_gana_equivocada(propias, k, comercios[llave]["categoria"]), k <= len(filas),
                                 (llave, k))

    def test_el_conjunto_de_entrada_no_trae_verdad(self):
        base = _poblacion()
        from .util import encabezado
        self.assertEqual(encabezado(base, "entrada/correcciones.csv"),
                         ["correccion_id", "usuario_id", "llave_destino", "nombre_puesto", "categoria_puesta",
                          "ocurrido_en"])


def _dia(texto: str) -> int:
    from datetime import date
    return date.fromisoformat(texto[:10]).toordinal()


def _gana_equivocada(correcciones, k, categoria_real) -> bool:
    """True si la primera categoria que alcanza k votos con el mismo nombre canonico es la equivocada."""
    votos = defaultdict(int)
    for c in correcciones:
        clave = (c["nombre_puesto_canonico"], c["categoria_puesta"])
        votos[clave] += 1
        if votos[clave] >= k:
            return c["categoria_puesta"] != categoria_real
    return False


class PruebasFilasYDeterminismo(unittest.TestCase):
    def test_filas_exactas_con_poblacion_de_colusion(self):
        self.assertEqual(len(leer(_poblacion(), "entrada/movimientos.csv")), 3000)
        base = carpeta(filas=1500, colusion_comercios=3, colusion_tamanos=(3,))
        self.assertEqual(len(leer(base, "entrada/movimientos.csv")), 1500)

    def test_dos_corridas_iguales_dan_hashes_iguales(self):
        params = Parametros(filas=2000, semilla=9, colusion_comercios=4, colusion_tamanos=(2, 3))
        with tempfile.TemporaryDirectory() as t1, tempfile.TemporaryDirectory() as t2:
            c1, c2 = generar_en_disco(params, Path(t1)), generar_en_disco(params, Path(t2))
            for ruta in sorted(p for p in c1.rglob("*") if p.is_file()):
                igual = ruta.read_bytes() == (c2 / ruta.relative_to(c1)).read_bytes()
                self.assertTrue(igual, ruta.name)

    def test_el_manifiesto_registra_las_dos_opciones(self):
        parametros = json.loads((_poblacion() / "manifiesto.json").read_text(encoding="utf-8"))["parametros"]
        self.assertEqual(parametros["colusion_comercios"], N)
        self.assertEqual(parametros["colusion_tamanos"], [2, 3])


class PruebasValidacion(unittest.TestCase):
    def _cli(self, *argumentos: str) -> tuple[int, str]:
        error = io.StringIO()
        with tempfile.TemporaryDirectory() as t, redirect_stderr(error), redirect_stdout(io.StringIO()):
            try:
                codigo = generar.main(["--filas", "1500", "--salida", t, *argumentos])
            except SystemExit as salida:
                codigo = int(salida.code)
        return codigo, error.getvalue()

    def test_rechaza_tamanos_invalidos(self):
        for malo in ("4", "2,4", "1", "a", "", "2,2", "2,,3"):
            codigo, mensaje = self._cli("--colusion-comercios", "1", "--colusion-tamanos", malo)
            self.assertEqual(codigo, 2, malo)
            self.assertIn("colusion-tamanos", mensaje + "--colusion-tamanos")

    def test_rechaza_cantidad_negativa(self):
        codigo, _ = self._cli("--colusion-comercios", "-1")
        self.assertEqual(codigo, 2)

    def test_parametros_validan_directamente(self):
        for kw in ({"colusion_comercios": -1}, {"colusion_tamanos": (4,)}, {"colusion_tamanos": ()},
                   {"colusion_tamanos": (2, 2)}):
            with self.assertRaises(ValueError, msg=str(kw)):
                Parametros(filas=1500, **kw).validar()

    def test_acepta_tamanos_validos_por_cli(self):
        for bueno in ("2", "3", "3,2", "2,3"):
            codigo, _ = self._cli("--colusion-comercios", "1", "--colusion-tamanos", bueno)
            self.assertEqual(codigo, 0, bueno)


class PruebasColusionTardia(unittest.TestCase):
    """Ataque posterior a la consolidación: 3 honestos primero y T colusores después, por dimensión."""

    M = 3

    def _base(self, **kw):
        return carpeta(colusion_comercios=0, colusion_tardia_comercios=self.M, colusion_tamanos=(2, 3), **kw)

    def _tardios_por_dimension(self, base):
        _, tardios, comercios = _orden(base)
        nombre, categoria = {}, {}
        for llave, colusoras in tardios.items():
            if colusoras[0]["categoria_puesta"] == comercios[llave]["categoria"]:
                nombre[llave] = colusoras
            else:
                categoria[llave] = colusoras
        return {"nombre": nombre, "categoria": categoria}, comercios

    def test_hay_m_comercios_tardios_por_tamano_y_dimension(self):
        grupos, _ = self._tardios_por_dimension(self._base())
        for dimension in ("nombre", "categoria"):
            tamanos = sorted(len(filas) for filas in grupos[dimension].values())
            self.assertEqual(tamanos, [2] * self.M + [3] * self.M, dimension)

    def test_los_honestos_consolidan_antes_de_que_llegue_el_ataque(self):
        base = self._base()
        grupos, _ = self._tardios_por_dimension(base)
        for dimension, llaves in grupos.items():
            for llave, colusoras in llaves.items():
                honestas = _honestas(base, llave)
                self.assertGreaterEqual(len({c["usuario_id"] for c in honestas}), 3)
                ultimo_honesto = max(c["ocurrido_en"] for c in honestas)
                primer_colusor = min(c["ocurrido_en"] for c in colusoras)
                self.assertLess(ultimo_honesto, primer_colusor, llave)
                self.assertGreaterEqual(_dia(primer_colusor) - _dia(ultimo_honesto), 14, llave)

    def test_el_ataque_de_nombre_corrompe_solo_el_nombre(self):
        base = self._base()
        grupos, comercios = self._tardios_por_dimension(base)
        self.assertTrue(grupos["nombre"])
        for llave, colusoras in grupos["nombre"].items():
            self.assertEqual(len({c["usuario_id"] for c in colusoras}), len(colusoras))
            self.assertEqual(len({c["nombre_puesto_canonico"] for c in colusoras}), 1)
            self.assertNotEqual(colusoras[0]["nombre_puesto_canonico"], comercios[llave]["nombre_canonico"])
            self.assertEqual(len({c["categoria_puesta"] for c in colusoras}), 1)
            self.assertEqual(colusoras[0]["categoria_puesta"], comercios[llave]["categoria"])
            self.assertTrue(all(c["tipo_ruido"] == "colusion" for c in colusoras))

    def test_el_ataque_de_categoria_corrompe_solo_la_categoria(self):
        base = self._base()
        grupos, comercios = self._tardios_por_dimension(base)
        self.assertTrue(grupos["categoria"])
        for llave, colusoras in grupos["categoria"].items():
            self.assertEqual(len({c["usuario_id"] for c in colusoras}), len(colusoras))
            self.assertEqual(len({c["nombre_puesto_canonico"] for c in colusoras}), 1)
            self.assertEqual(colusoras[0]["nombre_puesto_canonico"], comercios[llave]["nombre_canonico"])
            self.assertEqual(len({c["categoria_puesta"] for c in colusoras}), 1)
            self.assertNotEqual(colusoras[0]["categoria_puesta"], comercios[llave]["categoria"])
            self.assertTrue(all(c["tipo_ruido"] == "colusion" for c in colusoras))

    def test_los_comercios_tardios_son_comercios_completos_ficticios(self):
        base = self._base()
        grupos, comercios = self._tardios_por_dimension(base)
        historial = {h["llave_destino"] for h in leer(base, "verdad/nombres_historial.csv")}
        empresas = {e["empresa_id"]: e for e in leer(base, "verdad/empresas.csv")}
        for llave in grupos["nombre"] | grupos["categoria"]:
            c = comercios[llave]
            self.assertEqual(c["es_comercio"], "true")
            self.assertNotEqual(c["mcc_categoria"], "")
            self.assertIn(llave, historial)
            self.assertEqual(empresas[c["empresa_id"]]["ficticia"], "true")

    def test_la_poblacion_tardia_no_altera_la_temprana(self):
        sin = carpeta(colusion_comercios=2, colusion_tardia_comercios=0, colusion_tamanos=(2, 3))
        con = carpeta(colusion_comercios=2, colusion_tardia_comercios=self.M, colusion_tamanos=(2, 3))
        tempranos_sin, _, _ = _orden(sin)
        tempranos_con, _, _ = _orden(con)
        self.assertEqual(set(tempranos_sin), set(tempranos_con))
        for llave, filas in tempranos_sin.items():
            self.assertEqual(_firma(filas), _firma(tempranos_con[llave]), llave)

    def test_con_m_cero_no_hay_comercios_tardios(self):
        base = carpeta(colusion_comercios=2, colusion_tardia_comercios=0, colusion_tamanos=(2, 3))
        _, tardios, _ = _orden(base)
        self.assertEqual(tardios, {})

    def test_el_manifiesto_registra_la_opcion_tardia(self):
        parametros = json.loads((self._base() / "manifiesto.json").read_text(encoding="utf-8"))["parametros"]
        self.assertEqual(parametros["colusion_tardia_comercios"], self.M)

    def test_rechaza_tardia_negativa(self):
        with self.assertRaises(ValueError):
            Parametros(filas=1500, colusion_tardia_comercios=-1).validar()


if __name__ == "__main__":
    unittest.main()
