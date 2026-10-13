"""Pruebas del detector de recurrencia: ejemplos calculados a mano, reloj inyectado e identidad."""

import dataclasses
import unittest
from datetime import datetime, timezone

from analisis.configuracion import Configuracion
from analisis.pagos import extraer_pagos, normalizar_nombre
from analisis.recurrencia import calcular_indice, detectar
from generador.nombres import canonizar

from .util_analisis import fila, pagos_en

CFG = Configuracion.por_defecto()
FIN = datetime(2026, 12, 31, 23, 59, 59, tzinfo=timezone.utc)
CLARO = [("2026-04-05", 89900), ("2026-05-05", 89900), ("2026-06-05", 89900), ("2026-07-05", 89900),
         ("2026-08-09", 89900), ("2026-09-05", 94900)]
MOVISTAR = [("2026-01-10", 20000), ("2026-02-20", 25000), ("2026-03-20", 50000)]


def indice_de(filas, usuario, llave, ahora=FIN, cfg=CFG):
    resultado = detectar(extraer_pagos(filas), cfg, ahora)
    return resultado[(usuario, ("llave", llave))]


class PruebasEjemplosAMano(unittest.TestCase):
    def test_claro_da_092_y_es_recurrente(self):
        i = indice_de(pagos_en("u", "0019870620", 1, CLARO), "u", "0019870620")
        self.assertEqual(i.ocurrencias, 6)
        self.assertAlmostEqual(i.n, 1.0)
        self.assertAlmostEqual(i.monto, 1.0)
        self.assertAlmostEqual(i.fecha, 0.8)
        self.assertAlmostEqual(i.r, 0.92, places=9)
        self.assertEqual(i.cadencia_dias, 30)
        self.assertTrue(i.es_recurrente)

    def test_movistar_irregular_da_040_y_no_es_recurrente(self):
        i = indice_de(pagos_en("u", "0064503630", 1, MOVISTAR), "u", "0064503630")
        self.assertAlmostEqual(i.monto, 1 / 3, places=9)
        self.assertAlmostEqual(i.fecha, 0.5)
        self.assertAlmostEqual(i.r, 0.40, places=9)
        self.assertFalse(i.es_recurrente)

    def test_movistar_con_umbral_exactamente_040_es_recurrente(self):
        """R vale 0,4 de forma exacta: la comparación es inclusiva y no sufre error de coma flotante."""
        cfg = dataclasses.replace(CFG, umbral=0.4)
        i = indice_de(pagos_en("u", "0064503630", 1, MOVISTAR), "u", "0064503630", cfg=cfg)
        self.assertTrue(i.es_recurrente)


class PruebasMenosDeDosOcurrencias(unittest.TestCase):
    def test_un_solo_pago_da_r_cero(self):
        i = indice_de(pagos_en("u", "0011111111", 1, [("2026-05-05", 50000)]), "u", "0011111111")
        self.assertEqual(i.ocurrencias, 1)
        self.assertEqual(i.r, 0.0)
        self.assertFalse(i.es_recurrente)
        self.assertIsNone(i.cadencia_dias)

    def test_umbral_cero_tampoco_marca_un_pago_unico(self):
        cfg = dataclasses.replace(CFG, umbral=0.0)
        i = indice_de(pagos_en("u", "0011111111", 1, [("2026-05-05", 50000)]), "u", "0011111111", cfg=cfg)
        self.assertFalse(i.es_recurrente)

    def test_dos_pagos_iguales_a_30_dias_dan_dos_tercios(self):
        i = indice_de(pagos_en("u", "0011111111", 1, [("2026-05-10", 30000), ("2026-06-09", 30000)]),
                      "u", "0011111111")
        self.assertAlmostEqual(i.r, 2 / 3, places=9)


class PruebasRelojInyectado(unittest.TestCase):
    def test_un_pago_posterior_a_ahora_no_cuenta(self):
        filas = pagos_en("u", "0019870620", 1, CLARO)
        ahora = datetime(2026, 9, 4, 12, 0, 0, tzinfo=timezone.utc)
        i = indice_de(filas, "u", "0019870620", ahora=ahora)
        self.assertEqual(i.ocurrencias, 5)
        self.assertAlmostEqual(i.fecha, 0.75)
        self.assertAlmostEqual(i.r, 0.90, places=9)

    def test_ahora_anterior_al_segundo_pago_deja_un_pago_y_r_cero(self):
        filas = pagos_en("u", "0019870620", 1, CLARO)
        ahora = datetime(2026, 5, 4, 0, 0, 0, tzinfo=timezone.utc)
        i = indice_de(filas, "u", "0019870620", ahora=ahora)
        self.assertEqual((i.ocurrencias, i.r), (1, 0.0))

    def test_el_pago_exactamente_en_ahora_si_cuenta(self):
        filas = pagos_en("u", "0019870620", 1, CLARO)
        ahora = datetime(2026, 9, 5, 16, 0, 0, tzinfo=timezone.utc)
        self.assertEqual(indice_de(filas, "u", "0019870620", ahora=ahora).ocurrencias, 6)

    def test_ahora_anterior_a_todo_no_produce_parejas(self):
        filas = pagos_en("u", "0019870620", 1, CLARO)
        ahora = datetime(2026, 1, 1, tzinfo=timezone.utc)
        self.assertEqual(detectar(extraer_pagos(filas), CFG, ahora), {})

    def test_ahora_sin_zona_horaria_se_rechaza(self):
        with self.assertRaises(ValueError):
            detectar(extraer_pagos(pagos_en("u", "0019870620", 1, CLARO)), CFG, datetime(2026, 9, 4))


