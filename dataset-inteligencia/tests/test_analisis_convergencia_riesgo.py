"""Pruebas de las medidas de riesgo y cobertura, los ámbitos sin colusión, la densidad y el bootstrap de errores visibles.

La corrida diminuta de util_convergencia tiene cifras a mano. Sus comercios sin colusión son C1, C2, C5, C6 y C8.
La droguería C3 es de colusión (T=2) y queda fuera de `sin_colusion`. De los comercios de prueba (C1, C3, C6) quedan C1 y C6.
"""

import shutil
import tempfile
import unittest
from pathlib import Path

from analisis.bootstrap import generar_remuestreos
from analisis.convergencia_agregado import _intervalo
from analisis.convergencia_bootstrap_riesgo import AMBITOS_RIESGO, bootstrap_visibles
from analisis.convergencia_evaluacion import cargar_corrida_convergencia, evaluar_corrida
from analisis.convergencia_medidas import percentil_rango
from analizar import main

from .test_analisis_convergencia import ahora, replicador, votos
from .test_analisis_convergencia_evaluacion import CONFIG, medidas
from .util import carpeta, leer
from .util_convergencia import escribir_corrida_diminuta

AMBITOS = ("todos", "prueba", "sin_colusion", "prueba_sin_colusion")


class PruebasErroresVisiblesYRiesgo(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.mkdtemp()
        ruta = escribir_corrida_diminuta(Path(cls.tmp) / "tasa-0.35" / "corrida-a")
        cls.resultado = evaluar_corrida(cargar_corrida_convergencia(ruta), CONFIG)

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.tmp, ignore_errors=True)

    def m(self, k: int, ambito: str = "todos") -> dict:
        return medidas(self.resultado, "solo_comercio", k, ambito)

    def test_errores_visibles_y_riesgo_a_mano(self):
        m1 = self.m(1)
        self.assertAlmostEqual(m1["errores_visibles_nombre"], 1 / 6)
        self.assertAlmostEqual(m1["riesgo_nombre"], 1 / 5)
        self.assertAlmostEqual(m1["errores_visibles_categoria"], 0.0)
        self.assertAlmostEqual(m1["riesgo_categoria"], 0.0)
        self.assertAlmostEqual(self.m(2)["errores_visibles_nombre"], 0.0)

    def test_la_abstencion_no_cuenta_como_error_visible(self):
        m3 = self.m(3)
        self.assertAlmostEqual(m3["cobertura_nombre"], 1 / 6)
        self.assertAlmostEqual(m3["errores_visibles_nombre"], 0.0)
        self.assertAlmostEqual(m3["acierto_total_nombre"], 1 / 6)

    def test_errores_visibles_es_riesgo_por_cobertura(self):
        for ambito in AMBITOS:
            for k in (1, 2, 3):
                m = self.m(k, ambito)
                for dim in ("nombre", "categoria"):
                    acierto = m[f"acierto_consolidados_{dim}"]
                    if acierto is None:
                        self.assertIsNone(m[f"riesgo_{dim}"], (ambito, k, dim))
                        self.assertAlmostEqual(m[f"errores_visibles_{dim}"], 0.0)
                        continue
                    self.assertAlmostEqual(m[f"riesgo_{dim}"], 1 - acierto, msg=(ambito, k, dim))
                    self.assertAlmostEqual(m[f"errores_visibles_{dim}"], (1 - acierto) * m[f"cobertura_{dim}"],
                                           msg=(ambito, k, dim))

    def test_el_ambito_prueba_sin_colusion_es_la_interseccion(self):
        m = self.m(2, "prueba_sin_colusion")
        self.assertEqual(m["n_comercios"], 2)
        self.assertAlmostEqual(m["cobertura_nombre"], 1 / 2)
        self.assertNotIn("envenenamiento_T2_comercios", m)
        self.assertEqual(self.m(2, "sin_colusion")["n_comercios"], 5)
        self.assertEqual(self.m(2, "prueba")["n_comercios"], 3)

    def test_residuo_en_prueba_sin_colusion(self):
        m = self.m(2, "prueba_sin_colusion")
        self.assertEqual((m["residuo_sin_categoria"], m["residuo_sin_correcciones"],
                          m["residuo_con_correcciones_sin_consenso"], m["residuo_revela_naturaleza"]), (1, 1, 0, 1))
        self.assertAlmostEqual(m["residuo_fraccion"], 1 / 2)

    def test_densidad_de_pagadores_y_correctores_en_sin_colusion(self):
        m = self.m(2, "sin_colusion")
        pagadores = (m["pagadores_min"], m["pagadores_p25"], m["pagadores_mediana"], m["pagadores_p75"],
                     m["pagadores_max"])
        self.assertEqual(pagadores, (1, 1, 1, 1, 3))
        self.assertAlmostEqual(m["pagadores_fraccion_un_solo"], 4 / 5)
        correctores = (m["correctores_min"], m["correctores_p25"], m["correctores_mediana"], m["correctores_p75"],
                       m["correctores_max"])
        self.assertEqual(correctores, (0, 2, 2, 2, 3))
        self.assertAlmostEqual(m["correctores_fraccion_ge1"], 4 / 5)
        self.assertAlmostEqual(m["correctores_fraccion_ge2"], 4 / 5)
        self.assertAlmostEqual(m["correctores_fraccion_ge3"], 1 / 5)

    def test_la_cobertura_nunca_supera_la_cota_de_correctores(self):
        for k in (1, 2, 3):
            m = self.m(k, "sin_colusion")
            for dim in ("nombre", "categoria"):
                self.assertLessEqual(m[f"cobertura_{dim}"], m[f"correctores_fraccion_ge{k}"] + 1e-12, (k, dim))

    def test_los_valores_existentes_no_cambian(self):
        self.assertAlmostEqual(self.m(1)["acierto_consolidados_nombre"], 4 / 5)
        self.assertAlmostEqual(self.m(2)["cobertura_nombre"], 4 / 6)
        self.assertEqual(self.m(2)["dias_consolidar_nombre_mediana"], 6.5)


