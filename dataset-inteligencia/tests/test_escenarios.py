"""Pruebas de los escenarios fijos de hábitos y de movimientos."""

import statistics
import unittest
from collections import defaultdict
from datetime import date

from generador.orquestador import construir
from generador.parametros import Parametros

from .util import (carpeta, etiquetas_por_id, fecha_local, firmado, leer, local,
                   movimientos_de, total_por_mes, usuario_de_aviso)

PESO = 100


class PruebasPerfil1(unittest.TestCase):
    def test_restaurantes_boda_y_octubre(self):
        base = carpeta()
        u = usuario_de_aviso(base, "categoria_sobre_media", mediana="true", media="false")
        totales = total_por_mes(movimientos_de(base, u), etiquetas_por_id(base),
                                "restaurantes_domicilios", "salida")
        for mes in (4, 5, 6, 8, 9):
            self.assertEqual(totales[(2026, mes)], 300_000 * PESO, mes)
        self.assertEqual(totales[(2026, 7)], 2_400_000 * PESO)
        self.assertEqual(totales[(2026, 10)], 600_000 * PESO)

    def test_mediana_dispara_y_media_no(self):
        base = carpeta()
        u = usuario_de_aviso(base, "categoria_sobre_media", mediana="true", media="false")
        totales = total_por_mes(movimientos_de(base, u), etiquetas_por_id(base),
                                "restaurantes_domicilios", "salida")
        seis = [totales[(2026, m)] for m in range(4, 10)]
        self.assertEqual(statistics.mean(seis), 650_000 * PESO)
        self.assertEqual(statistics.median(seis), 300_000 * PESO)
        self.assertGreater(totales[(2026, 10)], statistics.median(seis))
        self.assertLess(totales[(2026, 10)], statistics.mean(seis))

    def test_el_pago_de_la_boda_es_uno_solo(self):
        base = carpeta()
        u = usuario_de_aviso(base, "categoria_sobre_media", mediana="true", media="false")
        julio = [m for m in movimientos_de(base, u)
                 if m["monto_centavos"] == str(2_400_000 * PESO) and m["direccion"] == "salida"]
        self.assertEqual(len(julio), 1)
        self.assertEqual(fecha_local(julio[0]["ocurrido_en"]).month, 7)


class PruebasPerfil2(unittest.TestCase):
    def setUp(self):
        self.base = carpeta()
        self.u = usuario_de_aviso(self.base, "pago_no_alcanza", mediana="true")
        self.filas = movimientos_de(self.base, self.u)
        self.etiquetas = etiquetas_por_id(self.base)

    def test_ingreso_mensual_con_un_mes_bajo(self):
        ingresos = total_por_mes(self.filas, self.etiquetas, "salario", "entrada")
        for mes in (4, 5, 6, 7, 9):
            self.assertEqual(ingresos[(2026, mes)], 1_000_000 * PESO, mes)
        self.assertEqual(ingresos[(2026, 8)], 600_000 * PESO)

    def test_gasto_mensual_de_800_mil(self):
        sin_gmf = [m for m in self.filas
                   if self.etiquetas[m["movimiento_id"]]["categoria_real"] != "comisiones_costos_financieros"
                   and self.etiquetas[m["movimiento_id"]]["categoria_real"] not in ("salario", "otros_ingresos")]
        por_mes: dict = defaultdict(int)
        for m in sin_gmf:
            f = fecha_local(m["ocurrido_en"])
            if m["direccion"] == "salida" and f < date(2026, 10, 1):
                por_mes[(f.year, f.month)] += int(m["monto_centavos"])
        for mes in range(4, 10):
            self.assertEqual(por_mes[(2026, mes)], 800_000 * PESO, mes)

    def test_internet_de_89900_el_dia_5(self):
        internet = [m for m in self.filas if m["descripcion"] == "Internet"]
        self.assertEqual(len(internet), 6)
        for m in internet:
            self.assertEqual(m["monto_centavos"], str(89_900 * PESO))
            self.assertEqual(fecha_local(m["ocurrido_en"]).day, 5)
        self.assertEqual([fecha_local(m["ocurrido_en"]).month for m in internet], [4, 5, 6, 7, 8, 9])

    def test_saldo_proyectado_al_5_de_octubre_bajo_el_internet(self):
        corte = "2026-10-03"
        saldo = sum(firmado(m) for m in self.filas if m["ocurrido_en"][:10] <= corte)
        self.assertGreaterEqual(saldo, 0)
        self.assertLess(saldo, 89_900 * PESO)
        aviso = next(a for a in leer(self.base, "verdad/avisos_esperados.csv")
                     if a["usuario_id"] == self.u and a["tipo"] == "pago_no_alcanza")
        self.assertEqual(aviso["fecha_evaluacion"], corte)

    def test_el_saldo_nunca_es_negativo(self):
        saldo = 0
        for m in self.filas:
            saldo += firmado(m)
            self.assertGreaterEqual(saldo, 0, m)

    def test_salario_de_las_2130_colombia_cae_el_1_de_octubre_en_utc(self):
        salarios = [m for m in self.filas if m["descripcion"] == "Salario"
                    and local(m["ocurrido_en"]).strftime("%Y-%m-%d %H:%M") == "2026-09-30 21:30"]
        self.assertEqual(len(salarios), 1)
        self.assertEqual(salarios[0]["ocurrido_en"], "2026-10-01T02:30:00Z")
        self.assertEqual(salarios[0]["monto_centavos"], str(1_000_000 * PESO))

    def test_aviso_de_caida_de_ingreso(self):
        tipos = {a["tipo"] for a in leer(self.base, "verdad/avisos_esperados.csv") if a["usuario_id"] == self.u}
        self.assertEqual(tipos, {"caida_ingreso", "pago_no_alcanza", "presupuesto_anticipado",
                                 "categoria_sobre_media"})

    def test_la_compra_que_agota_el_saldo_tambien_dispara_presupuesto_y_categoria(self):
        avisos = {a["tipo"]: a for a in leer(self.base, "verdad/avisos_esperados.csv") if a["usuario_id"] == self.u}
        for tipo in ("presupuesto_anticipado", "categoria_sobre_media"):
            self.assertEqual(avisos[tipo]["fecha_evaluacion"], "2026-10-02")
            self.assertEqual((avisos[tipo]["debe_disparar_con_mediana"], avisos[tipo]["debe_disparar_con_media"]),
                             ("true", "true"))


