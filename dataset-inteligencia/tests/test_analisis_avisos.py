"""Pruebas de la lógica de avisos (experimento C) con usuarios diminutos y cuentas hechas a mano.

Los montos de las decisiones van en centavos (el CSV va por 100). Cada prueba fija el valor medido, no solo
el booleano, y la causa de la decisión (regla, respaldo, historial insuficiente o nada que vencer en la ventana).
"""

import ast
import unittest
from datetime import date, datetime, timezone
from fractions import Fraction
from pathlib import Path

from analisis.avisos import SerieDeclarada, contexto_en, evaluar, fin_del_dia_local
from analisis.avisos_datos import construir_historiales
from analisis.avisos_estadistica import linea_base, mad, mediana, media, z_modificado
from analisis.configuracion import ConfiguracionInvalida, exacto
from analisis.avisos_config import RUTA_DEFECTO_AVISOS, cargar_configuracion_avisos, desde_dict_avisos

from .test_analisis_cargar import violaciones
from .util_avisos import (CLARO, INTERNET, MERCADO, OTRA, RESTAURANTE, Contador, deposito, pago, retencion,
                          usuario_caida, usuario_internet, usuario_p1, usuario_p4, usuario_p5)

CFG = cargar_configuracion_avisos(RUTA_DEFECTO_AVISOS)
RAIZ = Path(__file__).resolve().parent.parent / "analisis"
MODULOS_DE_AVISOS = ("avisos.py", "avisos_datos.py", "avisos_estadistica.py", "avisos_config.py")


def centavos(pesos: int) -> Fraction:
    return Fraction(pesos * 100)


def decidir(filas, usuario, fecha, tipo, regla="R1", base="mediana", series=(), categorias=None, cfg=CFG):
    historial = construir_historiales(filas, cfg)[usuario]
    ahora = fin_del_dia_local(date.fromisoformat(fecha))
    return evaluar(contexto_en(historial, tuple(series), categorias or {}, ahora, cfg), tipo, regla, base)


class PruebasProrrateo(unittest.TestCase):
    CFG = desde_dict_avisos({**CFG.a_dict(), "prorrateo_acumulado": True})

    def test_apagado_el_acumulado_es_el_total_parcial(self):
        d = decidir(usuario_p4(Contador()), "u4", "2026-10-20", "presupuesto_anticipado", "R2", "mediana")
        self.assertEqual(d.valor, centavos(1_300_000))
        self.assertFalse(d.dispara)

    def test_encendido_el_acumulado_se_proyecta_al_cierre_del_mes(self):
        d = decidir(usuario_p4(Contador()), "u4", "2026-10-20", "presupuesto_anticipado", "R2", "mediana",
                    cfg=self.CFG)
        self.assertEqual(d.valor, centavos(1_300_000) * Fraction(31, 20))
        self.assertTrue(d.dispara)

    def test_la_configuracion_valida_el_booleano(self):
        with self.assertRaises(ConfiguracionInvalida):
            desde_dict_avisos({**CFG.a_dict(), "prorrateo_acumulado": 1})

    def test_la_configuracion_valida_el_tope(self):
        with self.assertRaises(ConfiguracionInvalida):
            desde_dict_avisos({**CFG.a_dict(), "tope_avisos_mes": -1})


class PruebasEstadistica(unittest.TestCase):
    TOTALES = (10, 13, 11, 13, 12, 13)

    def test_mediana_media_y_mad_del_perfil_4(self):
        self.assertEqual(mediana(self.TOTALES), Fraction(25, 2))
        self.assertEqual(media(self.TOTALES), Fraction(12))
        self.assertEqual(mad(self.TOTALES), Fraction(1, 2))

    def test_z_modificado_del_perfil_4_vale_0_6745(self):
        z = z_modificado(Fraction(13), mediana(self.TOTALES), mad(self.TOTALES), exacto(0.6745))
        self.assertEqual(z, Fraction(6745, 10000))

    def test_linea_base_elige_mediana_o_media(self):
        self.assertEqual(linea_base((1, 2, 9), "mediana"), Fraction(2))
        self.assertEqual(linea_base((1, 2, 9), "media"), Fraction(4))
        with self.assertRaises(ValueError):
            linea_base((1, 2, 9), "moda")

    def test_mad_de_valores_con_un_atipico_es_cero(self):
        self.assertEqual(mad((300, 300, 300, 2400, 300, 300)), Fraction(0))


