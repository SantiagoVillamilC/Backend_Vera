"""Pruebas de verdad/series.csv: Claro, telefonía, servicios públicos y compras habituales."""

import unittest
from datetime import date

from .util import carpeta, fecha_local, leer, movimientos_de, usuario_de_aviso

MOTIVOS = {"regular", "dia_variable", "monto_variable_servicio_publico", "compra_habitual", "pago_suelto"}
RECURRENTES_POR_DISENO = {"regular", "dia_variable", "monto_variable_servicio_publico"}


def series(base):
    """Solo las 8 series de los escenarios fijos. Las de fondo se prueban en test_series_poblacion."""
    return [s for s in leer(base, "verdad/series.csv") if not s["detalle"].startswith("Serie de fondo")]


def de_usuario(base, usuario_id):
    return [s for s in series(base) if s["usuario_id"] == usuario_id]


class PruebasSeries(unittest.TestCase):
    def test_encabezado_y_dominios(self):
        base = carpeta()
        filas = series(base)
        self.assertEqual(len(filas), 8)
        self.assertEqual(list(leer(base, "verdad/series.csv")[0]),
                         ["serie_id", "usuario_id", "llave_destino", "es_recurrente", "cadencia_dias",
                          "motivo", "detalle"])
        self.assertEqual(len({s["serie_id"] for s in filas}), len(filas))
        for s in filas:
            self.assertIn(s["es_recurrente"], ("true", "false"))
            self.assertIn(s["motivo"], MOTIVOS)
            self.assertEqual(s["cadencia_dias"] == "", s["es_recurrente"] == "false", s)
            self.assertEqual(s["es_recurrente"] == "true", s["motivo"] in RECURRENTES_POR_DISENO, s)

    def test_claro_del_perfil_3_es_recurrente_regular_con_el_alza(self):
        base = carpeta()
        u = usuario_de_aviso(base, "cobro_nuevo", mediana="false", media="false")
        (claro,) = de_usuario(base, u)
        self.assertEqual((claro["es_recurrente"], claro["motivo"], claro["cadencia_dias"]),
                         ("true", "regular", "30"))
        self.assertIn("5,6 %", claro["detalle"])
        pagos = [m for m in movimientos_de(base, u) if m["descripcion"] == "Claro internet"]
        self.assertEqual({m["llave_destino"] for m in pagos}, {claro["llave_destino"]})

    def test_internet_del_perfil_2_y_arriendo_estan_etiquetados(self):
        base = carpeta()
        u = usuario_de_aviso(base, "pago_no_alcanza", mediana="true")
        (internet,) = de_usuario(base, u)
        self.assertEqual((internet["es_recurrente"], internet["motivo"]), ("true", "regular"))

    def telefonia(self, base):
        """Series de la llave de telefonía, por usuario."""
        llaves = {}
        for s in series(base):
            llaves.setdefault(s["llave_destino"], []).append(s)
        (serie_llave,) = [v for v in llaves.values() if len(v) == 2 and
                          {x["motivo"] for x in v} == {"pago_suelto", "dia_variable"}]
        return serie_llave

    def test_caso_negativo_estilo_movistar(self):
        base = carpeta()
        negativa = next(s for s in self.telefonia(base) if s["motivo"] == "pago_suelto")
        self.assertEqual((negativa["es_recurrente"], negativa["cadencia_dias"]), ("false", ""))
        pagos = [(fecha_local(m["ocurrido_en"]), m["monto_centavos"])
                 for m in movimientos_de(base, negativa["usuario_id"]) if m["llave_destino"] == negativa["llave_destino"]]
        self.assertEqual(pagos, [(date(2026, 1, 10), "2000000"), (date(2026, 2, 20), "2500000"),
                                 (date(2026, 3, 20), "5000000")])

    def test_la_llave_de_telefonia_lleva_mcc_4814_en_la_variante_b(self):
        base_b = carpeta(variante="B")
        negativa = next(s for s in self.telefonia(base_b) if s["motivo"] == "pago_suelto")
        filas = [m for m in movimientos_de(base_b, negativa["usuario_id"])
                 if m["llave_destino"] == negativa["llave_destino"]]
        self.assertEqual(len(filas), 3)
        self.assertEqual({(m["canal"], m["mcc"]) for m in filas}, {("qr", "4814")})
        base_a = carpeta(variante="A")
        self.assertTrue(all(m["mcc"] == "" for m in leer(base_a, "entrada/movimientos.csv")))

    def test_telefonia_que_paga_el_dia_20_mas_o_menos_2_con_monto_estable(self):
        base = carpeta()
        positiva = next(s for s in self.telefonia(base) if s["motivo"] == "dia_variable")
        self.assertEqual((positiva["es_recurrente"], positiva["cadencia_dias"]), ("true", "30"))
        pagos = [m for m in movimientos_de(base, positiva["usuario_id"])
                 if m["llave_destino"] == positiva["llave_destino"]]
        self.assertEqual(len(pagos), 6)
        self.assertEqual({m["monto_centavos"] for m in pagos}, {"5990000"})
        dias = [fecha_local(m["ocurrido_en"]).day for m in pagos]
        self.assertTrue(all(18 <= d <= 22 for d in dias), dias)
        self.assertGreater(len(set(dias)), 2)

    def test_servicio_publico_recurrente_con_variacion_de_monto_mayor_a_10_por_ciento(self):
        base = carpeta()
        servicios = [s for s in series(base) if s["motivo"] == "monto_variable_servicio_publico"]
        self.assertGreaterEqual(len(servicios), 1)
        con_variacion = []
        for s in servicios:
            self.assertEqual(s["es_recurrente"], "true")
            pagos = [int(m["monto_centavos"]) for m in movimientos_de(base, s["usuario_id"])
                     if m["llave_destino"] == s["llave_destino"] and m["direccion"] == "salida"
                     and m["tipo_asiento"] == "liquidacion_confirmada"]
            self.assertEqual({fecha_local(m["ocurrido_en"]).day for m in movimientos_de(base, s["usuario_id"])
                              if m["llave_destino"] == s["llave_destino"]}, {12})
            if (max(pagos) - min(pagos)) / (sum(pagos) / len(pagos)) > 0.10:
                con_variacion.append(s)
        self.assertGreaterEqual(len(con_variacion), 1)

    def test_servicio_publico_cambia_como_maximo_30_por_ciento_entre_meses(self):
        base = carpeta()
        luz = next(s for s in series(base) if s["detalle"].startswith("Servicio público (luz)"))
        pagos = [int(m["monto_centavos"]) for m in movimientos_de(base, luz["usuario_id"])
                 if m["llave_destino"] == luz["llave_destino"] and m["tipo_asiento"] == "liquidacion_confirmada"]
        self.assertEqual(len(pagos), 6)
        for antes, despues in zip(pagos, pagos[1:]):
            self.assertLessEqual(abs(despues / antes - 1), 0.30, (antes, despues))
        self.assertGreater((max(pagos) - min(pagos)) / (sum(pagos) / len(pagos)), 0.10)

    def test_compra_habitual_no_es_recurrente(self):
        base = carpeta()
        compras = [s for s in series(base) if s["motivo"] == "compra_habitual"]
        self.assertGreaterEqual(len(compras), 1)
        for s in compras:
            self.assertEqual((s["es_recurrente"], s["cadencia_dias"]), ("false", ""))
            pagos = [m for m in movimientos_de(base, s["usuario_id"])
                     if m["llave_destino"] == s["llave_destino"] and m["direccion"] == "salida"
                     and m["tipo_asiento"] == "liquidacion_confirmada"]
            self.assertGreaterEqual(len(pagos), 15)
            montos = [m["monto_centavos"] for m in pagos]
            self.assertEqual(len(montos), len(set(montos)), "ningún monto debe repetirse")
            dias = [fecha_local(m["ocurrido_en"]) for m in pagos]
            intervalos = [(b - a).days for a, b in zip(dias, dias[1:])]
            self.assertTrue(all(5 <= i <= 9 for i in intervalos), intervalos)
            self.assertGreater(len(set(intervalos)), 3)

    def test_la_etiqueta_no_depende_de_un_umbral_numerico(self):
        base = carpeta()
        for s in series(base):
            self.assertNotIn("umbral", s["detalle"].lower())
            self.assertNotIn("10 %", s["detalle"])


if __name__ == "__main__":
    unittest.main()
