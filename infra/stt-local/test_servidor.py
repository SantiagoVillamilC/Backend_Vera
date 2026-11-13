"""Pruebas del contrato HTTP sin descargar el modelo."""
import http.client
import json
import sys
import threading
import types
import unittest
from unittest.mock import patch
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


if __name__ == "__main__":
    unittest.main()
