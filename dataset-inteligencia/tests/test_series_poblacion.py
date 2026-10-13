"""Población de series de fondo: escala con --filas y las etiquetas salen de la intención de diseño."""

import unittest
from collections import Counter, defaultdict
from datetime import date

from generador.orquestador import construir
from generador.parametros import Parametros

from .util import carpeta, fecha_local, leer

MOTIVOS = {"regular", "dia_variable", "monto_variable_servicio_publico", "compra_habitual", "pago_suelto"}
PREFIJO_FONDO = "Serie de fondo"
TIPOS_DE_PAGO = ("liquidacion_confirmada", "transferencia_interna", "despacho_riel")


def series(base):
    return leer(base, "verdad/series.csv")


def de_fondo(base):
    return [s for s in series(base) if s["detalle"].startswith(PREFIJO_FONDO)]


def fijas(base):
    return [s for s in series(base) if not s["detalle"].startswith(PREFIJO_FONDO)]


def pagos_de(base, serie):
    return [m for m in leer(base, "entrada/movimientos.csv")
            if m["usuario_id"] == serie["usuario_id"] and m["llave_destino"] == serie["llave_destino"]
            and m["tipo_asiento"] in TIPOS_DE_PAGO]


def intervalos(pagos):
    dias = [fecha_local(m["ocurrido_en"]) for m in pagos]
    return [(b - a).days for a, b in zip(dias, dias[1:])]


class PruebasPoblacionDeSeries(unittest.TestCase):
    def setUp(self):
        self.base = carpeta(filas=3000)
        self.todas = series(self.base)

    def test_al_menos_100_series_con_3000_filas(self):
        self.assertGreaterEqual(len(self.todas), 100)

    def test_proporcion_de_recurrentes_entre_40_y_60_por_ciento(self):
        recurrentes = sum(1 for s in self.todas if s["es_recurrente"] == "true")
        self.assertTrue(0.40 <= recurrentes / len(self.todas) <= 0.60, recurrentes / len(self.todas))
        fondo = de_fondo(self.base)
        proporcion = sum(1 for s in fondo if s["es_recurrente"] == "true") / len(fondo)
        self.assertTrue(0.40 <= proporcion <= 0.60, proporcion)

    def test_los_motivos_incluyen_todos_los_tipos(self):
        self.assertEqual({s["motivo"] for s in de_fondo(self.base)}, MOTIVOS)
        conteo = Counter(s["motivo"] for s in de_fondo(self.base))
        self.assertTrue(all(n >= 8 for n in conteo.values()), conteo)

    def test_los_8_escenarios_fijos_siguen_intactos(self):
        fijos = fijas(self.base)
        self.assertEqual(len(fijos), 8)
        self.assertEqual(Counter(s["motivo"] for s in fijos),
                         Counter({"regular": 3, "dia_variable": 1, "monto_variable_servicio_publico": 2,
                                  "pago_suelto": 1, "compra_habitual": 1}))

    def test_mas_del_40_por_ciento_de_los_usuarios_de_fondo_tienen_series(self):
        dataset = construir(Parametros(filas=3000))
        fondo = {u.usuario_id for u in dataset.usuarios if u.clave.startswith("fondo-")}
        con_series = {s.usuario_id for _, s in dataset.series} & fondo
        self.assertGreaterEqual(len(con_series) / len(fondo), 0.40)

    def test_las_series_escalan_con_filas(self):
        pequena = len(series(carpeta(filas=1200)))
        media = len(self.todas)
        grande = len(series(carpeta(filas=6000)))
        self.assertLess(pequena, media)
        self.assertGreater(grande, media * 1.6)

    def test_cada_par_usuario_llave_aparece_en_una_sola_serie(self):
        pares = [(s["usuario_id"], s["llave_destino"]) for s in self.todas]
        self.assertEqual(len(pares), len(set(pares)))

    def test_toda_serie_tiene_pagos_reales(self):
        for s in de_fondo(self.base):
            self.assertGreaterEqual(len(pagos_de(self.base, s)), 2, s["detalle"])

    def test_la_etiqueta_no_nombra_umbrales(self):
        for s in self.todas:
            self.assertNotIn("umbral", s["detalle"].lower())
            self.assertEqual(s["es_recurrente"] == "true",
                             s["motivo"] in ("regular", "dia_variable", "monto_variable_servicio_publico"))


