"""Pruebas de la población de usuarios de gasto con verdad por diseño (experimento C).

La verdad se comprueba contra la INTENCIÓN de diseño (la brecha grande con la que se generó cada usuario),
nunca afinando el dato al umbral de la regla. Cada diseño es positivo en su tipo y negativo en los otros
cuatro, y la decisión de la regla (R1 y R2, con mediana y con media) debe coincidir con esa intención.
"""

import io
import json
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from datetime import date
from pathlib import Path
from types import SimpleNamespace

import generar
from analisis.avisos import contexto_en, evaluar, fin_del_dia_local
from analisis.avisos_config import RUTA_DEFECTO_AVISOS, cargar_configuracion_avisos
from analisis.avisos_corrida import cargar_corrida_avisos
from analisis.avisos_datos import construir_historiales
from analisis.avisos_fuentes import series_oraculo
from generador import gasto_poblacion
from generador.modelos import Aporte
from generador.orquestador import generar_en_disco, minimo_filas
from generador.parametros import Parametros

from .util import carpeta, leer

CFG = cargar_configuracion_avisos(RUTA_DEFECTO_AVISOS)
FECHA = "2026-10-20"
TIPOS = ("presupuesto_anticipado", "categoria_sobre_media", "caida_ingreso", "pago_no_alcanza", "cobro_nuevo")
DISENOS = ("presupuesto_pos", "presupuesto_neg", "categoria_pos", "categoria_neg", "caida_pos", "caida_neg",
           "pago_no_alcanza_pos", "cobro_nuevo_pos", "sin_avisos", "tope_excedido")
POSITIVO = {
    "presupuesto_pos": ("presupuesto_anticipado",),
    "categoria_pos": ("categoria_sobre_media",),
    "caida_pos": ("caida_ingreso",),
    "pago_no_alcanza_pos": ("pago_no_alcanza",),
    "cobro_nuevo_pos": ("cobro_nuevo",),
    "tope_excedido": ("presupuesto_anticipado", "categoria_sobre_media", "caida_ingreso", "cobro_nuevo"),
}
REGLAS = ("R1", "R2")
BASES = ("mediana", "media")


def _verdades_por_diseno(base: Path) -> dict[str, list[dict]]:
    """Filas de avisos_esperados.csv de la población de gasto, agrupadas por el nombre del diseño."""
    grupos: dict[str, list[dict]] = {}
    for fila in leer(base, "verdad/avisos_esperados.csv"):
        diseno = fila["detalle"].split(":", 1)[0].strip()
        if diseno in DISENOS:
            grupos.setdefault(diseno, []).append(fila)
    return grupos


class PruebasDelModulo(unittest.TestCase):
    def test_la_fecha_de_evaluacion_y_la_lista_de_disenos(self):
        self.assertEqual(gasto_poblacion.FECHA_EVALUACION, date(2026, 10, 20))
        self.assertEqual(tuple(gasto_poblacion.DISENOS), DISENOS)

    def test_con_cero_devuelve_un_aporte_vacio_sin_tocar_el_contexto(self):
        ctx = SimpleNamespace(parametros=SimpleNamespace(gasto_usuarios=0, semilla=2026))
        self.assertEqual(gasto_poblacion.construir(ctx), Aporte())


