"""Pruebas de las variantes A (hoy) y B (QR): mismo mundo, distinta presentación."""

import re
import unittest
from datetime import date

from .util import carpeta, etiquetas_por_id, fecha_local, leer

MCC_POR_CATEGORIA = {
    "alimentos_mercado": {"5411", "5499"},
    "restaurantes_domicilios": {"5812", "5813", "5814"},
    "transporte": {"4111", "4121", "4131", "5541", "5542"},
    "vivienda_servicios": {"4814", "4899", "4900"},
    "salud": {"5912", "8011", "8062", "8099"},
    "educacion": {"8211", "8220", "8299", "5942"},
    "entretenimiento": {"7832", "7922", "5815", "5816", "5817", "5818"},
    "compras": {"5311", "5310", "5651", "5691", "5732", "5964"},
}
ARCHIVOS_DE_VERDAD = (
    "verdad/empresas.csv", "verdad/comercios.csv", "verdad/etiquetas.csv",
    "verdad/correcciones_verdad.csv", "verdad/avisos_esperados.csv",
    "verdad/nombres_historial.csv", "verdad/casos_vista.csv", "verdad/series.csv",
    "entrada/correcciones.csv",
)


def pagos_a_comercio(base):
    return [m for m in leer(base, "entrada/movimientos.csv")
            if m["tipo_llave"] == "comercio" and m["direccion"] == "salida"
            and m["tipo_asiento"] == "liquidacion_confirmada"]


class PruebasVarianteA(unittest.TestCase):
    def test_mcc_vacio_y_canal_llave_en_todas_las_filas(self):
        for m in leer(carpeta(variante="A"), "entrada/movimientos.csv"):
            self.assertEqual(m["mcc"], "")
            self.assertEqual(m["canal"], "llave")

    def test_nombre_vacio_en_cerca_del_8_por_ciento_de_los_pagos_a_comercio(self):
        pagos = pagos_a_comercio(carpeta(variante="A"))
        vacios = sum(1 for m in pagos if m["nombre_comercio_crudo"] == "")
        self.assertGreater(len(pagos), 1000)
        self.assertTrue(0.05 <= vacios / len(pagos) <= 0.12, vacios / len(pagos))

    def test_nombre_enmascarado_o_generico(self):
        for m in pagos_a_comercio(carpeta(variante="A")):
            nombre = m["nombre_comercio_crudo"]
            self.assertTrue(nombre == "" or nombre == "Pago a comercio" or "X" in nombre or len(nombre) <= 4,
                            nombre)

    def test_pagos_a_personas_llevan_nombre_enmascarado(self):
        base = carpeta(variante="A")
        personas = [m for m in leer(base, "entrada/movimientos.csv")
                    if m["tipo_llave"] in ("celular", "correo", "alfanumerico", "documento")
                    and m["tipo_asiento"] != "compensacion"]
        self.assertTrue(personas)
        self.assertTrue(all(re.search(r"X", m["nombre_comercio_crudo"]) for m in personas[:50]))


class PruebasVarianteB(unittest.TestCase):
    def setUp(self):
        self.base = carpeta(variante="B")
        self.filas = leer(self.base, "entrada/movimientos.csv")

    def test_mcc_de_4_digitos_solo_en_filas_qr(self):
        for m in self.filas:
            if m["canal"] == "qr":
                self.assertRegex(m["mcc"], r"^[0-9]{4}$")
            else:
                self.assertEqual(m["mcc"], "")
                self.assertEqual(m["canal"], "llave")

    def test_el_mcc_sale_de_la_categoria_real(self):
        etiquetas = etiquetas_por_id(self.base)
        qr = [m for m in self.filas if m["canal"] == "qr"]
        self.assertGreater(len(qr), 500)
        for m in qr:
            self.assertIn(m["mcc"], MCC_POR_CATEGORIA[etiquetas[m["movimiento_id"]]["categoria_real"]])
            self.assertEqual(m["tipo_llave"], "comercio")

    def test_fraccion_qr_cercana_al_60_por_ciento(self):
        pagos = pagos_a_comercio(self.base)
        fraccion = sum(1 for m in pagos if m["canal"] == "qr") / len(pagos)
        self.assertTrue(0.45 <= fraccion <= 0.70, fraccion)

    def test_nombre_qr_de_maximo_25_caracteres(self):
        for m in self.filas:
            if m["canal"] == "qr":
                self.assertTrue(0 < len(m["nombre_comercio_crudo"]) <= 25, m)

    def test_fraccion_qr_parametrizable(self):
        base = carpeta(variante="B", fraccion_qr=0.0)
        self.assertTrue(all(m["canal"] == "llave" for m in leer(base, "entrada/movimientos.csv")))


class PruebasNombreQrConElTiempo(unittest.TestCase):
    """El nombre del QR es el que el comercio tenía en la fecha del pago, no el de fin de periodo."""

    def setUp(self):
        self.base = carpeta(variante="B")
        self.movs = {m["movimiento_id"]: m for m in leer(self.base, "entrada/movimientos.csv")}

    def qr_del_caso(self, caso):
        vistas = [v for v in leer(self.base, "verdad/casos_vista.csv") if v["caso_id"] == caso]
        llave = self.movs[vistas[0]["movimiento_id"]]["llave_destino"]
        return [m for m in self.movs.values() if m["llave_destino"] == llave and m["canal"] == "qr"]

    def test_antes_del_cambio_no_aparece_el_nombre_nuevo(self):
        qr = self.qr_del_caso("cambio_nombre")
        self.assertTrue(qr)
        for m in qr:
            if fecha_local(m["ocurrido_en"]) < date(2026, 7, 15):
                self.assertNotIn("LUCY", m["nombre_comercio_crudo"])
            else:
                self.assertNotIn("ESQUINA", m["nombre_comercio_crudo"])

    def test_el_mcc_y_el_nombre_del_cambio_de_categoria_son_coherentes(self):
        qr = self.qr_del_caso("cambio_nombre_categoria")
        for m in qr:
            antes = fecha_local(m["ocurrido_en"]) < date(2026, 8, 10)
            self.assertEqual(m["mcc"] in MCC_POR_CATEGORIA["restaurantes_domicilios"], antes, m)
            self.assertEqual("LIBR" in m["nombre_comercio_crudo"], not antes, m)


class PruebasVerdadIdentica(unittest.TestCase):
    def test_misma_verdad_entre_variantes(self):
        a, b = carpeta(variante="A"), carpeta(variante="B")
        for ruta in ARCHIVOS_DE_VERDAD:
            self.assertEqual((a / ruta).read_bytes(), (b / ruta).read_bytes(), ruta)

    def test_misma_estructura_de_movimientos(self):
        a = leer(carpeta(variante="A"), "entrada/movimientos.csv")
        b = leer(carpeta(variante="B"), "entrada/movimientos.csv")
        claves = ("movimiento_id", "usuario_id", "transferencia_id", "ocurrido_en", "direccion",
                  "monto_centavos", "tipo_asiento", "descripcion", "tipo_llave", "llave_destino")
        self.assertEqual([[f[k] for k in claves] for f in a], [[f[k] for k in claves] for f in b])


if __name__ == "__main__":
    unittest.main()
