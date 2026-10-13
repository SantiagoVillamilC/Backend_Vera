"""Pruebas de la configuración del detector de recurrencia: valores por defecto, carga y validación."""

import dataclasses
import json
import tempfile
import unittest
from pathlib import Path

from analisis.configuracion import Configuracion, ConfiguracionInvalida, cargar_configuracion, desde_dict


class PruebasConfiguracion(unittest.TestCase):
    def test_valores_por_defecto_del_json(self):
        c = Configuracion.por_defecto()
        self.assertEqual(c.w_monto, 0.6)
        self.assertEqual(c.w_fecha, 0.4)
        self.assertEqual(c.tol_monto, 0.10)
        self.assertEqual(c.cadencias_dias, (7, 14, 30))
        self.assertEqual(c.tolerancias_dias, (2, 3, 4))
        self.assertEqual(c.n_min, 3)
        self.assertEqual(c.umbral, 0.5)

    def test_es_inmutable(self):
        c = Configuracion.por_defecto()
        with self.assertRaises(dataclasses.FrozenInstanceError):
            c.umbral = 0.9  # type: ignore[misc]

    def test_ida_y_vuelta_por_json(self):
        c = Configuracion.por_defecto()
        with tempfile.TemporaryDirectory() as t:
            ruta = Path(t) / "c.json"
            ruta.write_text(json.dumps(c.a_dict()), encoding="utf-8")
            self.assertEqual(cargar_configuracion(ruta), c)

    def test_hash_estable_y_sensible_al_contenido(self):
        c = Configuracion.por_defecto()
        self.assertEqual(c.hash(), Configuracion.por_defecto().hash())
        self.assertEqual(len(c.hash()), 64)
        self.assertNotEqual(c.hash(), dataclasses.replace(c, umbral=0.6).hash())

    def test_factor_de_tolerancia_devuelve_un_objeto_nuevo(self):
        c = Configuracion.por_defecto()
        d = c.con_factor_tolerancia(1.5)
        self.assertEqual(d.tolerancias_dias, (3.0, 4.5, 6.0))
        self.assertEqual(c.tolerancias_dias, (2, 3, 4))
        self.assertIsNot(c, d)


class PruebasValidacion(unittest.TestCase):
    def base(self) -> dict:
        return Configuracion.por_defecto().a_dict()

    def assertInvalida(self, cambios: dict, fragmento: str):
        with self.assertRaises(ConfiguracionInvalida) as cm:
            desde_dict({**self.base(), **cambios})
        self.assertIn(fragmento, str(cm.exception))

    def test_peso_negativo(self):
        self.assertInvalida({"w_monto": -0.2, "w_fecha": 1.2}, "w_monto")

    def test_pesos_que_no_suman_uno(self):
        self.assertInvalida({"w_monto": 0.5, "w_fecha": 0.4}, "sumar 1")

    def test_tolerancia_de_monto_no_positiva(self):
        self.assertInvalida({"tol_monto": 0}, "tol_monto")

    def test_tolerancias_de_dias_no_positivas(self):
        self.assertInvalida({"tolerancias_dias": [2, 0, 4]}, "tolerancias_dias")

    def test_cadencias_y_tolerancias_de_distinta_longitud(self):
        self.assertInvalida({"tolerancias_dias": [2, 3]}, "misma longitud")

    def test_cadencias_vacias_o_repetidas(self):
        self.assertInvalida({"cadencias_dias": [], "tolerancias_dias": []}, "cadencias_dias")
        self.assertInvalida({"cadencias_dias": [7, 7, 30]}, "repetidas")

    def test_n_min_menor_que_uno_o_no_entero(self):
        self.assertInvalida({"n_min": 0}, "n_min")
        self.assertInvalida({"n_min": 2.5}, "n_min")

    def test_umbral_fuera_de_rango(self):
        self.assertInvalida({"umbral": 1.5}, "umbral")

    def test_claves_faltantes_y_desconocidas(self):
        datos = self.base()
        del datos["umbral"]
        with self.assertRaises(ConfiguracionInvalida) as cm:
            desde_dict(datos)
        self.assertIn("umbral", str(cm.exception))
        self.assertInvalida({"inventada": 1}, "inventada")

    def test_tipos_incorrectos(self):
        self.assertInvalida({"umbral": "0.5"}, "umbral")
        self.assertInvalida({"n_min": True}, "n_min")

    def test_reporta_todos_los_problemas_a_la_vez(self):
        with self.assertRaises(ConfiguracionInvalida) as cm:
            desde_dict({**self.base(), "umbral": 2, "n_min": 0})
        mensaje = str(cm.exception)
        self.assertIn("umbral", mensaje)
        self.assertIn("n_min", mensaje)

    def test_json_ilegible(self):
        with tempfile.TemporaryDirectory() as t:
            ruta = Path(t) / "roto.json"
            ruta.write_text("{no es json", encoding="utf-8")
            with self.assertRaises(ConfiguracionInvalida):
                cargar_configuracion(ruta)


if __name__ == "__main__":
    unittest.main()