class PruebasPercentilPorRango(unittest.TestCase):
    def test_rango_mas_cercano(self):
        self.assertEqual(percentil_rango([1, 1, 1, 1, 3], 0.25), 1)
        self.assertEqual(percentil_rango([0, 2, 2, 2, 3], 0.25), 2)
        self.assertEqual(percentil_rango([3, 1, 2, 4], 0.75), 3)

    def test_sin_valores_es_none(self):
        self.assertIsNone(percentil_rango([], 0.25))


class PruebasBootstrapDeErroresVisibles(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.mkdtemp()
        rutas = [escribir_corrida_diminuta(Path(cls.tmp) / "tasa-0.35" / n, semilla=s)
                 for n, s in (("corrida-a", 1), ("corrida-b", 2))]
        cls.resultados = [evaluar_corrida(cargar_corrida_convergencia(r), CONFIG) for r in rutas]
        cls.filas = bootstrap_visibles(cls.resultados, CONFIG, b=200, semilla=2026)

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.tmp, ignore_errors=True)

    def buscar(self, ambito: str, medida: str, dimension: str, comparacion: str):
        hallados = [f for f in self.filas if (f.ambito, f.medida, f.dimension, f.comparacion)
                    == (ambito, medida, dimension, comparacion)]
        self.assertEqual(len(hallados), 1, (ambito, medida, dimension, comparacion))
        return hallados[0]

    def test_cubre_dos_ambitos_cuatro_medidas_y_dos_comparaciones(self):
        self.assertEqual(AMBITOS_RIESGO, ("sin_colusion", "prueba_sin_colusion"))
        self.assertEqual(len(self.filas), 2 * 2 * 2 * 2)
        self.assertEqual({f.comparacion for f in self.filas}, {"K2 menos K1", "K2 menos K3"})
        self.assertEqual({f.medida for f in self.filas}, {"errores_visibles", "cobertura"})

    def test_el_n_es_el_de_los_comercios_de_todas_las_semillas(self):
        self.assertEqual(self.buscar("sin_colusion", "cobertura", "nombre", "K2 menos K1").n, 10)
        self.assertEqual(self.buscar("prueba_sin_colusion", "cobertura", "nombre", "K2 menos K1").n, 4)

    def test_diferencias_observadas_a_mano(self):
        f = self.buscar("sin_colusion", "errores_visibles", "nombre", "K2 menos K1")
        self.assertAlmostEqual(f.intervalo.observada, 0.0 - 1 / 5)
        f = self.buscar("sin_colusion", "cobertura", "nombre", "K2 menos K1")
        self.assertAlmostEqual(f.intervalo.observada, 3 / 5 - 4 / 5)
        f = self.buscar("sin_colusion", "cobertura", "nombre", "K2 menos K3")
        self.assertAlmostEqual(f.intervalo.observada, 3 / 5 - 0.0)

    def test_usa_los_mismos_remuestreos_que_el_bootstrap_existente(self):
        f = self.buscar("sin_colusion", "cobertura", "nombre", "K2 menos K1")
        filas = [x for r in sorted(self.resultados, key=lambda r: r.nombre)
                 for x in r.comercios[("solo_comercio", 2)] if x.colusion_t == 0]
        otras = [x for r in sorted(self.resultados, key=lambda r: r.nombre)
                 for x in r.comercios[("solo_comercio", 1)] if x.colusion_t == 0]
        diferencias = [int(a.nombre_global is not None) - int(o.nombre_global is not None)
                       for a, o in zip(filas, otras)]
        esperado = _intervalo(diferencias, generar_remuestreos(len(filas), 200, 2026, "0.35:sin_colusion:nombre"))
        self.assertEqual(f.intervalo, esperado)

    def test_es_determinista(self):
        self.assertEqual(bootstrap_visibles(self.resultados, CONFIG, b=200, semilla=2026), self.filas)


