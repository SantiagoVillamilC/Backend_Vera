"""Pruebas de estructura: determinismo, filas exactas, columnas, manifiesto y CLI."""

import hashlib
import json
import os
import subprocess
import sys
import tempfile
import unittest
from datetime import date
from pathlib import Path

from generador.orquestador import FilasInsuficientes, generar_en_disco, minimo_filas
from generador.parametros import Parametros

from .util import carpeta, encabezado, leer

RAIZ = Path(__file__).resolve().parent.parent
LLAVES_DE_UNION = {"movimiento_id", "usuario_id", "correccion_id", "llave_destino"}
# Lo que no es fuente del proyecto: cache de bytecode, dependencias de terceros y el harness,
# que es su propio repo dentro de la carpeta y no se versiona con el producto.
EXCLUIDOS = {"__pycache__", "node_modules", "harness"}
ARCHIVOS = (
    "entrada/movimientos.csv", "entrada/correcciones.csv", "verdad/empresas.csv",
    "verdad/comercios.csv", "verdad/etiquetas.csv", "verdad/correcciones_verdad.csv",
    "verdad/avisos_esperados.csv", "verdad/nombres_historial.csv", "verdad/casos_vista.csv",
    "verdad/series.csv",
)
COLUMNAS_MOVIMIENTOS = [
    "movimiento_id", "usuario_id", "transferencia_id", "ocurrido_en", "direccion",
    "monto_centavos", "moneda", "tipo_asiento", "descripcion", "tipo_llave", "llave_destino",
    "nombre_comercio_crudo", "mcc", "canal",
]


def contenido(base: Path) -> dict[str, bytes]:
    return {str(p.relative_to(base)): p.read_bytes() for p in sorted(base.rglob("*")) if p.is_file()}


class PruebasDeterminismo(unittest.TestCase):
    def test_misma_semilla_archivos_identicos_byte_a_byte(self):
        with tempfile.TemporaryDirectory() as t1, tempfile.TemporaryDirectory() as t2:
            params = Parametros(filas=1200, semilla=77, variante="B")
            c1 = generar_en_disco(params, Path(t1))
            c2 = generar_en_disco(params, Path(t2))
            self.assertEqual(contenido(c1), contenido(c2))
            self.assertIn("manifiesto.json", contenido(c1))

    def test_misma_corrida_en_procesos_con_distinto_hash_seed(self):
        """El hash de str cambia por proceso: la salida no puede depender de él."""
        manifiestos = []
        for hash_seed in ("1", "2"):
            with tempfile.TemporaryDirectory() as t:
                r = subprocess.run(
                    [sys.executable, str(RAIZ / "generar.py"), "--filas", "900", "--variante", "B",
                     "--salida", t],
                    capture_output=True, text=True, cwd=RAIZ, check=False,
                    env={**os.environ, "PYTHONHASHSEED": hash_seed})
                self.assertEqual(r.returncode, 0, r.stderr)
                carpeta_salida = Path(t) / "2026-900-B"
                manifiestos.append((json.loads((carpeta_salida / "manifiesto.json").read_text(encoding="utf-8")),
                                    contenido(carpeta_salida)))
        self.assertEqual(manifiestos[0][0], manifiestos[1][0])
        self.assertEqual(manifiestos[0][1], manifiestos[1][1])

    def test_semilla_distinta_cambia_los_datos(self):
        a = (carpeta(semilla=2026) / "entrada/movimientos.csv").read_bytes()
        b = (carpeta(semilla=2027) / "entrada/movimientos.csv").read_bytes()
        self.assertNotEqual(a, b)

    def test_nombre_de_carpeta(self):
        self.assertEqual(carpeta(filas=3000, semilla=2026, variante="A").name, "2026-3000-A")


