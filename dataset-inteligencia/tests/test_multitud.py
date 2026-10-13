"""Pruebas del experimento de multitud: K=2 y los casos adversos."""

import unittest
from collections import defaultdict

from .util import carpeta, correcciones_completas, etiquetas_por_id, leer

K = 2


def correcciones_por_llave(base):
    por_llave = defaultdict(list)
    for c in correcciones_completas(base):
        por_llave[c["llave_destino"]].append(c)
    return por_llave


def comercios(base):
    return {c["llave_destino"]: c for c in leer(base, "verdad/comercios.csv")}


class PruebasCorrecciones(unittest.TestCase):
    def test_correcciones_y_verdad_se_corresponden(self):
        base = carpeta()
        ids = [c["correccion_id"] for c in leer(base, "entrada/correcciones.csv")]
        ids_verdad = [c["correccion_id"] for c in leer(base, "verdad/correcciones_verdad.csv")]
        self.assertEqual(len(ids), len(set(ids)))
        self.assertEqual(sorted(ids), sorted(ids_verdad))
        self.assertGreater(len(ids), 200)

    def test_tipos_de_ruido_presentes(self):
        base = carpeta()
        tipos = {c["tipo_ruido"] for c in leer(base, "verdad/correcciones_verdad.csv")}
        self.assertEqual(tipos, {"ninguno", "mayusculas", "tildes", "abreviatura", "tipeo",
                                 "etiqueta_erronea", "colusion"})

    def test_es_correcta_significa_categoria_igual_a_la_verdad(self):
        base = carpeta()
        verdad = comercios(base)
        for llave, filas in correcciones_por_llave(base).items():
            for c in filas:
                if c["tipo_ruido"] in ("etiqueta_erronea", "colusion"):
                    self.assertEqual(c["es_correcta"], "false", c)
                if verdad[llave]["es_comercio"] == "true" and c["tipo_ruido"] in (
                        "ninguno", "mayusculas", "tildes", "abreviatura", "tipeo"):
                    historial_vigente = verdad[llave]["categoria"]
                    if c["categoria_puesta"] == historial_vigente:
                        self.assertEqual(c["es_correcta"], "true", c)

    def test_las_tasas_se_respetan_aproximadamente(self):
        base = carpeta()
        verdad = leer(base, "verdad/correcciones_verdad.csv")
        erroneas = sum(1 for c in verdad if c["tipo_ruido"] == "etiqueta_erronea")
        con_ruido = sum(1 for c in verdad if c["tipo_ruido"] in ("mayusculas", "tildes", "abreviatura", "tipeo"))
        self.assertGreater(con_ruido / len(verdad), 0.10)
        self.assertLess(erroneas / len(verdad), 0.30)

    def test_tasa_cero_deja_solo_las_correcciones_de_los_casos(self):
        sin = leer(carpeta(tasa_correccion=0.0), "entrada/correcciones.csv")
        con = leer(carpeta(tasa_correccion=0.35), "entrada/correcciones.csv")
        self.assertGreater(len(sin), 20)
        self.assertGreater(len(con), len(sin) + 100)


class PruebasTiempoDeLasCorrecciones(unittest.TestCase):
    def test_correcciones_ordenadas_y_posteriores_al_movimiento_que_las_motiva(self):
        base = carpeta()
        correcciones = leer(base, "entrada/correcciones.csv")
        instantes = [c["ocurrido_en"] for c in correcciones]
        self.assertEqual(instantes, sorted(instantes))
        primero: dict = {}
        for m in leer(base, "entrada/movimientos.csv"):
            if m["direccion"] == "salida" and m["llave_destino"]:
                primero.setdefault((m["usuario_id"], m["llave_destino"]), m["ocurrido_en"])
        for c in correcciones:
            pago = primero[(c["usuario_id"], c["llave_destino"])]
            self.assertGreater(c["ocurrido_en"], pago, c)

    def test_los_usuarios_de_un_mismo_comercio_pagan_en_momentos_distintos(self):
        base = carpeta()
        pagos = defaultdict(dict)
        for m in leer(base, "entrada/movimientos.csv"):
            if m["tipo_llave"] == "comercio" and m["direccion"] == "salida":
                pagos[m["llave_destino"]].setdefault(m["usuario_id"], m["ocurrido_en"][:10])
        populares = [p for p in pagos.values() if len(p) >= 8]
        self.assertTrue(populares)
        for p in populares:
            fechas = sorted(p.values())
            self.assertGreaterEqual((_dias(fechas[-1]) - _dias(fechas[0])), 90)


def _dias(texto: str) -> int:
    from datetime import date
    return date.fromisoformat(texto).toordinal()


