"""Pruebas del CLI analizar.py: archivos de salida, manifiesto, errores y determinismo byte a byte."""

import csv
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from analisis.configuracion import Configuracion, cargar_configuracion

from .util import carpeta
from .util_analisis import corrida_mini

RAIZ = Path(__file__).resolve().parent.parent
CLI = RAIZ / "analizar.py"
REJILLA_CHICA = {"w_monto": [0.6], "tol_monto": [0.10], "factor_tolerancia_dias": [1.0], "n_min": [3],
                 "umbral": [0.5, 0.7]}


def ejecutar(*args: str, hashseed: str = "0") -> subprocess.CompletedProcess:
    entorno = {**os.environ, "PYTHONHASHSEED": hashseed}
    return subprocess.run([sys.executable, str(CLI), *args], capture_output=True, text=True, env=entorno,
                          cwd=RAIZ, check=False)


def leer_csv(ruta: Path) -> list[dict]:
    with open(ruta, newline="", encoding="utf-8") as f:
        return list(csv.DictReader(f))


def contenido(raiz: Path) -> dict[str, bytes]:
    return {str(p.relative_to(raiz)): p.read_bytes() for p in sorted(raiz.rglob("*")) if p.is_file()}


def valor(filas: list[dict], ambito: str, medida: str) -> str:
    return next(f["valor"] for f in filas if f["ambito"] == ambito and f["medida"] == medida)


