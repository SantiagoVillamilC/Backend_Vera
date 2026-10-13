"""Pruebas de la línea base citable (regla de práctica de lyndonkl con la selección de cadencia fijada)."""

import dataclasses
import tempfile
import unittest
from datetime import date, datetime, timezone
from pathlib import Path

from analisis.configuracion import Configuracion
from analisis.evaluacion import evaluar, evaluar_con, preparar_corrida
from analisis.linea_base import REGLA_PRACTICA, ReglaPractica, decidir_linea_base, detectar_linea_base
from analisis.pagos import extraer_pagos
from analisis.recurrencia import detectar_agrupado

from .util_analisis import corrida_mini, fila, pagos_en

FIN = datetime(2026, 12, 31, 23, 59, 59, tzinfo=timezone.utc)
CLARO = [("2026-04-05", 89900), ("2026-05-05", 89900), ("2026-06-05", 89900), ("2026-07-05", 89900),
         ("2026-08-09", 89900), ("2026-09-05", 94900)]
MOVISTAR = [("2026-01-10", 20000), ("2026-02-20", 25000), ("2026-03-20", 50000)]
MENSUAL = ["2026-05-05", "2026-06-04", "2026-07-04", "2026-08-03"]  # intervalos de 30 días


def decision_de(fechas_y_montos, usuario="u", llave="0011111111", ahora=FIN):
    filas = pagos_en(usuario, llave, 1, fechas_y_montos)
    return detectar_linea_base(extraer_pagos(filas), REGLA_PRACTICA, ahora)[(usuario, ("llave", llave))]


def con_montos(fechas, montos):
    return list(zip(fechas, montos))


class PruebasReglaPorDefecto(unittest.TestCase):
    def test_valores_publicados(self):
        r = ReglaPractica()
        self.assertEqual(r.n_min, 3)
        self.assertEqual(r.fraccion_intervalos, 0.8)
        self.assertEqual(r.cv_max, 0.40)
        self.assertEqual(r.cadencias_dias, (7, 14, 30))
        self.assertEqual(r.bandas_dias, (2, 3, 4))
        self.assertEqual(REGLA_PRACTICA, r)

    def test_es_inmutable(self):
        with self.assertRaises(dataclasses.FrozenInstanceError):
            REGLA_PRACTICA.n_min = 2  # type: ignore[misc]


class PruebasEjemplosAMano(unittest.TestCase):
    def test_claro_pasa_con_4_de_5_intervalos_exactamente_en_el_limite(self):
        d = decision_de(CLARO)
        self.assertEqual((d.ocurrencias, d.intervalos, d.en_banda), (6, 5, 4))
        self.assertEqual(d.cadencia_dias, 30)
        self.assertTrue(d.cumple_ocurrencias and d.cumple_intervalos and d.cumple_cv)
        self.assertLess(d.cv, 0.05)
        self.assertTrue(d.es_recurrente)

    def test_movistar_falla_por_intervalos_y_por_cv(self):
        d = decision_de(MOVISTAR)
        self.assertEqual((d.ocurrencias, d.intervalos, d.en_banda), (3, 2, 1))
        self.assertTrue(d.cumple_ocurrencias)
        self.assertFalse(d.cumple_intervalos)
        self.assertFalse(d.cumple_cv)
        self.assertAlmostEqual(d.cv, 0.41443, places=4)
        self.assertFalse(d.es_recurrente)

    def test_no_hay_indice_r_en_la_linea_base(self):
        self.assertIsNone(decision_de(CLARO).r)


