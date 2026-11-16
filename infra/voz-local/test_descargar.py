import hashlib
import os
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from unittest.mock import patch

import descargar

CPU = {"kokoro-v1.0.int8.onnx", "voices-v1.0.bin"}
FP32 = {"kokoro-v1.0.onnx"}
FP16 = {"kokoro-v1.0.fp16.onnx"}


class SeleccionTest(unittest.TestCase):
    def elegir(self, entorno):
        with patch.dict(os.environ, entorno, clear=True):
            return descargar.archivos_a_bajar()

    def test_por_defecto_baja_solo_los_pesos_de_cpu(self):
        self.assertEqual(set(self.elegir({})), CPU)

    def test_cero_y_vacio_cuentan_como_apagado(self):
        for variable in ("VERA_VOZ_DESCARGAR_GPU", "VERA_VOZ_DESCARGAR_FP16"):
            for valor in ("", "0"):
                with self.subTest(variable=variable, valor=valor):
                    self.assertEqual(set(self.elegir({variable: valor})), CPU)

    def test_gpu_agrega_solo_el_fp32_sin_quitar_los_de_cpu(self):
        self.assertEqual(set(self.elegir({"VERA_VOZ_DESCARGAR_GPU": "1"})), CPU | FP32)

    def test_gpu_no_baja_el_fp16(self):
        self.assertNotIn("kokoro-v1.0.fp16.onnx", self.elegir({"VERA_VOZ_DESCARGAR_GPU": "1"}))

    def test_fp16_es_un_opt_in_separado(self):
        self.assertEqual(set(self.elegir({"VERA_VOZ_DESCARGAR_FP16": "1"})), CPU | FP16)

    def test_gpu_y_fp16_juntos_bajan_los_cuatro(self):
        entorno = {"VERA_VOZ_DESCARGAR_GPU": "1", "VERA_VOZ_DESCARGAR_FP16": "1"}
        self.assertEqual(set(self.elegir(entorno)), CPU | FP32 | FP16)

    def test_otros_valores_abortan(self):
        for variable in ("VERA_VOZ_DESCARGAR_GPU", "VERA_VOZ_DESCARGAR_FP16"):
            for valor in ("si", "true", "2", " 1", "1 "):
                with self.subTest(variable=variable, valor=valor):
                    with self.assertRaises(SystemExit) as fallo:
                        self.elegir({variable: valor})
                    self.assertIn(variable, str(fallo.exception.code))

    def test_los_pesos_de_gpu_llevan_huella_y_tamano_oficiales(self):
        self.assertEqual(descargar.ARCHIVOS_GPU["kokoro-v1.0.onnx"], (
            "beb0d1848dee9a49da392cc3df26958d46cfa35d321edf434f52949153f0df3a", 325505369))
        self.assertEqual(descargar.ARCHIVOS_FP16["kokoro-v1.0.fp16.onnx"], (
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


NOMBRE = "peso.onnx"
CUERPO = b"contenido-del-peso-" * 1000
HUELLA = hashlib.sha256(CUERPO).hexdigest()


class _Manejador(BaseHTTPRequestHandler):
    def log_message(self, *argumentos):
        pass

    def do_GET(self):
        modo = self.server.modo
        cuerpo = CUERPO
        if modo == "de_mas":
            cuerpo = CUERPO + b"x" * 5000
        elif modo == "huella_mala":
            cuerpo = b"y" * len(CUERPO)
        elif modo == "corto_sin_longitud":
            cuerpo = CUERPO[:100]
        anunciado = len(CUERPO) if modo == "cortado" else len(cuerpo)
        self.send_response(200)
        if modo != "corto_sin_longitud":
            self.send_header("Content-Length", str(anunciado))
        self.end_headers()
        self.wfile.write(CUERPO[: len(CUERPO) // 2] if modo == "cortado" else cuerpo)


class DescargaTest(unittest.TestCase):
    def setUp(self):
        self.servidor = ThreadingHTTPServer(("127.0.0.1", 0), _Manejador)
        self.servidor.modo = "ok"
        hilo = threading.Thread(target=self.servidor.serve_forever, daemon=True)
        hilo.start()
        self.addCleanup(hilo.join, 5)
        self.addCleanup(self.servidor.server_close)
        self.addCleanup(self.servidor.shutdown)
        carpeta = tempfile.TemporaryDirectory()
        self.addCleanup(carpeta.cleanup)
        self.destino = Path(carpeta.name)
        base = "http://127.0.0.1:%d/" % self.servidor.server_address[1]
        parche = patch.object(descargar, "BASE", base)
        parche.start()
        self.addCleanup(parche.stop)

    def bajar(self, modo):
        self.servidor.modo = modo
        descargar.descargar(NOMBRE, HUELLA, len(CUERPO), self.destino)

    def assert_sin_rastro(self):
        self.assertFalse((self.destino / NOMBRE).exists(), "quedó un archivo con el nombre final")
        self.assertEqual(sorted(p.name for p in self.destino.iterdir()), [], "quedó un parcial")

    def test_descarga_correcta_deja_solo_el_archivo_final(self):
        self.bajar("ok")
        self.assertEqual((self.destino / NOMBRE).read_bytes(), CUERPO)
        self.assertEqual([p.name for p in self.destino.iterdir()], [NOMBRE])

    def test_tamano_de_mas_falla_y_no_deja_nada(self):
        with self.assertRaisesRegex(RuntimeError, "tamaño"):
            self.bajar("de_mas")
        self.assert_sin_rastro()

    def test_huella_incorrecta_falla_y_no_deja_nada(self):
        with self.assertRaisesRegex(RuntimeError, "huella"):
            self.bajar("huella_mala")
        self.assert_sin_rastro()

    def test_corte_a_mitad_falla_y_no_deja_nada(self):
        with self.assertRaises(Exception):
            self.bajar("cortado")
        self.assert_sin_rastro()

    def test_menos_bytes_sin_longitud_falla_y_no_deja_nada(self):
        with self.assertRaisesRegex(RuntimeError, "tamaño"):
            self.bajar("corto_sin_longitud")
        self.assert_sin_rastro()

    def test_archivo_valido_ya_presente_no_se_vuelve_a_bajar(self):
        (self.destino / NOMBRE).write_bytes(CUERPO)
        with patch.object(descargar, "urlopen", side_effect=AssertionError("no debe abrir la red")):
            descargar.descargar(NOMBRE, HUELLA, len(CUERPO), self.destino)
        self.assertEqual((self.destino / NOMBRE).read_bytes(), CUERPO)

    def test_archivo_presente_con_otra_huella_se_reemplaza(self):
        (self.destino / NOMBRE).write_bytes(b"z" * len(CUERPO))
        self.bajar("ok")
        self.assertEqual((self.destino / NOMBRE).read_bytes(), CUERPO)

    def test_archivo_invalido_previo_no_sobrevive_a_una_descarga_fallida(self):
        (self.destino / NOMBRE).write_bytes(b"z" * 10)
        with self.assertRaises(RuntimeError):
            self.bajar("huella_mala")
        self.assert_sin_rastro()

    def test_parcial_viejo_se_sobrescribe(self):
        (self.destino / (NOMBRE + ".parcial")).write_bytes(b"basura" * 100000)
        self.bajar("ok")
        self.assertEqual([p.name for p in self.destino.iterdir()], [NOMBRE])


class _Respuesta:
    """urlopen falso: entrega bloques de 'a', y opcionalmente lanza un error tras cierto total."""

    def __init__(self, total, falla_en=None):
        self.total, self.falla_en, self.entregado = total, falla_en, 0

    def __enter__(self):
        return self

    def __exit__(self, *argumentos):
        return False

    def read(self, n=-1):
        if self.falla_en is not None and self.entregado >= self.falla_en:
            raise TimeoutError("timed out")
        restante = self.total - self.entregado
        entrega = restante if n is None or n < 0 else min(n, restante)
        if self.falla_en is not None:
            entrega = min(entrega, self.falla_en - self.entregado)
        self.entregado += entrega
        return b"a" * entrega


class CorteTest(unittest.TestCase):
    def setUp(self):
        carpeta = tempfile.TemporaryDirectory()
        self.addCleanup(carpeta.cleanup)
        self.destino = Path(carpeta.name)

    def test_tiempo_agotado_a_mitad_no_deja_nada(self):
        respuesta = _Respuesta(total=1000, falla_en=500)
        with patch.object(descargar, "urlopen", return_value=respuesta):
            with self.assertRaises(TimeoutError):
                descargar.descargar(NOMBRE, "0" * 64, 1000, self.destino)
        self.assertEqual(list(self.destino.iterdir()), [])

    def test_corta_en_cuanto_supera_el_tamano_esperado(self):
        respuesta = _Respuesta(total=5 * 1024 * 1024)
        with patch.object(descargar, "urlopen", return_value=respuesta):
            with self.assertRaisesRegex(RuntimeError, "tamaño"):
                descargar.descargar(NOMBRE, "0" * 64, 1000, self.destino)
        self.assertLessEqual(respuesta.entregado, 1001)
        self.assertEqual(list(self.destino.iterdir()), [])


if __name__ == "__main__":
    unittest.main()
