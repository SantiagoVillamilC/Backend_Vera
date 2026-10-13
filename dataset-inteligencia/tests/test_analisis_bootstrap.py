"""Pruebas del bootstrap percentil pareado para la diferencia de F1."""

import random
import unittest

from analisis.bootstrap import f1_de, frase, generar_remuestreos, indices_percentil, intervalo_pareado


def resultados(aciertos: int, errores: int):
    """Series con 1 positivo real cada una: acierto es TP, error es FN."""
    return [(True, True)] * aciertos + [(False, True)] * errores


class PruebasBootstrap(unittest.TestCase):
    def test_indices_de_percentil_por_rango_mas_cercano(self):
        self.assertEqual(indices_percentil(2000), (49, 1949))
        self.assertEqual(indices_percentil(100), (2, 97))

    def test_f1_sobre_indices(self):
        datos = [(True, True), (True, False), (False, True), (False, False)]
        self.assertAlmostEqual(f1_de(datos, [0, 1, 2, 3]), 2 / (2 + 1 + 1))
        self.assertEqual(f1_de(datos, [3, 3]), 0.0)

    def test_remuestreos_deterministas_y_con_la_semilla_documentada(self):
        a = generar_remuestreos(10, 5, 2026, "c1")
        self.assertEqual(a, generar_remuestreos(10, 5, 2026, "c1"))
        self.assertNotEqual(a, generar_remuestreos(10, 5, 2026, "c2"))
        rng = random.Random("2026:c1")
        self.assertEqual(a[0], [rng.randrange(10) for _ in range(10)])
        self.assertTrue(all(len(v) == 10 and all(0 <= i < 10 for i in v) for v in a))

    def test_metodos_identicos_dan_intervalo_cero_que_incluye_cero(self):
        datos = resultados(60, 40)
        r = generar_remuestreos(100, 200, 2026, "c")
        i = intervalo_pareado(datos, datos, r)
        self.assertEqual((i.observada, i.media, i.inferior, i.superior), (0.0, 0.0, 0.0, 0.0))
        self.assertIn("incluye 0", frase(i, "la línea base"))

    def test_metodo_claramente_mejor_excluye_cero(self):
        r = generar_remuestreos(100, 300, 2026, "c")
        i = intervalo_pareado(resultados(95, 5), resultados(40, 60), r)
        self.assertGreater(i.observada, 0)
        self.assertGreater(i.inferior, 0)
        self.assertEqual(frase(i, "la línea base"), "el intervalo no incluye 0")
        self.assertLessEqual(i.inferior, i.media)
        self.assertLessEqual(i.media, i.superior)

    def test_frases_fijas(self):
        r = generar_remuestreos(100, 50, 2026, "c")
        igual = intervalo_pareado(resultados(60, 40), resultados(60, 40), r)
        self.assertEqual(frase(igual, "la línea base"), "el intervalo incluye 0 (no se distingue de la línea base)")
        self.assertEqual(frase(igual, "el restringido"), "el intervalo incluye 0 (no se distingue del restringido)")

    def test_misma_semilla_mismo_intervalo(self):
        r = generar_remuestreos(100, 100, 2026, "c")
        self.assertEqual(intervalo_pareado(resultados(80, 20), resultados(70, 30), r),
                         intervalo_pareado(resultados(80, 20), resultados(70, 30), r))

    def test_longitudes_distintas_se_rechazan(self):
        with self.assertRaises(ValueError):
            intervalo_pareado(resultados(5, 5), resultados(5, 4), generar_remuestreos(10, 5, 1, "c"))


if __name__ == "__main__":
    unittest.main()