class PruebasCadaCriterioPorSeparado(unittest.TestCase):
    def test_dos_pagos_perfectos_no_alcanzan_las_3_ocurrencias(self):
        d = decision_de([("2026-05-05", 50000), ("2026-06-04", 50000)])
        self.assertFalse(d.cumple_ocurrencias)
        self.assertFalse(d.es_recurrente)

    def test_un_pago_no_es_recurrente(self):
        self.assertFalse(decision_de([("2026-05-05", 50000)]).es_recurrente)

    def test_tres_de_cuatro_intervalos_es_75_por_ciento_y_falla(self):
        fechas = ["2026-05-01", "2026-05-31", "2026-06-30", "2026-08-14", "2026-09-13"]  # 30, 30, 45, 30
        d = decision_de(con_montos(fechas, [50000] * 5))
        self.assertEqual((d.intervalos, d.en_banda), (4, 3))
        self.assertFalse(d.cumple_intervalos)
        self.assertTrue(d.cumple_cv)
        self.assertFalse(d.es_recurrente)

    def test_cv_exactamente_040_pasa_porque_el_limite_es_inclusivo(self):
        d = decision_de(con_montos(MENSUAL, [60, 140, 60, 140]))
        self.assertAlmostEqual(d.cv, 0.40, places=9)
        self.assertTrue(d.cumple_cv)
        self.assertTrue(d.es_recurrente)

    def test_cv_050_con_intervalos_perfectos_falla_solo_por_cv(self):
        d = decision_de(con_montos(MENSUAL, [50, 150, 50, 150]))
        self.assertTrue(d.cumple_ocurrencias and d.cumple_intervalos)
        self.assertFalse(d.cumple_cv)
        self.assertFalse(d.es_recurrente)

    def test_solo_los_intervalos_fallan(self):
        fechas = ["2026-05-01", "2026-05-21", "2026-06-10", "2026-06-30"]  # intervalos de 20 días
        d = decision_de(con_montos(fechas, [50000] * 4))
        self.assertEqual(d.cadencia_dias, 14)  # 20 queda más cerca de 14 que de 30
        self.assertTrue(d.cumple_ocurrencias and d.cumple_cv)
        self.assertFalse(d.cumple_intervalos)
        self.assertFalse(d.es_recurrente)

    def test_el_cv_usa_desviacion_poblacional(self):
        # Montos 100, 100, 200: media 133,33 y desviación poblacional 47,14 (CV 0,3536).
        # Con desviación muestral el CV sería 0,433 y fallaría.
        d = decision_de(con_montos(MENSUAL[:3], [100, 100, 200]))
        self.assertAlmostEqual(d.cv, 0.35355, places=4)
        self.assertTrue(d.cumple_cv)


class PruebasBandasDeCadencia(unittest.TestCase):
    def test_semanal_con_mas_menos_2_dias(self):
        fechas = ["2026-05-01", "2026-05-08", "2026-05-16", "2026-05-25"]  # 7, 8, 9
        d = decision_de(con_montos(fechas, [20000] * 4))
        self.assertEqual(d.cadencia_dias, 7)
        self.assertTrue(d.es_recurrente)

    def test_quincenal_con_mas_menos_3_dias_en_el_borde(self):
        fechas = ["2026-05-01", "2026-05-12", "2026-05-29", "2026-06-12"]  # 11, 17, 14
        d = decision_de(con_montos(fechas, [20000] * 4))
        self.assertEqual((d.cadencia_dias, d.en_banda), (14, 3))
        self.assertTrue(d.es_recurrente)

    def test_mensual_con_mas_menos_4_dias_en_el_borde(self):
        fechas = ["2026-05-01", "2026-05-27", "2026-06-30"]  # 26 y 34
        d = decision_de(con_montos(fechas, [20000] * 3))
        self.assertEqual((d.cadencia_dias, d.en_banda), (30, 2))
        self.assertTrue(d.es_recurrente)

    def test_un_dia_fuera_de_la_banda_no_cuenta(self):
        fechas = ["2026-05-01", "2026-05-26", "2026-06-30"]  # 25 y 35: a 5 días de 30
        d = decision_de(con_montos(fechas, [20000] * 3))
        self.assertEqual(d.en_banda, 0)

    def test_empate_entre_cadencias_elige_la_menor(self):
        fechas = ["2026-05-01", "2026-05-11", "2026-05-22"]  # 10 y 11, mediana 10,5
        self.assertEqual(decision_de(con_montos(fechas, [1000] * 3)).cadencia_dias, 7)


