import base64
import io
import json
import threading
import unittest
import urllib.error
import urllib.request
import wave

import numpy as np
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


if __name__ == "__main__":
    unittest.main()