class PruebasPresupuestoAnticipado(unittest.TestCase):
    def setUp(self):
        self.p4 = usuario_p4(Contador())

    def test_perfil_4_r1_dispara_con_mediana_y_con_media(self):
        d = decidir(self.p4, "u4", "2026-10-20", "presupuesto_anticipado", "R1", "mediana")
        self.assertTrue(d.dispara)
        self.assertEqual((d.valor, d.referencia), (centavos(1_300_000), centavos(1_250_000)))
        d = decidir(self.p4, "u4", "2026-10-20", "presupuesto_anticipado", "R1", "media")
        self.assertTrue(d.dispara)
        self.assertEqual(d.referencia, centavos(1_200_000))

    def test_perfil_4_r2_no_dispara_porque_el_z_modificado_vale_0_6745(self):
        d = decidir(self.p4, "u4", "2026-10-20", "presupuesto_anticipado", "R2", "mediana")
        self.assertFalse(d.dispara)
        self.assertEqual(d.estadistico, Fraction(6745, 10000))
        self.assertEqual(d.causa, "z_bajo_el_umbral")

    def test_perfil_4_r2_con_la_media_como_centro_tampoco_dispara(self):
        d = decidir(self.p4, "u4", "2026-10-20", "presupuesto_anticipado", "R2", "media")
        self.assertFalse(d.dispara)
        self.assertEqual(d.estadistico, Fraction(6745, 10000) * 2)

    def test_perfil_4_un_dia_antes_el_acumulado_no_supera_la_mediana(self):
        d = decidir(self.p4, "u4", "2026-10-19", "presupuesto_anticipado", "R1", "mediana")
        self.assertFalse(d.dispara)
        self.assertEqual(d.valor, centavos(1_200_000))

    def test_media_inflada_por_un_mes_atipico_mediana_dispara_y_media_no(self):
        p1 = usuario_p1(Contador())
        d_mediana = decidir(p1, "u1", "2026-10-21", "presupuesto_anticipado", "R1", "mediana")
        d_media = decidir(p1, "u1", "2026-10-21", "presupuesto_anticipado", "R1", "media")
        self.assertEqual((d_mediana.dispara, d_mediana.referencia), (True, centavos(300_000)))
        self.assertEqual((d_media.dispara, d_media.referencia), (False, centavos(650_000)))
        self.assertEqual(d_mediana.valor, centavos(600_000))

    def test_r2_con_mad_cero_usa_el_respaldo_de_r1(self):
        p1 = usuario_p1(Contador())
        d = decidir(p1, "u1", "2026-10-21", "presupuesto_anticipado", "R2", "mediana")
        self.assertTrue(d.dispara)
        self.assertEqual(d.causa, "respaldo_r1_mad_cero")
        d = decidir(p1, "u1", "2026-10-21", "presupuesto_anticipado", "R2", "media")
        self.assertFalse(d.dispara)
        self.assertEqual(d.causa, "respaldo_r1_mad_cero")

    def test_r2_dispara_cuando_el_z_supera_3_5(self):
        filas = usuario_p4(Contador(), "u4")
        extra = [pago(Contador(), "u4", f"2026-10-19T18:{i:02d}:00Z", 100_000) for i in range(30)]
        for i, f in enumerate(extra):
            f["movimiento_id"] = str(9000 + i)
        d = decidir(filas + extra, "u4", "2026-10-20", "presupuesto_anticipado", "R2", "mediana")
        self.assertTrue(d.dispara)
        self.assertEqual(d.valor, centavos(4_300_000))
        self.assertEqual(d.estadistico, Fraction(6745, 10000) * Fraction(3_050_000, 50_000))

    def test_historial_insuficiente_no_dispara(self):
        p5 = usuario_p5(Contador())
        d = decidir(p5, "u5", "2026-10-21", "presupuesto_anticipado", "R1", "mediana")
        self.assertFalse(d.dispara)
        self.assertEqual(d.causa, "historial_insuficiente")
        self.assertEqual(d.valor, centavos(600_000))

    def test_con_tres_meses_completos_ya_se_evalua(self):
        ids = Contador()
        filas = [deposito(ids, "u7", "2026-07-01T14:00:00Z", 1_000_000)]
        for mes in (7, 8, 9):
            filas += [pago(ids, "u7", f"2026-{mes:02d}-10T18:00:00Z", 100_000)]
        filas += [pago(ids, "u7", "2026-10-05T18:00:00Z", 100_001)]
        d = decidir(filas, "u7", "2026-10-21", "presupuesto_anticipado", "R1", "mediana")
        self.assertTrue(d.dispara)
        self.assertEqual(d.referencia, centavos(100_000))

    def test_con_dos_meses_completos_no_se_evalua(self):
        ids = Contador()
        filas = [deposito(ids, "u7", "2026-08-01T14:00:00Z", 1_000_000)]
        for mes in (8, 9):
            filas += [pago(ids, "u7", f"2026-{mes:02d}-10T18:00:00Z", 100_000)]
        filas += [pago(ids, "u7", "2026-10-05T18:00:00Z", 900_000)]
        d = decidir(filas, "u7", "2026-10-21", "presupuesto_anticipado")
        self.assertEqual((d.dispara, d.causa), (False, "historial_insuficiente"))

    def test_primer_movimiento_la_noche_del_30_en_colombia_deja_completo_el_mes_siguiente(self):
        ids = Contador()
        filas = [deposito(ids, "u7", "2026-07-01T02:30:00Z", 1_000_000)]
        for mes in (7, 8, 9):
            filas += [pago(ids, "u7", f"2026-{mes:02d}-10T18:00:00Z", 100_000)]
        filas += [pago(ids, "u7", "2026-10-05T18:00:00Z", 100_001)]
        d = decidir(filas, "u7", "2026-10-21", "presupuesto_anticipado")
        self.assertTrue(d.dispara)

    def test_las_retenciones_y_los_envios_a_personas_no_son_gasto(self):
        p1 = usuario_p1(Contador())
        extra = [retencion(Contador(), "u1", "2026-10-02T18:00:00Z", 50_000)]
        persona = pago(Contador(), "u1", "2026-10-03T18:00:00Z", 70_000, "3001234567")
        persona["tipo_asiento"], persona["tipo_llave"] = "transferencia_interna", "celular"
        for i, f in enumerate(extra + [persona]):
            f["movimiento_id"] = str(8000 + i)
        d = decidir(p1 + extra + [persona], "u1", "2026-10-21", "presupuesto_anticipado")
        self.assertEqual(d.valor, centavos(600_000))

    def test_un_pago_devuelto_no_cuenta_como_gasto(self):
        ids = Contador()
        p1 = usuario_p1(ids)
        pago_devuelto = pago(ids, "u1", "2026-10-02T18:00:00Z", 900_000)
        pago_devuelto["transferencia_id"] = "T1"
        devolucion = deposito(ids, "u1", "2026-10-03T18:00:00Z", 900_000, "Devolucion")
        devolucion["tipo_asiento"], devolucion["transferencia_id"] = "compensacion", "T1"
        d = decidir(p1 + [pago_devuelto, devolucion], "u1", "2026-10-21", "presupuesto_anticipado")
        self.assertEqual(d.valor, centavos(600_000))


