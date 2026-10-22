"""TTS neural local. Sólo loopback, token efímero y audio en memoria."""
import hmac
import base64
import io
import json
import logging
import os
import re
import threading
import wave
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import numpy as np
import onnxruntime as ort
from kokoro_onnx import Kokoro


def texto_hablado(texto):
    # Sólo cambia la pronunciación; nunca calcula ni redondea cantidades.
    def monto(m):
        entero, centavos = m.groups()
        return entero + " pesos colombianos" + (" con " + centavos + " centavos" if centavos != "00" else "")
    texto = re.sub(r"\b(\d+)\.(\d{2})\s+COP\b", monto, texto)
    return texto.replace("COP", "pesos colombianos").replace("**", "").replace("`", "")


def frases(texto):
    # No corta dentro de números decimales. Mantiene un orden único y acotado.
    partes = re.split(r"(?<=[.!?;])\s+|\n+", texto)
    for parte in partes:
        palabras = parte.split()
        bloque = ""
        for palabra in palabras:
            if len(bloque) + len(palabra) > 200 and bloque:
                yield bloque
                bloque = ""
            bloque += (" " if bloque else "") + palabra
        if bloque:
            yield bloque


def crear_servidor(motor, token, direccion=("127.0.0.1", 8766)):
    cerrojo = threading.Lock()

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass  # No registra texto, audio ni credenciales.

        def responder(self, estado, contenido=b"", tipo="application/json"):
            self.send_response(estado)
            self.send_header("Content-Type", tipo)
            self.send_header("Content-Length", str(len(contenido)))
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            try:
                self.wfile.write(contenido)
            except (BrokenPipeError, ConnectionResetError):
                pass

        def autorizado(self):
            return hmac.compare_digest(self.headers.get("X-Vera-Token", "").encode("utf-8"), token.encode("utf-8"))

        def do_GET(self):
            if not self.autorizado():
                self.responder(401)
            elif self.path == "/healthz":
                self.responder(200, b'{"voz":"ef_dora","modelo":"Kokoro-82M"}')
            else:
                self.responder(404)

        def do_POST(self):
            self.connection.settimeout(10)
            if not self.autorizado():
                self.responder(401)
                return
            if self.path not in ("/sintetizar", "/flujo"):
                self.responder(404)
                return
            try:
                longitud = int(self.headers.get("Content-Length", "0"))
                if not 0 < longitud <= 32 * 1024:
                    raise ValueError()
                datos = json.loads(self.rfile.read(longitud))
                texto = datos["texto"]
                if not isinstance(texto, str) or not texto.strip() or len(texto.encode("utf-8")) > 8192:
                    raise ValueError()
            except (ValueError, KeyError, TypeError, TimeoutError):
                self.responder(422)
                return
            if not cerrojo.acquire(blocking=False):
                self.responder(409)
                return
            iniciado = False
            try:
                if self.path == "/flujo":
                    self.send_response(200)
                    self.send_header("Content-Type", "application/x-ndjson")
                    self.send_header("Cache-Control", "no-store")
                    self.end_headers()
                    iniciado = True
                    total = 0
                    for frase in frases(texto_hablado(texto)):
                        muestras, frecuencia = motor.create(frase, voice="ef_dora", speed=1.0, lang="es")
                        if frecuencia != 24000 or not np.isfinite(muestras).all():
                            raise ValueError()
                        pcm = (np.clip(muestras, -1, 1) * 32767).astype("<i2").tobytes()
                        total += len(pcm)
                        if not pcm or total > 4 * 1024 * 1024:
                            raise ValueError()
                        self.wfile.write(json.dumps({"tipo": "audio", "pcm": base64.b64encode(pcm).decode("ascii")}).encode() + b"\n")
                        self.wfile.flush()
                    self.wfile.write(b'{"tipo":"fin"}\n')
                    self.wfile.flush()
                    return
                muestras, frecuencia = motor.create(texto_hablado(texto), voice="ef_dora", speed=1.0, lang="es")
                if frecuencia != 24000 or not 0 < len(muestras) <= 2 * 1024 * 1024 or not np.isfinite(muestras).all():
                    raise ValueError()
                audio = io.BytesIO()
                with wave.open(audio, "wb") as wav:
                    wav.setnchannels(1)
                    wav.setsampwidth(2)
                    wav.setframerate(frecuencia)
                    wav.writeframes((np.clip(muestras, -1, 1) * 32767).astype("<i2").tobytes())
                self.responder(200, audio.getvalue(), "audio/wav")
            except Exception:
                if not iniciado:
                    self.responder(503)  # Los errores de librería pueden contener texto privado.
                # Con audio iniciado, cerrar sin fin permite detectar un corte.
            finally:
                cerrojo.release()

    servidor = ThreadingHTTPServer(direccion, Handler)
    servidor.daemon_threads = True
    return servidor


if __name__ == "__main__":
    logging.disable(logging.CRITICAL)
    secreto = os.environ.get("VERA_LAB_TOKEN", "")
    if len(secreto) < 32:
        raise SystemExit("Falta token efímero del laboratorio.")
    opciones = ort.SessionOptions()
    opciones.intra_op_num_threads = 2
    opciones.inter_op_num_threads = 1
    sesion = ort.InferenceSession("/opt/voz/modelos/kokoro-v1.0.int8.onnx", sess_options=opciones, providers=["CPUExecutionProvider"])
    motor_voz = Kokoro.from_session(sesion, "/opt/voz/modelos/voices-v1.0.bin")
    # Calienta ONNX antes de declarar el servicio disponible.
    motor_voz.create("Hola, soy Vera y estoy lista para ayudarte.", voice="ef_dora", lang="es")
    print("Voz neural local lista: Dora · español · sin cuota de Google.", flush=True)
    crear_servidor(motor_voz, secreto).serve_forever()
