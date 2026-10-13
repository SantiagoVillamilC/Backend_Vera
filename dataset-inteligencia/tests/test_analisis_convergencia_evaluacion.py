"""Pruebas de la evaluación de convergencia sobre la corrida diminuta de util_convergencia (cifras a mano)."""

import json
import shutil
import tempfile
import unittest
from pathlib import Path

from analisis.convergencia import SIN_CATEGORIA
from analisis.convergencia_agregado import agregar, diferencias_bootstrap
from analisis.convergencia_config import Politica, desde_dict_convergencia
from analisis.convergencia_evaluacion import (cargar_corrida_convergencia, evaluar_corrida, instante_de_fecha_local,
                                              valor_verdadero_en)
from analizar import main

from .util_convergencia import escribir_corrida_diminuta

CONFIG = desde_dict_convergencia({
    "ks": [1, 2, 3], "politicas": {"sin_filtro": None, "solo_comercio": ["comercio"]},
    "politica_principal": "solo_comercio", "k_referencia": 2, "umbral_similitud": None})


def medidas(resultado, politica: str, k: int, ambito: str) -> dict:
    return {m.medida: m.valor for m in resultado.medidas
            if (m.politica, m.k, m.ambito) == (politica, k, ambito)}


class PruebasConCorridaDiminuta(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.mkdtemp()
        ruta = escribir_corrida_diminuta(Path(cls.tmp) / "tasa-0.35" / "corrida-a")
        cls.resultado = evaluar_corrida(cargar_corrida_convergencia(ruta), CONFIG)

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.tmp, ignore_errors=True)

    def m(self, k: int, ambito: str = "todos", politica: str = "solo_comercio") -> dict:
        return medidas(self.resultado, politica, k, ambito)

    def test_cobertura(self):
        esperado = {1: (5 / 6, 5 / 6), 2: (4 / 6, 5 / 6), 3: (1 / 6, 2 / 6)}
        for k, (nombre, categoria) in esperado.items():
            self.assertAlmostEqual(self.m(k)["cobertura_nombre"], nombre, msg=f"K={k}")
            self.assertAlmostEqual(self.m(k)["cobertura_categoria"], categoria, msg=f"K={k}")
        self.assertEqual(self.m(1)["n_comercios"], 6)

    def test_el_ambito_sin_colusion_excluye_la_poblacion_de_colusion(self):
        m = self.m(2, "sin_colusion")
        self.assertEqual(m["n_comercios"], 5)
        self.assertAlmostEqual(m["cobertura_nombre"], 3 / 5)
        self.assertNotIn("envenenamiento_T2_comercios", m)
        self.assertEqual(m["espurios_categoria"], 0)

    def test_cobertura_solo_prueba(self):
        esperado = {1: (2 / 3, 2 / 3), 2: (2 / 3, 2 / 3), 3: (1 / 3, 1 / 3)}
        for k, (nombre, categoria) in esperado.items():
            m = self.m(k, "prueba")
            self.assertEqual(m["n_comercios"], 3)
            self.assertAlmostEqual(m["cobertura_nombre"], nombre, msg=f"K={k}")
            self.assertAlmostEqual(m["cobertura_categoria"], categoria, msg=f"K={k}")

    def test_acierto_entre_consolidados_y_total(self):
        self.assertAlmostEqual(self.m(1)["acierto_consolidados_nombre"], 4 / 5)
        self.assertAlmostEqual(self.m(1)["acierto_total_nombre"], 4 / 6)
        self.assertAlmostEqual(self.m(2)["acierto_consolidados_nombre"], 1.0)
        self.assertAlmostEqual(self.m(2)["acierto_total_nombre"], 4 / 6)
        self.assertAlmostEqual(self.m(3)["acierto_total_nombre"], 1 / 6)
        self.assertAlmostEqual(self.m(1)["acierto_consolidados_categoria"], 1.0)
        self.assertAlmostEqual(self.m(1)["acierto_total_categoria"], 5 / 6)
        self.assertAlmostEqual(self.m(3)["acierto_total_categoria"], 2 / 6)

    def test_dias_hasta_consolidar_en_nombre(self):
        self.assertEqual((self.m(1)["dias_consolidar_nombre_n"], self.m(1)["dias_consolidar_nombre_mediana"],
                          self.m(1)["dias_consolidar_nombre_p90"]), (4, 0, 4))
        self.assertEqual((self.m(2)["dias_consolidar_nombre_n"], self.m(2)["dias_consolidar_nombre_mediana"],
                          self.m(2)["dias_consolidar_nombre_p90"]), (4, 6.5, 10))
        self.assertEqual((self.m(3)["dias_consolidar_nombre_n"], self.m(3)["dias_consolidar_nombre_mediana"],
                          self.m(3)["dias_consolidar_nombre_p90"]), (1, 52, 52))

    def test_dias_hasta_consolidar_en_categoria(self):
        self.assertEqual((self.m(1)["dias_consolidar_categoria_mediana"],
                          self.m(1)["dias_consolidar_categoria_p90"]), (0, 61))
        self.assertEqual((self.m(2)["dias_consolidar_categoria_mediana"],
                          self.m(2)["dias_consolidar_categoria_p90"]), (9, 61))
        self.assertEqual((self.m(3)["dias_consolidar_categoria_mediana"],
                          self.m(3)["dias_consolidar_categoria_p90"]), (32.5, 61))

    def test_cambios_espurios(self):
        m1, m2, m3 = self.m(1), self.m(2), self.m(3)
        self.assertEqual((m1["espurios_nombre"], m1["espurios_nombre_revertidos"]), (2, 1))
        self.assertEqual((m1["espurios_categoria"], m1["espurios_categoria_revertidos"]), (1, 1))
        self.assertEqual((m1["espurios_nombre_revertidos_sin_colusion"],
                          m1["espurios_categoria_revertidos_sin_colusion"]), (1, 0))
        self.assertEqual((m2["espurios_nombre"], m2["espurios_categoria"], m2["espurios_categoria_revertidos"]),
                         (0, 1, 1))
        self.assertEqual((m3["espurios_nombre"], m3["espurios_categoria"]), (0, 0))

    def test_retraso_tras_un_cambio_real(self):
        for k, dias in ((1, 16), (2, 25)):
            m = self.m(k)
            self.assertEqual((m["retraso_nombre_cambios"], m["retraso_nombre_alcanzados"],
                              m["retraso_nombre_mediana"]), (1, 1, dias), k)
            self.assertEqual((m["retraso_categoria_cambios"], m["retraso_categoria_alcanzados"],
                              m["retraso_categoria_mediana"]), (1, 1, dias), k)
        m3 = self.m(3)
        self.assertEqual((m3["retraso_nombre_cambios"], m3["retraso_nombre_alcanzados"]), (1, 0))
        self.assertIsNone(m3["retraso_nombre_mediana"])

    def test_envenenamiento_por_tamano_y_k(self):
        esperado = {1: (1, 1, 61, 0), 2: (1, 1, 59, 0), 3: (1, 0, None, 0)}
        for k, (comercios, envenenados, dias, sin_relevo) in esperado.items():
            m = self.m(k)
            self.assertEqual((m["envenenamiento_T2_comercios"], m["envenenamiento_T2_envenenados"],
                              m["envenenamiento_T2_dias_mediana"], m["envenenamiento_T2_sin_relevo"]),
                             (comercios, envenenados, dias, sin_relevo), k)
        self.assertNotIn("envenenamiento_T3_comercios", self.m(2))
        self.assertEqual(self.m(1, "prueba")["envenenamiento_T2_envenenados"], 1)

    def test_personas_promovidas_bajo_cada_politica(self):
        for k, (sin_filtro, solo) in {1: (1, 0), 2: (1, 0), 3: (0, 0)}.items():
            self.assertEqual(self.m(k, "global", "sin_filtro")["personas_promovidas"], sin_filtro, k)
            self.assertEqual(self.m(k, "global", "solo_comercio")["personas_promovidas"], solo, k)
        self.assertEqual(self.m(2, "global")["personas_con_correcciones"], 1)

    def test_las_metricas_de_comercios_no_dependen_de_la_politica(self):
        for k in (1, 2, 3):
            a, b = self.m(k, "todos", "sin_filtro"), self.m(k, "todos", "solo_comercio")
            self.assertEqual(a, b)

    def test_aprobacion_de_casos_de_vista(self):
        esperado = {1: (3, 3), 2: (3, 3), 3: (3, 2)}
        for k, (total, aprobados) in esperado.items():
            m = self.m(k, "global")
            self.assertEqual((m["casos_total"], m["casos_aprobados"]), (total, aprobados), k)
        m3 = self.m(3, "global")
        self.assertEqual((m3["casos_global_total"], m3["casos_global_aprobados"]), (1, 0))
        self.assertEqual((m3["casos_privado_aprobados"], m3["casos_crudo_aprobados"]), (1, 1))
        self.assertEqual((m3["casos_columna_nombre_ok"], m3["casos_columna_categoria_ok"],
                          m3["casos_columna_origen_ok"]), (2, 2, 2))

    def test_detalle_de_casos_con_lo_esperado_y_lo_obtenido(self):
        filas = [d for d in self.resultado.casos if (d.politica, d.k) == ("solo_comercio", 3) and d.caso_id == "global"]
        self.assertEqual(len(filas), 1)
        d = filas[0]
        self.assertEqual((d.nombre_obtenido, d.categoria_obtenida, d.origen_obtenido), ("", SIN_CATEGORIA, "crudo"))
        self.assertEqual((d.nombre_ok, d.categoria_ok, d.origen_ok), (False, False, False))

    def test_residuo_sin_categoria_en_prueba_con_k_2(self):
        m = self.m(2, "prueba")
        self.assertEqual((m["residuo_sin_categoria"], m["residuo_sin_correcciones"],
                          m["residuo_revela_naturaleza"]), (1, 1, 1))
        self.assertAlmostEqual(m["residuo_fraccion"], 1 / 3)

    def test_los_comercios_se_exponen_en_orden_estable_para_el_bootstrap(self):
        filas = self.resultado.comercios[("solo_comercio", 2)]
        self.assertEqual([f.llave for f in filas], sorted(f.llave for f in filas))
        self.assertEqual(len(filas), 6)
        self.assertEqual([f.llave for f in filas], [f.llave for f in self.resultado.comercios[("solo_comercio", 1)]])

    def test_metadatos_de_la_corrida(self):
        self.assertEqual(self.resultado.nombre, "tasa-0.35/corrida-a")
        self.assertEqual(self.resultado.tasa, "0.35")
        self.assertEqual(self.resultado.semilla, 0)


