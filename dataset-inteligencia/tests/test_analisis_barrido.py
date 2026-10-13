"""Pruebas del barrido en cuadrícula: expansión de la rejilla, desempate, validación y determinismo."""

import dataclasses
import json
import tempfile
import unittest
from pathlib import Path

from analisis.barrido import barrer, cargar_rejilla, expandir, rejilla_desde_dict
from analisis.configuracion import Configuracion, ConfiguracionInvalida
from analisis.evaluacion import evaluar, preparar_corrida

from .util_analisis import corrida_mini

BASE = Configuracion.por_defecto()
REJILLA_REAL = Path(__file__).resolve().parent.parent / "analisis" / "config" / "rejilla_recurrencia.json"


def rejilla(**cambios) -> dict:
    base = {"w_monto": [0.6], "tol_monto": [0.10], "factor_tolerancia_dias": [1.0], "n_min": [3],
            "umbral": [0.5, 0.7]}
    return {**base, **cambios}


class PruebasRejilla(unittest.TestCase):
    def test_la_rejilla_real_cubre_los_rangos_pedidos(self):
        r = cargar_rejilla(REJILLA_REAL)
        self.assertEqual((min(r.valores["w_monto"]), max(r.valores["w_monto"])), (0.3, 0.8))
        self.assertEqual((min(r.valores["tol_monto"]), max(r.valores["tol_monto"])), (0.05, 0.30))
        self.assertEqual((min(r.valores["n_min"]), max(r.valores["n_min"])), (2, 4))
        self.assertEqual((min(r.valores["umbral"]), max(r.valores["umbral"])), (0.3, 0.9))
        self.assertIn(1.0, r.valores["factor_tolerancia_dias"])
        self.assertEqual(len(expandir(r, BASE)), 3024)

    def test_expansion_en_orden_y_w_fecha_derivado(self):
        configs = expandir(rejilla_desde_dict(rejilla(w_monto=[0.3, 0.6], factor_tolerancia_dias=[1.0, 2.0])), BASE)
        self.assertEqual(len(configs), 8)
        self.assertEqual([c.indice for c in configs], list(range(8)))
        primero, ultimo = configs[0], configs[-1]
        self.assertEqual(primero.configuracion.w_monto, 0.3)
        self.assertEqual(primero.configuracion.w_fecha, 0.7)
        self.assertEqual(primero.configuracion.umbral, 0.5)
        self.assertEqual(configs[1].configuracion.umbral, 0.7)  # el último eje cambia más rápido
        self.assertEqual(ultimo.configuracion.w_monto, 0.6)
        self.assertEqual(ultimo.configuracion.w_fecha, 0.4)
        self.assertEqual(ultimo.configuracion.tolerancias_dias, (4.0, 6.0, 8.0))
        self.assertEqual(ultimo.parametros["factor_tolerancia_dias"], 2.0)

    def test_rejillas_invalidas(self):
        for mal, fragmento in [
            ({"w_monto": []}, "w_monto"),
            ({"w_monto": [1.5]}, "w_monto"),
            ({"umbral": [2]}, "umbral"),
            ({"n_min": [0]}, "n_min"),
            ({"factor_tolerancia_dias": [0]}, "factor_tolerancia_dias"),
            ({"inventado": [1]}, "inventado"),
            ({"tol_monto": "0.1"}, "tol_monto"),
        ]:
            with self.assertRaises(ConfiguracionInvalida, msg=str(mal)) as cm:
                rejilla_desde_dict(rejilla(**mal))
            self.assertIn(fragmento, str(cm.exception))
        faltante = rejilla()
        del faltante["umbral"]
        with self.assertRaises(ConfiguracionInvalida):
            rejilla_desde_dict(faltante)