class PruebasFechaLocal(unittest.TestCase):
    def test_los_intervalos_se_miden_en_dias_locales_de_colombia(self):
        """Un pago a las 02:30 UTC del día 1 es del día 30 en Colombia: el intervalo mensual no se corre."""
        filas = [
            fila(1, "u", "2026-08-31T02:30:00Z", 50000), fila(2, "u", "2026-10-01T02:30:00Z", 50000),
            fila(3, "u", "2026-10-31T02:30:00Z", 50000),
        ]
        i = indice_de(filas, "u", "0011111111")
        self.assertAlmostEqual(i.fecha, 1.0)
        pagos = extraer_pagos(filas)
        self.assertEqual([p.fecha_local.isoformat() for p in pagos], ["2026-08-30", "2026-09-30", "2026-10-30"])


class PruebasTolerancias(unittest.TestCase):
    def test_tolerancia_de_monto_inclusiva_en_el_borde(self):
        filas = pagos_en("u", "0011111111", 1, [("2026-05-05", 100000), ("2026-06-04", 110000), ("2026-07-04", 100000)])
        self.assertAlmostEqual(calcular_indice_de(filas).monto, 1.0)

    def test_tolerancia_de_dias_inclusiva_en_el_borde(self):
        # Intervalos de 34 y 30 días: mediana 32, cadencia 30, tolerancia 4: ambos dentro.
        filas = pagos_en("u", "0011111111", 1, [("2026-05-01", 50000), ("2026-06-04", 50000), ("2026-07-04", 50000)])
        self.assertAlmostEqual(calcular_indice_de(filas).fecha, 1.0)

    def test_cadencia_semanal_se_reconoce(self):
        filas = pagos_en("u", "0011111111", 1, [(f"2026-05-{d:02d}", 20000) for d in (1, 8, 15, 22)])
        i = calcular_indice_de(filas)
        self.assertEqual(i.cadencia_dias, 7)
        self.assertAlmostEqual(i.fecha, 1.0)

    def test_empate_entre_cadencias_elige_la_menor(self):
        # Un único intervalo de 10,5 días no existe, así que se usan dos intervalos de 10 y 11 (mediana 10,5).
        filas = pagos_en("u", "0011111111", 1, [("2026-05-01", 1000), ("2026-05-11", 1000), ("2026-05-22", 1000)])
        self.assertEqual(calcular_indice_de(filas).cadencia_dias, 7)

    def test_calcular_indice_directo_con_fechas_y_montos(self):
        from datetime import date
        i = calcular_indice([date(2026, 1, 1), date(2026, 1, 31)], [100, 100], CFG)
        self.assertAlmostEqual(i.r, 2 / 3, places=9)


def calcular_indice_de(filas):
    return indice_de(filas, "u", "0011111111")