class PruebasDeLaVerdadEnElTiempo(unittest.TestCase):
    HISTORIAL = ({"nombre_canonico": "VIEJO", "categoria": "salud", "vigente_desde": "2026-01-01",
                  "vigente_hasta": "2026-06-14"},
                 {"nombre_canonico": "NUEVO", "categoria": "educacion", "vigente_desde": "2026-06-15",
                  "vigente_hasta": ""})

    def test_los_extremos_son_fechas_locales_de_colombia_e_inclusivas(self):
        from datetime import datetime, timezone
        antes = datetime(2026, 6, 15, 4, 59, 59, tzinfo=timezone.utc)  # 23:59:59 del 14 en Colombia
        despues = datetime(2026, 6, 15, 5, 0, 0, tzinfo=timezone.utc)
        self.assertEqual(valor_verdadero_en(self.HISTORIAL, antes, "nombre"), "VIEJO")
        self.assertEqual(valor_verdadero_en(self.HISTORIAL, despues, "nombre"), "NUEVO")
        self.assertEqual(valor_verdadero_en(self.HISTORIAL, despues, "categoria"), "educacion")

    def test_inicio_de_una_fecha_local_es_las_5_utc(self):
        self.assertEqual(instante_de_fecha_local("2026-06-15").isoformat(), "2026-06-15T05:00:00+00:00")


