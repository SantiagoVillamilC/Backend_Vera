"""Pruebas de las llaves: intersección norma y código, sin TIENDAD1BOG01."""

import random
import unittest

from generador.llaves import GeneradorLlaves

from .util import carpeta, leer, llave_valida

TIPOS = ("comercio", "celular", "alfanumerico", "documento", "correo")


class PruebasGeneradorLlaves(unittest.TestCase):
    def test_cada_tipo_cumple_norma_y_codigo(self):
        gen = GeneradorLlaves(random.Random(7))
        for tipo in TIPOS:
            for _ in range(500):
                llave = gen.nueva(tipo)
                self.assertTrue(llave_valida(tipo, llave), (tipo, llave))

    def test_son_unicas(self):
        gen = GeneradorLlaves(random.Random(8))
        llaves = [gen.nueva(t) for t in TIPOS for _ in range(300)]
        self.assertEqual(len(llaves), len(set(llaves)))

    def test_llave_de_ejemplo_no_es_valida(self):
        for tipo in TIPOS:
            self.assertFalse(llave_valida(tipo, "TIENDAD1BOG01"))


class PruebasLlavesEnLosDatos(unittest.TestCase):
    def test_toda_llave_de_movimientos_cumple(self):
        base = carpeta()
        for m in leer(base, "entrada/movimientos.csv"):
            if m["llave_destino"]:
                self.assertTrue(llave_valida(m["tipo_llave"], m["llave_destino"]), m)
            else:
                self.assertEqual(m["tipo_llave"], "")

    def test_toda_llave_de_la_verdad_cumple(self):
        base = carpeta()
        for c in leer(base, "verdad/comercios.csv"):
            self.assertTrue(llave_valida(c["clase_llave"], c["llave_destino"]), c)
            self.assertEqual(c["clase_llave"] == "comercio", c["es_comercio"] == "true")

    def test_llaves_de_correcciones_existen_en_la_verdad(self):
        base = carpeta()
        conocidas = {c["llave_destino"] for c in leer(base, "verdad/comercios.csv")}
        for c in leer(base, "entrada/correcciones.csv"):
            self.assertIn(c["llave_destino"], conocidas)

    def test_tiendad1bog01_no_aparece(self):
        base = carpeta()
        for ruta in sorted(base.rglob("*")):
            if ruta.is_file():
                self.assertNotIn("TIENDAD1BOG01", ruta.read_text(encoding="utf-8"), ruta.name)

    def test_se_ven_todos_los_tipos_de_llave(self):
        base = carpeta()
        tipos = {m["tipo_llave"] for m in leer(base, "entrada/movimientos.csv")}
        self.assertTrue({"comercio", "celular"} <= tipos, tipos)


if __name__ == "__main__":
    unittest.main()