class PruebasCategoriaSobreMedia(unittest.TestCase):
    def filas(self):
        ids = Contador()
        filas = [deposito(ids, "u8", "2026-04-01T14:00:00Z", 5_000_000)]
        for mes in range(4, 10):
            filas.append(pago(ids, "u8", f"2026-{mes:02d}-06T18:00:00Z", 100_000, RESTAURANTE))
            filas.append(pago(ids, "u8", f"2026-{mes:02d}-07T18:00:00Z", 500_000, MERCADO))
        filas.append(pago(ids, "u8", "2026-10-05T18:00:00Z", 250_000, RESTAURANTE))
        filas.append(pago(ids, "u8", "2026-10-06T18:00:00Z", 100_000, MERCADO))
        return filas

    CATEGORIAS = {RESTAURANTE: "restaurantes_domicilios", MERCADO: "alimentos_mercado"}

    def test_dispara_por_una_categoria_aunque_el_total_no_supere_su_base(self):
        filas = self.filas()
        total = decidir(filas, "u8", "2026-10-21", "presupuesto_anticipado")
        self.assertFalse(total.dispara)
        self.assertEqual((total.valor, total.referencia), (centavos(350_000), centavos(600_000)))
        d = decidir(filas, "u8", "2026-10-21", "categoria_sobre_media", categorias=self.CATEGORIAS)
        self.assertTrue(d.dispara)
        self.assertEqual((d.valor, d.referencia), (centavos(250_000), centavos(100_000)))
        self.assertIn("restaurantes_domicilios", d.detalle)

    def test_si_ninguna_categoria_supera_su_base_no_dispara(self):
        filas = self.filas()[:-2]
        d = decidir(filas, "u8", "2026-10-21", "categoria_sobre_media", categorias=self.CATEGORIAS)
        self.assertFalse(d.dispara)

    def test_sin_mapa_todo_cae_en_sin_categoria_y_se_evalua_como_total(self):
        d = decidir(self.filas(), "u8", "2026-10-21", "categoria_sobre_media")
        self.assertFalse(d.dispara)
        self.assertEqual(d.valor, centavos(350_000))
        self.assertIn("sin_categoria", d.detalle)

    def test_una_categoria_con_menos_de_tres_meses_con_gasto_no_se_evalua(self):
        ids = Contador()
        filas = self.filas()
        filas.append(pago(ids, "u8", "2026-10-08T18:00:00Z", 900_000, OTRA))
        filas[-1]["movimiento_id"] = "7777"
        categorias = {**self.CATEGORIAS, OTRA: "compras"}
        d = decidir(filas, "u8", "2026-10-21", "categoria_sobre_media", categorias=categorias)
        self.assertTrue(d.dispara)
        self.assertNotIn("compras", d.detalle)

    def test_perfil_4_r2_en_categoria_tampoco_dispara(self):
        p4 = usuario_p4(Contador())
        categorias = {MERCADO: "alimentos_mercado"}
        d1 = decidir(p4, "u4", "2026-10-20", "categoria_sobre_media", "R1", "mediana", categorias=categorias)
        d2 = decidir(p4, "u4", "2026-10-20", "categoria_sobre_media", "R2", "mediana", categorias=categorias)
        self.assertEqual((d1.dispara, d2.dispara), (True, False))