class PruebasDeAgregadoYBootstrap(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.mkdtemp()
        rutas = [escribir_corrida_diminuta(Path(cls.tmp) / "tasa-0.35" / n, semilla=s)
                 for n, s in (("corrida-a", 1), ("corrida-b", 2))]
        cls.resultados = [evaluar_corrida(cargar_corrida_convergencia(r), CONFIG) for r in rutas]

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.tmp, ignore_errors=True)

    def test_agregar_da_media_minimo_y_maximo_por_tasa(self):
        filas = agregar(self.resultados)
        buscado = [f for f in filas if (f.tasa, f.politica, f.k, f.ambito, f.medida)
                   == ("0.35", "solo_comercio", 2, "todos", "cobertura_nombre")]
        self.assertEqual(len(buscado), 1)
        f = buscado[0]
        self.assertEqual(f.semillas, 2)
        self.assertAlmostEqual(f.media, 4 / 6)
        self.assertAlmostEqual(f.minimo, 4 / 6)
        self.assertAlmostEqual(f.maximo, 4 / 6)

    def test_los_valores_indefinidos_no_entran_a_la_media(self):
        filas = agregar(self.resultados)
        f = next(f for f in filas if (f.k, f.ambito, f.medida) == (3, "todos", "retraso_nombre_mediana"))
        self.assertEqual(f.semillas, 0)
        self.assertIsNone(f.media)

    def test_bootstrap_pareado_con_semilla_fija(self):
        filas = diferencias_bootstrap(self.resultados, CONFIG, b=200, semilla=2026)
        nombre_todos = [f for f in filas if (f.ambito, f.dimension, f.comparacion) == ("todos", "nombre", "K2 menos K3")]
        self.assertEqual(len(nombre_todos), 1)
        f = nombre_todos[0]
        self.assertEqual(f.tasa, "0.35")
        self.assertAlmostEqual(f.intervalo.observada, 3 / 6)  # (4/6 - 1/6) con los 6 comercios repetidos
        self.assertLessEqual(f.intervalo.inferior, f.intervalo.observada)
        self.assertGreaterEqual(f.intervalo.superior, f.intervalo.observada)
        igual = [f for f in filas if (f.ambito, f.dimension, f.comparacion) == ("todos", "nombre", "K2 menos K1")][0]
        self.assertAlmostEqual(igual.intervalo.observada, 0.0)
        self.assertEqual(diferencias_bootstrap(self.resultados, CONFIG, b=200, semilla=2026), filas)


