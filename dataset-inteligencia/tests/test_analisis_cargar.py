"""Pruebas de los cargadores y de la separación estricta entre entrada y verdad."""

import ast
import shutil
import tempfile
import unittest
from pathlib import Path

from analisis.cargar import cargar_entrada, cargar_manifiesto, cargar_verdad

from .util import carpeta
from .util_analisis import corrida_mini

RAIZ = Path(__file__).resolve().parent.parent / "analisis"
# Módulos que forman el detector: reciben filas ya cargadas y deciden. Ninguno toca la verdad.
MODULOS_DEL_DETECTOR = ("configuracion.py", "pagos.py", "recurrencia.py", "linea_base.py")
IMPORTS_PROHIBIDOS = ("analisis.cargar", "analisis.evaluacion", "analisis.barrido")


def _docstrings(arbol: ast.AST) -> set[int]:
    ids = set()
    for nodo in ast.walk(arbol):
        if isinstance(nodo, (ast.Module, ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            cuerpo = nodo.body
            if cuerpo and isinstance(cuerpo[0], ast.Expr) and isinstance(cuerpo[0].value, ast.Constant):
                ids.add(id(cuerpo[0].value))
    return ids


def violaciones(fuente: str) -> list[str]:
    """Qué hace que un módulo del detector toque la verdad. Lista vacía si está limpio."""
    arbol = ast.parse(fuente)
    docs = _docstrings(arbol)
    encontradas = []
    for nodo in ast.walk(arbol):
        if isinstance(nodo, ast.ImportFrom):
            modulo = nodo.module or ""
            if any(a.name == "cargar_verdad" for a in nodo.names):
                encontradas.append("importa cargar_verdad")
            if modulo in IMPORTS_PROHIBIDOS or (modulo == "analisis" and any(
                    f"analisis.{a.name}" in IMPORTS_PROHIBIDOS for a in nodo.names)):
                encontradas.append(f"importa {modulo}")
        elif isinstance(nodo, ast.Import):
            encontradas += [f"importa {a.name}" for a in nodo.names if a.name in IMPORTS_PROHIBIDOS]
        elif isinstance(nodo, ast.Name) and nodo.id == "cargar_verdad":
            encontradas.append("usa cargar_verdad")
        elif isinstance(nodo, ast.Attribute) and nodo.attr == "cargar_verdad":
            encontradas.append("usa cargar_verdad")
        elif isinstance(nodo, ast.Constant) and isinstance(nodo.value, str) and id(nodo) not in docs:
            if "verdad" in nodo.value.lower():
                encontradas.append(f"menciona la carpeta verdad: {nodo.value!r}")
    return encontradas


class PruebaEstaticaDelDetector(unittest.TestCase):
    def test_ningun_modulo_del_detector_toca_la_verdad(self):
        for nombre in MODULOS_DEL_DETECTOR:
            ruta = RAIZ / nombre
            self.assertTrue(ruta.exists(), nombre)
            self.assertEqual(violaciones(ruta.read_text(encoding="utf-8")), [], nombre)

    def test_el_verificador_detecta_los_casos_malos(self):
        malos = [
            "from analisis.cargar import cargar_verdad\n",
            "from analisis import cargar\nx = cargar.cargar_verdad(r)\n",
            "import analisis.cargar\n",
            "from analisis.cargar import cargar_entrada\n",
            "ruta = base / 'verdad' / 'series.csv'\n",
            "def f(r):\n    return open(r + '/Verdad/series.csv')\n",
            "from analisis.evaluacion import evaluar\n",
        ]
        for fuente in malos:
            self.assertTrue(violaciones(fuente), fuente)

    def test_el_verificador_deja_pasar_codigo_limpio_y_docstrings(self):
        limpio = '"""Habla de la verdad en el docstring."""\nfrom analisis.pagos import extraer_pagos\nx = 1\n'
        self.assertEqual(violaciones(limpio), [])

    def test_cargar_entrada_no_nombra_la_carpeta_verdad(self):
        arbol = ast.parse((RAIZ / "cargar.py").read_text(encoding="utf-8"))
        funcion = next(n for n in ast.walk(arbol) if isinstance(n, ast.FunctionDef) and n.name == "cargar_entrada")
        constantes = [n.value for n in ast.walk(funcion) if isinstance(n, ast.Constant) and isinstance(n.value, str)]
        self.assertFalse([c for c in constantes if "verdad" in c.lower()])


class PruebasCargadores(unittest.TestCase):
    def test_cargar_entrada_lee_solo_entrada(self):
        with tempfile.TemporaryDirectory() as t:
            corrida = corrida_mini(Path(t) / "c")
            shutil.rmtree(corrida / "verdad")
            entrada = cargar_entrada(corrida)
            self.assertEqual(len(entrada.movimientos), 19)
            self.assertEqual(entrada.correcciones, ())
            self.assertEqual({m["usuario_id"] for m in entrada.movimientos}, {f"u{i}" for i in range(1, 8)})

    def test_cargar_verdad_lee_solo_verdad(self):
        with tempfile.TemporaryDirectory() as t:
            corrida = corrida_mini(Path(t) / "c")
            shutil.rmtree(corrida / "entrada")
            verdad = cargar_verdad(corrida)
            self.assertEqual(len(verdad.series), 4)
            self.assertEqual(verdad.series[0]["motivo"], "regular")

    def test_cargar_manifiesto(self):
        with tempfile.TemporaryDirectory() as t:
            m = cargar_manifiesto(corrida_mini(Path(t) / "c", semilla=9))
            self.assertEqual(m["parametros"]["fecha_fin"], "2026-10-21")
            self.assertEqual(m["parametros"]["semilla"], 9)

    def test_corrida_inexistente_da_error_claro(self):
        with self.assertRaises(FileNotFoundError) as cm:
            cargar_entrada(Path("/no/existe/esta/corrida"))
        self.assertIn("movimientos.csv", str(cm.exception))

    def test_con_una_corrida_real_del_generador(self):
        base = carpeta()
        entrada = cargar_entrada(base)
        verdad = cargar_verdad(base)
        self.assertEqual(len(entrada.movimientos), 3000)
        self.assertGreater(len(verdad.series), 100)
        self.assertNotIn("es_recurrente", entrada.movimientos[0])


if __name__ == "__main__":
    unittest.main()
