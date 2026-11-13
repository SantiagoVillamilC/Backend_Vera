"""Simulador mínimo de POST /v1/vera/sintetizar para desarrollar la app sin backend.

Solo biblioteca estándar de Python 3. No carga Kokoro ni ningún modelo: devuelve un WAV fijo
(ejemplos/vera-hola.wav, PCM16 mono 24 kHz) o un tono generado si el archivo no está.

Reproduce el contrato de api/openapi.yaml y el orden de comprobaciones de
internal/platform/httpserver/vera_sintesis.go: sesión, consentimiento, cuerpo, cuota y síntesis.
El endpoint real no recibe token de voz: VERA_VOZ_TOKEN es solo el secreto entre el backend y Kokoro.
Una cabecera X-Vera-Token que llegue se ignora.

Uso:
    python3 infra/voz-local/simulado.py --puerto 8780
    # Emulador Android: usar --host 0.0.0.0 y llamar a http://10.0.2.2:8780

El caso se elige con la cabecera X-Simulado-Caso (por defecto "audio"):
    audio              200 audio/wav
    degradado          200 JSON {"audio_disponible": false, "voz_local": true} y X-Vera-Audio: no-disponible
    sin-consentimiento 403 ConsentRequired
    cuota              429 TooManyRequests con Retry-After
    no-verificable     503 AssistantUnavailable
    sin-configurar     igual que degradado: el backend sin VERA_VOZ_TOKEN no tiene sintetizador y responde 200 degradado
"""
import argparse
import io
import json
import math
import struct
import wave
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

EJEMPLO = Path(__file__).resolve().parent / "ejemplos" / "vera-hola.wav"
CASOS = {"audio", "degradado", "sin-consentimiento", "cuota", "no-verificable", "sin-configurar"}
LIMITE_CUERPO = 1 << 20
LIMITE_TEXTO = 400  # caracteres, igual que maxLength de SintetizarVeraRequest


def wav_de_ejemplo():
    """Devuelve el WAV de ejemplo o, si falta, un tono de 1 s con el mismo formato."""
    if EJEMPLO.is_file():
        return EJEMPLO.read_bytes()
    salida = io.BytesIO()
    with wave.open(salida, "wb") as wav:
        wav.setnchannels(1)
        wav.setsampwidth(2)
        wav.setframerate(24000)
        muestras = (int(8000 * math.sin(2 * math.pi * 440 * n / 24000)) for n in range(24000))
        wav.writeframes(b"".join(struct.pack("<h", m) for m in muestras))
    return salida.getvalue()


def crear_servidor(direccion=("127.0.0.1", 8780)):
    audio = wav_de_ejemplo()

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def enviar(self, estado, cuerpo, tipo, extra=None):
            self.send_response(estado)
            self.send_header("Content-Type", tipo)
            self.send_header("Content-Length", str(len(cuerpo)))
            self.send_header("Cache-Control", "no-store")
            for nombre, valor in (extra or {}).items():
                self.send_header(nombre, valor)
            self.end_headers()
            self.wfile.write(cuerpo)

        def sobre(self, estado, datos=None, error=None, extra=None):
            cuerpo = json.dumps({"success": error is None, "data": datos, "error": error, "meta": None})
            self.enviar(estado, cuerpo.encode(), "application/json; charset=utf-8", extra)

        def fallo(self, estado, codigo, mensaje, extra=None):
            self.sobre(estado, error={"code": codigo, "message": mensaje}, extra=extra)

        def do_POST(self):
            if self.path != "/v1/vera/sintetizar":
                self.fallo(404, "NotFound", "Recurso inexistente.")
                return
            caso = self.headers.get("X-Simulado-Caso", "audio")
            if caso not in CASOS:
                self.fallo(422, "ValidationError", "Caso simulado desconocido: " + caso)
                return
            if not self.headers.get("Authorization", "").startswith("Bearer "):
                self.fallo(401, "Unauthorized", "Missing or invalid token.")
                return
            if caso == "sin-consentimiento":
                self.fallo(403, "ConsentRequired", "Falta el consentimiento vigente del asistente.")
                return
            texto = self.leer_texto()
            if texto is None:
                return
            if caso == "cuota":
                self.fallo(429, "TooManyRequests", "Límite de uso del asistente alcanzado. Intenta de nuevo después del intervalo indicado.", {"Retry-After": "60"})
                return
            if caso == "no-verificable":
                self.fallo(503, "AssistantUnavailable", "No se pudo verificar el límite del asistente.")
                return
            if caso in ("degradado", "sin-configurar"):
                self.sobre(200, {"audio_disponible": False, "voz_local": True}, extra={"X-Vera-Audio": "no-disponible"})
                return
            self.enviar(200, audio, "audio/wav")

        def leer_texto(self):
            try:
                longitud = int(self.headers.get("Content-Length", "0"))
            except ValueError:
                longitud = -1
            if not 0 <= longitud <= LIMITE_CUERPO:
                self.fallo(413, "PayloadTooLarge", "Request body exceeds 1 MiB.")
                return None
            try:
                cuerpo = json.loads(self.rfile.read(longitud))
                texto = cuerpo["texto"]
                valido = isinstance(cuerpo, dict) and set(cuerpo) == {"texto"} and isinstance(texto, str)
            except (ValueError, KeyError, TypeError):
                valido = False
            if not valido:
                self.fallo(422, "ValidationError", "El cuerpo debe ser un objeto JSON válido con los campos permitidos.")
                return None
            if not texto.strip() or len(texto) > LIMITE_TEXTO:
                self.fallo(422, "ValidationError", "El texto debe contener entre 1 y %d caracteres." % LIMITE_TEXTO)
                return None
            return texto

    servidor = ThreadingHTTPServer(direccion, Handler)
    servidor.daemon_threads = True
    return servidor


if __name__ == "__main__":
    analizador = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    analizador.add_argument("--host", default="127.0.0.1")
    analizador.add_argument("--puerto", type=int, default=8780)
    argumentos = analizador.parse_args()
    print("Simulador de /v1/vera/sintetizar en http://%s:%d " % (argumentos.host, argumentos.puerto), flush=True)
    crear_servidor((argumentos.host, argumentos.puerto)).serve_forever()
