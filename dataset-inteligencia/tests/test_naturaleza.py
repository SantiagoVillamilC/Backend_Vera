"""Pruebas de la columna nombre_revela_naturaleza de verdad/comercios.csv."""

import re
import unittest

from .util import carpeta, encabezado, leer

# Lista de palabras escrita aquí solo para verificar. El generador NO la usa para etiquetar.
PALABRAS_DE_NATURALEZA = {
    "SUPERMERCADO", "MINIMERCADO", "PANADERIA", "FRUVER", "CARNICERIA", "RESTAURANTE", "ASADERO",
    "CAFETERIA", "PIZZERIA", "HELADERIA", "TAXIS", "PARQUEADERO", "ESTACION", "TRANSPORTES", "LAVADERO",
    "FERRETERIA", "LAVANDERIA", "TELECOMUNICACIONES", "SERVICIOS", "CERRAJERIA", "DROGUERIA", "CLINICA",
    "LABORATORIO", "CONSULTORIO", "OPTICA", "COLEGIO", "ACADEMIA", "PAPELERIA", "INSTITUTO", "LIBRERIA",
    "CINE", "BOLICHE", "GIMNASIO", "VIDEOJUEGOS", "TEATRO", "ALMACEN", "BOUTIQUE", "TECNOLOGIA", "CALZADO",
    "MUEBLES", "TIENDA", "TIENDAS", "SUPERTIENDAS", "COMIDAS", "TELEFONIA", "ENERGIA",
}


def comercios(base):
    return [c for c in leer(base, "verdad/comercios.csv") if c["es_comercio"] == "true"]


def palabras(nombre_canonico: str) -> set[str]:
    return set(re.split(r"-", nombre_canonico))


def apartado_de_prueba(filas):
    """Comercios del conjunto de prueba, según la columna explícita `conjunto`."""
    return [c for c in filas if c["conjunto"] == "prueba"]


def proporcion(filas) -> float:
    return sum(1 for c in filas if c["nombre_revela_naturaleza"] == "true") / len(filas)


class PruebasNombreRevelaNaturaleza(unittest.TestCase):
    def test_la_columna_existe_y_es_booleana(self):
        base = carpeta()
        self.assertIn("nombre_revela_naturaleza", encabezado(base, "verdad/comercios.csv"))
        for c in leer(base, "verdad/comercios.csv"):
            self.assertIn(c["nombre_revela_naturaleza"], ("true", "false"))
            if c["es_comercio"] == "false":
                self.assertEqual(c["nombre_revela_naturaleza"], "false")

    def test_cerca_de_la_mitad_en_el_catalogo(self):
        for filas in (3000, 1200):
            self.assertTrue(0.40 <= proporcion(comercios(carpeta(filas=filas))) <= 0.60, filas)

    def test_ambos_valores_y_cerca_de_la_mitad_en_el_apartado_de_prueba(self):
        for filas in (3000, 1200):
            prueba = apartado_de_prueba(comercios(carpeta(filas=filas)))
            self.assertGreaterEqual(len(prueba), 20)
            valores = {c["nombre_revela_naturaleza"] for c in prueba}
            self.assertEqual(valores, {"true", "false"})
            self.assertTrue(0.35 <= proporcion(prueba) <= 0.65, (filas, proporcion(prueba)))

    def test_coincide_con_la_construccion_del_nombre(self):
        """Los que revelan traen una palabra de naturaleza y los de marca no traen ninguna."""
        for c in comercios(carpeta()):
            tiene = bool(palabras(c["nombre_canonico"]) & PALABRAS_DE_NATURALEZA)
            self.assertEqual(c["nombre_revela_naturaleza"] == "true", tiene, c["nombre_canonico"])

    def test_se_mantiene_con_otra_semilla(self):
        base = carpeta(semilla=99)
        self.assertTrue(0.40 <= proporcion(comercios(base)) <= 0.60)


if __name__ == "__main__":
    unittest.main()
