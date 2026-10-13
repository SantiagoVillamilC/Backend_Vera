"""Partición explícita entrenamiento y prueba de verdad/comercios.csv."""

import unittest

from .util import carpeta, encabezado, leer

SEMILLAS = (2026, 1, 7)


def comercios(base):
    return [c for c in leer(base, "verdad/comercios.csv") if c["es_comercio"] == "true"]


def tasa_true(filas) -> float:
    return sum(1 for c in filas if c["nombre_revela_naturaleza"] == "true") / len(filas)


class PruebasParticion(unittest.TestCase):
    def test_la_columna_conjunto_es_la_ultima_y_solo_la_llevan_los_comercios(self):
        base = carpeta(filas=3000)
        self.assertEqual(encabezado(base, "verdad/comercios.csv")[-2:], ["nombre_revela_naturaleza", "conjunto"])
        for c in leer(base, "verdad/comercios.csv"):
            if c["es_comercio"] == "true":
                self.assertIn(c["conjunto"], ("entrenamiento", "prueba"))
            else:
                self.assertEqual(c["conjunto"], "")

    def test_30_por_ciento_en_prueba_mas_menos_2_puntos(self):
        for semilla in SEMILLAS:
            filas = comercios(carpeta(filas=3000, semilla=semilla))
            prueba = [c for c in filas if c["conjunto"] == "prueba"]
            self.assertTrue(0.28 <= len(prueba) / len(filas) <= 0.32, (semilla, len(prueba), len(filas)))

    def test_balance_de_naturaleza_difiere_menos_de_5_puntos(self):
        for semilla in SEMILLAS:
            filas = comercios(carpeta(filas=3000, semilla=semilla))
            prueba = [c for c in filas if c["conjunto"] == "prueba"]
            entrenamiento = [c for c in filas if c["conjunto"] == "entrenamiento"]
            self.assertLess(abs(tasa_true(prueba) - tasa_true(entrenamiento)), 0.05, semilla)
            self.assertTrue(0.40 <= tasa_true(prueba) <= 0.60, (semilla, tasa_true(prueba)))

    def test_ambos_valores_de_naturaleza_estan_en_los_dos_conjuntos(self):
        filas = comercios(carpeta(filas=3000))
        for conjunto in ("entrenamiento", "prueba"):
            valores = {c["nombre_revela_naturaleza"] for c in filas if c["conjunto"] == conjunto}
            self.assertEqual(valores, {"true", "false"}, conjunto)

    def test_la_particion_depende_de_la_semilla_y_no_de_la_posicion(self):
        a = {c["llave_destino"]: c["conjunto"] for c in comercios(carpeta(filas=3000, semilla=2026))}
        b = {c["llave_destino"]: c["conjunto"] for c in comercios(carpeta(filas=3000, semilla=1))}
        self.assertNotEqual(sorted(k for k, v in a.items() if v == "prueba"),
                            sorted(k for k, v in b.items() if v == "prueba"))
        ordenadas = sorted(a)
        posiciones = [i % 10 < 3 for i, k in enumerate(ordenadas) if a[k] == "prueba"]
        self.assertFalse(all(posiciones), "no debe ser el criterio de posiciones 0, 1 y 2 de cada 10")

    def test_misma_particion_en_las_dos_variantes(self):
        a = leer(carpeta(filas=3000, variante="A"), "verdad/comercios.csv")
        b = leer(carpeta(filas=3000, variante="B"), "verdad/comercios.csv")
        self.assertEqual(a, b)

    def test_funciona_con_pocas_filas(self):
        filas = comercios(carpeta(filas=391))
        prueba = [c for c in filas if c["conjunto"] == "prueba"]
        self.assertTrue(0.28 <= len(prueba) / len(filas) <= 0.32)


if __name__ == "__main__":
    unittest.main()