class PruebasDelResumenNuevo(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.mkdtemp()
        corrida = escribir_corrida_diminuta(Path(cls.tmp) / "tasa-0.35" / "corrida-a")
        main(["--experimento", "convergencia", "--corridas", str(corrida), "--resultados",
              str(Path(cls.tmp) / "res"), "--bootstrap-b", "50"])
        cls.carpeta = next((Path(cls.tmp) / "res" / "convergencia").iterdir())
        cls.texto = (cls.carpeta / "resumen.md").read_text(encoding="utf-8")

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.tmp, ignore_errors=True)

    def test_abre_con_la_tabla_titular_de_sin_colusion(self):
        lineas = self.texto.splitlines()
        primera = next(l for l in lineas if l.startswith("|"))
        for esperado in ("tasa", "K", "cobertura nombre", "riesgo nombre", "errores visibles nombre",
                         "cobertura categoría", "riesgo categoría", "errores visibles categoría"):
            self.assertIn(esperado, primera)
        self.assertNotIn("acierto", primera)
        self.assertLess(self.texto.index(primera), self.texto.index("Etiquetas de verificación"))
        self.assertLess(self.texto.index(primera), self.texto.index("## Tasa de corrección"))

    def test_marca_todos_y_prueba_como_contaminados(self):
        self.assertIn("ámbito todos (contaminado por la población de colusión)", self.texto)
        self.assertIn("ámbito prueba (contaminado por la población de colusión)", self.texto)

    def test_el_acierto_total_solo_va_en_una_seccion_secundaria(self):
        self.assertIn("Acierto total (secundario)", self.texto)
        self.assertIn("castiga la abstención", self.texto)
        antes, despues = self.texto.split("Acierto total (secundario)", 1)
        self.assertNotIn("acierto total", antes.split("## Tasa de corrección", 1)[0])

    def test_etiquetas_de_verificacion(self):
        seccion = self.texto.split("## Etiquetas de verificación", 1)[1]
        for esperado in ("Medido", "Probado en código", "envenenamiento", "personas promovidas", "casos_vista",
                         "cambio_nombre", "2 contra 2", "regla de deriva"):
            self.assertIn(esperado.lower(), seccion.lower())

    def test_secciones_nuevas(self):
        for esperado in ("## Densidad de pagadores y correctores", "## Bootstrap pareado de errores visibles y cobertura",
                         "#### Residuo sin_categoria en prueba_sin_colusion", "cota superior de la cobertura"):
            self.assertIn(esperado, self.texto)

    def test_el_bootstrap_nuevo_se_escribe_en_su_csv(self):
        encabezado = (self.carpeta / "bootstrap_riesgo.csv").read_text(encoding="utf-8").splitlines()[0]
        self.assertEqual(encabezado, "tasa,ambito,medida,dimension,comparacion,comercios,diferencia_observada,"
                                     "media_bootstrap,inferior,superior,lectura")

    def test_no_hay_rayas_largas_ni_punto_y_coma_en_el_texto(self):
        for caracter in (chr(0x2014), chr(0x2013), chr(0x3B)):
            self.assertNotIn(caracter, self.texto)


