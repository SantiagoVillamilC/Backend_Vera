"""Pruebas del arnés de evaluación de avisos: lee la verdad, arma las dos fuentes de series y compara con las etiquetas."""

import csv
import json
import os
import subprocess
import sys
import tempfile
import unittest
from datetime import date
from fractions import Fraction
from pathlib import Path

from analisis.avisos_config import RUTA_DEFECTO_AVISOS, cargar_configuracion_avisos
from analisis.avisos_evaluacion import (Combinacion, FilaEvaluada, aplicar_tope, cargar_corrida_avisos,
                                         combinaciones, evaluar_corrida, resumir, variacion)
from analisis.avisos_fondo import agregar_fondo, filas_sin_etiqueta

from .util_avisos import (CLARO, INTERNET, MERCADO, RESTAURANTE, Contador, deposito, etiqueta, corrida_avisos, pago,
                          serie_de, usuario_internet, usuario_p1, usuario_p4, usuario_p5)

CFG = cargar_configuracion_avisos(RUTA_DEFECTO_AVISOS)
RAIZ = Path(__file__).resolve().parent.parent
CLI = RAIZ / "analizar.py"
ARCHIVOS = ["configuracion.json", "filas.csv", "filas_categoria_inferida.csv", "fondo_detalle.csv", "fondo_resumen.csv",
            "manifiesto.json", "resumen.csv", "resumen.md", "variacion.csv"]
R1_MEDIANA_ORACULO = Combinacion("R1", "mediana", "oraculo")
R2_MEDIANA_ORACULO = Combinacion("R2", "mediana", "oraculo")


def centavos(pesos: int) -> Fraction:
    return Fraction(pesos * 100)


def claro(ids: Contador, usuario: str = "u3") -> list[dict]:
    filas = [deposito(ids, usuario, "2026-04-01T14:00:00Z", 5_000_000)]
    for mes, dia, monto in ((4, 5, 89_900), (5, 5, 89_900), (6, 5, 89_900), (7, 5, 89_900), (8, 9, 89_900), (9, 5, 94_900)):
        filas.append(pago(ids, usuario, f"2026-{mes:02d}-{dia:02d}T15:00:00Z", monto, CLARO))
    return filas


def fondo(ids: Contador, usuario: str = "uf") -> list[dict]:
    """Usuario sin etiqueta: 100.000 al mes de abril a agosto y 300.000 en septiembre."""
    filas = [deposito(ids, usuario, "2026-04-01T14:00:00Z", 2_000_000)]
    filas += [pago(ids, usuario, f"2026-{mes:02d}-10T18:00:00Z", 100_000 if mes < 9 else 300_000, MERCADO)
              for mes in range(4, 10)]
    return filas


def filas_de_la_corrida() -> list[dict]:
    ids = Contador()
    return (usuario_p1(ids) + usuario_p4(ids) + usuario_p5(ids) + usuario_internet(ids, "u2", 600_000)
            + claro(ids) + fondo(ids))


ETIQUETAS = [
    etiqueta("u1", "categoria_sobre_media", "2026-10-21", True, False),
    etiqueta("u1", "presupuesto_anticipado", "2026-10-21", True, False),
    etiqueta("u4", "presupuesto_anticipado", "2026-10-20", True, True),
    etiqueta("u4", "categoria_sobre_media", "2026-10-20", True, True),
    etiqueta("u5", "categoria_sobre_media", "2026-10-21", False, False),
    etiqueta("u5", "presupuesto_anticipado", "2026-10-21", False, False),
    etiqueta("u2", "pago_no_alcanza", "2026-10-03", True, True),
    etiqueta("u3", "cobro_nuevo", "2026-10-21", False, False),
]
SERIES = [serie_de("u2", INTERNET, numero=1), serie_de("u3", CLARO, numero=2)]
CATEGORIAS = {MERCADO: "alimentos_mercado", RESTAURANTE: "restaurantes_domicilios"}


def correccion(numero: int, usuario: str, llave: str, categoria: str) -> dict:
    return {"correccion_id": f"c{numero}", "usuario_id": usuario, "llave_destino": llave, "nombre_puesto": "Nombre",
            "categoria_puesta": categoria, "ocurrido_en": f"2026-0{numero}-15T15:00:00Z"}


def crear(destino: Path, **cambios) -> Path:
    argumentos = {"filas": filas_de_la_corrida(), "etiquetas": ETIQUETAS, "series": SERIES, "categorias": CATEGORIAS}
    return corrida_avisos(destino, **{**argumentos, **cambios})