class PruebasBarrido(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        raiz = Path(self.dir.name)
        self.entrenamiento = preparar_corrida(corrida_mini(raiz / "entrenamiento", semilla=2026))
        self.validaciones = tuple(
            preparar_corrida(corrida_mini(raiz / f"val{s}", semilla=s)) for s in (1, 7))

    def barrer(self, **cambios):
        return barrer(self.entrenamiento, self.validaciones, rejilla_desde_dict(rejilla(**cambios)), BASE)

    def test_elige_la_mejor_configuracion_de_entrenamiento(self):
        r = self.barrer()
        self.assertEqual(len(r.filas), 2)
        self.assertEqual(r.mejor.configuracion.umbral, 0.7)  # F1 2/3 frente a 0,5 con umbral 0,5
        self.assertAlmostEqual(r.mejor.matriz.f1, 2 / 3)
        self.assertEqual([f.matriz.f1 for f in r.filas], [0.5, 2 / 3])

    def test_desempate_es_el_primero_de_la_rejilla(self):
        # Con umbral 0,7 y 0,8 las decisiones son idénticas: F1, precisión y FPR iguales.
        r = self.barrer(umbral=[0.7, 0.8])
        self.assertEqual(r.filas[0].matriz, r.filas[1].matriz)
        self.assertEqual(r.mejor.indice, 0)
        self.assertEqual(r.mejor.configuracion.umbral, 0.7)

    def test_desempate_prefiere_mayor_precision_antes_que_el_indice(self):
        # Mismo F1, distinta precisión: se fabrica con dos configuraciones que den matrices distintas
        # pero F1 igual mediante la comparación directa de la clave de orden.
        from analisis.barrido import clave_de_orden
        from analisis.evaluacion import Matriz, Secundaria
        a = clave_de_orden(Matriz(2, 2, 0, 0), Secundaria(10, 0), 0)   # F1 2/3, precisión 0,5
        b = clave_de_orden(Matriz(1, 0, 0, 1), Secundaria(10, 0), 1)   # F1 2/3, precisión 1
        self.assertLess(b, a)
        c = clave_de_orden(Matriz(1, 0, 0, 1), Secundaria(10, 3), 2)   # misma matriz, más FPR secundaria
        self.assertLess(b, c)
        d = clave_de_orden(Matriz(1, 0, 0, 1), Secundaria(10, 0), 3)   # todo igual, índice mayor
        self.assertLess(b, d)

    def test_evalua_la_mejor_en_cada_validacion(self):
        r = self.barrer()
        self.assertEqual([nombre for nombre, _ in r.validacion], ["val1", "val7"])
        for corrida, (_, evaluacion) in zip(self.validaciones, r.validacion):
            self.assertEqual(evaluacion, evaluar(corrida, r.mejor.configuracion))
        self.assertEqual(r.entrenamiento, evaluar(self.entrenamiento, r.mejor.configuracion))

    def test_incluye_la_referencia_con_la_configuracion_base(self):
        r = self.barrer()
        self.assertEqual(r.referencia_entrenamiento, evaluar(self.entrenamiento, BASE))
        self.assertEqual(len(r.referencia_validacion), len(self.validaciones))

    def test_dos_ejecuciones_dan_el_mismo_resultado(self):
        self.assertEqual(self.barrer(), self.barrer())

    def test_la_mejor_configuracion_se_recarga_y_reproduce_la_fila(self):
        r = self.barrer()
        with tempfile.TemporaryDirectory() as t:
            ruta = Path(t) / "mejor.json"
            ruta.write_text(json.dumps(r.mejor.configuracion.a_dict()), encoding="utf-8")
            from analisis.configuracion import cargar_configuracion
            recargada = cargar_configuracion(ruta)
        self.assertEqual(recargada, r.mejor.configuracion)
        self.assertEqual(evaluar(self.entrenamiento, recargada).global_, r.mejor.matriz)

    def test_rechaza_barrer_sin_validacion(self):
        with self.assertRaises(ValueError):
            barrer(self.entrenamiento, (), rejilla_desde_dict(rejilla()), BASE)

    def test_rechaza_semillas_de_validacion_repetidas_o_iguales_a_la_de_entrenamiento(self):
        raiz = Path(self.dir.name)
        igual = preparar_corrida(corrida_mini(raiz / "igual", semilla=2026))
        with self.assertRaises(ValueError):
            barrer(self.entrenamiento, (igual,), rejilla_desde_dict(rejilla()), BASE)
        with self.assertRaises(ValueError):
            barrer(self.entrenamiento, (self.validaciones[0], self.validaciones[0]),
                   rejilla_desde_dict(rejilla()), BASE)

    def test_el_barrido_no_modifica_la_configuracion_base(self):
        copia = dataclasses.replace(BASE)
        self.barrer()
        self.assertEqual(BASE, copia)


if __name__ == "__main__":
    unittest.main()