class PruebasVerdad(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.base = carpeta(gasto_usuarios=len(DISENOS))

    def test_un_usuario_por_diseno_con_cinco_filas_una_por_tipo(self):
        grupos = _verdades_por_diseno(self.base)
        self.assertEqual(set(grupos), set(DISENOS))
        for diseno, filas in grupos.items():
            self.assertEqual(sorted(f["tipo"] for f in filas), sorted(TIPOS), diseno)
            self.assertEqual(len({f["usuario_id"] for f in filas}), 1, diseno)
            for fila in filas:
                self.assertEqual(fila["fecha_evaluacion"], FECHA, diseno)

    def test_la_intencion_es_positiva_solo_en_los_tipos_del_diseno(self):
        for diseno, filas in _verdades_por_diseno(self.base).items():
            positivos = POSITIVO.get(diseno, ())
            for fila in filas:
                con_mediana = fila["debe_disparar_con_mediana"] == "true"
                self.assertEqual(con_mediana, fila["tipo"] in positivos, (diseno, fila["tipo"]))
                self.assertEqual(fila["debe_disparar_con_media"], fila["debe_disparar_con_mediana"],
                                 (diseno, fila["tipo"]))


class PruebasDiseno(unittest.TestCase):
    """La regla (R1 y R2, mediana y media) coincide con la intención de diseño en los cinco tipos."""

    @classmethod
    def setUpClass(cls):
        cls.base = carpeta(gasto_usuarios=len(DISENOS))
        cls.corrida = cargar_corrida_avisos(cls.base)
        cls.historiales = construir_historiales(cls.corrida.movimientos, CFG)
        cls.oraculo = series_oraculo(cls.corrida.series)
        cls.verdades = _verdades_por_diseno(cls.base)

    def _contexto(self, usuario: str):
        ahora = fin_del_dia_local(date(2026, 10, 20))
        return contexto_en(self.historiales[usuario], self.oraculo.get(usuario, ()), self.corrida.categorias,
                           ahora, CFG)

    def test_cada_diseno_coincide_con_las_cuatro_combinaciones(self):
        for diseno, filas in self.verdades.items():
            ctx = self._contexto(filas[0]["usuario_id"])
            for fila in filas:
                esperado = fila["debe_disparar_con_mediana"] == "true"
                decisiones = [evaluar(ctx, fila["tipo"], regla, base).dispara
                              for regla in REGLAS for base in BASES]
                with self.subTest(diseno=diseno, tipo=fila["tipo"]):
                    self.assertEqual(set(decisiones), {esperado}, decisiones)


class PruebasFilasYCiclo(unittest.TestCase):
    def test_filas_exactas_con_poblacion_de_gasto(self):
        base = carpeta(filas=3000, gasto_usuarios=9)
        self.assertEqual(len(leer(base, "entrada/movimientos.csv")), 3000)

    def test_con_dos_mas_que_disenos_repiten_los_dos_primeros(self):
        grupos = _verdades_por_diseno(carpeta(gasto_usuarios=len(DISENOS) + 2))
        self.assertEqual(len({f["usuario_id"] for f in grupos["presupuesto_pos"]}), 2)
        self.assertEqual(len({f["usuario_id"] for f in grupos["presupuesto_neg"]}), 2)
        for diseno in DISENOS[2:]:
            self.assertEqual(len({f["usuario_id"] for f in grupos[diseno]}), 1, diseno)

    def test_el_minimo_de_filas_crece_con_la_poblacion_de_gasto(self):
        self.assertGreater(minimo_filas(Parametros(filas=1, gasto_usuarios=3)),
                           minimo_filas(Parametros(filas=1)))

    def test_el_manifiesto_registra_la_opcion(self):
        parametros = json.loads((carpeta(gasto_usuarios=3) / "manifiesto.json").read_text(
            encoding="utf-8"))["parametros"]
        self.assertEqual(parametros["gasto_usuarios"], 3)


class PruebasConCero(unittest.TestCase):
    def test_sin_gasto_usuarios_no_hay_verdad_de_diseno(self):
        self.assertEqual(_verdades_por_diseno(carpeta(gasto_usuarios=0)), {})


class PruebasDeterminismo(unittest.TestCase):
    def test_dos_corridas_iguales_dan_archivos_identicos(self):
        params = Parametros(filas=2000, semilla=11, gasto_usuarios=5)
        with tempfile.TemporaryDirectory() as t1, tempfile.TemporaryDirectory() as t2:
            c1, c2 = generar_en_disco(params, Path(t1)), generar_en_disco(params, Path(t2))
            for ruta in sorted(p for p in c1.rglob("*") if p.is_file()):
                self.assertEqual(ruta.read_bytes(), (c2 / ruta.relative_to(c1)).read_bytes(), ruta.name)


class PruebasValidacion(unittest.TestCase):
    def _cli(self, *argumentos: str) -> tuple[int, str]:
        error = io.StringIO()
        with tempfile.TemporaryDirectory() as t, redirect_stderr(error), redirect_stdout(io.StringIO()):
            try:
                codigo = generar.main(["--filas", "1500", "--salida", t, *argumentos])
            except SystemExit as salida:
                codigo = int(salida.code)
        return codigo, error.getvalue()

    def test_parametros_validan_directamente(self):
        for valor in (-1, -5):
            with self.assertRaises(ValueError):
                Parametros(filas=1500, gasto_usuarios=valor).validar()

    def test_rechaza_negativo_por_cli(self):
        codigo, _ = self._cli("--gasto-usuarios", "-1")
        self.assertEqual(codigo, 2)

    def test_acepta_cero_y_positivo_por_cli(self):
        for valor in ("0", "3"):
            codigo, _ = self._cli("--gasto-usuarios", valor)
            self.assertEqual(codigo, 0, valor)


if __name__ == "__main__":
    unittest.main()
