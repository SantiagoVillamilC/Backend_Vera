import os
import unittest
from pathlib import Path
from unittest.mock import patch

import descargar

CPU = {"kokoro-v1.0.int8.onnx", "voices-v1.0.bin"}
GPU = {"kokoro-v1.0.onnx", "kokoro-v1.0.fp16.onnx"}


class SeleccionTest(unittest.TestCase):
    def elegir(self, entorno):
        with patch.dict(os.environ, entorno, clear=True):
            return descargar.archivos_a_bajar()

    def test_por_defecto_baja_solo_los_pesos_de_cpu(self):
        self.assertEqual(set(self.elegir({})), CPU)

    def test_cero_y_vacio_cuentan_como_apagado(self):
        for valor in ("", "0"):
            with self.subTest(valor=valor):
                self.assertEqual(set(self.elegir({"VERA_VOZ_DESCARGAR_GPU": valor})), CPU)

    def test_uno_agrega_fp32_y_fp16_sin_quitar_los_de_cpu(self):
        self.assertEqual(set(self.elegir({"VERA_VOZ_DESCARGAR_GPU": "1"})), CPU | GPU)

    def test_otros_valores_abortan(self):
        for valor in ("si", "true", "2", " 1", "1 "):
            with self.subTest(valor=valor):
                with self.assertRaises(SystemExit) as fallo:
                    self.elegir({"VERA_VOZ_DESCARGAR_GPU": valor})
                self.assertIn("VERA_VOZ_DESCARGAR_GPU", str(fallo.exception.code))

    def test_los_pesos_de_gpu_llevan_huella_y_tamano_oficiales(self):
        self.assertEqual(descargar.ARCHIVOS_GPU["kokoro-v1.0.onnx"], (
            "beb0d1848dee9a49da392cc3df26958d46cfa35d321edf434f52949153f0df3a", 325505369))
        self.assertEqual(descargar.ARCHIVOS_GPU["kokoro-v1.0.fp16.onnx"], (
            "f3a290d384fbb27966d462905c71a46cef9e5fd00516b40df32a0b4afe77ac96", 163527961))


class DestinoTest(unittest.TestCase):
    def test_por_defecto_es_opt_voz_modelos(self):
        with patch.dict(os.environ, {}, clear=True):
            self.assertEqual(descargar.directorio_destino(), Path("/opt/voz/modelos"))

    def test_se_puede_elegir_por_variable(self):
        with patch.dict(os.environ, {"VERA_VOZ_MODELOS_DESTINO": "/tmp/pruebas"}, clear=True):
            self.assertEqual(descargar.directorio_destino(), Path("/tmp/pruebas"))

    def test_vacio_cuenta_como_ausente(self):
        with patch.dict(os.environ, {"VERA_VOZ_MODELOS_DESTINO": ""}, clear=True):
            self.assertEqual(descargar.directorio_destino(), Path("/opt/voz/modelos"))


if __name__ == "__main__":
    unittest.main()
