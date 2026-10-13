"""Pruebas de los casos de vista: persistencia privada, cambio de nombre y ruido."""

import unittest
from collections import defaultdict
from datetime import date

from .util import carpeta, correcciones_completas, fecha_local, leer

PERSISTENCIA = "persistencia_privada"
CAMBIO = "cambio_nombre"
CAMBIO_CATEGORIA = "cambio_nombre_categoria"
RUIDO = "ruido_sin_cambio"
NOMBRADOS = {PERSISTENCIA, CAMBIO, CAMBIO_CATEGORIA, RUIDO}


def vistas(base, caso):
    filas = [v for v in leer(base, "verdad/casos_vista.csv") if v["caso_id"] == caso]
    return sorted(filas, key=lambda v: v["ocurrido_en"])


def movimientos_por_id(base):
    return {m["movimiento_id"]: m for m in leer(base, "entrada/movimientos.csv")}


def llave_del_caso(base, caso):
    movs = movimientos_por_id(base)
    llaves = {movs[v["movimiento_id"]]["llave_destino"] for v in vistas(base, caso)}
    assert len(llaves) == 1, llaves
    return llaves.pop()


def correcciones_de(base, llave):
    return [c for c in correcciones_completas(base) if c["llave_destino"] == llave]


def primeros_pagos(base, llave):
    primero: dict = {}
    for m in leer(base, "entrada/movimientos.csv"):
        if m["llave_destino"] == llave and m["direccion"] == "salida":
            primero.setdefault(m["usuario_id"], m["ocurrido_en"])
    return dict(sorted(primero.items(), key=lambda kv: kv[1]))


def fecha_k(correcciones, k=2):
    """Instante en que k usuarios distintos coinciden en el mismo nombre normalizado."""
    vistos = defaultdict(set)
    for c in sorted(correcciones, key=lambda c: c["ocurrido_en"]):
        clave = c["nombre_puesto_canonico"]
        vistos[clave].add(c["usuario_id"])
        if len(vistos[clave]) >= k:
            return c["ocurrido_en"]
    return None


class PruebasCasosVista(unittest.TestCase):
    def test_solo_existen_los_casos_nombrados(self):
        base = carpeta()
        vs = leer(base, "verdad/casos_vista.csv")
        self.assertEqual({v["caso_id"] for v in vs}, NOMBRADOS)
        self.assertLess(len(vs), 30)

    def test_cada_fila_apunta_a_un_movimiento_real(self):
        base = carpeta()
        movs = movimientos_por_id(base)
        for v in leer(base, "verdad/casos_vista.csv"):
            m = movs[v["movimiento_id"]]
            self.assertEqual(m["usuario_id"], v["usuario_id"])
            self.assertEqual(m["ocurrido_en"], v["ocurrido_en"])
            self.assertEqual(m["direccion"], "salida")
            self.assertIn(v["origen_esperado"], ("privado", "global", "crudo", "curado"))

    def test_caso_2_produce_exactamente_las_filas_esperadas(self):
        base = carpeta()
        v = vistas(base, PERSISTENCIA)
        self.assertEqual([x["origen_esperado"] for x in v], ["crudo", "privado", "global", "privado"])
        self.assertEqual([x["nombre_esperado"] for x in v],
                         ["", "PEPE-SUPERMERCADO", "SUPERMERCADO-DON-PEPE", "PEPE-SUPERMERCADO"])
        self.assertEqual([x["categoria_esperada"] for x in v],
                         ["sin_categoria", "alimentos_mercado", "alimentos_mercado", "alimentos_mercado"])
        self.assertEqual(v[1]["usuario_id"], v[3]["usuario_id"])
        self.assertEqual(len({x["usuario_id"] for x in v}), 3)

    def test_caso_2_el_comercio_no_tiene_nombre_crudo_en_esos_pagos(self):
        base = carpeta()
        llave = llave_del_caso(base, PERSISTENCIA)
        for m in leer(base, "entrada/movimientos.csv"):
            if m["llave_destino"] == llave:
                self.assertEqual((m["nombre_comercio_crudo"], m["mcc"], m["canal"]), ("", "", "llave"))

    def test_caso_2_k2_se_alcanza_despues_de_la_correccion_de_a(self):
        base = carpeta()
        llave = llave_del_caso(base, PERSISTENCIA)
        corr = sorted(correcciones_de(base, llave), key=lambda c: c["ocurrido_en"])
        a = corr[0]
        self.assertEqual(a["nombre_puesto"], "PEPE SUPERMERCADO")
        k2 = fecha_k(corr)
        self.assertIsNotNone(k2)
        self.assertGreater(k2, a["ocurrido_en"])
        self.assertGreaterEqual((fecha_local(k2) - fecha_local(a["ocurrido_en"])).days, 60)
        v = vistas(base, PERSISTENCIA)
        crudo, privado_tras_k, global_, _ = v
        self.assertLess(crudo["ocurrido_en"], a["ocurrido_en"])
        self.assertGreater(privado_tras_k["ocurrido_en"], k2)
        self.assertGreater(global_["ocurrido_en"], k2)
        self.assertEqual(privado_tras_k["usuario_id"], a["usuario_id"])

    def test_caso_2_quien_vio_crudo_y_quien_vio_global_no_corrigieron(self):
        base = carpeta()
        llave = llave_del_caso(base, PERSISTENCIA)
        v = vistas(base, PERSISTENCIA)
        corrigieron = {c["usuario_id"] for c in correcciones_de(base, llave)}
        self.assertNotIn(v[0]["usuario_id"], corrigieron)
        self.assertNotIn(v[2]["usuario_id"], corrigieron)

    def test_caso_2_pagos_de_los_usuarios_muy_separados_en_el_tiempo(self):
        base = carpeta()
        pagos = list(primeros_pagos(base, llave_del_caso(base, PERSISTENCIA)).values())
        self.assertEqual(len(pagos), 5)
        for antes, despues in zip(pagos, pagos[1:]):
            self.assertGreaterEqual((fecha_local(despues) - fecha_local(antes)).days, 30)

    def test_caso_2_el_nombre_global_es_el_vigente_de_la_verdad(self):
        base = carpeta()
        llave = llave_del_caso(base, PERSISTENCIA)
        verdad = next(c for c in leer(base, "verdad/comercios.csv") if c["llave_destino"] == llave)
        self.assertEqual(verdad["nombre_canonico"], "SUPERMERCADO-DON-PEPE")
        self.assertEqual(verdad["es_comercio"], "true")

    def test_los_comercios_de_los_casos_estan_escalonados(self):
        base = carpeta()
        for caso in NOMBRADOS:
            pagos = list(primeros_pagos(base, llave_del_caso(base, caso)).values())
            for antes, despues in zip(pagos, pagos[1:]):
                self.assertGreaterEqual((fecha_local(despues) - fecha_local(antes)).days, 14, caso)


