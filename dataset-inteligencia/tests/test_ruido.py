"""Pruebas unitarias del enmascarado y del ruido de nombres."""

import random
import unittest

from generador import ruido

TIPOS = {"mayusculas", "tildes", "abreviatura", "tipeo"}


class PruebasEnmascarado(unittest.TestCase):
    def test_ejemplo_de_la_especificacion(self):
        self.assertEqual(ruido.enmascarar_nombre("Andrea Pérez"), "AnXXXa PXXez")

    def test_palabras_cortas(self):
        self.assertEqual(ruido.enmascarar_nombre("D1"), "D1")
        self.assertEqual(ruido.enmascarar_nombre("Ana"), "AXa")

    def test_conserva_el_largo(self):
        for nombre in ("Supermercado Don Pepe", "ÉXITO CALLE 80", "Ñoño Ríos"):
            self.assertEqual(len(ruido.enmascarar_nombre(nombre)), len(nombre))


class PruebasRuido(unittest.TestCase):
    def test_quitar_tildes(self):
        self.assertEqual(ruido.quitar_tildes("ÉXITO USAQUÉN"), "EXITO USAQUEN")

    def test_sin_ruido_si_la_probabilidad_es_cero(self):
        azar = random.Random(1)
        for _ in range(50):
            self.assertEqual(ruido.variar_nombre("DROGUERÍA AURORA CHAPINERO", azar, 0.0),
                             ("DROGUERÍA AURORA CHAPINERO", "ninguno"))

    def test_con_ruido_siempre_cambia_y_declara_el_tipo(self):
        azar = random.Random(2)
        for _ in range(300):
            texto, tipo = ruido.variar_nombre("DROGUERÍA AURORA CHAPINERO", azar, 1.0)
            self.assertIn(tipo, TIPOS)
            self.assertNotEqual(texto, "DROGUERÍA AURORA CHAPINERO")

    def test_tipo_tildes_no_deja_tildes(self):
        azar = random.Random(3)
        vistos = set()
        for _ in range(400):
            texto, tipo = ruido.variar_nombre("ÉXITO USAQUÉN", azar, 1.0)
            vistos.add(tipo)
            if tipo == "tildes":
                self.assertEqual(texto, "EXITO USAQUEN")
        self.assertGreaterEqual(len(vistos), 3)

    def test_nombre_qr_respeta_el_limite(self):
        azar = random.Random(4)
        for nombre in ("D1 CHAPINERO", "SUPERTIENDAS OLÍMPICA IBAGUÉ", "Supermercado Don Pepe"):
            for _ in range(100):
                qr = ruido.nombre_qr(nombre, azar)
                self.assertTrue(0 < len(qr) <= 25, qr)
                self.assertEqual(qr, qr.upper())
                self.assertEqual(qr, ruido.quitar_tildes(qr))


if __name__ == "__main__":
    unittest.main()
