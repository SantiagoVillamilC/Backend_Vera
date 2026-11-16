import base64
import contextlib
import io
import json
import logging
import os
import sys
import threading
import time
import types
import unittest
import urllib.error
import urllib.request
import wave
from unittest.mock import patch

import numpy as np
import servidor
from servidor import crear_servidor, frases, texto_hablado


class MotorPrueba:
    def __init__(self):
        self.textos = []

    def create(self, texto, **opciones):
        self.textos.append((texto, opciones))
        return np.full(240, 0.2, dtype=np.float32), 24000


class VozLocalTest(unittest.TestCase):
    def setUp(self):
        self.motor = MotorPrueba()
        self.servidor = crear_servidor(self.motor, "token-ficticio", ("127.0.0.1", 0))
        self.hilo = threading.Thread(target=self.servidor.serve_forever, daemon=True)
        self.hilo.start()
        self.url = f"http://127.0.0.1:{self.servidor.server_port}"

    def tearDown(self):
        self.servidor.shutdown()
        self.servidor.server_close()
        self.hilo.join()

    def solicitar(self, ruta, datos=None, token="token-ficticio"):
        return urllib.request.urlopen(urllib.request.Request(
            self.url + ruta, data=json.dumps(datos).encode() if datos else None,
            headers={"X-Vera-Token": token, "Content-Type": "application/json"}), timeout=2)

    def test_wav_y_flujo_conservan_pcm_y_voz(self):
        with self.solicitar("/sintetizar", {"texto": "Saldo: 120000.00 COP."}) as respuesta:
            with wave.open(io.BytesIO(respuesta.read())) as audio:
                self.assertEqual((audio.getframerate(), audio.getnchannels(), audio.getsampwidth()), (24000, 1, 2))
                self.assertEqual(audio.getnframes(), 240)
        with self.solicitar("/flujo", {"texto": "Hola. Estoy lista."}) as respuesta:
            eventos = [json.loads(linea) for linea in respuesta]
        self.assertEqual([e["tipo"] for e in eventos], ["audio", "audio", "fin"])
        self.assertEqual(len(base64.b64decode(eventos[0]["pcm"])), 480)
        self.assertEqual(self.motor.textos[0][0], "Saldo: 120000 pesos colombianos.")
        self.assertTrue(all(o["voice"] == "ef_dora" and o["lang"] == "es" for _, o in self.motor.textos))

    def test_healthz_200_con_token(self):
        with self.solicitar("/healthz") as respuesta:
            self.assertEqual(respuesta.status, 200)
            self.assertEqual(json.loads(respuesta.read()), {"voz": "ef_dora", "modelo": "Kokoro-82M"})

    def test_rechaza_token_y_textos_invalidos_sin_inferencia(self):
        for ruta, datos, token, esperado in [("/healthz", None, "otro", 401), ("/sintetizar", {"texto": " "}, "token-ficticio", 422), ("/sintetizar", {"texto": 12}, "token-ficticio", 422)]:
            with self.assertRaises(urllib.error.HTTPError) as fallo:
                self.solicitar(ruta, datos, token)
            self.assertEqual(fallo.exception.code, esperado)
            fallo.exception.close()
        self.assertEqual(self.motor.textos, [])

    def test_pronunciacion_no_redondea_y_no_corta_centavos(self):
        self.assertEqual(texto_hablado("-50000.25 COP y 100.00 COP"), "-50000 pesos colombianos con 25 centavos y 100 pesos colombianos")
        self.assertEqual(list(frases("Tienes 50000.25 COP. ¿Quieres revisar tus límites?")), ["Tienes 50000.25 COP.", "¿Quieres revisar tus límites?"])

    def test_fallo_tras_fragmento_no_anuncia_fin(self):
        original = self.motor.create
        def fallar(texto, **opciones):
            if self.motor.textos:
                raise ValueError("dato privado")
            return original(texto, **opciones)
        self.motor.create = fallar
        with self.solicitar("/flujo", {"texto": "Primera frase. Segunda frase."}) as respuesta:
            contenido = respuesta.read()
        self.assertIn(b'"audio"', contenido)
        self.assertNotIn(b'"fin"', contenido)
        self.assertNotIn(b"privado", contenido)


class MotorEspera:
    """Motor falso: cada síntesis avisa que entró y espera a que la prueba la suelte."""

    def __init__(self, esperar):
        self.esperar = esperar

    def create(self, texto, **opciones):
        self.esperar()
        return np.full(240, 0.2, dtype=np.float32), 24000


