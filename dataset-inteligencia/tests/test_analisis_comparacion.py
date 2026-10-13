"""Pruebas del experimento de comparación: línea base, óptimo restringido y óptimo libre con bootstrap."""

import csv
import dataclasses
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from analisis.barrido import cargar_rejilla, expandir, rejilla_desde_dict
from analisis.comparacion import BOOTSTRAP_B_DEFECTO, correr_comparacion, validar_restringida
from analisis.configuracion import Configuracion, ConfiguracionInvalida
from analisis.evaluacion import evaluar, preparar_corrida

from .util_analisis import corrida_con_diferencias

RAIZ = Path(__file__).resolve().parent.parent
CONFIG = RAIZ / "analisis" / "config"
BASE = Configuracion.por_defecto()
LIBRE = {"w_monto": [0.6], "tol_monto": [0.10], "factor_tolerancia_dias": [0.5, 1.0], "n_min": [3],
         "umbral": [0.5, 0.7]}
RESTRINGIDA = {"w_monto": [0.6], "tol_monto": [0.10], "factor_tolerancia_dias": [1.0], "n_min": [3],
               "umbral": [0.5, 0.7]}


def leer(ruta: Path) -> list[dict]:
    with open(ruta, newline="", encoding="utf-8") as f:
        return list(csv.DictReader(f))


class PruebasRejillaRestringida(unittest.TestCase):
    def test_rechaza_lo_que_amplia_hacia_abajo(self):
        for cambios in ({"factor_tolerancia_dias": [0.5]}, {"factor_tolerancia_dias": [1.0, 1.5]},
                        {"n_min": [2]}, {"n_min": [3, 4]}):
            with self.assertRaises(ConfiguracionInvalida, msg=str(cambios)):
                validar_restringida(rejilla_desde_dict({**RESTRINGIDA, **cambios}), BASE)

    def test_rechaza_una_base_con_tolerancias_distintas(self):
        base_rara = dataclasses.replace(BASE, tolerancias_dias=(1, 1.5, 2))
        with self.assertRaises(ConfiguracionInvalida):
            validar_restringida(rejilla_desde_dict(RESTRINGIDA), base_rara)
        validar_restringida(rejilla_desde_dict(RESTRINGIDA), BASE)

    def test_las_dos_rejillas_reales_comparten_ejes_y_la_restringida_tiene_252(self):
        libre = cargar_rejilla(CONFIG / "rejilla_recurrencia.json")
        restr = cargar_rejilla(CONFIG / "rejilla_recurrencia_restringida.json")
        for eje in ("w_monto", "tol_monto", "umbral"):
            self.assertEqual(libre.valores[eje], restr.valores[eje])
        self.assertEqual(restr.valores["factor_tolerancia_dias"], (1.0,))
        self.assertEqual(restr.valores["n_min"], (3,))
        validar_restringida(restr, BASE)
        configs = expandir(restr, BASE)
        self.assertEqual(len(configs), 252)
        self.assertTrue(all(c.configuracion.tolerancias_dias == (2, 3, 4) and c.configuracion.n_min == 3
                            for c in configs))


