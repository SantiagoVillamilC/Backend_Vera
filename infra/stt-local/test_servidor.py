"""Pruebas del contrato HTTP sin descargar el modelo."""
import http.client
import json
import os
import sys
import threading
import time
import types
import unittest
from unittest.mock import patch
import servidor
from servidor import crear_servidor

TOKEN = "token-ficticio-local-123456789012345"

class Modelo:
    def transcribe(self, audio, **opciones):
        assert opciones == dict(language="es", beam_size=1, vad_filter=True)
        return iter([types.SimpleNamespace(text=" hola ")]) if len(audio) else iter([]), None

class Contrato(unittest.TestCase):
    def setUp(self):
        self.modulos = patch.dict(sys.modules, {"faster_whisper": types.ModuleType("faster_whisper"), "faster_whisper.audio": types.SimpleNamespace(decode_audio=lambda audio, sampling_rate: [] if audio.read() == b"silencio" else [0])})
        self.modulos.start()
        self.servidor = crear_servidor(Modelo(), TOKEN, "small", ("127.0.0.1", 0))
        self.hilo = threading.Thread(target=self.servidor.serve_forever, daemon=True)
        self.hilo.start()

    def tearDown(self):
        self.servidor.shutdown()
        self.servidor.server_close()
        self.hilo.join()
        self.modulos.stop()

    def pedir(self, datos=b"clip", **extra):
        conexion = http.client.HTTPConnection(*self.servidor.server_address, timeout=3)
        cabeceras = {"X-Vera-Token": TOKEN, "Content-Type": "audio/wav"}
        cabeceras.update(extra)
        conexion.request("POST", "/transcribir", datos, cabeceras)
        r = conexion.getresponse()
        resultado = r.status, r.read()
        conexion.close()
        return resultado

    def test_voz_y_silencio(self):
        for clip, texto in [(b"clip", "hola"), (b"silencio", "")]:
            estado, cuerpo = self.pedir(clip)
            self.assertEqual(estado, 200)
            self.assertEqual(json.loads(cuerpo)["transcripcion"], texto)

    def test_errores(self):
        for headers, estado in [({"Content-Length": "no-numero"}, 422), ({"Content-Length": "9000000"}, 413), ({"Content-Type": "text/plain"}, 415), ({"X-Vera-Token": "malo"}, 401), ({"Transfer-Encoding": "chunked"}, 422)]:
            with self.subTest(headers=headers):
                self.assertEqual(self.pedir(**headers)[0], estado)

    def test_solo_loopback(self):
        with self.assertRaises(ValueError):
            crear_servidor(Modelo(), TOKEN, "small", ("0.0.0.0", 0))

    def test_audio_largo_e_invalido(self):
        with patch("faster_whisper.audio.decode_audio", return_value=range(16000 * 61)):
            self.assertEqual(self.pedir()[0], 413)
        with patch("faster_whisper.audio.decode_audio", side_effect=ValueError("privado")):
            estado, cuerpo = self.pedir()
            self.assertEqual(estado, 422)
            self.assertNotIn(b"privado", cuerpo)

    def test_bytes_que_no_son_audio_dan_422_sin_inferencia(self):
        # Criterio "un WAV roto rompe el job": el decodificador rechaza lo que no es RIFF/WAVE
        # y el servidor responde 422 sin llamar al modelo ni filtrar el detalle del error.
        def decodificar(audio, sampling_rate):
            if audio.read(4) != b"RIFF":
                raise ValueError("detalle interno del decodificador")
            return [0]

        llamadas = []
        with patch("faster_whisper.audio.decode_audio", side_effect=decodificar), \
                patch.object(Modelo, "transcribe", side_effect=lambda *a, **k: llamadas.append(1)):
            for basura in (b"esto no es audio", b"\x00" * 64, b"RIF"):
                with self.subTest(bytes=basura[:8]):
                    estado, cuerpo = self.pedir(basura)
                    self.assertEqual(estado, 422)
                    self.assertNotIn(b"decodificador", cuerpo)
        self.assertEqual(llamadas, [])


class ModeloEspera:
    """Modelo falso: cada transcripción avisa que entró y espera a que la prueba la suelte."""

    def __init__(self, esperar):
        self.esperar = esperar

    def transcribe(self, audio, **opciones):
        self.esperar()
        return iter([types.SimpleNamespace(text="hola")]), None