class ConcurrenciaTest(unittest.TestCase):
    def setUp(self):
        self.servidores = []
        self.hilos = []

    def tearDown(self):
        for hilo in self.hilos:
            hilo.join(timeout=10)
        for srv, hilo in self.servidores:
            srv.shutdown()
            srv.server_close()
            hilo.join()

    def iniciar(self, esperar, **opciones):
        srv = crear_servidor(MotorEspera(esperar), "token-ficticio", ("127.0.0.1", 0), **opciones)
        hilo = threading.Thread(target=srv.serve_forever, daemon=True)
        hilo.start()
        self.servidores.append((srv, hilo))
        return srv

    def pedir(self, srv):
        peticion = urllib.request.Request(
            f"http://127.0.0.1:{srv.server_port}/sintetizar", data=json.dumps({"texto": "hola"}).encode(),
            headers={"X-Vera-Token": "token-ficticio", "Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(peticion, timeout=5) as respuesta:
                return respuesta.status
        except urllib.error.HTTPError as error:
            error.close()
            return error.code

    def pedir_en_hilo(self, srv):
        resultado = []
        hilo = threading.Thread(target=lambda: resultado.append(self.pedir(srv)), daemon=True)
        hilo.start()
        self.hilos.append(hilo)
        return hilo, resultado

    def test_por_defecto_la_segunda_solicitud_simultanea_da_409(self):
        dentro, soltar = threading.Event(), threading.Event()
        srv = self.iniciar(lambda: (dentro.set(), soltar.wait(5)))
        hilo, primera = self.pedir_en_hilo(srv)
        self.assertTrue(dentro.wait(2))
        inicio = time.monotonic()
        self.assertEqual(self.pedir(srv), 409)
        self.assertLess(time.monotonic() - inicio, 0.5, "el 409 por defecto debe ser inmediato")
        soltar.set()
        hilo.join(5)
        self.assertEqual(primera, [200])

    def test_espera_fuera_de_rango_lanza_value_error(self):
        for espera in (-1, 10001, 0.5, True, "100", None):
            with self.subTest(espera_ms=espera):
                with self.assertRaises(ValueError):
                    crear_servidor(MotorEspera(lambda: None), "token-ficticio", ("127.0.0.1", 0),
                                   espera_ms=espera)

    def test_los_limites_de_espera_son_validos(self):
        for espera in (0, 10000):
            with self.subTest(espera_ms=espera):
                srv = crear_servidor(MotorEspera(lambda: None), "token-ficticio", ("127.0.0.1", 0),
                                     espera_ms=espera)
                srv.server_close()

    def test_con_espera_la_segunda_aguarda_y_da_200(self):
        dentro, soltar = threading.Event(), threading.Event()
        srv = self.iniciar(lambda: (dentro.set(), soltar.wait(5)), espera_ms=3000)
        hilo, primera = self.pedir_en_hilo(srv)
        self.assertTrue(dentro.wait(2))
        temporizador = threading.Timer(0.3, soltar.set)
        temporizador.start()
        inicio = time.monotonic()
        self.assertEqual(self.pedir(srv), 200)
        self.assertGreaterEqual(time.monotonic() - inicio, 0.2)
        temporizador.join()
        hilo.join(5)
        self.assertEqual(primera, [200])

    def test_si_la_espera_vence_antes_de_que_se_libere_da_409(self):
        dentro, soltar = threading.Event(), threading.Event()
        srv = self.iniciar(lambda: (dentro.set(), soltar.wait(5)), espera_ms=100)
        hilo, primera = self.pedir_en_hilo(srv)
        self.assertTrue(dentro.wait(2))
        self.assertEqual(self.pedir(srv), 409)
        soltar.set()
        hilo.join(5)
        self.assertEqual(primera, [200])

    def test_dos_instancias_en_puertos_distintos_sintetizan_a_la_vez(self):
        # El paralelismo de Kokoro son varios procesos: cada instancia conserva su cerrojo propio.
        barrera = threading.Barrier(2, timeout=3)
        uno, otro = self.iniciar(barrera.wait), self.iniciar(barrera.wait)
        self.assertNotEqual(uno.server_port, otro.server_port)
        (h1, r1), (h2, r2) = self.pedir_en_hilo(uno), self.pedir_en_hilo(otro)
        h1.join(5)
        h2.join(5)
        self.assertEqual((r1, r2), ([200], [200]))


class OrtFalso:
    """Falso de onnxruntime sin GPU: anota cada llamada en orden y devuelve proveedores a elección."""

    def __init__(self, proveedores=("CPUExecutionProvider",)):
        self.proveedores = list(proveedores)
        self.registro = []
        self.sesiones = []
        self.SessionOptions = types.SimpleNamespace

    def preload_dlls(self, *args, **kwargs):
        self.registro.append("preload_dlls")

    def InferenceSession(self, ruta, sess_options=None, providers=None):
        self.registro.append("InferenceSession")
        self.sesiones.append({"ruta": ruta, "opciones": sess_options, "proveedores": providers})
        return types.SimpleNamespace(get_providers=lambda: self.registro.append("get_providers") or self.proveedores)


class CrearSesionTest(unittest.TestCase):
    def test_sin_variables_pide_cpu_int8_y_los_hilos_de_siempre(self):
        ort = OrtFalso()
        servidor.crear_sesion(ort, "cpu", None)
        (sesion,) = ort.sesiones
        self.assertEqual(sesion["ruta"], "/opt/voz/modelos/kokoro-v1.0.int8.onnx")
        self.assertEqual(sesion["proveedores"], ["CPUExecutionProvider"])
        self.assertEqual((sesion["opciones"].intra_op_num_threads, sesion["opciones"].inter_op_num_threads), (2, 1))

    def test_cpu_no_hace_llamadas_nuevas(self):
        ort = OrtFalso()
        servidor.crear_sesion(ort, "cpu", None)
        self.assertEqual(ort.registro, ["InferenceSession"])

    def test_cuda_fp32_usa_el_modelo_fp32_y_las_opciones_del_proveedor(self):
        ort = OrtFalso(["CUDAExecutionProvider", "CPUExecutionProvider"])
        servidor.crear_sesion(ort, "cuda", "fp32")
        (sesion,) = ort.sesiones
        self.assertEqual(sesion["ruta"], "/opt/voz/modelos/kokoro-v1.0.onnx")
        self.assertEqual(sesion["proveedores"], [("CUDAExecutionProvider", {"cudnn_conv_algo_search": "HEURISTIC"}), "CPUExecutionProvider"])
        self.assertEqual((sesion["opciones"].intra_op_num_threads, sesion["opciones"].inter_op_num_threads), (2, 1))

    def test_cuda_fp16_usa_el_modelo_fp16(self):
        ort = OrtFalso(["CUDAExecutionProvider", "CPUExecutionProvider"])
        servidor.crear_sesion(ort, "cuda", "fp16")
        self.assertEqual(ort.sesiones[0]["ruta"], "/opt/voz/modelos/kokoro-v1.0.fp16.onnx")

    def test_cuda_nunca_carga_el_modelo_int8(self):
        for precision in ("fp32", "fp16"):
            ort = OrtFalso(["CUDAExecutionProvider", "CPUExecutionProvider"])
            servidor.crear_sesion(ort, "cuda", precision)
            self.assertNotIn("int8", ort.sesiones[0]["ruta"])

    def test_cuda_precarga_las_librerias_antes_de_crear_la_sesion(self):
        ort = OrtFalso(["CUDAExecutionProvider", "CPUExecutionProvider"])
        servidor.crear_sesion(ort, "cuda", "fp32")
        self.assertEqual(ort.registro, ["preload_dlls", "InferenceSession", "get_providers"])

    def test_cuda_respeta_el_directorio_indicado(self):
        ort = OrtFalso(["CUDAExecutionProvider", "CPUExecutionProvider"])
        servidor.crear_sesion(ort, "cuda", "fp32", directorio="/otro/sitio")
        self.assertEqual(ort.sesiones[0]["ruta"], "/otro/sitio/kokoro-v1.0.onnx")

    def test_cuda_que_cae_a_cpu_aborta_en_vez_de_seguir_en_silencio(self):
        ort = OrtFalso(["CPUExecutionProvider"])
        with self.assertRaises(SystemExit) as fallo:
            servidor.crear_sesion(ort, "cuda", "fp32")
        self.assertIn("CUDAExecutionProvider", str(fallo.exception.code))
        self.assertIn("CPUExecutionProvider", str(fallo.exception.code))

    def test_cuda_con_cpu_primero_aborta_aunque_cuda_figure_despues(self):
        ort = OrtFalso(["CPUExecutionProvider", "CUDAExecutionProvider"])
        with self.assertRaises(SystemExit):
            servidor.crear_sesion(ort, "cuda", "fp16")

    def test_cpu_rechaza_cualquier_precision(self):
        for precision in ("fp32", "fp16", "int8"):
            with self.subTest(precision=precision):
                with self.assertRaises(SystemExit):
                    servidor.crear_sesion(OrtFalso(), "cpu", precision)

    def test_un_dispositivo_desconocido_aborta_sin_crear_sesion(self):
        ort = OrtFalso()
        with self.assertRaises(SystemExit):
            servidor.crear_sesion(ort, "tpu", None)
        self.assertEqual(ort.sesiones, [])


class CalentamientoTest(unittest.TestCase):
    def test_en_cpu_calienta_con_una_sola_frase_como_siempre(self):
        motor = MotorPrueba()
        servidor.calentar(motor, "cpu")
        self.assertEqual([t for t, _ in motor.textos], ["Hola, soy Vera y estoy lista para ayudarte."])
        self.assertEqual(motor.textos[0][1], {"voice": "ef_dora", "lang": "es"})

    def test_en_cuda_calienta_con_tres_longitudes_distintas_y_empieza_por_la_de_siempre(self):
        motor = MotorPrueba()
        servidor.calentar(motor, "cuda")
        textos = [t for t, _ in motor.textos]
        self.assertEqual(len(textos), 3)
        self.assertEqual(textos[0], "Hola, soy Vera y estoy lista para ayudarte.")
        self.assertEqual(len({len(t) for t in textos}), 3)
        self.assertTrue(all(o == {"voice": "ef_dora", "lang": "es"} for _, o in motor.textos))


class ConfiguracionTest(unittest.TestCase):
    def setUp(self):
        self.addCleanup(logging.disable, logging.NOTSET)

    def ejecutar_main(self, extra, motor_listo=False, ort=None):
        entorno = dict({"VERA_LAB_TOKEN": "t" * 32}, **extra)
        self.llamadas_kokoro = []
        self.motor = MotorPrueba()
        def desde_sesion(*a, **k):
            self.llamadas_kokoro.append(a)
            return self.motor
        modulos = {
            "onnxruntime": ort or types.SimpleNamespace(SessionOptions=types.SimpleNamespace, InferenceSession=lambda *a, **k: object()),
            "kokoro_onnx": types.SimpleNamespace(Kokoro=types.SimpleNamespace(from_session=desde_sesion)),
        }
        falso = types.SimpleNamespace(serve_forever=lambda: None)
        with patch.dict(os.environ, entorno, clear=True), patch.dict(sys.modules, modulos), \
                patch.object(servidor, "crear_servidor", return_value=falso) as crear, \
                contextlib.redirect_stdout(io.StringIO()):
            servidor.main()
        return crear

    def test_sin_variables_nuevas_main_arranca_en_cpu_con_una_frase_de_calentamiento(self):
        ort = OrtFalso()
        self.ejecutar_main({}, ort=ort)
        self.assertEqual(ort.registro, ["InferenceSession"])
        self.assertEqual(ort.sesiones[0]["ruta"], "/opt/voz/modelos/kokoro-v1.0.int8.onnx")
        self.assertEqual(self.llamadas_kokoro[0][1], "/opt/voz/modelos/voices-v1.0.bin")
        self.assertEqual(len(self.motor.textos), 1)

    def test_main_con_cuda_usa_fp32_y_calienta_tres_textos(self):
        ort = OrtFalso(["CUDAExecutionProvider", "CPUExecutionProvider"])
        self.ejecutar_main({"VERA_VOZ_DISPOSITIVO": "cuda"}, ort=ort)
        self.assertEqual(ort.sesiones[0]["ruta"], "/opt/voz/modelos/kokoro-v1.0.onnx")
        self.assertEqual(len(self.motor.textos), 3)

    def test_main_con_cuda_y_fp16_usa_el_modelo_fp16(self):
        ort = OrtFalso(["CUDAExecutionProvider", "CPUExecutionProvider"])
        self.ejecutar_main({"VERA_VOZ_DISPOSITIVO": "cuda", "VERA_VOZ_PRECISION": "fp16"}, ort=ort)
        self.assertEqual(ort.sesiones[0]["ruta"], "/opt/voz/modelos/kokoro-v1.0.fp16.onnx")

    def test_main_con_cuda_que_cae_a_cpu_no_llega_a_servir(self):
        ort = OrtFalso(["CPUExecutionProvider"])
        with self.assertRaises(SystemExit):
            self.ejecutar_main({"VERA_VOZ_DISPOSITIVO": "cuda"}, ort=ort)
        self.assertEqual(self.motor.textos, [])

    def test_dispositivo_explicito_cpu_equivale_al_valor_por_defecto(self):
        ort = OrtFalso()
        self.ejecutar_main({"VERA_VOZ_DISPOSITIVO": "cpu"}, ort=ort)
        self.assertEqual(ort.registro, ["InferenceSession"])

    def test_dispositivo_y_precision_vacios_cuentan_como_ausentes(self):
        ort = OrtFalso()
        self.ejecutar_main({"VERA_VOZ_DISPOSITIVO": "", "VERA_VOZ_PRECISION": ""}, ort=ort)
        self.assertEqual(ort.sesiones[0]["ruta"], "/opt/voz/modelos/kokoro-v1.0.int8.onnx")

    def test_dispositivo_y_precision_invalidos_abortan_antes_de_cargar_el_modelo(self):
        invalidos = [{"VERA_VOZ_DISPOSITIVO": v} for v in ("gpu", "CUDA", " cuda", "cuda ", "cuda\n", "1", "cpu,cuda")]
        invalidos += [{"VERA_VOZ_DISPOSITIVO": "cuda", "VERA_VOZ_PRECISION": v} for v in ("fp8", "FP16", " fp16", "int8", "fp32 ", "bf16")]
        invalidos += [{"VERA_VOZ_PRECISION": v} for v in ("fp32", "fp16", "int8", "x")]
        invalidos += [{"VERA_VOZ_DISPOSITIVO": "cpu", "VERA_VOZ_PRECISION": v} for v in ("fp32", "fp16", "int8")]
        for extra in invalidos:
            with self.subTest(extra=str(extra)):
                with patch.dict(os.environ, dict({"VERA_LAB_TOKEN": "t" * 32}, **extra), clear=True), \
                        patch.dict(sys.modules, {"onnxruntime": None, "kokoro_onnx": None}):
                    with self.assertRaises(SystemExit) as fallo:
                        servidor.main()
                    self.assertIn("VERA_VOZ_", str(fallo.exception.code))

    def test_los_mensajes_nombran_la_variable_y_los_valores_validos(self):
        casos = [
            ({"VERA_VOZ_DISPOSITIVO": "gpu"}, ["VERA_VOZ_DISPOSITIVO", "cpu", "cuda"]),
            ({"VERA_VOZ_DISPOSITIVO": "cuda", "VERA_VOZ_PRECISION": "fp8"}, ["VERA_VOZ_PRECISION", "fp32", "fp16"]),
            ({"VERA_VOZ_PRECISION": "fp16"}, ["VERA_VOZ_PRECISION", "VERA_VOZ_DISPOSITIVO=cuda"]),
        ]
        for extra, fragmentos in casos:
            with self.subTest(extra=str(extra)):
                with patch.dict(os.environ, dict({"VERA_LAB_TOKEN": "t" * 32}, **extra), clear=True), \
                        patch.dict(sys.modules, {"onnxruntime": None, "kokoro_onnx": None}):
                    with self.assertRaises(SystemExit) as fallo:
                        servidor.main()
                for fragmento in fragmentos:
                    self.assertIn(fragmento, str(fallo.exception.code))

    def test_el_puerto_sale_de_vera_voz_puerto(self):
        crear = self.ejecutar_main({"VERA_VOZ_PUERTO": "9123", "VERA_VOZ_ESPERA_MS": "2500"})
        self.assertEqual(crear.call_args.args[2], ("127.0.0.1", 9123))
        self.assertEqual(crear.call_args.kwargs, {"espera_ms": 2500})

    def test_por_defecto_puerto_8766_y_sin_espera(self):
        crear = self.ejecutar_main({})
        self.assertEqual(crear.call_args.args[2], ("127.0.0.1", 8766))
        self.assertEqual(crear.call_args.kwargs, {"espera_ms": 0})

    def test_valores_invalidos_abortan_antes_de_cargar_el_modelo(self):
        invalidos = [{"VERA_VOZ_PUERTO": v} for v in ("1023", "65536", "0", "-1", "abc", "8766.0", " 8766", "9" * 5000)]
        invalidos += [{"VERA_VOZ_ESPERA_MS": v} for v in ("10001", "-5", "x", "1e3")]
        for extra in invalidos:
            with self.subTest(extra=str(extra)[:60]):
                with patch.dict(os.environ, dict({"VERA_LAB_TOKEN": "t" * 32}, **extra), clear=True), \
                        patch.dict(sys.modules, {"onnxruntime": None, "kokoro_onnx": None}):
                    # Con los módulos en None, importarlos daría ImportError: SystemExit prueba que se validó antes.
                    with self.assertRaises(SystemExit):
                        servidor.main()


if __name__ == "__main__":
    unittest.main()