class BaseComparacion(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.raiz = Path(self.dir.name)
        self.corridas = {s: corrida_con_diferencias(self.raiz / f"c{s}", semilla=s) for s in (2026, 1, 7, 11, 5, 9)}
        self.libre, self.restr = self.raiz / "libre.json", self.raiz / "restr.json"
        self.libre.write_text(json.dumps(LIBRE), encoding="utf-8")
        self.restr.write_text(json.dumps(RESTRINGIDA), encoding="utf-8")

    def argumentos(self, salida: str, b: str = "100") -> list[str]:
        c = self.corridas
        return ["--experimento", "recurrencia-comparacion", "--entrenamiento", str(c[2026]), "--validacion",
                str(c[1]), str(c[7]), str(c[11]), "--verificacion", str(c[5]), str(c[9]), "--rejilla",
                str(self.libre), "--rejilla-restringida", str(self.restr), "--bootstrap-b", b,
                "--resultados", str(self.raiz / salida)]

    def correr(self, salida: str = "res", hashseed: str = "0") -> Path:
        r = subprocess.run([sys.executable, str(RAIZ / "analizar.py"), *self.argumentos(salida)],
                           capture_output=True, text=True, cwd=RAIZ, check=False,
                           env={**os.environ, "PYTHONHASHSEED": hashseed})
        self.assertEqual(r.returncode, 0, r.stderr)
        self.stdout = r.stdout
        (carpeta,) = list((self.raiz / salida / "recurrencia").iterdir())
        return carpeta


class PruebasSalida(BaseComparacion):
    def test_archivos_y_defecto_de_b(self):
        carpeta = self.correr()
        self.assertEqual(BOOTSTRAP_B_DEFECTO, 2000)
        self.assertEqual(sorted(p.name for p in carpeta.iterdir()),
                         ["comparacion.csv", "comparacion_ic.csv", "manifiesto.json", "mejor_libre.json",
                          "mejor_restringida.json"])

    def test_comparacion_csv_por_corpus_y_metodo(self):
        filas = leer(self.correr() / "comparacion.csv")
        self.assertEqual(list(filas[0]), ["corrida", "rol", "semilla", "metodo", "ambito", "tp", "fp", "tn", "fn",
                                          "precision", "recall", "f1", "tasa_falsos_positivos", "nota"])
        glob = {(f["corrida"], f["metodo"]): f for f in filas if f["ambito"] == "global"}
        self.assertEqual({m for _, m in glob}, {"linea_base", "restringido", "libre"})
        self.assertEqual(len(glob), 18)
        self.assertEqual({f["rol"] for f in filas}, {"entrenamiento", "validacion", "verificacion"})
        base, restr = glob[("c1", "linea_base")], glob[("c1", "restringido")]
        self.assertEqual((base["tp"], base["fn"]), ("1", "31"))
        self.assertEqual((restr["tp"], restr["fp"], restr["fn"]), ("31", "1", "1"))
        self.assertEqual(restr["f1"], "0.968750")

    def test_motivos_sin_positivos_traen_fp_tn_y_tasa(self):
        filas = leer(self.correr() / "comparacion.csv")
        pago = next(f for f in filas if f["corrida"] == "c1" and f["metodo"] == "restringido"
                    and f["ambito"] == "motivo:pago_suelto")
        self.assertEqual((pago["fp"], pago["tn"], pago["tasa_falsos_positivos"]), ("1", "0", "1.000000"))
        self.assertEqual((pago["precision"], pago["recall"], pago["f1"]), ("n/a", "n/a", "n/a"))
        self.assertIn("sin positivos", pago["nota"])
        compra = next(f for f in filas if f["corrida"] == "c1" and f["metodo"] == "restringido"
                      and f["ambito"] == "motivo:compra_habitual")
        self.assertEqual((compra["fp"], compra["tn"], compra["tasa_falsos_positivos"]), ("0", "1", "0.000000"))
        regular = next(f for f in filas if f["corrida"] == "c1" and f["metodo"] == "restringido"
                       and f["ambito"] == "motivo:dia_variable")
        self.assertNotEqual(regular["f1"], "n/a")
        self.assertEqual(regular["nota"], "")

    def test_intervalos_y_frases(self):
        filas = leer(self.correr() / "comparacion_ic.csv")
        self.assertEqual(list(filas[0]), ["corrida", "rol", "comparacion", "n_series", "diferencia_observada",
                                          "diferencia_media_bootstrap", "ic95_inferior", "ic95_superior",
                                          "bootstrap_b", "semilla_bootstrap", "frase"])
        self.assertEqual(len(filas), 18)  # 3 validaciones, la agregada y 2 verificaciones, por 3 comparaciones
        self.assertEqual({f["corrida"] for f in filas}, {"c1", "c7", "c11", "validacion_agregada", "c5", "c9"})
        self.assertEqual(next(f for f in filas if f["corrida"] == "validacion_agregada")["n_series"], "102")
        res = {(f["corrida"], f["comparacion"]): f for f in filas}
        rb = res[("c1", "restringido_vs_linea_base")]
        self.assertEqual(rb["frase"], "el intervalo no incluye 0")
        self.assertGreater(float(rb["ic95_inferior"]), 0)
        self.assertEqual(rb["bootstrap_b"], "100")
        lr = res[("c1", "libre_vs_restringido")]
        self.assertEqual(lr["frase"], "el intervalo incluye 0 (no se distingue del restringido)")
        self.assertEqual({f["rol"] for f in filas if f["corrida"] in ("c5", "c9")}, {"verificacion"})

    def test_manifiesto_y_configuraciones(self):
        carpeta = self.correr()
        m = json.loads((carpeta / "manifiesto.json").read_text(encoding="utf-8"))
        self.assertEqual((m["bootstrap_b"], m["semilla_bootstrap"]), (100, 2026))
        self.assertEqual(m["semillas"]["verificacion"], {"c5": 5, "c9": 9})
        self.assertIn("libre_sha256", m)
        self.assertIn("restringida_sha256", m)
        restr = json.loads((carpeta / "mejor_restringida.json").read_text(encoding="utf-8"))
        self.assertEqual((restr["tolerancias_dias"], restr["n_min"]), ([2, 3, 4], 3))
        self.assertIn("tasa de falsos positivos", self.stdout)

    def test_misma_entrada_da_archivos_identicos_byte_a_byte(self):
        a, b = self.correr("a", "1"), self.correr("b", "random")
        self.assertEqual({p.name: p.read_bytes() for p in a.iterdir()}, {p.name: p.read_bytes() for p in b.iterdir()})


class PruebasValidaciones(unittest.TestCase):
    def test_semillas_repetidas_se_rechazan(self):
        with tempfile.TemporaryDirectory() as t:
            raiz = Path(t)
            c = {s: corrida_con_diferencias(raiz / f"c{s}", semilla=s) for s in (2026, 1, 5)}
            libre = rejilla_desde_dict(LIBRE)
            restr = rejilla_desde_dict(RESTRINGIDA)
            with self.assertRaises(ValueError):
                correr_comparacion(c[2026], [c[1]], [c[1]], libre, restr, BASE, raiz / "r", 50, 2026)
            self.assertTrue(evaluar(preparar_corrida(c[1]), BASE))


if __name__ == "__main__":
    unittest.main()