class PruebasPagoNoAlcanza(unittest.TestCase):
    SERIE = (SerieDeclarada(("llave", INTERNET), 30),)

    def test_saldo_menor_que_el_pago_dispara(self):
        filas = usuario_internet(Contador(), "u2", 600_000)
        d = decidir(filas, "u2", "2026-10-03", "pago_no_alcanza", series=self.SERIE)
        self.assertTrue(d.dispara)
        self.assertEqual((d.valor, d.referencia), (centavos(60_600), centavos(89_900)))

    def test_saldo_mayor_que_el_pago_no_dispara(self):
        filas = usuario_internet(Contador(), "u2", 1_000_000)
        d = decidir(filas, "u2", "2026-10-03", "pago_no_alcanza", series=self.SERIE)
        self.assertFalse(d.dispara)
        self.assertEqual((d.valor, d.referencia), (centavos(460_600), centavos(89_900)))

    def test_saldo_igual_al_pago_no_dispara(self):
        filas = usuario_internet(Contador(), "u2", 539_400 + 89_900)
        d = decidir(filas, "u2", "2026-10-03", "pago_no_alcanza", series=self.SERIE)
        self.assertEqual((d.valor, d.dispara), (centavos(89_900), False))

    def test_pago_fuera_de_la_ventana_de_7_dias_no_dispara_aunque_el_saldo_sea_corto(self):
        filas = usuario_internet(Contador(), "u2", 600_000)
        d = decidir(filas, "u2", "2026-09-27", "pago_no_alcanza", series=self.SERIE)
        self.assertFalse(d.dispara)
        self.assertEqual(d.causa, "sin_pago_en_la_ventana")

    def test_el_limite_de_la_ventana_es_inclusivo(self):
        filas = usuario_internet(Contador(), "u2", 600_000)
        self.assertTrue(decidir(filas, "u2", "2026-09-28", "pago_no_alcanza", series=self.SERIE).dispara)

    def test_sin_series_no_hay_nada_que_vencer(self):
        filas = usuario_internet(Contador(), "u2", 600_000)
        d = decidir(filas, "u2", "2026-10-03", "pago_no_alcanza")
        self.assertEqual((d.dispara, d.causa), (False, "sin_series"))

    def test_el_monto_esperado_es_la_mediana_de_los_montos_de_la_serie(self):
        ids = Contador()
        filas = usuario_internet(ids, "u2", 1_000_000)
        filas.append(pago(ids, "u2", "2026-08-05T20:00:00Z", 900_000, INTERNET))
        d = decidir(filas, "u2", "2026-10-03", "pago_no_alcanza", series=self.SERIE)
        self.assertEqual(d.referencia, centavos(89_900))

    def test_suma_los_pagos_de_todas_las_series_dentro_de_la_ventana(self):
        ids = Contador()
        filas = usuario_internet(ids, "u2", 879_400)
        filas += [pago(ids, "u2", f"2026-{mes:02d}-06T15:00:00Z", 40_000, OTRA) for mes in range(4, 10)]
        un_solo = decidir(filas, "u2", "2026-10-03", "pago_no_alcanza", series=self.SERIE)
        dos = decidir(filas, "u2", "2026-10-03", "pago_no_alcanza",
                      series=self.SERIE + (SerieDeclarada(("llave", OTRA), 30),))
        self.assertEqual((un_solo.dispara, un_solo.valor), (False, centavos(100_000)))
        self.assertEqual((dos.dispara, dos.referencia), (True, centavos(129_900)))

    def test_la_retencion_tambien_baja_el_saldo(self):
        ids = Contador()
        filas = usuario_internet(ids, "u2", 630_000)
        sin_retencion = decidir(filas, "u2", "2026-10-03", "pago_no_alcanza", series=self.SERIE)
        con_retencion = decidir(filas + [retencion(ids, "u2", "2026-09-20T15:00:00Z", 20_000)], "u2", "2026-10-03",
                                "pago_no_alcanza", series=self.SERIE)
        self.assertEqual(sin_retencion.valor - con_retencion.valor, centavos(20_000))
        self.assertEqual((sin_retencion.dispara, con_retencion.dispara), (False, True))

    def test_un_pago_vencido_se_proyecta_al_siguiente_ciclo(self):
        filas = usuario_internet(Contador(), "u2", 600_000)
        d = decidir(filas, "u2", "2026-10-21", "pago_no_alcanza", series=self.SERIE)
        self.assertEqual((d.dispara, d.causa), (False, "sin_pago_en_la_ventana"))