class Concurrencia(unittest.TestCase):
    def setUp(self):
        self.modulos = patch.dict(sys.modules, {"faster_whisper": types.ModuleType("faster_whisper"), "faster_whisper.audio": types.SimpleNamespace(decode_audio=lambda audio, sampling_rate: [0])})
        self.modulos.start()
        self.servidor = None
        self.hilos = []

    def tearDown(self):
        for hilo in self.hilos:
            hilo.join(timeout=10)
        if self.servidor:
            self.servidor.shutdown()
            self.servidor.server_close()
            self.hilo_servidor.join()
        self.modulos.stop()

    def iniciar(self, esperar, **opciones):
        self.servidor = crear_servidor(ModeloEspera(esperar), TOKEN, "small", ("127.0.0.1", 0), **opciones)
        self.hilo_servidor = threading.Thread(target=self.servidor.serve_forever, daemon=True)
        self.hilo_servidor.start()

    def pedir(self):
        conexion = http.client.HTTPConnection(*self.servidor.server_address, timeout=5)
        conexion.request("POST", "/transcribir", b"clip", {"X-Vera-Token": TOKEN, "Content-Type": "audio/wav"})
        estado = conexion.getresponse().status
        conexion.close()
        return estado

    def pedir_en_hilo(self):
        resultado = []
        hilo = threading.Thread(target=lambda: resultado.append(self.pedir()), daemon=True)
        hilo.start()
        self.hilos.append(hilo)
        return hilo, resultado

    def test_por_defecto_la_segunda_solicitud_simultanea_da_409(self):
        dentro, soltar = threading.Event(), threading.Event()
        self.iniciar(lambda: (dentro.set(), soltar.wait(5)))
        hilo, primera = self.pedir_en_hilo()
        self.assertTrue(dentro.wait(2))
        inicio = time.monotonic()
        self.assertEqual(self.pedir(), 409)
        self.assertLess(time.monotonic() - inicio, 0.5, "el 409 por defecto debe ser inmediato")
        soltar.set()
        hilo.join(5)
        self.assertEqual(primera, [200])

    def test_concurrencia_y_espera_fuera_de_rango_lanzan_value_error(self):
        for concurrencia in (0, -1, 17, 1.5, True, "2", None):
            with self.subTest(concurrencia=concurrencia):
                with self.assertRaises(ValueError):
                    crear_servidor(ModeloEspera(lambda: None), TOKEN, "small", ("127.0.0.1", 0),
                                   concurrencia=concurrencia)
        for espera in (-1, 10001, 0.5, True, "100", None):
            with self.subTest(espera_ms=espera):
                with self.assertRaises(ValueError):
                    crear_servidor(ModeloEspera(lambda: None), TOKEN, "small", ("127.0.0.1", 0),
                                   espera_ms=espera)

    def test_los_limites_del_rango_son_validos(self):
        for opciones in ({"concurrencia": 1, "espera_ms": 0}, {"concurrencia": 16, "espera_ms": 10000}):
            with self.subTest(**opciones):
                srv = crear_servidor(ModeloEspera(lambda: None), TOKEN, "small", ("127.0.0.1", 0), **opciones)
                srv.server_close()

    def test_con_concurrencia_2_dos_solicitudes_corren_a_la_vez(self):
        # Si el servidor las serializara, la barrera se rompe, el servidor responde 422 y la prueba falla.
        barrera = threading.Barrier(2, timeout=3)
        self.iniciar(barrera.wait, concurrencia=2)
        (h1, r1), (h2, r2) = self.pedir_en_hilo(), self.pedir_en_hilo()
        h1.join(5)
        h2.join(5)
        self.assertEqual((r1, r2), ([200], [200]))

    def test_con_concurrencia_2_la_tercera_da_409(self):
        dentro, soltar = threading.Barrier(3, timeout=3), threading.Event()
        self.iniciar(lambda: (dentro.wait(), soltar.wait(5)), concurrencia=2)
        hilos = [self.pedir_en_hilo(), self.pedir_en_hilo()]
        dentro.wait()
        inicio = time.monotonic()
        self.assertEqual(self.pedir(), 409)
        self.assertLess(time.monotonic() - inicio, 0.5, "el 409 por defecto debe ser inmediato")
        soltar.set()
        for hilo, resultado in hilos:
            hilo.join(5)
            self.assertEqual(resultado, [200])

    def test_con_espera_la_segunda_aguarda_y_da_200(self):
        dentro, soltar = threading.Event(), threading.Event()
        self.iniciar(lambda: (dentro.set(), soltar.wait(5)), concurrencia=1, espera_ms=3000)
        hilo, primera = self.pedir_en_hilo()
        self.assertTrue(dentro.wait(2))
        temporizador = threading.Timer(0.3, soltar.set)
        temporizador.start()
        inicio = time.monotonic()
        self.assertEqual(self.pedir(), 200)
        self.assertGreaterEqual(time.monotonic() - inicio, 0.2)
        temporizador.join()
        hilo.join(5)
        self.assertEqual(primera, [200])

    def test_si_la_espera_vence_antes_de_que_se_libere_da_409(self):
        dentro, soltar = threading.Event(), threading.Event()
        self.iniciar(lambda: (dentro.set(), soltar.wait(5)), concurrencia=1, espera_ms=100)
        hilo, primera = self.pedir_en_hilo()
        self.assertTrue(dentro.wait(2))
        self.assertEqual(self.pedir(), 409)
        soltar.set()
        hilo.join(5)
        self.assertEqual(primera, [200])