class PruebasFilas(unittest.TestCase):
    def test_filas_exactas(self):
        for filas in (1000, 3000, 4567):
            for variante in ("A", "B"):
                base = carpeta(filas=filas, variante=variante)
                self.assertEqual(len(leer(base, "entrada/movimientos.csv")), filas, (filas, variante))

    def test_el_minimo_es_exacto(self):
        minimo = minimo_filas(Parametros(filas=1))
        self.assertGreater(minimo, 100)
        self.assertEqual(len(leer(carpeta(filas=minimo), "entrada/movimientos.csv")), minimo)
        with tempfile.TemporaryDirectory() as t:
            with self.assertRaises(FilasInsuficientes) as ctx:
                generar_en_disco(Parametros(filas=minimo - 1), Path(t))
        self.assertEqual(ctx.exception.minimo, minimo)
        self.assertIn(str(minimo), str(ctx.exception))

    def test_ids_de_movimiento_crecen_con_el_tiempo(self):
        filas = leer(carpeta(), "entrada/movimientos.csv")
        self.assertEqual([int(f["movimiento_id"]) for f in filas], list(range(1, len(filas) + 1)))
        instantes = [f["ocurrido_en"] for f in filas]
        self.assertEqual(instantes, sorted(instantes))

    def test_moneda_y_formato_de_fecha(self):
        for f in leer(carpeta(), "entrada/movimientos.csv"):
            self.assertEqual(f["moneda"], "COP")
            self.assertRegex(f["ocurrido_en"], r"^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$")
            self.assertGreater(int(f["monto_centavos"]), 0)

    def test_tipos_de_asiento_y_depositos(self):
        validos = {"deposito", "retencion", "transferencia_interna", "despacho_riel",
                   "liquidacion_confirmada", "compensacion"}
        filas = leer(carpeta(), "entrada/movimientos.csv")
        self.assertEqual({f["tipo_asiento"] for f in filas}, validos)
        for f in filas:
            self.assertEqual(f["transferencia_id"] == "", f["tipo_asiento"] == "deposito")


class PruebasColumnas(unittest.TestCase):
    def test_encabezado_de_movimientos(self):
        self.assertEqual(encabezado(carpeta(), "entrada/movimientos.csv"), COLUMNAS_MOVIMIENTOS)

    def test_ningun_encabezado_de_entrada_en_verdad_salvo_llaves_de_union(self):
        base = carpeta()
        entrada = {c for a in ("movimientos", "correcciones") for c in encabezado(base, f"entrada/{a}.csv")}
        for archivo in ARCHIVOS:
            if not archivo.startswith("verdad/"):
                continue
            compartidas = entrada & set(encabezado(base, archivo))
            permitidas = LLAVES_DE_UNION | ({"ocurrido_en"} if archivo.endswith("casos_vista.csv") else set())
            self.assertLessEqual(compartidas, permitidas, (archivo, compartidas))

    def test_ocurrido_en_solo_se_comparte_en_casos_vista(self):
        base = carpeta()
        for archivo in ARCHIVOS:
            if archivo.startswith("verdad/") and not archivo.endswith("casos_vista.csv"):
                self.assertNotIn("ocurrido_en", encabezado(base, archivo), archivo)

    def test_columnas_de_las_tablas_nuevas(self):
        base = carpeta()
        self.assertEqual(encabezado(base, "verdad/nombres_historial.csv"),
                         ["llave_destino", "nombre_canonico", "categoria", "vigente_desde", "vigente_hasta"])
        self.assertEqual(encabezado(base, "verdad/casos_vista.csv"),
                         ["caso_id", "usuario_id", "movimiento_id", "ocurrido_en",
                          "nombre_esperado", "categoria_esperada", "origen_esperado"])

    def test_empresas_reales_solo_las_cinco_cadenas(self):
        empresas = leer(carpeta(), "verdad/empresas.csv")
        reales = [e for e in empresas if e["ficticia"] == "false"]
        self.assertEqual(len(reales), 5)
        texto = " ".join(e["razon_social"] for e in reales)
        for marca in ("D1", "Éxito", "Ara", "Alkosto", "Olímpica"):
            self.assertIn(marca, texto)
        self.assertGreater(len(empresas), 30)

    def test_categorias_validas(self):
        catalogo = {
            "salario", "transferencia_recibida", "reembolso", "otros_ingresos", "alimentos_mercado",
            "restaurantes_domicilios", "transporte", "vivienda_servicios", "salud", "educacion",
            "entretenimiento", "compras", "comisiones_costos_financieros", "otros_gastos", "sin_categoria",
        }
        base = carpeta()
        self.assertLessEqual({e["categoria_real"] for e in leer(base, "verdad/etiquetas.csv")}, catalogo)
        self.assertLessEqual({c["categoria_puesta"] for c in leer(base, "entrada/correcciones.csv")}, catalogo)
        self.assertEqual(len(leer(base, "verdad/etiquetas.csv")), 3000)

    def test_personas_son_sin_categoria(self):
        for e in leer(carpeta(), "verdad/etiquetas.csv"):
            if e["es_persona"] == "true":
                self.assertEqual(e["categoria_real"], "sin_categoria")