class PruebasCasosAdversos(unittest.TestCase):
    def test_a_un_comercio_alcanza_k_con_correcciones_consistentes(self):
        base = carpeta()
        verdad = comercios(base)
        alcanzan = []
        for llave, filas in correcciones_por_llave(base).items():
            if verdad[llave]["es_comercio"] != "true":
                continue
            buenas = [c for c in filas if c["es_correcta"] == "true"]
            usuarios_por_nombre = defaultdict(set)
            for c in buenas:
                usuarios_por_nombre[c["nombre_puesto_canonico"]].add(c["usuario_id"])
            if any(len(u) >= K for u in usuarios_por_nombre.values()):
                alcanzan.append(llave)
        self.assertGreaterEqual(len(alcanzan), 1)

    def test_b_arrendadora_alcanza_dos_pero_es_persona(self):
        base = carpeta()
        verdad = comercios(base)
        etiquetas = etiquetas_por_id(base)
        halladas = []
        for llave, filas in correcciones_por_llave(base).items():
            usuarios = {c["usuario_id"] for c in filas if c["categoria_puesta"] == "vivienda_servicios"}
            if verdad[llave]["es_comercio"] == "false" and len(usuarios) >= K:
                halladas.append(llave)
        self.assertEqual(len(halladas), 1)
        llave = halladas[0]
        self.assertEqual(verdad[llave]["clase_llave"], "celular")
        self.assertEqual(verdad[llave]["categoria"], "sin_categoria")
        pagos = [m for m in leer(base, "entrada/movimientos.csv") if m["llave_destino"] == llave]
        self.assertGreaterEqual(len(pagos), 4)
        for m in pagos:
            self.assertEqual(etiquetas[m["movimiento_id"]]["es_persona"], "true")
            self.assertEqual(etiquetas[m["movimiento_id"]]["categoria_real"], "sin_categoria")
        self.assertTrue(all(c["es_correcta"] == "false" for c in correcciones_por_llave(base)[llave]))

    def test_c_colusion_de_dos_usuarios_sobre_el_mismo_comercio(self):
        base = carpeta()
        verdad = comercios(base)
        colusion = [c for filas in correcciones_por_llave(base).values() for c in filas
                    if c["tipo_ruido"] == "colusion"]
        self.assertEqual(len(colusion), 2)
        self.assertEqual(len({c["llave_destino"] for c in colusion}), 1)
        self.assertEqual(len({c["usuario_id"] for c in colusion}), 2)
        self.assertEqual(len({c["categoria_puesta"] for c in colusion}), 1)
        real = verdad[colusion[0]["llave_destino"]]
        self.assertEqual(real["es_comercio"], "true")
        self.assertNotEqual(colusion[0]["categoria_puesta"], real["categoria"])
        self.assertTrue(all(c["es_correcta"] == "false" for c in colusion))

    def test_d_nombres_parecidos_son_comercios_distintos(self):
        base = carpeta()
        verdad = [c for c in comercios(base).values() if c["es_comercio"] == "true"]
        pares = [(a, b) for a in verdad for b in verdad
                 if a is not b and b["nombre_canonico"].startswith(a["nombre_canonico"] + "-")
                 and a["empresa_id"] != b["empresa_id"] and a["categoria"] != b["categoria"]]
        self.assertGreaterEqual(len(pares), 1)
        corregidos = correcciones_por_llave(base)
        for a, b in pares[:1]:
            self.assertNotEqual(a["llave_destino"], b["llave_destino"])
            self.assertTrue(corregidos[a["llave_destino"]] and corregidos[b["llave_destino"]])

    def test_e_el_gmf_no_se_puede_categorizar_por_usuario(self):
        base = carpeta()
        self.assertTrue(all(c["llave_destino"] for c in leer(base, "entrada/correcciones.csv")))
        etiquetas = etiquetas_por_id(base)
        gmf = [m for m in leer(base, "entrada/movimientos.csv")
               if etiquetas[m["movimiento_id"]]["categoria_real"] == "comisiones_costos_financieros"]
        self.assertTrue(gmf)
        self.assertTrue(all(m["nombre_comercio_crudo"] == "" and m["mcc"] == "" for m in gmf))

    def test_cadenas_con_varias_sucursales(self):
        base = carpeta()
        empresas = {e["empresa_id"]: e for e in leer(base, "verdad/empresas.csv")}
        sucursales = defaultdict(list)
        for c in leer(base, "verdad/comercios.csv"):
            if c["es_comercio"] == "true":
                sucursales[c["empresa_id"]].append(c["nombre_canonico"])
        d1 = next(i for i, e in empresas.items() if e["razon_social"].endswith("D1"))
        self.assertGreaterEqual(len(sucursales[d1]), 3)
        self.assertIn("D1-CHAPINERO", sucursales[d1])
        self.assertIn("TIENDAS-D1-SUBA", sucursales[d1])


if __name__ == "__main__":
    unittest.main()