class PruebasIntencionDeLasSeriesDeFondo(unittest.TestCase):
    def setUp(self):
        self.base = carpeta(filas=3000)
        self.fondo = de_fondo(self.base)

    def por_motivo(self, motivo):
        return [s for s in self.fondo if s["motivo"] == motivo]

    def test_regulares_pagan_mensual_con_monto_casi_igual(self):
        regulares = self.por_motivo("regular")
        self.assertGreaterEqual(len(regulares), 10)
        for s in regulares:
            pagos = pagos_de(self.base, s)
            self.assertGreaterEqual(len(pagos), 4, s["detalle"])
            self.assertTrue(all(28 <= i <= 31 for i in intervalos(pagos)), (s["detalle"], intervalos(pagos)))
            self.assertLessEqual(len({m["monto_centavos"] for m in pagos}), 2, s["detalle"])

    def test_dia_variable_paga_cerca_del_mismo_dia_con_el_mismo_monto(self):
        variables = self.por_motivo("dia_variable")
        self.assertGreaterEqual(len(variables), 8)
        corridos = 0
        for s in variables:
            pagos = pagos_de(self.base, s)
            self.assertEqual(len({m["monto_centavos"] for m in pagos}), 1, s["detalle"])
            self.assertTrue(all(18 <= i <= 42 for i in intervalos(pagos)), (s["detalle"], intervalos(pagos)))
            corridos += len({fecha_local(m["ocurrido_en"]).day for m in pagos}) > 1
        self.assertGreaterEqual(corridos, len(variables) - 1)

    def test_servicios_publicos_cambian_como_maximo_30_por_ciento_entre_meses(self):
        servicios = self.por_motivo("monto_variable_servicio_publico")
        self.assertGreaterEqual(len(servicios), 8)
        con_variacion = 0
        for s in servicios:
            montos = [int(m["monto_centavos"]) for m in pagos_de(self.base, s)]
            self.assertGreaterEqual(len(montos), 4)
            for antes, despues in zip(montos, montos[1:]):
                self.assertLessEqual(abs(despues / antes - 1), 0.30, (s["detalle"], montos))
            con_variacion += (max(montos) - min(montos)) / (sum(montos) / len(montos)) > 0.10
        self.assertGreaterEqual(con_variacion, len(servicios) // 2)

    def test_compras_habituales_no_repiten_monto_y_tienen_intervalos_cortos(self):
        compras = self.por_motivo("compra_habitual")
        self.assertGreaterEqual(len(compras), 8)
        for s in compras:
            self.assertEqual(s["es_recurrente"], "false")
            pagos = pagos_de(self.base, s)
            montos = [m["monto_centavos"] for m in pagos]
            self.assertEqual(len(montos), len(set(montos)), s["detalle"])
            self.assertTrue(all(4 <= i <= 10 for i in intervalos(pagos)), (s["detalle"], intervalos(pagos)))

    def test_pagos_sueltos_incluyen_casualidades_que_parecen_recurrentes(self):
        sueltos = self.por_motivo("pago_suelto")
        self.assertGreaterEqual(len(sueltos), 8)
        casualidades = [s for s in sueltos if "casualidad" in s["detalle"]]
        self.assertGreaterEqual(len(casualidades), 3)
        for s in casualidades:
            pagos = pagos_de(self.base, s)
            self.assertEqual(len(pagos), 2)
            self.assertEqual(len({m["monto_centavos"] for m in pagos}), 1)
            self.assertTrue(27 <= intervalos(pagos)[0] <= 32)
            self.assertEqual((s["es_recurrente"], s["cadencia_dias"]), ("false", ""))

    def test_hay_arriendos_a_personas_con_dia_variable(self):
        arriendos = [s for s in self.por_motivo("dia_variable") if "rriendo" in s["detalle"]]
        self.assertGreaterEqual(len(arriendos), 2)
        personas = {c["llave_destino"] for c in leer(self.base, "verdad/comercios.csv") if c["es_comercio"] == "false"}
        self.assertTrue(all(s["llave_destino"] in personas for s in arriendos))

    def test_hay_suscripciones_con_alza_puntual(self):
        altas = [s for s in self.por_motivo("regular") if "alza" in s["detalle"]]
        self.assertGreaterEqual(len(altas), 3)
        for s in altas:
            self.assertEqual(len({m["monto_centavos"] for m in pagos_de(self.base, s)}), 2)

    def test_los_pagos_de_las_series_respetan_la_ventana(self):
        fecha_fin = date(2026, 10, 21)
        por_serie = defaultdict(list)
        for s in self.fondo:
            por_serie[s["serie_id"]] = pagos_de(self.base, s)
        for pagos in por_serie.values():
            self.assertTrue(all(fecha_local(m["ocurrido_en"]) <= fecha_fin for m in pagos))


if __name__ == "__main__":
    unittest.main()