class PruebasManifiesto(unittest.TestCase):
    def test_manifiesto_completo_y_correcto(self):
        base = carpeta(filas=3000, variante="B")
        manifiesto = json.loads((base / "manifiesto.json").read_text(encoding="utf-8"))
        self.assertEqual(manifiesto["parametros"]["filas"], 3000)
        self.assertEqual(manifiesto["parametros"]["variante"], "B")
        self.assertEqual(manifiesto["parametros"]["fecha_fin"], "2026-10-21")
        self.assertIn("version", manifiesto["generador"])
        self.assertEqual(set(manifiesto["archivos"]), set(ARCHIVOS))
        for ruta, info in manifiesto["archivos"].items():
            datos = (base / ruta).read_bytes()
            self.assertEqual(info["sha256"], hashlib.sha256(datos).hexdigest(), ruta)
            self.assertEqual(info["filas"], len(leer(base, ruta)), ruta)


class PruebasCli(unittest.TestCase):
    def ejecutar(self, *args: str) -> subprocess.CompletedProcess:
        return subprocess.run([sys.executable, str(RAIZ / "generar.py"), *args],
                              capture_output=True, text=True, cwd=RAIZ, check=False)

    def test_corrida_valida(self):
        with tempfile.TemporaryDirectory() as t:
            r = self.ejecutar("--filas", "700", "--variante", "B", "--semilla", "5", "--salida", t)
            self.assertEqual(r.returncode, 0, r.stderr)
            self.assertTrue((Path(t) / "5-700-B" / "manifiesto.json").is_file())

    def test_filas_insuficientes_falla_con_el_minimo(self):
        minimo = minimo_filas(Parametros(filas=1))
        with tempfile.TemporaryDirectory() as t:
            r = self.ejecutar("--filas", "10", "--salida", t)
            self.assertNotEqual(r.returncode, 0)
            self.assertIn(str(minimo), r.stderr)

    def test_fecha_fin_invalida(self):
        with tempfile.TemporaryDirectory() as t:
            r = self.ejecutar("--filas", "700", "--fecha-fin", "2026-09-01", "--salida", t)
            self.assertNotEqual(r.returncode, 0)

    def test_fecha_fin_posterior_funciona(self):
        base = carpeta(filas=1500, fecha_fin=date(2026, 11, 15))
        self.assertEqual(len(leer(base, "entrada/movimientos.csv")), 1500)


class PruebasEscrituraDelProyecto(unittest.TestCase):
    def test_sin_rayas_largas_ni_medias(self):
        prohibidos = ("\u2014", "\u2013")
        for ruta in sorted(RAIZ.rglob("*")):
            if ruta.suffix in {".py", ".md"} and not (set(ruta.parts) & EXCLUIDOS):
                texto = ruta.read_text(encoding="utf-8")
                for p in prohibidos:
                    self.assertNotIn(p, texto, str(ruta))

    def test_sin_voseo_evidente(self):
        for ruta in sorted(RAIZ.rglob("*.md")):
            if set(ruta.parts) & EXCLUIDOS:
                continue
            texto = ruta.read_text(encoding="utf-8").lower()
            for palabra in (" vos ", "corrés", "tenés", "podés", "elegís"):
                self.assertNotIn(palabra, texto, str(ruta))


if __name__ == "__main__":
    unittest.main()