class PruebasPerfil3(unittest.TestCase):
    def setUp(self):
        self.base = carpeta()
        self.u = usuario_de_aviso(self.base, "cobro_nuevo", mediana="false", media="false")
        self.filas = movimientos_de(self.base, self.u)

    def test_claro_dia_5_luego_dia_9_y_luego_94900(self):
        claro = [m for m in self.filas if m["descripcion"] == "Claro internet"]
        observado = [(fecha_local(m["ocurrido_en"]), m["monto_centavos"]) for m in claro]
        esperado = [(date(2026, 4, 5), "8990000"), (date(2026, 5, 5), "8990000"),
                    (date(2026, 6, 5), "8990000"), (date(2026, 7, 5), "8990000"),
                    (date(2026, 8, 9), "8990000"), (date(2026, 9, 5), "9490000")]
        self.assertEqual(observado, esperado)

    def test_la_serie_recurrente_es_una_sola(self):
        claro = [m for m in self.filas if m["descripcion"] == "Claro internet"]
        self.assertEqual(len({m["llave_destino"] for m in claro}), 1)
        self.assertEqual({m["tipo_llave"] for m in claro}, {"comercio"})

    def test_ingreso_irregular_entre_400_mil_y_1_2_millones(self):
        etiquetas = etiquetas_por_id(self.base)
        ingresos = [int(m["monto_centavos"]) for m in self.filas
                    if m["direccion"] == "entrada" and etiquetas[m["movimiento_id"]]["categoria_real"] != "salario"]
        self.assertGreaterEqual(len(ingresos), 6)
        self.assertTrue(all(400_000 * PESO <= i <= 1_200_000 * PESO for i in ingresos))
        self.assertGreater(len(set(ingresos)), 3)

    def test_sin_avisos_falsos_y_con_saldo_suficiente(self):
        avisos = [a for a in leer(self.base, "verdad/avisos_esperados.csv") if a["usuario_id"] == self.u]
        self.assertTrue(avisos)
        for a in avisos:
            self.assertEqual((a["debe_disparar_con_mediana"], a["debe_disparar_con_media"]), ("false", "false"))
        self.assertGreaterEqual(sum(firmado(m) for m in self.filas), 94_900 * PESO)