class PruebasMismosPagosQueElIndice(unittest.TestCase):
    def test_el_reloj_inyectado_excluye_pagos_posteriores(self):
        filas = pagos_en("u", "0011111111", 1, CLARO)
        ahora = datetime(2026, 5, 4, tzinfo=timezone.utc)
        d = detectar_linea_base(extraer_pagos(filas), REGLA_PRACTICA, ahora)[("u", ("llave", "0011111111"))]
        self.assertEqual(d.ocurrencias, 1)
        self.assertFalse(d.es_recurrente)

    def test_ahora_sin_zona_horaria_se_rechaza(self):
        with self.assertRaises(ValueError):
            detectar_linea_base(extraer_pagos(pagos_en("u", "0011111111", 1, CLARO)), REGLA_PRACTICA,
                                datetime(2026, 9, 4))

    def test_el_mcc_no_mezcla_llaves_distintas(self):
        a = pagos_en("u", "0011111111", 1, con_montos(MENSUAL[:3], [59900] * 3), mcc="4814", canal="qr")
        b = pagos_en("u", "0022222222", 10, [("2026-05-20", 20000), ("2026-06-02", 90000), ("2026-07-30", 33000)],
                     mcc="4814", canal="qr")
        r = detectar_linea_base(extraer_pagos(a + b), REGLA_PRACTICA, FIN)
        self.assertEqual(sorted(k[1] for k in r), [("llave", "0011111111"), ("llave", "0022222222")])
        self.assertTrue(r[("u", ("llave", "0011111111"))].es_recurrente)
        self.assertFalse(r[("u", ("llave", "0022222222"))].es_recurrente)

    def test_un_envio_devuelto_no_cuenta_como_pago(self):
        filas = pagos_en("u", "3072614021", 1, con_montos(MENSUAL[:3], [100000] * 3), tipo_asiento="despacho_riel",
                         tipo_llave="celular")
        devuelto = fila(10, "u", "2026-05-20T15:00:00Z", 100000, "3072614021", tipo_asiento="despacho_riel",
                        tipo_llave="celular", transferencia_id="t1")
        devolucion = fila(11, "u", "2026-05-21T15:00:00Z", 100000, "3072614021", tipo_asiento="compensacion",
                          direccion="entrada", tipo_llave="celular", transferencia_id="t1")
        d = detectar_linea_base(extraer_pagos(filas + [devuelto, devolucion]), REGLA_PRACTICA, FIN)
        self.assertEqual(d[("u", ("llave", "3072614021"))].ocurrencias, 3)

    def test_decidir_directo_con_fechas_y_montos_no_muta_nada(self):
        fechas, montos = [date(2026, 5, 5), date(2026, 6, 4), date(2026, 7, 4)], [100, 100, 100]
        copia = (list(fechas), list(montos))
        self.assertTrue(decidir_linea_base(fechas, montos, REGLA_PRACTICA).es_recurrente)
        self.assertEqual((fechas, montos), copia)


class PruebasEvaluarConUnDetectorCualquiera(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.corrida = preparar_corrida(corrida_mini(Path(self.dir.name) / "mini"))

    def test_linea_base_sobre_la_corrida_minima(self):
        e = evaluar_con(self.corrida, lambda grupos, ahora: {
            p: d for p, d in __import__("analisis.linea_base", fromlist=["x"]).detectar_linea_base_agrupado(
                grupos, REGLA_PRACTICA, ahora).items()})
        self.assertEqual(dataclasses.astuple(e.global_), (1, 0, 2, 1))  # A acierta, B se pierde, C y D son TN
        self.assertAlmostEqual(e.global_.f1, 2 / 3)
        self.assertTrue(all(s.r is None for s in e.por_serie))
        self.assertEqual(e.secundaria.parejas, 2)
        self.assertEqual(e.secundaria.falsos_positivos, 0)

    def test_evaluar_es_evaluar_con_el_indice(self):
        cfg = Configuracion.por_defecto()
        self.assertEqual(evaluar(self.corrida, cfg),
                         evaluar_con(self.corrida, lambda g, a: detectar_agrupado(g, cfg, a)))


if __name__ == "__main__":
    unittest.main()