class PruebasCobroNuevo(unittest.TestCase):
    def claro(self, ids: Contador, usuario: str = "u3") -> list[dict]:
        filas = [deposito(ids, usuario, "2026-04-01T14:00:00Z", 5_000_000)]
        for mes, dia, monto in ((4, 5, 89_900), (5, 5, 89_900), (6, 5, 89_900), (7, 5, 89_900), (8, 9, 89_900),
                                (9, 5, 94_900)):
            filas.append(pago(ids, usuario, f"2026-{mes:02d}-{dia:02d}T15:00:00Z", monto, CLARO))
        return filas

    def test_el_cambio_de_dia_y_de_monto_de_una_serie_existente_no_es_cobro_nuevo(self):
        filas = self.claro(Contador())
        serie = (SerieDeclarada(("llave", CLARO), 30),)
        d = decidir(filas, "u3", "2026-10-21", "cobro_nuevo", series=serie)
        self.assertFalse(d.dispara)
        self.assertEqual(d.causa, "sin_llave_nueva")

    def test_una_llave_nueva_con_dos_pagos_de_monto_similar_dispara(self):
        ids = Contador()
        filas = self.claro(ids)
        filas += [pago(ids, "u3", "2026-10-01T15:00:00Z", 50_000, OTRA), pago(ids, "u3", "2026-10-08T15:00:00Z", 52_000, OTRA)]
        d = decidir(filas, "u3", "2026-10-21", "cobro_nuevo")
        self.assertTrue(d.dispara)
        self.assertIn(OTRA, d.detalle)

    def test_una_llave_nueva_con_un_solo_pago_no_dispara(self):
        ids = Contador()
        filas = self.claro(ids) + [pago(ids, "u3", "2026-10-01T15:00:00Z", 50_000, OTRA)]
        self.assertFalse(decidir(filas, "u3", "2026-10-21", "cobro_nuevo").dispara)

    def test_un_solo_pago_pero_con_serie_declarada_dispara(self):
        ids = Contador()
        filas = self.claro(ids) + [pago(ids, "u3", "2026-10-01T15:00:00Z", 50_000, OTRA)]
        d = decidir(filas, "u3", "2026-10-21", "cobro_nuevo", series=(SerieDeclarada(("llave", OTRA), 30),))
        self.assertTrue(d.dispara)

    def test_dos_pagos_de_monto_muy_distinto_no_cuentan_como_similares(self):
        ids = Contador()
        filas = self.claro(ids)
        filas += [pago(ids, "u3", "2026-10-01T15:00:00Z", 50_000, OTRA), pago(ids, "u3", "2026-10-08T15:00:00Z", 200_000, OTRA)]
        self.assertFalse(decidir(filas, "u3", "2026-10-21", "cobro_nuevo").dispara)

    def test_el_primer_pago_hace_exactamente_35_dias_todavia_es_nuevo(self):
        ids = Contador()
        filas = self.claro(ids)
        filas += [pago(ids, "u3", "2026-09-16T15:00:00Z", 50_000, OTRA), pago(ids, "u3", "2026-10-01T15:00:00Z", 50_000, OTRA)]
        self.assertTrue(decidir(filas, "u3", "2026-10-21", "cobro_nuevo").dispara)

    def test_el_primer_pago_hace_36_dias_ya_es_conocido(self):
        ids = Contador()
        filas = self.claro(ids)
        filas += [pago(ids, "u3", "2026-09-15T15:00:00Z", 50_000, OTRA), pago(ids, "u3", "2026-10-01T15:00:00Z", 50_000, OTRA)]
        d = decidir(filas, "u3", "2026-10-21", "cobro_nuevo")
        self.assertFalse(d.dispara)

    def test_un_pago_posterior_a_ahora_no_crea_un_cobro_nuevo(self):
        ids = Contador()
        filas = self.claro(ids)
        filas += [pago(ids, "u3", "2026-10-22T15:00:00Z", 50_000, OTRA), pago(ids, "u3", "2026-10-23T15:00:00Z", 50_000, OTRA)]
        self.assertFalse(decidir(filas, "u3", "2026-10-21", "cobro_nuevo").dispara)


