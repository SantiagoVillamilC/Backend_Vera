"""Pruebas de analisis.similitud: Jaro-Winkler y coseno TF-IDF de trigramas.

Las funciones reciben texto ya normalizado. Aqui no se fija ningun umbral:
los casos del negativo dificil son solo documentales.
"""

from __future__ import annotations

import unittest
from types import MappingProxyType

from analisis.similitud import (
    ajustar_idf,
    coseno_tfidf,
    jaro,
    jaro_winkler,
    trigramas,
)

CORPUS_D1 = (
    "TIENDAS D1 SUBA",
    "TIENDAS D1 KENNEDY",
    "TIENDAS D1 BOSA",
    "TIENDAS D1 USME",
    "TIENDAS D1 FONTIBON",
    "TIENDAS D1 ENGATIVA",
    "FARMACIA CENTRAL",
    "RUTA VELOZ",
)


def _idf_uniforme(corpus: tuple[str, ...]) -> MappingProxyType:
    claves = {t for texto in corpus for t in trigramas(texto)}
    return MappingProxyType({t: 1.0 for t in claves})


class JaroWinklerTest(unittest.TestCase):
    def test_ejemplos_publicados(self) -> None:
        self.assertAlmostEqual(jaro_winkler("MARTHA", "MARHTA"), 0.961, places=3)
        self.assertAlmostEqual(jaro_winkler("DWAYNE", "DUANE"), 0.84, places=3)
        self.assertAlmostEqual(jaro_winkler("DIXON", "DICKSONX"), 0.813, places=3)

    def test_jaro_sin_prefijo(self) -> None:
        self.assertAlmostEqual(jaro("MARTHA", "MARHTA"), 0.944, places=3)

    def test_identicos_dan_uno(self) -> None:
        self.assertEqual(jaro("ABC", "ABC"), 1.0)
        self.assertEqual(jaro_winkler("ABC", "ABC"), 1.0)

    def test_vacias_dan_cero(self) -> None:
        for a, b in (("", ""), ("", "ABC"), ("ABC", "")):
            self.assertEqual(jaro(a, b), 0.0)
            self.assertEqual(jaro_winkler(a, b), 0.0)

    def test_sin_coincidencias_da_cero(self) -> None:
        self.assertEqual(jaro("ABC", "XYZ"), 0.0)

    def test_simetria(self) -> None:
        for a, b in (("MARTHA", "MARHTA"), ("DIXON", "DICKSONX"), ("AB", "ABCDEF")):
            self.assertAlmostEqual(jaro(a, b), jaro(b, a))
            self.assertAlmostEqual(jaro_winkler(a, b), jaro_winkler(b, a))

    def test_rango(self) -> None:
        self.assertTrue(0.0 <= jaro_winkler("RUTA VELOZ", "RUTA VELOZ COM") <= 1.0)


class TrigramasTest(unittest.TestCase):
    def test_relleno_con_espacios(self) -> None:
        self.assertEqual(trigramas("AB"), (" AB", "AB "))

    def test_conserva_repetidos(self) -> None:
        self.assertEqual(trigramas("AAA").count("AAA"), 1)
        self.assertEqual(trigramas("AAAA").count("AAA"), 2)

    def test_vacio_sin_trigramas(self) -> None:
        self.assertEqual(trigramas(""), ())

    def test_n_distinto(self) -> None:
        self.assertEqual(trigramas("AB", n=2), (" A", "AB", "B "))

    def test_no_normaliza(self) -> None:
        self.assertEqual(trigramas("aB"), (" aB", "aB "))


class AjustarIdfTest(unittest.TestCase):
    def test_inmutable(self) -> None:
        idf = ajustar_idf(CORPUS_D1)
        with self.assertRaises(TypeError):
            idf["XXX"] = 1.0  # type: ignore[index]

    def test_formula_suavizada(self) -> None:
        import math

        idf = ajustar_idf(("AB", "AB", "CD"))
        self.assertAlmostEqual(idf[" AB"], math.log(4 / 3) + 1)
        self.assertAlmostEqual(idf[" CD"], math.log(4 / 2) + 1)

    def test_corpus_vacio(self) -> None:
        self.assertEqual(len(ajustar_idf(())), 0)

    def test_frecuente_pesa_menos(self) -> None:
        idf = ajustar_idf(CORPUS_D1)
        self.assertLess(idf[" D1"], idf[" FA"])


class CosenoTfidfTest(unittest.TestCase):
    def setUp(self) -> None:
        self.idf = ajustar_idf(CORPUS_D1)

    def test_identicos_dan_uno(self) -> None:
        self.assertAlmostEqual(coseno_tfidf("RUTA VELOZ", "RUTA VELOZ", self.idf), 1.0)

    def test_sin_trigramas_comunes_da_cero(self) -> None:
        self.assertEqual(coseno_tfidf("AAAA", "ZZZZ", self.idf), 0.0)

    def test_vacio_da_cero(self) -> None:
        self.assertEqual(coseno_tfidf("", "RUTA", self.idf), 0.0)

    def test_rango_y_simetria(self) -> None:
        ab = coseno_tfidf("TIENDAS D1 SUBA", "FARMACIA CENTRAL", self.idf)
        ba = coseno_tfidf("FARMACIA CENTRAL", "TIENDAS D1 SUBA", self.idf)
        self.assertTrue(0.0 <= ab <= 1.0)
        self.assertAlmostEqual(ab, ba)

    def test_trigrama_fuera_del_idf_usa_el_maximo(self) -> None:
        idf = MappingProxyType({" AB": 1.0, "AB ": 3.0})
        # "ABC" no esta en el idf: sus trigramas pesan 3.0 (el maximo).
        con_maximo = coseno_tfidf("AB", "ABC", idf, n=3)
        explicito = MappingProxyType({" AB": 1.0, "AB ": 3.0, "ABC": 3.0, "BC ": 3.0})
        self.assertAlmostEqual(con_maximo, coseno_tfidf("AB", "ABC", explicito))
        self.assertGreater(con_maximo, 0.0)

    def test_idf_ponderado_baja_el_coseno_de_prefijo_comun(self) -> None:
        a, b = "TIENDAS D1 SUBA", "TIENDAS D1 KENNEDY"
        uniforme = coseno_tfidf(a, b, _idf_uniforme(CORPUS_D1))
        ponderado = coseno_tfidf(a, b, self.idf)
        self.assertLess(ponderado, uniforme)


class NegativoDificilDocumentalTest(unittest.TestCase):
    """Registro documental, sin umbral: ambos metodos dan similitud alta."""

    def test_ruta_veloz_extensiones(self) -> None:
        idf = ajustar_idf(CORPUS_D1)
        base = "RUTA VELOZ"
        for otro in ("RUTA VELOZ COMIDAS", "RUTA VELOZ COM"):
            with self.subTest(otro=otro):
                self.assertGreater(jaro_winkler(base, otro), 0.9)
                self.assertGreater(coseno_tfidf(base, otro, idf), 0.7)


if __name__ == "__main__":
    unittest.main()
