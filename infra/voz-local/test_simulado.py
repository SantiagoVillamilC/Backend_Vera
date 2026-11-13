import io
import json
import threading
import unittest
import urllib.error
import urllib.request
import wave

from simulado import crear_servidor


class SimuladoTest(unittest.TestCase):
    def setUp(self):
        self.servidor = crear_servidor(("127.0.0.1", 0))
        self.hilo = threading.Thread(target=self.servidor.serve_forever, daemon=True)
        self.hilo.start()
        self.url = "http://127.0.0.1:%d/v1/vera/sintetizar" % self.servidor.server_port

    def tearDown(self):
        self.servidor.shutdown()
        self.servidor.server_close()
        self.hilo.join()

    def pedir(self, cuerpo=b'{"texto":"Hola"}', caso=None, token=None, bearer=True):
        cabeceras = {"Content-Type": "application/json"}
        if token is not None:
            cabeceras["X-Vera-Token"] = token
        if bearer:
            cabeceras["Authorization"] = "Bearer de-prueba"
        if caso:
            cabeceras["X-Simulado-Caso"] = caso
        try:
            with urllib.request.urlopen(urllib.request.Request(self.url, data=cuerpo, headers=cabeceras), timeout=2) as r:
                return r.status, r.headers, r.read()
        except urllib.error.HTTPError as e:
            with e:
                return e.code, e.headers, e.read()

    def test_audio_es_wav_pcm16_mono_24khz(self):
        estado, cabeceras, cuerpo = self.pedir()
        self.assertEqual((estado, cabeceras["Content-Type"]), (200, "audio/wav"))
        with wave.open(io.BytesIO(cuerpo)) as audio:
            self.assertEqual((audio.getframerate(), audio.getnchannels(), audio.getsampwidth()), (24000, 1, 2))

    def test_degradado_es_json_con_cabecera(self):
        estado, cabeceras, cuerpo = self.pedir(caso="degradado")
        self.assertEqual(estado, 200)
        self.assertEqual(cabeceras["X-Vera-Audio"], "no-disponible")
        self.assertTrue(cabeceras["Content-Type"].startswith("application/json"))
        self.assertEqual(json.loads(cuerpo), {"success": True, "data": {"audio_disponible": False, "voz_local": True}, "error": None, "meta": None})

    def test_errores_con_su_codigo_y_estado(self):
        casos = [
            (dict(caso="sin-consentimiento"), 403, "ConsentRequired"),
            (dict(caso="cuota"), 429, "TooManyRequests"),
            (dict(caso="no-verificable"), 503, "AssistantUnavailable"),
            (dict(bearer=False), 401, "Unauthorized"),
            (dict(cuerpo=b'{"texto":"  "}'), 422, "ValidationError"),
            (dict(cuerpo=b'{"texto":"a","otro":1}'), 422, "ValidationError"),
            (dict(cuerpo=json.dumps({"texto": "a" * 401}).encode()), 422, "ValidationError"),
            (dict(cuerpo=json.dumps({"texto": "a" * 8193}).encode()), 422, "ValidationError"),
            (dict(cuerpo=b"no es json"), 422, "ValidationError"),
        ]
        for argumentos, estado_esperado, codigo in casos:
            with self.subTest(argumentos=list(argumentos)):
                estado, _, cuerpo = self.pedir(**argumentos)
                sobre = json.loads(cuerpo)
                self.assertEqual((estado, sobre["error"]["code"], sobre["success"]), (estado_esperado, codigo, False))

    def test_el_token_de_voz_no_se_exige_ni_se_evalua(self):
        for token in (None, "otro", "x"):
            with self.subTest(token=token):
                estado, cabeceras, _ = self.pedir(token=token)
                self.assertEqual((estado, cabeceras["Content-Type"]), (200, "audio/wav"))

    def test_sin_configurar_es_degradado_y_no_401(self):
        estado, cabeceras, cuerpo = self.pedir(caso="sin-configurar")
        self.assertEqual((estado, cabeceras["X-Vera-Audio"]), (200, "no-disponible"))
        self.assertEqual(json.loads(cuerpo)["data"], {"audio_disponible": False, "voz_local": True})

    def test_el_tope_de_texto_es_de_400_caracteres(self):
        for texto, estado_esperado in (("a" * 400, 200), ("á" * 400, 200), ("á" * 401, 422)):
            with self.subTest(largo=len(texto), bytes=len(texto.encode())):
                estado, _, _ = self.pedir(cuerpo=json.dumps({"texto": texto}).encode())
                self.assertEqual(estado, estado_esperado)

    def test_cuota_trae_retry_after(self):
        _, cabeceras, _ = self.pedir(caso="cuota")
        self.assertEqual(cabeceras["Retry-After"], "60")


if __name__ == "__main__":
    unittest.main()
