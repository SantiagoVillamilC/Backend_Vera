"""Pruebas de canonizar y mostrar, y de su uso en los datos."""

import random
import unittest

from generador.nombres import canonizar, mostrar
from generador.ruido import variante_formato

from .util import carpeta, correcciones_completas, leer


class PruebasCanonizar(unittest.TestCase):
    def test_tres_formas_dan_la_misma_clave(self):
        for crudo in ("SUPERMERCADO_don_pepe", "supermercado don pepe", "SUPERMERCADO-DON-PEPE"):
            self.assertEqual(canonizar(crudo), "SUPERMERCADO-DON-PEPE", crudo)

    def test_idempotente(self):
        casos = ("SUPERMERCADO_don_pepe", "  --Éxito   Calle 80--  ", "McDonald's", "A - . B", "L. & Hijos",
                 "ñandú  _ año", "x" * 80, "a-" * 30, "---", "", "Tienda Doña Lucy")
        for caso in casos:
            self.assertEqual(canonizar(canonizar(caso)), canonizar(caso), caso)

    def test_ejemplo_mcdonalds(self):
        self.assertEqual(canonizar("McDonald's"), "MCDONALDS")

    def test_reglas_una_por_una(self):
        self.assertEqual(canonizar("a   b__c--d"), "A-B-C-D")
        self.assertEqual(canonizar("--hola--"), "HOLA")
        self.assertEqual(canonizar("Éxito Usaquén"), "EXITO-USAQUEN")
        self.assertEqual(canonizar("Doña Niño"), "DOÑA-NIÑO")
        self.assertEqual(canonizar("a.b&c'd"), "ABCD")

    def test_recorta_a_40(self):
        self.assertEqual(len(canonizar("A" * 100)), 40)
        self.assertFalse(canonizar("palabra " * 20).endswith("-"))

    def test_los_typos_y_abreviaturas_siguen_siendo_claves_distintas(self):
        base = canonizar("Supermercado Don Pepe")
        self.assertNotEqual(canonizar("SUPERMERC DON PEPE"), base)
        self.assertNotEqual(canonizar("Supermercdo Don Pepe"), base)

    def test_mostrar_reemplaza_guiones_por_espacios(self):
        self.assertEqual(mostrar("SUPERMERCADO-DON-PEPE"), "SUPERMERCADO DON PEPE")
        self.assertEqual(mostrar(canonizar("tienda doña lucy")), "TIENDA DOÑA LUCY")


class PruebasFormatoTecleado(unittest.TestCase):
    def test_las_variantes_de_formato_colapsan_a_la_misma_clave(self):
        azar = random.Random(11)
        vistos = set()
        for _ in range(200):
            texto = variante_formato("TIENDA DOÑA LUCY", azar)
            vistos.add(texto)
            self.assertEqual(canonizar(texto), "TIENDA-DOÑA-LUCY", texto)
        self.assertTrue(any("_" in t for t in vistos))
        self.assertTrue(any("-" in t for t in vistos))
        self.assertTrue(any(t.islower() for t in vistos))


class PruebasCanonizarEnLosDatos(unittest.TestCase):
    def test_la_verdad_guarda_nombres_canonizados(self):
        base = carpeta()
        for c in leer(base, "verdad/comercios.csv"):
            self.assertEqual(canonizar(c["nombre_canonico"]), c["nombre_canonico"])
        for h in leer(base, "verdad/nombres_historial.csv"):
            self.assertEqual(canonizar(h["nombre_canonico"]), h["nombre_canonico"])

    def test_nombre_puesto_conserva_el_crudo_y_la_columna_nueva_es_su_canonico(self):
        base = carpeta()
        correcciones = correcciones_completas(base)
        crudos_no_canonicos = [c for c in correcciones if c["nombre_puesto"] != c["nombre_puesto_canonico"]]
        self.assertGreater(len(crudos_no_canonicos), 20)
        for c in correcciones:
            self.assertEqual(c["nombre_puesto_canonico"], canonizar(c["nombre_puesto"]))

    def test_el_ruido_de_formato_aparece_en_los_nombres_crudos(self):
        crudos = [c["nombre_puesto"] for c in correcciones_completas(carpeta())]
        self.assertTrue(any("_" in n for n in crudos))
        self.assertTrue(any(n.islower() for n in crudos))
        self.assertTrue(any("-" in n for n in crudos))
        self.assertTrue(any(n.isupper() and " " in n for n in crudos))

    def test_la_columna_nueva_esta_en_el_encabezado(self):
        base = carpeta()
        filas = leer(base, "verdad/correcciones_verdad.csv")
        self.assertEqual(list(filas[0]), ["correccion_id", "es_correcta", "tipo_ruido", "nombre_puesto_canonico"])


if __name__ == "__main__":
    unittest.main()