class PruebasDeLaRegladeEnvenenamiento(unittest.TestCase):
    """Un comercio queda envenenado exactamente cuando K es menor o igual a T, con 3 honestos después de T colusores."""

    @staticmethod
    def corrida(t: int, k: int):
        colusores = [(f"c{i}", "Drogueria", "entretenimiento") for i in range(t)]
        honestos = [(f"h{i}", "Drogueria", "salud") for i in range(3)]
        r = replicador(votos(colusores + honestos), k)
        cambios = [c for c in r.cambios() if c.dimension == "categoria"]
        final = r.estado_en(ahora("12-31")).global_de("0011111111", "categoria")
        return any(c.despues == "entretenimiento" for c in cambios), final

    def test_envenenado_exactamente_cuando_k_es_menor_o_igual_a_t(self):
        for t in (1, 2, 3, 4):
            for k in (1, 2, 3, 4):
                envenenado, _ = self.corrida(t, k)
                self.assertEqual(envenenado, k <= t, (t, k))

    def test_el_relevo_depende_de_tres_honestos_contra_t_colusores(self):
        for t in (1, 2):
            self.assertEqual(self.corrida(t, 1)[1], "salud", t)
        for t in (3, 4):
            self.assertEqual(self.corrida(t, 1)[1], "entretenimiento", t)

    def test_el_empate_conserva_al_incumbente_con_t_igual_a_3(self):
        for k in (1, 2, 3):
            self.assertEqual(self.corrida(3, k)[1], "entretenimiento", k)


class PruebasDelFiltroPorTipoDeLlave(unittest.TestCase):
    def test_las_llaves_de_comercio_son_de_clase_comercio_y_las_personas_no(self):
        base = carpeta()
        movimientos = {m["llave_destino"]: m["tipo_llave"] for m in leer(base, "entrada/movimientos.csv")}
        comercios = leer(base, "verdad/comercios.csv")
        self.assertGreater(len(comercios), 0)
        for c in comercios:
            self.assertEqual(c["es_comercio"] == "true", c["clase_llave"] == "comercio", c["llave_destino"])
            if c["llave_destino"] in movimientos:
                self.assertEqual(movimientos[c["llave_destino"]], c["clase_llave"], c["llave_destino"])
        personas = {c["clase_llave"] for c in comercios if c["es_comercio"] != "true"}
        self.assertLessEqual(personas, {"celular", "correo", "alfanumerico", "documento"})


if __name__ == "__main__":
    unittest.main()
