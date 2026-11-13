"""Transcripción local para el asistente. Sólo loopback, token y audio en memoria.

Servicio local: replica el patrón del servicio de voz del laboratorio
(infra/voz-local/servidor.py) para que el backend solo tenga que cambiar a quién
le manda el audio. No guarda audio ni texto, y no habla con servicios externos.

Uso:
    WHISPER_TOKEN=... python servidor.py
Variables: WHISPER_TOKEN (obligatoria, >=32), WHISPER_MODELO=small,
WHISPER_DIRECCION=127.0.0.1:8767, WHISPER_GPU=auto|si|no.
"""
import hmac
import io
import json
import os
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

MAX_BYTES = 8 * 1024 * 1024
MAX_SEGUNDOS = 60
TIPOS = {"audio/wav", "audio/x-wav", "audio/m4a", "audio/mp4", "audio/mpeg"}


def cargar_modelo(nombre, gpu):
    from faster_whisper import WhisperModel

    if gpu in ("auto", "si"):
        try:
            return WhisperModel(nombre, device="cuda", compute_type="float16")
        except Exception:
            if gpu == "si":
                raise
    return WhisperModel(nombre, device="cpu", compute_type="int8")


def crear_servidor(modelo, token, nombre_modelo, direccion):
    if direccion[0] != "127.0.0.1" or len(token) < 32:
        raise ValueError("Se requiere loopback y un token de al menos 32 caracteres")
    cerrojo = threading.Lock()

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass  # nunca registra audio, texto ni token

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
            recibido = self.headers.get("X-Vera-Token", "").encode("utf-8")
            return hmac.compare_digest(recibido, token.encode("utf-8"))

        def do_GET(self):
            if not self.autorizado():
                self.responder(401)
            elif self.path == "/healthz":
                self.responder(200, json.dumps({"transcripcion": True, "modelo": nombre_modelo}).encode())
            else:
                self.responder(404)

        def do_POST(self):
            self.connection.settimeout(60)
            if not self.autorizado():
                self.responder(401)
                return
            if self.path != "/transcribir":
                self.responder(404)
                return
            tipo = (self.headers.get("Content-Type") or "").split(";")[0].strip().lower()
            if tipo not in TIPOS:
                self.responder(415)
                return
            longitud = 0
            if self.headers.get("Transfer-Encoding"):
                self.responder(422)
                return
            try:
                longitud = int(self.headers.get("Content-Length", "0"))
                if not 0 < longitud <= MAX_BYTES:
                    raise ValueError()
                audio = self.rfile.read(longitud)
                if len(audio) != longitud:
                    raise ValueError()
            except (ValueError, TimeoutError):
                self.responder(413 if longitud > MAX_BYTES else 422)
                return
            if not cerrojo.acquire(blocking=False):
                self.responder(409)
                return
            try:
                # Decodificar antes del VAD: también limita clips largos en silencio.
                from faster_whisper.audio import decode_audio
                muestras = decode_audio(io.BytesIO(audio), sampling_rate=16000)
                if len(muestras) > MAX_SEGUNDOS * 16000:
                    self.responder(413)
                    return
                segmentos, _ = modelo.transcribe(
                    muestras, language="es", beam_size=1, vad_filter=True
                )
                texto = " ".join(s.text.strip() for s in segmentos).strip()
            except Exception:
                self.responder(422)
                return
            finally:
                cerrojo.release()
            cuerpo = json.dumps({"transcripcion": texto, "modelo": nombre_modelo, "idioma": "es"})
            self.responder(200, cuerpo.encode("utf-8"))

    return ThreadingHTTPServer(direccion, Handler)


def main():
    token = os.environ.get("WHISPER_TOKEN", "")
    if len(token) < 32:
        raise SystemExit("WHISPER_TOKEN debe tener al menos 32 caracteres")
    nombre_modelo = os.environ.get("WHISPER_MODELO", "small")
    gpu = os.environ.get("WHISPER_GPU", "auto")
    host, _, puerto = os.environ.get("WHISPER_DIRECCION", "127.0.0.1:8767").rpartition(":")
    if host != "127.0.0.1" or gpu not in ("auto", "si", "no") or nombre_modelo != "small":
        raise SystemExit("Se requiere 127.0.0.1, modelo small y GPU auto|si|no")
    modelo = cargar_modelo(nombre_modelo, gpu)
    # transcribe devuelve un generador: consumirlo es necesario para calentar.
    import numpy as np
    segmentos, _ = modelo.transcribe(np.zeros(16000, dtype=np.float32), language="es", beam_size=1, vad_filter=True)
    list(segmentos)
    servidor = crear_servidor(modelo, token, nombre_modelo, (host or "127.0.0.1", int(puerto)))
    servidor.serve_forever()


if __name__ == "__main__":
    main()
