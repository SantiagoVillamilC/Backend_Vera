"""Pruebas de la evaluación del detector contra verdad/series.csv."""

import dataclasses
import tempfile
import unittest
from datetime import datetime, timezone
from pathlib import Path

from analisis.configuracion import Configuracion
from analisis.evaluacion import Matriz, evaluar, ocurrencias_necesarias, preparar_corrida

from .util import carpeta
from .util_analisis import A, B, C, D, corrida_mini, escribir_corrida, pagos_en, serie

CFG = Configuracion.por_defecto()


class PruebasMatriz(unittest.TestCase):
    def test_metricas_de_una_matriz_conocida(self):
        m = Matriz(tp=6, fp=2, tn=10, fn=4)
        self.assertAlmostEqual(m.precision, 6 / 8)
        self.assertAlmostEqual(m.recall, 6 / 10)
        self.assertAlmostEqual(m.f1, 2 * 6 / (2 * 6 + 2 + 4))
        self.assertAlmostEqual(m.tasa_falsos_positivos, 2 / 12)

    def test_razones_indefinidas_son_none_y_f1_es_cero_si_hay_positivos(self):
        sin_predicciones = Matriz(tp=0, fp=0, tn=5, fn=3)
        self.assertIsNone(sin_predicciones.precision)
        self.assertEqual(sin_predicciones.recall, 0.0)
        self.assertEqual(sin_predicciones.f1, 0.0)
        sin_positivos = Matriz(tp=0, fp=2, tn=3, fn=0)
        self.assertIsNone(sin_positivos.recall)
        self.assertEqual(sin_positivos.precision, 0.0)
        self.assertEqual(Matriz(0, 0, 0, 0).f1, 0.0)
        self.assertIsNone(Matriz(0, 0, 0, 0).tasa_falsos_positivos)

    def test_f1_exacto_para_comparar(self):
        self.assertEqual(Matriz(1, 1, 1, 1).f1_exacto, Matriz(2, 2, 2, 2).f1_exacto)


