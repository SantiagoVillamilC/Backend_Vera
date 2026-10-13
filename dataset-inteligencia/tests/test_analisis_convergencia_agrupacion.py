"""Pruebas de la agrupación por similitud previa al conteo de votos (experimento A, extensión), con casos a mano."""

import unittest
from datetime import datetime, timezone

from analisis.configuracion import ConfiguracionInvalida
from analisis.convergencia import Replicador
from analisis.convergencia_agrupacion import crear_agrupador
from analisis.convergencia_config import Politica, desde_dict_convergencia
from analisis.similitud import ajustar_idf, coseno_tfidf, jaro_winkler

from .util_analisis import fila
from .util_convergencia import corr, t

LLAVE = "0011111111"
POLITICA = Politica("sin_filtro", None)
CORPUS = ["RUTA-VELOZ", "RUTA-VELOZ-COMIDAS", "TIENDA-UNO", "TIENDA-DOS", "MINIMERCADO-LA-ESQUINA",
          "FARMACIA-CENTRAL", "PANADERIA-EL-TRIGO"]
BASE = {"ks": [1, 2, 3], "politicas": {"sin_filtro": None}, "politica_principal": "sin_filtro",
        "k_referencia": 2, "umbral_similitud": None}


def ahora() -> datetime:
    return datetime.strptime(t("12-31"), "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)


def votos(nombres: list[str], llave: str = LLAVE) -> list[dict]:
    return [corr(i + 1, f"u{i + 1}", llave, n, "compras", f"05-{i + 1:02d}") for i, n in enumerate(nombres)]


def replicador(filas: list[dict], k: int, metodo: str, umbral: float | None) -> Replicador:
    movimientos = [fila(1, "x", t("01-01"), 1, LLAVE, tipo_llave="comercio")]
    agrupador = crear_agrupador(metodo, umbral, CORPUS)
    return Replicador.desde_filas(filas, movimientos, k, POLITICA, agrupador)


def global_nombre(r: Replicador, llave: str = LLAVE) -> str | None:
    return r.estado_en(ahora()).global_de(llave, "nombre")


class PruebasAgrupacion(unittest.TestCase):
    def test_dos_abreviaturas_iguales_se_agrupan_y_promueven_con_k_2(self):
        filas = votos(["MINIMERCADO-LA-ESQ", "MINIMERCADO-LA-ESQ"])
        self.assertIsNone(global_nombre(replicador(filas, 3, "jaro_winkler", 0.97)))
        self.assertEqual(global_nombre(replicador(filas, 2, "ninguno", None)), "MINIMERCADO-LA-ESQ")

    def test_una_abreviatura_y_su_nombre_se_agrupan_con_jaro_winkler(self):
        self.assertGreaterEqual(jaro_winkler("MINIMERCADO-LA-ESQ", "MINIMERCADO-LA-ESQUINA"), 0.96)
        filas = votos(["MINIMERCADO-LA-ESQUINA", "MINIMERCADO-LA-ESQ"])
        self.assertIsNone(global_nombre(replicador(filas, 2, "ninguno", None)))
        self.assertEqual(global_nombre(replicador(filas, 2, "jaro_winkler", 0.96)), "MINIMERCADO-LA-ESQUINA")

    def test_un_tipeo_se_agrupa_con_su_nombre_y_el_representante_es_el_primero(self):
        self.assertGreaterEqual(jaro_winkler("TIENDA-UNO", "TIENDA-UNNO"), 0.97)
        filas = votos(["TIENDA-UNO", "TIENDA-UNNO"])
        self.assertEqual(global_nombre(replicador(filas, 2, "jaro_winkler", 0.97)), "TIENDA-UNO")
        invertido = votos(["TIENDA-UNNO", "TIENDA-UNO"])
        self.assertEqual(global_nombre(replicador(invertido, 2, "jaro_winkler", 0.97)), "TIENDA-UNNO")

    def test_un_tipeo_se_agrupa_con_coseno_si_supera_el_umbral(self):
        idf = ajustar_idf(CORPUS)
        puntaje = coseno_tfidf("TIENDA-UNO", "TIENDA-UNNO", idf)
        filas = votos(["TIENDA-UNO", "TIENDA-UNNO"])
        agrupado = global_nombre(replicador(filas, 2, "coseno_tfidf", puntaje - 0.001))
        separado = global_nombre(replicador(filas, 2, "coseno_tfidf", puntaje + 0.001))
        self.assertEqual((agrupado, separado), ("TIENDA-UNO", None))

    def test_ruta_veloz_y_ruta_veloz_comidas_no_se_agrupan_con_coseno_078(self):
        idf = ajustar_idf(CORPUS)
        self.assertLess(coseno_tfidf("RUTA-VELOZ", "RUTA-VELOZ-COMIDAS", idf), 0.78)
        filas = votos(["RUTA-VELOZ", "RUTA-VELOZ-COMIDAS"])
        self.assertIsNone(global_nombre(replicador(filas, 2, "coseno_tfidf", 0.78)))

    def test_nombres_distintos_no_se_agrupan(self):
        filas = votos(["TIENDA-UNO", "TIENDA-DOS"])
        self.assertIsNone(global_nombre(replicador(filas, 2, "jaro_winkler", 0.97)))

    def test_gana_el_representante_de_mayor_puntaje_y_el_empate_el_mas_antiguo(self):
        a = crear_agrupador("jaro_winkler", 0.5, [])
        self.assertEqual(a.asignar(("ABCD", "ABCE"), "ABCE"), ("ABCE", ()))
        rep, nuevos = a.asignar(("ABCD", "ABCE"), "ABCF")
        self.assertEqual((rep, nuevos), ("ABCD", ()))  # empate de puntaje: el primero creado

    def test_el_agrupador_es_por_llave(self):
        filas = votos(["TIENDA-UNO"]) + [corr(2, "u2", "0022222222", "TIENDA-UNO", "compras", "05-02")]
        self.assertIsNone(global_nombre(replicador(filas, 2, "jaro_winkler", 0.97)))

    def test_determinismo_y_orden_cronologico(self):
        filas = votos(["TIENDA-UNO", "TIENDA-UNNO", "TIENDA-UNO"])
        r1 = replicador(filas, 2, "jaro_winkler", 0.97)
        r2 = replicador(list(reversed(filas)), 2, "jaro_winkler", 0.97)
        self.assertEqual(r1.cambios(), r2.cambios())
        self.assertEqual(r1.correcciones_efectivas(), r1.correcciones_efectivas())

    def test_apagado_es_identico_al_replicador_sin_agrupador(self):
        filas = votos(["TIENDA-UNO", "TIENDA-UNNO", "TIENDA-UNO"])
        movimientos = [fila(1, "x", t("01-01"), 1, LLAVE, tipo_llave="comercio")]
        sin = Replicador.desde_filas(filas, movimientos, 2, POLITICA)
        self.assertEqual(replicador(filas, 2, "ninguno", None).cambios(), sin.cambios())


class PruebasConfiguracion(unittest.TestCase):
    def test_por_defecto_el_metodo_es_ninguno_y_el_umbral_nulo(self):
        cfg = desde_dict_convergencia(BASE)
        self.assertEqual((cfg.metodo_similitud, cfg.umbral_similitud), ("ninguno", None))
        self.assertNotIn("metodo_similitud", cfg.a_dict())

    def test_metodo_activo_con_umbral_numerico(self):
        cfg = desde_dict_convergencia({**BASE, "metodo_similitud": "jaro_winkler", "umbral_similitud": 0.97})
        self.assertEqual((cfg.metodo_similitud, cfg.umbral_similitud), ("jaro_winkler", 0.97))
        self.assertEqual(desde_dict_convergencia(cfg.a_dict()).hash(), cfg.hash())

    def test_rechaza_metodo_desconocido_umbral_faltante_o_fuera_de_rango(self):
        for malo in ({"metodo_similitud": "levenshtein", "umbral_similitud": 0.9},
                     {"metodo_similitud": "coseno_tfidf", "umbral_similitud": None},
                     {"metodo_similitud": "coseno_tfidf", "umbral_similitud": 1.5},
                     {"metodo_similitud": "coseno_tfidf", "umbral_similitud": True},
                     {"metodo_similitud": "ninguno", "umbral_similitud": 0.9}):
            with self.subTest(malo=malo), self.assertRaises(ConfiguracionInvalida):
                desde_dict_convergencia({**BASE, **malo})

    def test_el_hash_de_apagado_no_cambia(self):
        self.assertEqual(desde_dict_convergencia(BASE).hash(),
                         desde_dict_convergencia({**BASE, "metodo_similitud": "ninguno"}).hash())


if __name__ == "__main__":
    unittest.main()