class PruebasIdentidad(unittest.TestCase):
    def test_el_mcc_nunca_es_identidad(self):
        """Dos llaves distintas con el mismo MCC y el mismo nombre no se mezclan."""
        a = pagos_en("u", "0011111111", 1, [("2026-05-05", 59900), ("2026-06-05", 59900), ("2026-07-05", 59900)],
                     mcc="4814", canal="qr", nombre_comercio_crudo="TELEFONIA XYZ")
        b = pagos_en("u", "0022222222", 10, [("2026-05-20", 20000), ("2026-06-02", 90000), ("2026-07-30", 33000)],
                     mcc="4814", canal="qr", nombre_comercio_crudo="TELEFONIA XYZ")
        resultado = detectar(extraer_pagos(a + b), CFG, FIN)
        self.assertEqual(sorted(k[1] for k in resultado), [("llave", "0011111111"), ("llave", "0022222222")])
        self.assertEqual(resultado[("u", ("llave", "0011111111"))].ocurrencias, 3)
        self.assertEqual(resultado[("u", ("llave", "0022222222"))].ocurrencias, 3)
        self.assertTrue(resultado[("u", ("llave", "0011111111"))].es_recurrente)
        self.assertFalse(resultado[("u", ("llave", "0022222222"))].es_recurrente)

    def test_usuarios_distintos_a_la_misma_llave_son_parejas_distintas(self):
        filas = (pagos_en("u1", "0011111111", 1, [("2026-05-05", 50000), ("2026-06-05", 50000)])
                 + pagos_en("u2", "0011111111", 10, [("2026-05-05", 50000)]))
        resultado = detectar(extraer_pagos(filas), CFG, FIN)
        self.assertEqual(resultado[("u1", ("llave", "0011111111"))].ocurrencias, 2)
        self.assertEqual(resultado[("u2", ("llave", "0011111111"))].ocurrencias, 1)

    def test_sin_llave_la_identidad_degradada_es_el_nombre_canonico(self):
        extra = {"llave_destino": "", "tipo_llave": ""}
        nombres = ["Gimnasio_Fit", "GIMNASIO FIT", "gimnasio-fit"]
        filas = [fila(i + 1, "u", f"2026-0{5 + i}-05T16:00:00Z", 80000, nombre_comercio_crudo=n, **extra)
                 for i, n in enumerate(nombres)]
        resultado = detectar(extraer_pagos(filas), CFG, FIN)
        self.assertEqual(list(resultado), [("u", ("nombre", "GIMNASIO-FIT"))])
        self.assertEqual(resultado[("u", ("nombre", "GIMNASIO-FIT"))].ocurrencias, 3)

    def test_la_identidad_por_nombre_no_choca_con_una_llave(self):
        con_llave = fila(1, "u", "2026-05-05T16:00:00Z", 1000, "GIMNASIO-FIT")
        sin_llave = fila(2, "u", "2026-06-05T16:00:00Z", 1000, "", tipo_llave="", nombre_comercio_crudo="Gimnasio Fit")
        resultado = detectar(extraer_pagos([con_llave, sin_llave]), CFG, FIN)
        self.assertEqual(len(resultado), 2)

    def test_sin_llave_y_con_nombre_generico_o_vacio_no_hay_identidad(self):
        extra = {"llave_destino": "", "tipo_llave": ""}
        filas = [fila(1, "u", "2026-05-05T16:00:00Z", 1000, nombre_comercio_crudo="", **extra),
                 fila(2, "u", "2026-06-05T16:00:00Z", 1000, nombre_comercio_crudo="Pago a comercio", **extra),
                 fila(3, "u", "2026-07-05T16:00:00Z", 1000, nombre_comercio_crudo="PAGO A COMERCIO", **extra)]
        self.assertEqual(extraer_pagos(filas), ())

    def test_normalizar_nombre_coincide_con_canonizar_del_generador(self):
        for texto in ["Gimnasio_Fit", "  McDonald's ", "SUPERMERCADO_don_pepe", "Cafetería   Ñandú & Co.",
                      "A -. B", "x" * 60, "TIENDA-DOÑA-LUCY"]:
            self.assertEqual(normalizar_nombre(texto), canonizar(texto), texto)


class PruebasQueEsUnPago(unittest.TestCase):
    def test_solo_cuentan_salidas_de_tipos_de_pago_con_llave(self):
        base = pagos_en("u", "0011111111", 1, [("2026-05-05", 50000)])[0]
        casos = {
            "deposito": {**base, "tipo_asiento": "deposito", "direccion": "entrada", "llave_destino": ""},
            "gmf": {**base, "tipo_asiento": "retencion", "descripcion": "GMF 4x1000", "llave_destino": ""},
            "retencion_con_llave": {**base, "tipo_asiento": "retencion", "descripcion": "Envío a persona"},
            "compensacion": {**base, "tipo_asiento": "compensacion", "direccion": "entrada"},
            "entrada_con_llave": {**base, "direccion": "entrada"},
        }
        for nombre, f in casos.items():
            self.assertEqual(extraer_pagos([f]), (), nombre)
        for tipo in ("liquidacion_confirmada", "transferencia_interna", "despacho_riel"):
            self.assertEqual(len(extraer_pagos([{**base, "tipo_asiento": tipo}])), 1, tipo)

    def test_un_envio_devuelto_no_es_un_pago(self):
        envio = fila(1, "u", "2026-09-05T15:00:00Z", 200000, "3072614021", tipo_asiento="despacho_riel",
                     tipo_llave="celular", transferencia_id="t1")
        gmf = fila(2, "u", "2026-09-05T15:00:00Z", 800, "", tipo_asiento="retencion", tipo_llave="",
                   transferencia_id="t1", descripcion="GMF 4x1000")
        devolucion = fila(3, "u", "2026-09-06T14:00:00Z", 200800, "3072614021", tipo_asiento="compensacion",
                          direccion="entrada", tipo_llave="celular", transferencia_id="t1")
        otro = fila(4, "u", "2026-10-06T14:00:00Z", 200000, "3072614021", tipo_asiento="despacho_riel",
                    tipo_llave="celular", transferencia_id="t2")
        pagos = extraer_pagos([envio, gmf, devolucion, otro])
        self.assertEqual([p.movimiento_id for p in pagos], [4])

    def test_el_orden_de_las_filas_de_entrada_no_cambia_el_resultado(self):
        filas = pagos_en("u", "0019870620", 1, CLARO)
        directo = detectar(extraer_pagos(filas), CFG, FIN)
        invertido = detectar(extraer_pagos(list(reversed(filas))), CFG, FIN)
        self.assertEqual(directo, invertido)

    def test_no_muta_la_entrada(self):
        filas = pagos_en("u", "0019870620", 1, CLARO)
        copia = [dict(f) for f in filas]
        detectar(extraer_pagos(filas), CFG, FIN)
        self.assertEqual(filas, copia)


if __name__ == "__main__":
    unittest.main()