class PruebasEvaluarCorridaMinima(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.corrida = preparar_corrida(corrida_mini(Path(self.dir.name) / "mini"))

    def test_ahora_es_el_fin_de_la_corrida(self):
        self.assertEqual(self.corrida.ahora, datetime(2026, 10, 21, 23, 59, 59, tzinfo=timezone.utc))

    def test_matriz_global_calculada_a_mano(self):
        e = evaluar(self.corrida, CFG)
        self.assertEqual(dataclasses.astuple(e.global_), (1, 1, 1, 1))
        self.assertAlmostEqual(e.global_.precision, 0.5)
        self.assertAlmostEqual(e.global_.recall, 0.5)
        self.assertAlmostEqual(e.global_.f1, 0.5)

    def test_desglose_por_motivo(self):
        e = evaluar(self.corrida, CFG)
        self.assertEqual(sorted(e.por_motivo), ["compra_habitual", "dia_variable", "pago_suelto", "regular"])
        self.assertEqual(dataclasses.astuple(e.por_motivo["regular"]), (1, 0, 0, 0))
        self.assertEqual(dataclasses.astuple(e.por_motivo["dia_variable"]), (0, 0, 0, 1))
        self.assertEqual(dataclasses.astuple(e.por_motivo["pago_suelto"]), (0, 1, 0, 0))
        self.assertEqual(dataclasses.astuple(e.por_motivo["compra_habitual"]), (0, 0, 1, 0))
        self.assertEqual(e.por_motivo["regular"].precision, 1.0)
        self.assertIsNone(e.por_motivo["pago_suelto"].recall)

    def test_secundaria_solo_cuenta_parejas_con_dos_o_mas_pagos_fuera_de_series(self):
        e = evaluar(self.corrida, CFG)
        self.assertEqual(e.secundaria.parejas, 2)  # E y G, no F (un pago) ni las de series.csv
        self.assertEqual(e.secundaria.falsos_positivos, 1)  # E
        self.assertAlmostEqual(e.secundaria.tasa, 0.5)

    def test_por_serie_trae_la_decision_de_cada_pareja(self):
        e = evaluar(self.corrida, CFG)
        decisiones = {(s.usuario_id, s.llave_destino): s for s in e.por_serie}
        self.assertTrue(decisiones[A].predicho)
        self.assertAlmostEqual(decisiones[A].r, 1.0)
        self.assertAlmostEqual(decisiones[B].r, 0.2, places=9)
        self.assertAlmostEqual(decisiones[C].r, 2 / 3, places=9)
        self.assertAlmostEqual(decisiones[D].r, 0.2, places=9)

    def test_umbral_mas_alto_quita_el_falso_positivo(self):
        e = evaluar(self.corrida, dataclasses.replace(CFG, umbral=0.7))
        self.assertEqual(dataclasses.astuple(e.global_), (1, 0, 2, 1))
        self.assertAlmostEqual(e.global_.precision, 1.0)
        self.assertAlmostEqual(e.global_.f1, 2 / 3)

    def test_ocurrencias_necesarias(self):
        o = ocurrencias_necesarias(self.corrida, CFG)
        por_pareja = {(s.usuario_id, s.llave_destino): s for s in o.por_serie}
        self.assertEqual(set(por_pareja), {A, B})  # solo las series recurrentes
        self.assertEqual(por_pareja[A].necesarias, 2)
        self.assertEqual(por_pareja[A].pagos, 5)
        self.assertIsNone(por_pareja[B].necesarias)
        self.assertEqual((o.total, o.detectadas, o.no_detectadas), (2, 1, 1))
        self.assertEqual(o.mediana, 2)
        self.assertEqual(o.por_motivo["regular"].mediana, 2)
        self.assertIsNone(o.por_motivo["dia_variable"].mediana)

    def test_no_muta_la_corrida_ni_depende_del_orden_de_llamadas(self):
        antes = evaluar(self.corrida, CFG)
        evaluar(self.corrida, dataclasses.replace(CFG, umbral=0.9))
        self.assertEqual(evaluar(self.corrida, CFG), antes)

    def test_hashes_de_los_archivos_de_la_corrida(self):
        self.assertEqual(
            sorted(self.corrida.hashes),
            ["entrada/correcciones.csv", "entrada/movimientos.csv", "manifiesto.json", "verdad/series.csv"])
        self.assertTrue(all(len(h) == 64 for h in self.corrida.hashes.values()))


class PruebasDeteccionSostenida(unittest.TestCase):
    def test_la_deteccion_debe_sostenerse_hasta_el_final(self):
        """Un cambio de decision que se revierte no cuenta: la serie queda detectada solo desde el ultimo cambio."""
        # Intervalos de 30 o 31 días: Fecha vale 1 desde el segundo pago. El cuarto pago (90.000) rompe el monto.
        # R por número de pagos k: 0 (k=1), 0,667 (k=2), 1 (k=3), 0,85 (k=4), 0,88 (k=5) y 0,90 (k=6).
        pagos = [("2026-05-05", 50000), ("2026-06-05", 50000), ("2026-07-05", 50000), ("2026-08-05", 90000),
                 ("2026-09-05", 50000), ("2026-10-05", 50000)]
        with tempfile.TemporaryDirectory() as t:
            corrida = preparar_corrida(escribir_corrida(
                Path(t) / "c", pagos_en(*A, 1, pagos), [serie(*A, True, "regular", 1)]))
            esperado = {0.5: 2, 0.8: 3, 0.9: 6}
            for umbral, necesarias in esperado.items():
                o = ocurrencias_necesarias(corrida, dataclasses.replace(CFG, umbral=umbral))
                self.assertEqual(o.por_serie[0].necesarias, necesarias, umbral)

    def test_el_pago_del_ultimo_dia_cuenta(self):
        """ahora es el final del dia de fecha_fin, no su medianoche."""
        filas = pagos_en(*A, 1, [("2026-09-21", 50000), ("2026-10-21", 50000)])
        filas[-1]["ocurrido_en"] = "2026-10-21T23:00:00Z"
        with tempfile.TemporaryDirectory() as t:
            corrida = preparar_corrida(escribir_corrida(Path(t) / "c", filas, [serie(*A, True, "regular", 1)]))
            e = evaluar(corrida, CFG)
        self.assertEqual(e.por_serie[0].ocurrencias, 2)


class PruebasCorridaReal(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.corrida = preparar_corrida(carpeta())

    def test_toda_pareja_de_series_tiene_al_menos_dos_pagos(self):
        e = evaluar(self.corrida, CFG)
        self.assertEqual(len(e.por_serie), len(self.corrida.series))
        self.assertEqual(e.pares_sin_pagos, 0)
        self.assertGreaterEqual(min(s.ocurrencias for s in e.por_serie), 2)

    def test_claro_es_tp_y_movistar_es_tn_en_la_corrida_real(self):
        e = evaluar(self.corrida, CFG)
        claro = [s for s in e.por_serie if "Claro internet" in s.detalle]
        movistar = [s for s in e.por_serie if s.detalle.startswith("Tres pagos a la misma llave de telefonía")]
        self.assertEqual((len(claro), len(movistar)), (1, 1))
        self.assertTrue(claro[0].predicho)
        self.assertAlmostEqual(claro[0].r, 0.92, places=9)
        self.assertFalse(movistar[0].predicho)
        self.assertAlmostEqual(movistar[0].r, 0.40, places=9)

    def test_matrices_suman_el_numero_de_series(self):
        e = evaluar(self.corrida, CFG)
        g = e.global_
        self.assertEqual(g.tp + g.fp + g.tn + g.fn, len(self.corrida.series))
        self.assertEqual(sum(sum(dataclasses.astuple(m)) for m in e.por_motivo.values()), len(self.corrida.series))

    def test_la_evaluacion_es_determinista(self):
        self.assertEqual(evaluar(self.corrida, CFG), evaluar(self.corrida, CFG))


if __name__ == "__main__":
    unittest.main()