class Configuracion(unittest.TestCase):
    ENTORNO = {"WHISPER_TOKEN": TOKEN}

    def ejecutar_main(self, extra):
        entorno = dict(self.ENTORNO, **extra)
        with patch.dict(os.environ, entorno, clear=True), \
                patch.object(servidor, "cargar_modelo", side_effect=AssertionError("cargó el modelo antes de validar")):
            servidor.main()

    def test_valores_invalidos_abortan_antes_de_cargar_el_modelo(self):
        invalidos = [
            {"WHISPER_CONCURRENCIA": v} for v in ("0", "17", "-1", "abc", "2.5", " 2", "2\n", "9" * 5000)
        ] + [
            {"WHISPER_ESPERA_MS": v} for v in ("10001", "-5", "x", "1e3")
        ]
        for extra in invalidos:
            with self.subTest(extra=str(extra)[:60]):
                with self.assertRaises(SystemExit):
                    self.ejecutar_main(extra)

    def test_los_valores_validos_llegan_al_modelo_y_al_servidor(self):
        modelo = types.SimpleNamespace(transcribe=lambda *a, **k: (iter([]), None))
        servidor_falso = types.SimpleNamespace(serve_forever=lambda: None)
        entorno = dict(self.ENTORNO, WHISPER_CONCURRENCIA="4", WHISPER_ESPERA_MS="2500")
        with patch.dict(os.environ, entorno, clear=True), \
                patch.object(servidor, "cargar_modelo", return_value=modelo) as cargar, \
                patch.object(servidor, "crear_servidor", return_value=servidor_falso) as crear:
            servidor.main()
        self.assertEqual(cargar.call_args.args, ("small", "auto", 4))
        self.assertEqual(crear.call_args.kwargs, {"concurrencia": 4, "espera_ms": 2500})

    def test_por_defecto_concurrencia_1_y_sin_espera(self):
        modelo = types.SimpleNamespace(transcribe=lambda *a, **k: (iter([]), None))
        servidor_falso = types.SimpleNamespace(serve_forever=lambda: None)
        with patch.dict(os.environ, self.ENTORNO, clear=True), \
                patch.object(servidor, "cargar_modelo", return_value=modelo) as cargar, \
                patch.object(servidor, "crear_servidor", return_value=servidor_falso) as crear:
            servidor.main()
        self.assertEqual(cargar.call_args.args, ("small", "auto", 1))
        self.assertEqual(crear.call_args.kwargs, {"concurrencia": 1, "espera_ms": 0})


class CargaDelModelo(unittest.TestCase):
    def modelo_falso(self, fallar_cuda=False):
        llamadas = []

        def whisper(nombre, **opciones):
            llamadas.append((nombre, opciones))
            if fallar_cuda and opciones["device"] == "cuda":
                raise RuntimeError("sin GPU")
            return "modelo"

        return llamadas, patch.dict(sys.modules, {"faster_whisper": types.SimpleNamespace(WhisperModel=whisper)})

    def test_concurrencia_1_conserva_la_llamada_original(self):
        llamadas, modulos = self.modelo_falso()
        with modulos:
            servidor.cargar_modelo("small", "si")
        self.assertEqual(llamadas, [("small", {"device": "cuda", "compute_type": "float16"})])

    def test_num_workers_llega_a_cuda(self):
        llamadas, modulos = self.modelo_falso()
        with modulos:
            servidor.cargar_modelo("small", "si", 3)
        self.assertEqual(llamadas, [("small", {"device": "cuda", "compute_type": "float16", "num_workers": 3})])

    def test_num_workers_llega_a_la_rama_cpu(self):
        llamadas, modulos = self.modelo_falso(fallar_cuda=True)
        with modulos:
            servidor.cargar_modelo("small", "auto", 3)
        self.assertEqual(llamadas[-1], ("small", {"device": "cpu", "compute_type": "int8", "num_workers": 3}))
        llamadas, modulos = self.modelo_falso()
        with modulos:
            servidor.cargar_modelo("small", "no", 1)
        self.assertEqual(llamadas, [("small", {"device": "cpu", "compute_type": "int8"})])


if __name__ == "__main__":
    unittest.main()