class PruebasCaidaDeIngreso(unittest.TestCase):
    ESTABLE = (1_000_000, 1_000_000, 1_000_000, 1_000_000, 600_000, 1_000_000)

    def test_perfil_estable_con_una_caida_del_40_por_ciento_dispara(self):
        filas = usuario_caida(Contador(), "u2", self.ESTABLE)
        for base in ("mediana", "media"):
            d = decidir(filas, "u2", "2026-08-31", "caida_ingreso", "R1", base)
            self.assertTrue(d.dispara, base)
            self.assertEqual((d.valor, d.referencia), (centavos(600_000), centavos(1_000_000)))

    def test_el_saldo_inicial_no_es_ingreso(self):
        filas = usuario_caida(Contador(), "u2", self.ESTABLE)
        d = decidir(filas, "u2", "2026-08-31", "caida_ingreso", "R1", "media")
        self.assertEqual(d.referencia, centavos(1_000_000))

    def test_r2_con_mad_cero_usa_el_respaldo_de_r1_completo(self):
        filas = usuario_caida(Contador(), "u2", self.ESTABLE)
        d = decidir(filas, "u2", "2026-08-31", "caida_ingreso", "R2", "mediana")
        self.assertEqual((d.dispara, d.causa), (True, "respaldo_r1_mad_cero"))

    def test_una_caida_menor_al_30_por_ciento_no_dispara(self):
        filas = usuario_caida(Contador(), "u2", (1_000_000,) * 4 + (750_000, 1_000_000))
        self.assertFalse(decidir(filas, "u2", "2026-08-31", "caida_ingreso").dispara)

    def test_a_mitad_de_mes_se_evalua_el_ultimo_mes_completo(self):
        filas = usuario_caida(Contador(), "u2", self.ESTABLE)
        d = decidir(filas, "u2", "2026-08-15", "caida_ingreso")
        self.assertFalse(d.dispara)
        self.assertEqual(d.valor, centavos(1_000_000))

    def test_perfil_irregular_no_dispara_por_la_guardia_del_cv_robusto(self):
        filas = usuario_caida(Contador(), "u3", (950_000, 890_000, 550_000, 430_000, 700_000, 300_000))
        d = decidir(filas, "u3", "2026-09-30", "caida_ingreso", "R1", "mediana")
        self.assertEqual((d.dispara, d.causa), (False, "guardia_cv_robusto"))
        self.assertEqual(d.valor, centavos(300_000))
        self.assertEqual(d.referencia, centavos(700_000))
        self.assertEqual(d.estadistico, Fraction(190, 700))

    def test_perfil_irregular_r2_tampoco_dispara(self):
        filas = usuario_caida(Contador(), "u3", (950_000, 890_000, 550_000, 430_000, 700_000, 300_000))
        d = decidir(filas, "u3", "2026-09-30", "caida_ingreso", "R2", "mediana")
        self.assertFalse(d.dispara)
        self.assertEqual(d.estadistico, Fraction(6745, 10000) * Fraction(-400_000, 190_000))

    def test_con_menos_de_tres_meses_previos_no_se_evalua(self):
        filas = usuario_caida(Contador(), "u2", self.ESTABLE)
        d = decidir(filas, "u2", "2026-06-30", "caida_ingreso")
        self.assertEqual((d.dispara, d.causa), (False, "historial_insuficiente"))