class BaseCli(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.raiz = Path(self.dir.name)
        self.mini = corrida_mini(self.raiz / "mini", semilla=2026)
        self.rejilla = self.raiz / "rejilla.json"
        self.rejilla.write_text(json.dumps(REJILLA_CHICA), encoding="utf-8")

    def correr_evaluacion(self, salida: str, *extra: str, hashseed: str = "0") -> Path:
        r = ejecutar("--experimento", "recurrencia", "--corrida", str(self.mini),
                     "--resultados", str(self.raiz / salida), *extra, hashseed=hashseed)
        self.assertEqual(r.returncode, 0, r.stderr)
        return self.raiz / salida / "recurrencia"

    def correr_barrido(self, salida: str, hashseed: str = "0") -> Path:
        val = [corrida_mini(self.raiz / f"val{s}", semilla=s) for s in (1, 7)]
        r = ejecutar("--experimento", "recurrencia-barrido", "--entrenamiento", str(self.mini),
                     "--validacion", *map(str, val), "--rejilla", str(self.rejilla),
                     "--resultados", str(self.raiz / salida), hashseed=hashseed)
        self.assertEqual(r.returncode, 0, r.stderr)
        return self.raiz / salida / "recurrencia"


class PruebasEvaluacion(BaseCli):
    def test_produce_los_archivos_y_las_metricas_de_la_corrida_minima(self):
        base = self.correr_evaluacion("res")
        (carpeta_id,) = list(base.iterdir())
        self.assertEqual(sorted(p.name for p in carpeta_id.iterdir()),
                         ["configuracion.json", "manifiesto.json", "metricas.csv"])
        filas = leer_csv(carpeta_id / "metricas.csv")
        self.assertEqual(list(filas[0]), ["corrida", "ambito", "medida", "valor"])
        self.assertEqual(valor(filas, "global", "tp"), "1")
        self.assertEqual(valor(filas, "global", "f1"), "0.500000")
        for medida in ("precision", "recall", "f1"):  # sin positivos reales no hay razón que informar
            self.assertEqual(valor(filas, "motivo:pago_suelto", medida), "n/a")
        self.assertEqual(valor(filas, "motivo:pago_suelto", "tasa_falsos_positivos"), "1.000000")
        self.assertEqual(valor(filas, "motivo:regular", "precision"), "1.000000")
        self.assertEqual(valor(filas, "secundaria", "tasa_falsos_positivos"), "0.500000")
        self.assertIn("suposicion", {f["medida"] for f in filas if f["ambito"] == "secundaria"})
        self.assertEqual(valor(filas, "ocurrencias_necesarias", "mediana"), "2")
        self.assertEqual(valor(filas, "ocurrencias_necesarias", "no_detectadas"), "1")

    def test_manifiesto_con_hash_de_configuracion_entradas_semillas_y_version(self):
        (carpeta_id,) = list(self.correr_evaluacion("res").iterdir())
        m = json.loads((carpeta_id / "manifiesto.json").read_text(encoding="utf-8"))
        self.assertEqual(m["configuracion_sha256"], Configuracion.por_defecto().hash())
        self.assertEqual(m["semillas"], {"mini": 2026})
        self.assertIn("version", m)
        self.assertEqual(m["experimento"], "recurrencia")
        entradas = m["entradas"]["mini"]
        self.assertEqual(sorted(entradas), ["entrada/correcciones.csv", "entrada/movimientos.csv",
                                            "manifiesto.json", "verdad/series.csv"])
        self.assertTrue(all(len(h) == 64 for h in entradas.values()))
        self.assertEqual(m["ahora"], {"mini": "2026-10-21T23:59:59Z"})

    def test_acepta_config_y_cambia_el_resultado(self):
        estricta = self.raiz / "estricta.json"
        estricta.write_text(json.dumps({**Configuracion.por_defecto().a_dict(), "umbral": 0.7}), encoding="utf-8")
        (carpeta_id,) = list(self.correr_evaluacion("res", "--config", str(estricta)).iterdir())
        filas = leer_csv(carpeta_id / "metricas.csv")
        self.assertEqual(valor(filas, "global", "fp"), "0")
        self.assertEqual(cargar_configuracion(carpeta_id / "configuracion.json").umbral, 0.7)

    def test_crea_el_gitignore_de_resultados(self):
        self.correr_evaluacion("res")
        self.assertEqual((self.raiz / "res" / ".gitignore").read_text(encoding="utf-8"), "*\n!.gitignore\n")

    def test_el_gitignore_real_del_proyecto_existe(self):
        self.assertEqual((RAIZ / "resultados" / ".gitignore").read_text(encoding="utf-8"), "*\n!.gitignore\n")

    def test_misma_entrada_da_archivos_identicos_byte_a_byte(self):
        a = contenido(self.correr_evaluacion("a", hashseed="1"))
        b = contenido(self.correr_evaluacion("b", hashseed="2"))
        c = contenido(self.correr_evaluacion("c", hashseed="random"))
        self.assertEqual(a, b)
        self.assertEqual(a, c)
        self.assertEqual(len(a), 3)

    def test_la_salida_no_filtra_rutas_del_sistema(self):
        base = self.correr_evaluacion("res")
        for ruta, datos in contenido(base).items():
            self.assertNotIn(self.dir.name.encode(), datos, ruta)
            self.assertNotIn(str(RAIZ).encode(), datos, ruta)

    def test_con_una_corrida_real_del_generador(self):
        r = ejecutar("--experimento", "recurrencia", "--corrida", str(carpeta()),
                     "--resultados", str(self.raiz / "real"))
        self.assertEqual(r.returncode, 0, r.stderr)
        (carpeta_id,) = list((self.raiz / "real" / "recurrencia").iterdir())
        filas = leer_csv(carpeta_id / "metricas.csv")
        total = sum(int(valor(filas, "global", m)) for m in ("tp", "fp", "tn", "fn"))
        self.assertEqual(total, 164)


class PruebasBarridoCli(BaseCli):
    def test_produce_los_cuatro_archivos(self):
        (carpeta_id,) = list(self.correr_barrido("res").iterdir())
        self.assertEqual(sorted(p.name for p in carpeta_id.iterdir()),
                         ["barrido.csv", "manifiesto.json", "mejor_configuracion.json", "metricas.csv"])
        barrido = leer_csv(carpeta_id / "barrido.csv")
        self.assertEqual(len(barrido), 2)
        self.assertEqual([f["es_mejor"] for f in barrido], ["false", "true"])
        self.assertEqual(barrido[1]["f1"], "0.666667")

    def test_metricas_lado_a_lado_entrenamiento_y_validacion(self):
        (carpeta_id,) = list(self.correr_barrido("res").iterdir())
        filas = leer_csv(carpeta_id / "metricas.csv")
        ambitos = {f["ambito"] for f in filas}
        for esperado in ("mejor:entrenamiento", "mejor:validacion:val1", "mejor:validacion:val7",
                         "referencia:entrenamiento", "referencia:validacion:val1"):
            self.assertTrue(any(a.startswith(esperado) for a in ambitos), esperado)
        self.assertEqual(valor(filas, "mejor:entrenamiento:global", "f1"), "0.666667")
        self.assertIn("optimo_sobre_el_corpus_sintetico", {f["medida"] for f in filas})

    def test_manifiesto_del_barrido(self):
        (carpeta_id,) = list(self.correr_barrido("res").iterdir())
        m = json.loads((carpeta_id / "manifiesto.json").read_text(encoding="utf-8"))
        self.assertEqual(m["semillas"], {"entrenamiento": {"mini": 2026}, "validacion": {"val1": 1, "val7": 7}})
        self.assertEqual(m["rejilla"]["tamano"], 2)
        self.assertEqual(len(m["rejilla_sha256"]), 64)
        mejor = cargar_configuracion(carpeta_id / "mejor_configuracion.json")
        self.assertEqual(m["configuracion_sha256"], mejor.hash())
        self.assertEqual(mejor.umbral, 0.7)

    def test_mejor_configuracion_via_config_reproduce_la_fila_de_entrenamiento(self):
        (carpeta_id,) = list(self.correr_barrido("res").iterdir())
        barrido = leer_csv(carpeta_id / "barrido.csv")
        mejor_fila = next(f for f in barrido if f["es_mejor"] == "true")
        (otra,) = list(self.correr_evaluacion("otra", "--config", str(carpeta_id / "mejor_configuracion.json")).iterdir())
        filas = leer_csv(otra / "metricas.csv")
        self.assertEqual(valor(filas, "global", "f1"), mejor_fila["f1"])
        self.assertEqual(valor(filas, "global", "precision"), mejor_fila["precision"])

    def test_dos_ejecuciones_dan_archivos_identicos_byte_a_byte(self):
        a = contenido(self.correr_barrido("a", hashseed="3"))
        b = contenido(self.correr_barrido("b", hashseed="random"))
        self.assertEqual(a, b)
        self.assertEqual(len(a), 4)


class PruebasErrores(BaseCli):
    def test_corrida_inexistente(self):
        r = ejecutar("--experimento", "recurrencia", "--corrida", str(self.raiz / "nada"),
                     "--resultados", str(self.raiz / "res"))
        self.assertEqual(r.returncode, 2)
        self.assertIn("Error", r.stderr)
        self.assertIn("No existe el archivo", r.stderr)

    def test_configuracion_invalida(self):
        mala = self.raiz / "mala.json"
        mala.write_text(json.dumps({**Configuracion.por_defecto().a_dict(), "umbral": 3}), encoding="utf-8")
        r = ejecutar("--experimento", "recurrencia", "--corrida", str(self.mini), "--config", str(mala),
                     "--resultados", str(self.raiz / "res"))
        self.assertEqual(r.returncode, 2)
        self.assertIn("umbral", r.stderr)

    def test_barrido_sin_validacion(self):
        r = ejecutar("--experimento", "recurrencia-barrido", "--entrenamiento", str(self.mini),
                     "--rejilla", str(self.rejilla), "--resultados", str(self.raiz / "res"))
        self.assertNotEqual(r.returncode, 0)

    def test_reporta_el_tiempo_por_stderr(self):
        r = ejecutar("--experimento", "recurrencia", "--corrida", str(self.mini),
                     "--resultados", str(self.raiz / "res"))
        self.assertIn("tiempo", r.stderr)


if __name__ == "__main__":
    unittest.main()
