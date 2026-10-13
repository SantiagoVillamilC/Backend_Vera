"""Pruebas de la calibración del umbral de similitud con pares escritos a mano."""

import unittest

from analisis.similitud_calibracion import (CRITERIO_MAX_F1, CRITERIO_RECALL_CON_PRECISION, PRECISION_MINIMA,
                                            UMBRALES, barrer, elegir_umbral, evaluar_umbral_fijo)
from analisis.similitud_pares import Par

TABLA = {
    ("A", "A1"): 0.95, ("B", "B1"): 0.80, ("C", "C1"): 0.60,   # positivos
    ("X", "Y"): 0.55, ("P", "Q"): 0.30, ("R", "S"): 0.97,      # negativos (el último es difícil)
}


def puntaje(a: str, b: str) -> float:
    return TABLA[(a, b)]


def par(a: str, b: str, positivo: bool) -> Par:
    return Par(a, b, positivo, positivo, "correccion", "ninguno")


PARES = [par("A", "A1", True), par("B", "B1", True), par("C", "C1", True),
         par("X", "Y", False), par("P", "Q", False)]
PARES_CON_DIFICIL = PARES + [par("R", "S", False)]


class PruebasBarrido(unittest.TestCase):
    def test_rejilla_de_050_a_099_en_pasos_de_001(self):
        self.assertEqual(len(UMBRALES), 50)
        self.assertEqual(UMBRALES[0], 0.50)
        self.assertEqual(UMBRALES[-1], 0.99)

    def test_recall_es_monotono_al_bajar_el_umbral(self):
        medidas = barrer(PARES_CON_DIFICIL, puntaje)
        recalls = [m.recall for m in sorted(medidas, key=lambda m: m.umbral, reverse=True)]
        self.assertEqual(recalls, sorted(recalls))

    def test_conteos_en_un_umbral_conocido(self):
        m = {x.umbral: x for x in barrer(PARES, puntaje)}[0.70]
        self.assertEqual((m.tp, m.fp, m.fn), (2, 0, 1))
        self.assertAlmostEqual(m.precision, 1.0)
        self.assertAlmostEqual(m.recall, 2 / 3)

    def test_sin_predicciones_la_precision_es_vacua_y_el_recall_cero(self):
        m = {x.umbral: x for x in barrer(PARES, puntaje)}[0.99]
        self.assertEqual((m.tp, m.fp, m.fn), (0, 0, 3))
        self.assertEqual((m.precision, m.recall, m.f1), (1.0, 0.0, 0.0))

    def test_los_pares_ambiguos_quedan_fuera_de_la_calibracion(self):
        ambiguo = Par("R", "S", False, False, "correccion", "colusor", ambiguo=True)
        self.assertEqual(barrer(PARES + [ambiguo], puntaje), barrer(PARES, puntaje))

    def test_determinismo(self):
        self.assertEqual(barrer(PARES_CON_DIFICIL, puntaje), barrer(list(PARES_CON_DIFICIL), puntaje))

    def test_el_puntaje_recibe_los_textos_canonizados(self):
        vistos = []

        def espia(a, b):
            vistos.append((a, b))
            return 0.9

        barrer([Par("Ruta  Véloz", "RUTA VELOZ", True, True, "correccion", "ninguno")], espia)
        self.assertEqual(vistos[0][0], vistos[0][1])


class PruebasElegirUmbral(unittest.TestCase):
    def test_max_f1_conocido_de_antemano(self):
        # Sin el negativo difícil, el mejor F1 es 1.0 y el umbral más alto que lo logra es 0.60.
        medidas = barrer(PARES, puntaje)
        elegido = elegir_umbral(medidas, CRITERIO_MAX_F1)
        self.assertEqual(elegido.umbral, 0.60)
        self.assertAlmostEqual(elegido.f1, 1.0)

    def test_criterio_b_respeta_la_precision_minima(self):
        medidas = barrer(PARES_CON_DIFICIL, puntaje)
        elegido = elegir_umbral(medidas, CRITERIO_RECALL_CON_PRECISION)
        self.assertGreaterEqual(elegido.precision, PRECISION_MINIMA)
        # El negativo difícil (0.97) obliga a subir el umbral por encima de 0.97. Con recall 0 en
        # todos los admisibles, el empate se resuelve por el umbral más alto.
        self.assertEqual(elegido.umbral, 0.99)
        self.assertEqual(elegido.tp, 0)

    def test_criterio_b_maximiza_recall_entre_los_admisibles(self):
        elegido = elegir_umbral(barrer(PARES, puntaje), CRITERIO_RECALL_CON_PRECISION)
        self.assertEqual(elegido.umbral, 0.60)
        self.assertAlmostEqual(elegido.recall, 1.0)

    def test_criterio_desconocido_falla(self):
        with self.assertRaises(ValueError):
            elegir_umbral(barrer(PARES, puntaje), "otro")


class PruebasValidacion(unittest.TestCase):
    def test_evalua_el_umbral_fijo_en_otra_semilla(self):
        m = evaluar_umbral_fijo(PARES_CON_DIFICIL, puntaje, 0.90)
        self.assertEqual((m.tp, m.fp, m.fn), (1, 1, 2))
        self.assertEqual(m.umbral, 0.90)

    def test_umbral_none_equivale_a_abstenerse(self):
        m = evaluar_umbral_fijo(PARES, puntaje, None)
        self.assertEqual((m.tp, m.fp, m.fn), (0, 0, 3))


if __name__ == "__main__":
    unittest.main()