class PruebasDelReloj(unittest.TestCase):
    def test_el_fin_del_dia_local_es_el_dia_siguiente_a_las_04_59_59_utc(self):
        self.assertEqual(fin_del_dia_local(date(2026, 10, 20)), datetime(2026, 10, 21, 4, 59, 59, tzinfo=timezone.utc))

    def test_los_movimientos_futuros_no_cambian_la_decision(self):
        base = usuario_p4(Contador())
        futuros = [pago(Contador(), "u4", f"2026-10-2{1 + i % 8}T18:{i:02d}:00Z", 100_000) for i in range(40)]
        for i, f in enumerate(futuros):
            f["movimiento_id"] = str(5000 + i)
        for tipo in ("presupuesto_anticipado", "categoria_sobre_media", "cobro_nuevo"):
            a = decidir(base, "u4", "2026-10-20", tipo)
            b = decidir(base + futuros, "u4", "2026-10-20", tipo)
            self.assertEqual(a, b, tipo)

    def test_un_pago_en_el_ultimo_segundo_del_dia_local_cuenta_y_uno_a_las_05_00_no(self):
        base = usuario_p4(Contador())
        dentro = pago(Contador(), "u4", "2026-10-21T04:59:59Z", 100_000)
        fuera = pago(Contador(), "u4", "2026-10-21T05:00:00Z", 100_000)
        dentro["movimiento_id"], fuera["movimiento_id"] = "6001", "6002"
        d = decidir(base + [dentro, fuera], "u4", "2026-10-20", "presupuesto_anticipado")
        self.assertEqual(d.valor, centavos(1_400_000))

    def test_un_pago_de_la_noche_del_30_en_colombia_pertenece_al_mes_anterior(self):
        base = usuario_p4(Contador())
        noche = pago(Contador(), "u4", "2026-10-01T03:00:00Z", 100_000)
        noche["movimiento_id"] = "6003"
        d = decidir(base + [noche], "u4", "2026-10-20", "presupuesto_anticipado")
        self.assertEqual(d.valor, centavos(1_300_000))

    def test_un_ahora_sin_zona_horaria_se_rechaza(self):
        historial = construir_historiales(usuario_p4(Contador()), CFG)["u4"]
        with self.assertRaises(ValueError):
            contexto_en(historial, (), {}, datetime(2026, 10, 20, 23, 59, 59), CFG)