class PruebasHistorial(unittest.TestCase):
    def historial(self, base):
        por_llave = defaultdict(list)
        for h in leer(base, "verdad/nombres_historial.csv"):
            por_llave[h["llave_destino"]].append(h)
        return por_llave

    def test_todo_comercio_tiene_al_menos_una_fila(self):
        base = carpeta()
        comercios = {c["llave_destino"] for c in leer(base, "verdad/comercios.csv") if c["es_comercio"] == "true"}
        self.assertEqual(set(self.historial(base)), comercios)

    def test_no_hay_intervalos_solapados_por_llave(self):
        for llave, filas in self.historial(carpeta()).items():
            filas = sorted(filas, key=lambda h: h["vigente_desde"])
            for i, h in enumerate(filas):
                ultimo = i == len(filas) - 1
                self.assertEqual(h["vigente_hasta"] == "", ultimo, (llave, h))
                if not ultimo:
                    self.assertLess(h["vigente_hasta"], filas[i + 1]["vigente_desde"], llave)
                    self.assertGreaterEqual(h["vigente_hasta"], h["vigente_desde"])

    def test_la_ultima_fila_coincide_con_comercios_csv(self):
        base = carpeta()
        historial = self.historial(base)
        for c in leer(base, "verdad/comercios.csv"):
            if c["es_comercio"] == "true":
                ultima = max(historial[c["llave_destino"]], key=lambda h: h["vigente_desde"])
                self.assertEqual((ultima["nombre_canonico"], ultima["categoria"]),
                                 (c["nombre_canonico"], c["categoria"]))

    def test_solo_los_casos_de_cambio_tienen_dos_filas(self):
        base = carpeta()
        con_dos = {k for k, v in self.historial(base).items() if len(v) == 2}
        esperadas = {llave_del_caso(base, CAMBIO), llave_del_caso(base, CAMBIO_CATEGORIA)}
        self.assertEqual(con_dos, esperadas)
        self.assertTrue(all(len(v) <= 2 for v in self.historial(base).values()))

    def test_el_ruido_no_cambia_el_historial(self):
        base = carpeta()
        self.assertEqual(len(self.historial(base)[llave_del_caso(base, RUIDO)]), 1)


