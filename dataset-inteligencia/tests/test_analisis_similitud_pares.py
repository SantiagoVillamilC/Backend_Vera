"""Pruebas de los pares etiquetados para calibrar el umbral de similitud."""

import unittest
from pathlib import Path

from analisis.similitud_pares import Par, construir_pares

from .test_analisis_cargar import violaciones

COMERCIOS = [
    {"llave_destino": "K1", "empresa_id": "E1", "nombre_canonico": "RUTA VELOZ"},
    {"llave_destino": "K2", "empresa_id": "E2", "nombre_canonico": "RUTA VELOZ COMIDAS"},
    {"llave_destino": "K3", "empresa_id": "E3", "nombre_canonico": "TIENDAS D1 SUBA"},
    {"llave_destino": "K4", "empresa_id": "E3", "nombre_canonico": "D1 CHAPINERO"},
]
CORRECCIONES = [
    {"correccion_id": "C1", "llave_destino": "K2", "nombre_puesto": "RUTA VELOZ COM"},
    {"correccion_id": "C2", "llave_destino": "K2", "nombre_puesto": "RUTA VELOZ"},
]
CORRECCIONES_VERDAD = [
    {"correccion_id": "C1", "es_correcta": "1", "tipo_ruido": "abreviatura",
     "nombre_puesto_canonico": "RUTA VELOZ COMIDAS"},
    {"correccion_id": "C2", "es_correcta": "0", "tipo_ruido": "colusion",
     "nombre_puesto_canonico": "RUTA VELOZ"},
]


def _buscar(pares, a, b, origen):
    for p in pares:
        if p.origen == origen and {p.texto_a, p.texto_b} == {a, b}:
            return p
    raise AssertionError(f"no hay par {a!r} / {b!r} ({origen})")


class PruebaParesSimilitud(unittest.TestCase):
    def setUp(self):
        self.pares = construir_pares(COMERCIOS, CORRECCIONES, CORRECCIONES_VERDAD)

    def test_nombres_parecidos_de_empresas_distintas(self):
        p = _buscar(self.pares, "RUTA VELOZ", "RUTA VELOZ COMIDAS", "canonico")
        self.assertFalse(p.misma_llave)
        self.assertFalse(p.misma_empresa)
        self.assertEqual(p.tipo_ruido, "ninguno")

    def test_sucursales_misma_empresa_distinta_llave(self):
        p = _buscar(self.pares, "TIENDAS D1 SUBA", "D1 CHAPINERO", "canonico")
        self.assertFalse(p.misma_llave)
        self.assertTrue(p.misma_empresa)

    def test_correccion_con_abreviatura(self):
        p = _buscar(self.pares, "RUTA VELOZ COM", "RUTA VELOZ COMIDAS", "correccion")
        self.assertTrue(p.misma_llave)
        self.assertTrue(p.misma_empresa)
        self.assertEqual(p.tipo_ruido, "abreviatura")

    def test_correccion_de_colusor_es_ambigua(self):
        p = _buscar(self.pares, "RUTA VELOZ", "RUTA VELOZ COMIDAS", "correccion")
        self.assertTrue(p.ambiguo)
        self.assertFalse(p.misma_llave)
        self.assertEqual(p.tipo_ruido, "colusion")

    def test_ruido_de_escritura_es_positivo_aunque_el_texto_difiera(self):
        comercios = [{"llave_destino": "K2", "empresa_id": "E2", "nombre_canonico": "RUTA VELOZ COMIDAS"}]
        for tipo, texto in (("tipeo", "RUTA VELOS COMIDAS"), ("mayusculas", "ruta veloz comidas"),
                            ("abreviatura", "RUTA VELOZ COM")):
            with self.subTest(tipo=tipo):
                pares = construir_pares(
                    comercios, [{"correccion_id": "X", "llave_destino": "K2", "nombre_puesto": texto}],
                    [{"correccion_id": "X", "tipo_ruido": tipo, "nombre_puesto_canonico": texto}])
                p = _buscar(pares, texto, "RUTA VELOZ COMIDAS", "correccion")
                self.assertTrue(p.misma_llave)
                self.assertFalse(p.ambiguo)
                self.assertTrue(p.misma_empresa)

    def test_ruido_de_escritura_con_correccion_incorrecta_no_es_positivo_automatico(self):
        comercios = [{"llave_destino": "K2", "empresa_id": "E2", "nombre_canonico": "RUTA VELOZ COMIDAS"}]
        pares = construir_pares(
            comercios, [{"correccion_id": "X", "llave_destino": "K2", "nombre_puesto": "OTRO SITIO"}],
            [{"correccion_id": "X", "es_correcta": "false", "tipo_ruido": "mayusculas",
              "nombre_puesto_canonico": "OTRO SITIO"}])
        self.assertTrue(_buscar(pares, "OTRO SITIO", "RUTA VELOZ COMIDAS", "correccion").ambiguo)

    def test_ninguno_con_texto_igual_es_positivo(self):
        comercios = [{"llave_destino": "K2", "empresa_id": "E2", "nombre_canonico": "RUTA VELOZ COMIDAS"}]
        pares = construir_pares(
            comercios, [{"correccion_id": "X", "llave_destino": "K2", "nombre_puesto": "Ruta  Veloz Comidas"}],
            [{"correccion_id": "X", "tipo_ruido": "ninguno", "nombre_puesto_canonico": "x"}])
        p = _buscar(pares, "Ruta  Veloz Comidas", "RUTA VELOZ COMIDAS", "correccion")
        self.assertTrue(p.misma_llave)
        self.assertFalse(p.ambiguo)

    def test_los_ambiguos_no_son_positivos_ni_negativos(self):
        ambiguos = [p for p in self.pares if p.ambiguo]
        self.assertEqual(len(ambiguos), 1)
        self.assertFalse(any(p.ambiguo and p.misma_llave for p in self.pares))

    def test_determinismo_y_sin_duplicados(self):
        otra = construir_pares(list(reversed(COMERCIOS)), CORRECCIONES, CORRECCIONES_VERDAD)
        self.assertEqual(self.pares, construir_pares(COMERCIOS, CORRECCIONES, CORRECCIONES_VERDAD))
        self.assertEqual(self.pares, otra)
        claves = [(p.origen, p.tipo_ruido, p.misma_llave, frozenset((p.texto_a, p.texto_b)))
                  for p in self.pares]
        self.assertEqual(len(claves), len(set(claves)))
        self.assertIsInstance(self.pares[0], Par)

    def test_el_modulo_no_toca_la_carpeta_de_la_respuesta(self):
        ruta = Path(__file__).resolve().parent.parent / "analisis" / "similitud_pares.py"
        self.assertEqual(violaciones(ruta.read_text(encoding="utf-8")), [])


if __name__ == "__main__":
    unittest.main()