class Base(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.raiz = Path(self.dir.name)


class PruebasDeLaEvaluacion(Base):
    def setUp(self):
        super().setUp()
        self.corrida = cargar_corrida_avisos(crear(self.raiz / "tasa-0.1" / "mini"))
        self.filas = evaluar_corrida(self.corrida, CFG)

    def fila(self, usuario: str, tipo: str, combinacion: Combinacion):
        return next(f for f in self.filas if f.usuario_id == usuario and f.tipo == tipo
                    and f.combinacion == combinacion)

    def test_hay_una_fila_por_etiqueta_y_por_combinacion(self):
        self.assertEqual(len(combinaciones(CFG)), 8)
        self.assertEqual(len(self.filas), 8 * len(ETIQUETAS))
        self.assertEqual(self.corrida.nombre, "tasa-0.1/mini")

    def test_r1_con_mediana_y_oraculo_aprueba_las_ocho_filas(self):
        propias = [f for f in self.filas if f.combinacion == R1_MEDIANA_ORACULO]
        self.assertTrue(all(f.aprobada for f in propias), [f for f in propias if not f.aprobada])

    def test_r2_falla_las_dos_filas_del_perfil_4_y_dice_por_que(self):
        for tipo in ("presupuesto_anticipado", "categoria_sobre_media"):
            f = self.fila("u4", tipo, R2_MEDIANA_ORACULO)
            self.assertEqual((f.esperado, f.dispara, f.aprobada), (True, False, False))
            self.assertEqual(f.estadistico, Fraction(6745, 10000))
            self.assertEqual(f.causa, "z_bajo_el_umbral")

    def test_la_columna_esperada_depende_de_la_linea_base(self):
        con_mediana = self.fila("u1", "presupuesto_anticipado", R1_MEDIANA_ORACULO)
        con_media = self.fila("u1", "presupuesto_anticipado", Combinacion("R1", "media", "oraculo"))
        self.assertEqual((con_mediana.esperado, con_mediana.dispara), (True, True))
        self.assertEqual((con_media.esperado, con_media.dispara), (False, False))
        self.assertEqual((con_mediana.referencia, con_media.referencia), (centavos(300_000), centavos(650_000)))
        self.assertTrue(con_mediana.aprobada and con_media.aprobada)

    def test_historial_insuficiente_se_marca_con_su_causa(self):
        f = self.fila("u5", "presupuesto_anticipado", R1_MEDIANA_ORACULO)
        self.assertEqual((f.dispara, f.aprobada, f.causa), (False, True, "historial_insuficiente"))

    def test_pago_no_alcanza_con_oraculo_y_con_series_detectadas(self):
        for fuente in ("oraculo", "detectadas"):
            f = self.fila("u2", "pago_no_alcanza", Combinacion("R1", "mediana", fuente))
            self.assertTrue(f.dispara and f.aprobada, fuente)
            self.assertEqual((f.valor, f.referencia), (centavos(60_600), centavos(89_900)))

    def test_cobro_nuevo_no_dispara_por_el_cambio_de_dia_y_de_monto(self):
        for fuente in ("oraculo", "detectadas"):
            f = self.fila("u3", "cobro_nuevo", Combinacion("R1", "mediana", fuente))
            self.assertEqual((f.dispara, f.aprobada, f.causa), (False, True, "sin_llave_nueva"))

    def test_el_resumen_cuenta_aprobadas_falsos_positivos_y_falsos_negativos(self):
        por_combinacion = {r.combinacion: r for r in resumir(self.filas)}
        r1, r2 = por_combinacion[R1_MEDIANA_ORACULO], por_combinacion[R2_MEDIANA_ORACULO]
        self.assertEqual((r1.aprobadas, r1.total, r1.falsos_positivos, r1.negativas, r1.falsos_negativos, r1.positivas),
                         (8, 8, 0, 3, 0, 5))
        self.assertEqual((r2.aprobadas, r2.total, r2.falsos_positivos, r2.negativas, r2.falsos_negativos, r2.positivas),
                         (6, 8, 0, 3, 2, 5))
        self.assertEqual(len(por_combinacion), 8)

    def test_el_resumen_por_tipo_separa_cobro_nuevo_que_solo_tiene_negativas(self):
        r1 = next(r for r in resumir(self.filas) if r.combinacion == R1_MEDIANA_ORACULO)
        self.assertEqual(r1.por_tipo["cobro_nuevo"], (1, 1, 0, 1, 0, 0))

    def test_la_variacion_entre_corridas_marca_las_filas_que_cambian(self):
        otra = cargar_corrida_avisos(crear(self.raiz / "tasa-0.1" / "otra", series=[]))
        filas_otra = evaluar_corrida(otra, CFG)
        v = variacion([self.filas, filas_otra])
        clave = next(f for f in v if f.tipo == "pago_no_alcanza" and f.combinacion == R1_MEDIANA_ORACULO)
        self.assertEqual((clave.corridas, clave.aprobadas, clave.varia), (2, 1, True))
        estable = next(f for f in v if f.tipo == "presupuesto_anticipado" and f.combinacion == R1_MEDIANA_ORACULO)
        self.assertEqual((estable.corridas, estable.aprobadas, estable.varia), (2, 2, False))


class PruebasDeLasFuentes(Base):
    def test_el_oraculo_ignora_las_series_no_recurrentes_y_las_detectadas_las_encuentran_solas(self):
        corrida = cargar_corrida_avisos(crear(
            self.raiz / "t" / "c", series=[serie_de("u2", INTERNET, motivo="pago_suelto", recurrente=False)]))
        filas = evaluar_corrida(corrida, CFG)
        oraculo = next(f for f in filas if f.tipo == "pago_no_alcanza" and f.combinacion == R1_MEDIANA_ORACULO)
        detectadas = next(f for f in filas if f.tipo == "pago_no_alcanza"
                          and f.combinacion == Combinacion("R1", "mediana", "detectadas"))
        self.assertEqual((oraculo.dispara, oraculo.aprobada, oraculo.causa), (False, False, "sin_series"))
        self.assertEqual((detectadas.dispara, detectadas.aprobada), (True, True))

    def test_las_series_detectadas_se_calculan_con_el_reloj_de_la_fecha_de_evaluacion(self):
        ids = Contador()
        filas = usuario_internet(ids, "u2", 400_000)
        corrida = cargar_corrida_avisos(crear(
            self.raiz / "t" / "c", filas=filas, etiquetas=[etiqueta("u2", "pago_no_alcanza", "2026-08-04", True, True)],
            series=[]))
        fila = next(f for f in evaluar_corrida(corrida, CFG)
                    if f.combinacion == Combinacion("R1", "mediana", "detectadas"))
        self.assertEqual((fila.dispara, fila.causa), (True, "pago_en_la_ventana"))

    def test_la_categoria_inferida_con_k_2_se_reporta_aparte(self):
        correcciones = [correccion(1, "x", RESTAURANTE, "restaurantes_domicilios"),
                        correccion(2, "y", RESTAURANTE, "restaurantes_domicilios")]
        corrida = cargar_corrida_avisos(crear(self.raiz / "t" / "c", correcciones=correcciones))
        oraculo = evaluar_corrida(corrida, CFG)
        inferida = evaluar_corrida(corrida, CFG, categoria_inferida=True)
        self.assertTrue(all(f.categoria_fuente == "oraculo" for f in oraculo))
        self.assertTrue(all(f.categoria_fuente == "inferida" for f in inferida))
        u1 = next(f for f in inferida if f.usuario_id == "u1" and f.tipo == "categoria_sobre_media"
                  and f.combinacion == R1_MEDIANA_ORACULO)
        u4 = next(f for f in inferida if f.usuario_id == "u4" and f.tipo == "categoria_sobre_media"
                  and f.combinacion == R1_MEDIANA_ORACULO)
        self.assertIn("restaurantes_domicilios", u1.detalle)
        self.assertIn("sin_categoria", u4.detalle)
        self.assertTrue(u1.aprobada and u4.aprobada)

    def test_con_una_sola_correccion_no_hay_categoria_inferida_con_k_2(self):
        corrida = cargar_corrida_avisos(crear(
            self.raiz / "t" / "c", correcciones=[correccion(1, "x", RESTAURANTE, "restaurantes_domicilios")]))
        inferida = evaluar_corrida(corrida, CFG, categoria_inferida=True)
        u1 = next(f for f in inferida if f.usuario_id == "u1" and f.tipo == "categoria_sobre_media")
        self.assertIn("sin_categoria", u1.detalle)


class PruebasDelFondo(Base):
    def setUp(self):
        super().setUp()
        self.corrida = cargar_corrida_avisos(crear(self.raiz / "t" / "c"))
        self.filas = filas_sin_etiqueta(self.corrida, CFG)

    def test_las_filas_ya_etiquetadas_no_se_repiten_como_fondo(self):
        etiquetadas = {(e.usuario_id, e.tipo, e.fecha_evaluacion) for e in self.corrida.etiquetas}
        for f in self.filas:
            self.assertNotIn((f.usuario_id, f.tipo, f.fecha_evaluacion), etiquetadas)

    def test_el_usuario_de_fondo_recibe_un_solo_aviso_de_presupuesto_en_septiembre(self):
        propias = [f for f in self.filas if f.usuario_id == "uf" and f.tipo == "presupuesto_anticipado"
                   and f.combinacion == R1_MEDIANA_ORACULO]
        self.assertEqual({f.grupo for f in propias}, {"fondo"})
        disparos = [f for f in propias if f.dispara]
        self.assertEqual([(f.mes, f.fecha_evaluacion) for f in disparos], [("2026-09", date(2026, 9, 30))])
        self.assertEqual(sorted({f.fecha_evaluacion for f in propias})[-1], date(2026, 10, 21))
        self.assertIn(date(2026, 4, 30), {f.fecha_evaluacion for f in propias})

    def test_los_perfiles_etiquetados_en_otras_fechas_van_en_su_propio_grupo(self):
        grupos = {f.usuario_id: f.grupo for f in self.filas}
        self.assertEqual(grupos["uf"], "fondo")
        for usuario in ("u1", "u2", "u3", "u4", "u5"):
            self.assertEqual(grupos[usuario], "perfil_etiquetado")

    def test_el_resumen_cuenta_evaluaciones_evaluables_y_avisos(self):
        resumen = {(r.grupo, r.combinacion, r.tipo): r for r in agregar_fondo(self.filas)}
        r = resumen[("fondo", R1_MEDIANA_ORACULO, "presupuesto_anticipado")]
        self.assertEqual((r.avisos, r.evaluaciones), (1, 7))
        self.assertEqual(r.evaluables, 4)
        self.assertEqual(r.usuarios, 1)


class PruebasDelCli(Base):
    def correr(self, salida: str, *corridas: Path, hashseed: str = "0", extra: tuple[str, ...] = ()):
        entorno = {**os.environ, "PYTHONHASHSEED": hashseed}
        return subprocess.run([sys.executable, str(CLI), "--experimento", "avisos", "--corridas",
                               *map(str, corridas), "--resultados", str(self.raiz / salida), *extra],
                              capture_output=True, text=True, env=entorno, cwd=RAIZ, check=False)

    def corridas(self) -> tuple[Path, Path]:
        return (crear(self.raiz / "tasa-0.1" / "c1"), crear(self.raiz / "tasa-0.35" / "c1", series=[]))

    def carpeta(self, salida: str) -> Path:
        (unica,) = list((self.raiz / salida / "avisos").iterdir())
        return unica

    def test_produce_los_nueve_archivos(self):
        r = self.correr("res", *self.corridas())
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(sorted(p.name for p in self.carpeta("res").iterdir()), ARCHIVOS)
        self.assertIn("tiempo", r.stderr)

    def test_misma_entrada_da_archivos_identicos_byte_a_byte(self):
        corridas = self.corridas()
        self.correr("a", *corridas, hashseed="1")
        self.correr("b", *corridas, hashseed="random")
        a, b = self.carpeta("a"), self.carpeta("b")
        self.assertEqual(a.name, b.name)
        for nombre in ARCHIVOS:
            self.assertEqual((a / nombre).read_bytes(), (b / nombre).read_bytes(), nombre)

    def test_la_salida_no_filtra_rutas_del_sistema(self):
        self.correr("res", *self.corridas())
        for p in self.carpeta("res").iterdir():
            datos = p.read_bytes()
            self.assertNotIn(self.dir.name.encode(), datos, p.name)
            self.assertNotIn(str(RAIZ).encode(), datos, p.name)

    def test_el_resumen_se_declara_prueba_de_aceptacion_y_rotula_el_fondo(self):
        self.correr("res", *self.corridas())
        texto = (self.carpeta("res") / "resumen.md").read_text(encoding="utf-8")
        self.assertIn("prueba de aceptación", texto)
        self.assertIn("sin verdad, no es tasa de falsos", texto)
        self.assertIn("cobro_nuevo", texto)
        self.assertIn("propuesta sin fuente", texto)

    def test_resumen_csv_trae_las_ocho_combinaciones_por_corrida(self):
        self.correr("res", *self.corridas())
        with open(self.carpeta("res") / "resumen.csv", newline="", encoding="utf-8") as f:
            filas = list(csv.DictReader(f))
        self.assertEqual(list(filas[0]), ["corrida", "regla", "linea_base", "fuente", "tipo", "aprobadas", "total",
                                          "falsos_positivos", "negativas", "falsos_negativos", "positivas"])
        globales = [r for r in filas if r["tipo"] == "todos"]
        self.assertEqual(len(globales), 16)
        r1 = next(r for r in globales if r["corrida"] == "tasa-0.1/c1" and (r["regla"], r["linea_base"], r["fuente"])
                  == ("R1", "mediana", "oraculo"))
        self.assertEqual((r1["aprobadas"], r1["total"], r1["falsos_negativos"]), ("8", "8", "0"))

    def test_manifiesto_con_hashes_semillas_y_reloj(self):
        self.correr("res", *self.corridas())
        m = json.loads((self.carpeta("res") / "manifiesto.json").read_text(encoding="utf-8"))
        self.assertEqual(m["experimento"], "avisos")
        self.assertEqual(m["configuracion_sha256"], CFG.hash())
        self.assertEqual(sorted(m["semillas"]), ["tasa-0.1/c1", "tasa-0.35/c1"])
        self.assertIn("nota", m)

    def test_sin_corridas_es_un_error_de_uso(self):
        entorno = {**os.environ}
        r = subprocess.run([sys.executable, str(CLI), "--experimento", "avisos", "--resultados", str(self.raiz / "x")],
                           capture_output=True, text=True, env=entorno, cwd=RAIZ, check=False)
        self.assertEqual(r.returncode, 2)
        self.assertIn("--corridas", r.stderr)

    def test_una_corrida_sin_avisos_esperados_da_error_claro(self):
        corrida = crear(self.raiz / "t" / "c")
        (corrida / "verdad" / "avisos_esperados.csv").unlink()
        r = self.correr("res", corrida)
        self.assertEqual(r.returncode, 2)
        self.assertIn("No existe el archivo", r.stderr)

    def test_corridas_con_el_mismo_nombre_se_rechazan(self):
        a = crear(self.raiz / "t" / "c")
        r = self.correr("res", a, a)
        self.assertEqual(r.returncode, 2)
        self.assertIn("nombres distintos", r.stderr)


TIPOS_TOPE = ("presupuesto_anticipado", "categoria_sobre_media", "caida_ingreso", "pago_no_alcanza", "cobro_nuevo")
COMBO = Combinacion("R1", "mediana", "oraculo")


def _fila(tipo: str, esperado: bool, dispara: bool, usuario: str = "u1", fecha: date = date(2026, 10, 20),
          combo: Combinacion = COMBO) -> FilaEvaluada:
    return FilaEvaluada("c", 1, usuario, tipo, fecha, combo, esperado, dispara, None, None, None, "", "", "oraculo")


class PruebasDelTope(unittest.TestCase):
    def test_tope_cero_no_cambia_nada(self):
        filas = [_fila(tipo, True, True) for tipo in TIPOS_TOPE]
        self.assertEqual(aplicar_tope(filas, 0), tuple(filas))

    def test_recorta_la_intencion_y_el_disparo_por_prioridad(self):
        filas = [_fila(tipo, True, True) for tipo in TIPOS_TOPE]
        recorte = {(f.tipo, f.esperado, f.dispara) for f in aplicar_tope(filas, 3)}
        self.assertEqual(recorte, {("pago_no_alcanza", True, True), ("caida_ingreso", True, True),
                                   ("cobro_nuevo", True, True), ("presupuesto_anticipado", False, False),
                                   ("categoria_sobre_media", False, False)})

    def test_la_intencion_y_el_disparo_se_recortan_por_aparte(self):
        filas = [_fila("pago_no_alcanza", True, False), _fila("cobro_nuevo", False, True),
                 _fila("presupuesto_anticipado", False, True), _fila("categoria_sobre_media", False, True)]
        recorte = {f.tipo: (f.esperado, f.dispara) for f in aplicar_tope(filas, 2)}
        self.assertEqual(recorte, {"pago_no_alcanza": (True, False), "cobro_nuevo": (False, True),
                                   "presupuesto_anticipado": (False, True), "categoria_sobre_media": (False, False)})

    def test_usuarios_y_fechas_distintas_no_se_mezclan(self):
        filas = [_fila(tipo, True, True, usuario="u1") for tipo in TIPOS_TOPE]
        filas += [_fila(tipo, True, True, usuario="u2") for tipo in TIPOS_TOPE]
        filas += [_fila(tipo, True, True, usuario="u1", fecha=date(2026, 9, 30)) for tipo in TIPOS_TOPE]
        recorte = aplicar_tope(filas, 3)
        for usuario, fecha in (("u1", date(2026, 10, 20)), ("u2", date(2026, 10, 20)), ("u1", date(2026, 9, 30))):
            sobreviven = [f for f in recorte
                          if f.usuario_id == usuario and f.fecha_evaluacion == fecha and f.esperado]
            self.assertEqual(len(sobreviven), 3, (usuario, fecha))


if __name__ == "__main__":
    unittest.main()