class PruebasPerfil4Y5(unittest.TestCase):
    def test_presupuesto_anticipado_el_20_de_octubre(self):
        base = carpeta()
        u = usuario_de_aviso(base, "presupuesto_anticipado", "2026-10-20", mediana="true", media="true")
        aviso = next(a for a in leer(base, "verdad/avisos_esperados.csv")
                     if a["usuario_id"] == u and a["tipo"] == "presupuesto_anticipado")
        self.assertEqual(aviso["fecha_evaluacion"], "2026-10-20")
        gasto: dict = defaultdict(int)
        for m in movimientos_de(base, u):
            if m["direccion"] == "salida":
                f = fecha_local(m["ocurrido_en"])
                gasto[f] += int(m["monto_centavos"])
        previos: dict = defaultdict(int)
        for f, v in gasto.items():
            if f < date(2026, 10, 1):
                previos[(f.year, f.month)] += v
        promedio = statistics.mean(previos.values())
        self.assertEqual(len(previos), 6)
        acumulado = lambda dia: sum(v for f, v in gasto.items() if date(2026, 10, 1) <= f <= dia)  # noqa: E731
        self.assertLessEqual(acumulado(date(2026, 10, 19)), promedio)
        self.assertGreater(acumulado(date(2026, 10, 20)), promedio)

    def test_usuario_creado_el_15_de_agosto_sin_historial_suficiente(self):
        base = carpeta()
        u = usuario_de_aviso(base, "categoria_sobre_media", mediana="false", media="false")
        primero = min(m["ocurrido_en"] for m in movimientos_de(base, u))
        self.assertEqual(fecha_local(primero), date(2026, 8, 15))
        aviso = next(a for a in leer(base, "verdad/avisos_esperados.csv") if a["usuario_id"] == u)
        self.assertIn("historial insuficiente", aviso["detalle"])


class PruebasCasosDeMovimiento(unittest.TestCase):
    def grupos(self, base):
        grupos = defaultdict(list)
        for m in leer(base, "entrada/movimientos.csv"):
            if m["transferencia_id"]:
                grupos[m["transferencia_id"]].append(m)
        return grupos

    def test_toda_devolucion_agrupada_suma_cero(self):
        base = carpeta()
        con_devolucion = [g for g in self.grupos(base).values()
                          if any(m["tipo_asiento"] == "compensacion" for m in g)]
        self.assertGreaterEqual(len(con_devolucion), 2)
        for g in con_devolucion:
            self.assertEqual(sum(firmado(m) for m in g), 0, g)

    def fila(self, base, monto, tipo):
        return next(m for m in leer(base, "entrada/movimientos.csv")
                    if m["monto_centavos"] == str(monto) and m["tipo_asiento"] == tipo)

    def test_envio_fallido_del_5_de_septiembre(self):
        base = carpeta()
        envio = self.fila(base, 200_000 * PESO, "retencion")
        self.assertEqual(fecha_local(envio["ocurrido_en"]), date(2026, 9, 5))
        grupo = self.grupos(base)[envio["transferencia_id"]]
        gmf = [m for m in grupo if "GMF" in m["descripcion"]]
        self.assertEqual([m["monto_centavos"] for m in gmf], [str(800 * PESO)])
        dev = next(m for m in grupo if m["tipo_asiento"] == "compensacion")
        self.assertEqual(fecha_local(dev["ocurrido_en"]), date(2026, 9, 6))
        self.assertEqual(dev["direccion"], "entrada")

    def test_envio_indeterminado_del_30_de_septiembre(self):
        base = carpeta()
        envio = self.fila(base, 150_000 * PESO, "despacho_riel")
        self.assertEqual(envio["ocurrido_en"], "2026-09-30T23:00:00Z")
        self.assertEqual(local(envio["ocurrido_en"]).strftime("%H:%M"), "18:00")
        dev = next(m for m in self.grupos(base)[envio["transferencia_id"]] if m["tipo_asiento"] == "compensacion")
        self.assertEqual(fecha_local(dev["ocurrido_en"]), date(2026, 10, 1))

    def test_la_contraparte_nunca_es_el_mismo_usuario(self):
        dataset = construir(Parametros(filas=3000))
        propias = dataset.llaves_propias
        for m in dataset.movimientos:
            if m.destino is not None:
                self.assertNotEqual(m.destino.llave_destino, propias[m.usuario_id])

    def test_gmf_es_movimiento_propio_sin_llave(self):
        base = carpeta()
        etiquetas = etiquetas_por_id(base)
        gmf = [m for m in leer(base, "entrada/movimientos.csv")
               if etiquetas[m["movimiento_id"]]["categoria_real"] == "comisiones_costos_financieros"]
        self.assertGreater(len(gmf), 5)
        for m in gmf:
            self.assertEqual(m["llave_destino"], "")
            self.assertEqual(m["direccion"], "salida")
            self.assertIn("GMF", m["descripcion"])
            self.assertEqual(etiquetas[m["movimiento_id"]]["llave_destino"], "")


if __name__ == "__main__":
    unittest.main()