class PruebasDelCli(unittest.TestCase):
    def test_el_cli_escribe_los_archivos_y_es_determinista(self):
        with tempfile.TemporaryDirectory() as t:
            corrida = escribir_corrida_diminuta(Path(t) / "tasa-0.35" / "corrida-a")
            salidas = []
            for i in range(2):
                destino = Path(t) / f"resultados{i}"
                codigo = main(["--experimento", "convergencia", "--corridas", str(corrida), "--resultados", str(destino),
                               "--bootstrap-b", "50"])
                self.assertEqual(codigo, 0)
                carpetas = list((destino / "convergencia").iterdir())
                self.assertEqual(len(carpetas), 1)
                salidas.append(carpetas[0])
            self.assertEqual(salidas[0].name, salidas[1].name)
            esperados = {"medidas.csv", "agregado.csv", "casos_vista.csv", "bootstrap.csv", "bootstrap_riesgo.csv",
                         "configuracion.json", "manifiesto.json", "resumen.md"}
            self.assertEqual({p.name for p in salidas[0].iterdir()}, esperados)
            for nombre in esperados:
                self.assertEqual((salidas[0] / nombre).read_bytes(), (salidas[1] / nombre).read_bytes(), nombre)
            manifiesto = json.loads((salidas[0] / "manifiesto.json").read_text(encoding="utf-8"))
            self.assertEqual(manifiesto["experimento"], "convergencia")
            self.assertTrue((Path(t) / "resultados0" / ".gitignore").exists())

    def test_el_cli_exige_corridas(self):
        with self.assertRaises(SystemExit):
            main(["--experimento", "convergencia"])


if __name__ == "__main__":
    unittest.main()