class PruebasDeConfiguracion(unittest.TestCase):
    def test_el_archivo_por_defecto_declara_las_reglas_y_el_optimo_restringido(self):
        self.assertEqual((CFG.meses_minimos, CFG.ventana_pago_dias, CFG.ventana_cobro_dias), (3, 7, 35))
        self.assertEqual((CFG.caida_d, CFG.cv_robusto_max, CFG.z_umbral, CFG.z_factor), (0.3, 0.25, 3.5, 0.6745))
        self.assertEqual(CFG.reglas, ("R1", "R2"))
        self.assertEqual(CFG.lineas_base, ("mediana", "media"))
        self.assertEqual(CFG.fuentes_series, ("oraculo", "detectadas"))
        r = CFG.recurrencia
        self.assertEqual((r.w_monto, r.tol_monto, r.n_min, r.umbral), (0.6, 0.15, 3, 0.7))
        self.assertEqual((r.cadencias_dias, r.tolerancias_dias), ((7, 14, 30), (2, 3, 4)))

    def test_rechaza_claves_faltantes_y_valores_fuera_de_rango_juntando_los_problemas(self):
        datos = {**CFG.a_dict(), "caida_d": 1.5, "reglas": ["R3"]}
        del datos["meses_minimos"]
        with self.assertRaises(ConfiguracionInvalida) as cm:
            desde_dict_avisos(datos)
        mensaje = str(cm.exception)
        for texto in ("meses_minimos", "caida_d", "reglas"):
            self.assertIn(texto, mensaje)

    def test_el_hash_es_estable_y_cambia_con_un_parametro(self):
        self.assertEqual(CFG.hash(), cargar_configuracion_avisos(RUTA_DEFECTO_AVISOS).hash())
        otra = desde_dict_avisos({**CFG.a_dict(), "ventana_pago_dias": 5})
        self.assertNotEqual(CFG.hash(), otra.hash())

    def test_a_dict_y_desde_dict_son_inversos(self):
        self.assertEqual(desde_dict_avisos(CFG.a_dict()), CFG)

    def test_un_parametro_distinto_cambia_la_decision(self):
        filas = usuario_internet(Contador(), "u2", 600_000)
        corta = desde_dict_avisos({**CFG.a_dict(), "ventana_pago_dias": 1})
        d = decidir(filas, "u2", "2026-10-03", "pago_no_alcanza", series=PruebasPagoNoAlcanza.SERIE, cfg=corta)
        self.assertFalse(d.dispara)


class PruebaEstaticaDeAvisos(unittest.TestCase):
    def test_ningun_modulo_de_avisos_toca_la_verdad(self):
        for nombre in MODULOS_DE_AVISOS:
            ruta = RAIZ / nombre
            self.assertTrue(ruta.exists(), nombre)
            self.assertEqual(violaciones(ruta.read_text(encoding="utf-8")), [], nombre)

    def test_ningun_modulo_de_avisos_importa_el_cargador_ni_la_evaluacion(self):
        prohibidos = {"cargar", "evaluacion", "avisos_evaluacion", "avisos_fondo", "avisos_experimento"}
        for nombre in MODULOS_DE_AVISOS:
            arbol = ast.parse((RAIZ / nombre).read_text(encoding="utf-8"))
            for nodo in ast.walk(arbol):
                if isinstance(nodo, ast.ImportFrom):
                    modulo = (nodo.module or "").split(".")[-1]
                    self.assertNotIn(modulo, prohibidos, f"{nombre} importa {nodo.module}")
                    self.assertFalse(any(a.name in prohibidos for a in nodo.names), nombre)
                elif isinstance(nodo, ast.Import):
                    self.assertFalse(any(a.name.split(".")[-1] in prohibidos for a in nodo.names), nombre)

    def test_las_funciones_de_avisos_miden_menos_de_50_lineas_y_los_archivos_menos_de_800(self):
        for nombre in MODULOS_DE_AVISOS:
            texto = (RAIZ / nombre).read_text(encoding="utf-8")
            self.assertLess(len(texto.splitlines()), 800, nombre)
            for nodo in ast.walk(ast.parse(texto)):
                if isinstance(nodo, ast.FunctionDef):
                    self.assertLess(nodo.end_lineno - nodo.lineno + 1, 50, f"{nombre}:{nodo.name}")


if __name__ == "__main__":
    unittest.main()