class PruebasCambioDeNombre(unittest.TestCase):
    def cambio(self, caso):
        base = carpeta()
        llave = llave_del_caso(base, caso)
        antes, despues = sorted(
            (h for h in leer(base, "verdad/nombres_historial.csv") if h["llave_destino"] == llave),
            key=lambda h: h["vigente_desde"])
        return base, llave, antes, despues, date.fromisoformat(despues["vigente_desde"])

    def test_cambio_de_nombre_ejemplo_tienda_dona_lucy(self):
        _, _, antes, despues, _ = self.cambio(CAMBIO)
        self.assertEqual(despues["nombre_canonico"], "TIENDA-DOÑA-LUCY")
        self.assertNotEqual(antes["nombre_canonico"], despues["nombre_canonico"])
        self.assertEqual(antes["categoria"], despues["categoria"])

    def test_cambio_de_nombre_y_de_categoria(self):
        _, _, antes, despues, _ = self.cambio(CAMBIO_CATEGORIA)
        self.assertNotEqual(antes["nombre_canonico"], despues["nombre_canonico"])
        self.assertNotEqual(antes["categoria"], despues["categoria"])

    def test_correcciones_posteriores_a_t_apuntan_al_nombre_nuevo(self):
        for caso in (CAMBIO, CAMBIO_CATEGORIA):
            base, llave, antes, despues, t = self.cambio(caso)
            corr = correcciones_de(base, llave)
            posteriores = [c for c in corr if fecha_local(c["ocurrido_en"]) >= t]
            previas = [c for c in corr if fecha_local(c["ocurrido_en"]) < t]
            self.assertGreaterEqual(len(posteriores), 2, caso)
            self.assertGreaterEqual(len(previas), 2, caso)
            for c in posteriores:
                self.assertEqual(c["nombre_puesto_canonico"], despues["nombre_canonico"], caso)
                self.assertEqual(c["categoria_puesta"], despues["categoria"], caso)
            for c in previas:
                self.assertEqual(c["nombre_puesto_canonico"], antes["nombre_canonico"], caso)
            self.assertIsNotNone(fecha_k(posteriores))

    def test_etiquetas_siguen_la_categoria_vigente_en_cada_fecha(self):
        base, llave, antes, despues, t = self.cambio(CAMBIO_CATEGORIA)
        etiquetas = {e["movimiento_id"]: e for e in leer(base, "verdad/etiquetas.csv")}
        pagos = [m for m in leer(base, "entrada/movimientos.csv") if m["llave_destino"] == llave]
        for m in pagos:
            esperada = despues["categoria"] if fecha_local(m["ocurrido_en"]) >= t else antes["categoria"]
            self.assertEqual(etiquetas[m["movimiento_id"]]["categoria_real"], esperada)

    def test_vistas_del_cambio_de_nombre(self):
        base = carpeta()
        v = vistas(base, CAMBIO)
        self.assertEqual([(x["origen_esperado"], x["nombre_esperado"]) for x in v],
                         [("global", "MINIMERCADO-LA-ESQUINA"), ("global", "MINIMERCADO-LA-ESQUINA"),
                          ("global", "TIENDA-DOÑA-LUCY")])
        v = vistas(base, CAMBIO_CATEGORIA)
        self.assertEqual([(x["nombre_esperado"], x["categoria_esperada"]) for x in v],
                         [("CAFETERIA-EL-ROBLE", "restaurantes_domicilios"),
                          ("CAFETERIA-EL-ROBLE", "restaurantes_domicilios"),
                          ("LIBRERIA-EL-ROBLE", "educacion")])

    def test_el_nuevo_nombre_global_aparece_despues_de_k2_nuevo(self):
        base, llave, _, despues, t = self.cambio(CAMBIO)
        posteriores = [c for c in correcciones_de(base, llave) if fecha_local(c["ocurrido_en"]) >= t]
        k2 = fecha_k(posteriores)
        v = vistas(base, CAMBIO)
        self.assertGreater(v[2]["ocurrido_en"], k2)
        self.assertLess(v[1]["ocurrido_en"], k2)


class PruebasRuidoSinCambio(unittest.TestCase):
    def test_el_desacuerdo_posterior_es_solo_ruido(self):
        base = carpeta()
        llave = llave_del_caso(base, RUIDO)
        verdad = next(c for c in leer(base, "verdad/comercios.csv") if c["llave_destino"] == llave)
        corr = sorted(correcciones_de(base, llave), key=lambda c: c["ocurrido_en"])
        consolidado = fecha_k(corr)
        self.assertIsNotNone(consolidado)
        posteriores = [c for c in corr if c["ocurrido_en"] > consolidado]
        self.assertGreaterEqual(len(posteriores), 3)
        distintos = defaultdict(set)
        for c in posteriores:
            nombre = c["nombre_puesto_canonico"]
            if nombre != verdad["nombre_canonico"]:
                distintos[nombre].add(c["usuario_id"])
        self.assertTrue(distintos)
        self.assertTrue(all(len(u) < 2 for u in distintos.values()))
        categorias = defaultdict(set)
        for c in posteriores:
            if c["categoria_puesta"] != verdad["categoria"]:
                categorias[c["categoria_puesta"]].add(c["usuario_id"])
        self.assertTrue(all(len(u) < 2 for u in categorias.values()))

    def test_las_vistas_del_ruido_conservan_el_nombre_original(self):
        base = carpeta()
        llave = llave_del_caso(base, RUIDO)
        verdad = next(c for c in leer(base, "verdad/comercios.csv") if c["llave_destino"] == llave)
        for v in vistas(base, RUIDO):
            self.assertEqual((v["origen_esperado"], v["nombre_esperado"], v["categoria_esperada"]),
                             ("global", verdad["nombre_canonico"], verdad["categoria"]))


if __name__ == "__main__":
    unittest.main()
