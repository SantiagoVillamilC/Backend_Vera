"""TTS neural local. Sólo loopback, token efímero y audio en memoria.

Variables opcionales: VERA_VOZ_PUERTO=1024..65535 (por defecto 8766, siempre en 127.0.0.1) y
VERA_VOZ_ESPERA_MS=0..10000 (cuánto espera una solicitud por el cerrojo antes de recibir 409, por
defecto 0, es decir 409 inmediato como siempre).

Un proceso sintetiza de a una solicitud a propósito: no se verificó que el fonemizador (espeak)
sea seguro entre hilos. Para paralelizar se levantan varios procesos, cada uno con su
VERA_VOZ_PUERTO.

Variante opcional en GPU, apagada por defecto: VERA_VOZ_DISPOSITIVO=cpu|cuda (por defecto cpu).
Con cpu todo queda como siempre: CPUExecutionProvider, kokoro-v1.0.int8.onnx, 2 hilos intra-op y 1
inter-op. Con cuda se carga el modelo fp32 (kokoro-v1.0.onnx), o el fp16 (kokoro-v1.0.fp16.onnx) si
además VERA_VOZ_PRECISION=fp16 (fp32 o fp16, por defecto fp32, y sólo es válida con cuda). El int8
no se usa en cuda porque sus operadores cuantizados corren en CPU y falsearían una medición. Si
CUDA no carga, onnxruntime cae a CPU en silencio, así que el arranque verifica que el primer
proveedor de la sesión sea CUDAExecutionProvider y, si no, aborta. El peso fp32 se baja con
VERA_VOZ_DESCARGAR_GPU=1 en descargar.py y el fp16 con VERA_VOZ_DESCARGAR_FP16=1 (opt-in aparte).
Las librerías de NVIDIA salen de requirements-cuda.txt.
Advertencia: en una GTX 1650 con onnxruntime-gpu 1.31.0 el modelo fp16 produjo audio vacío en CUDA
(el mismo archivo en CPU sí produce audio), así que el calentamiento falla con un error de numpy. No
se verificó en otras GPU.
"""
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


def entero_de_entorno(nombre, minimo, maximo, defecto):
    """Lee un entero acotado del entorno. Vacío o ausente da el valor por defecto."""
    crudo = os.environ.get(nombre, "")
    if crudo == "":
        return defecto
    if len(crudo) > 6 or not re.fullmatch(r"[0-9]+", crudo) or not minimo <= int(crudo) <= maximo:
        raise SystemExit(f"{nombre} debe ser un entero entre {minimo} y {maximo}")
    return int(crudo)


def adquirir(cerrojo, espera_ms):
    """Toma el cerrojo sin esperar (espera_ms=0) o esperando como máximo espera_ms."""
    if espera_ms == 0:
        return cerrojo.acquire(blocking=False)
    return cerrojo.acquire(timeout=espera_ms / 1000)


def validar_rango(nombre, valor, minimo, maximo):
    """Exige un entero (no booleano) en [minimo, maximo]. Lanza ValueError si no."""
    if type(valor) is not int or not minimo <= valor <= maximo:
        raise ValueError(f"{nombre} debe ser un entero entre {minimo} y {maximo}")


def crear_servidor(motor, token, direccion=("127.0.0.1", 8766), espera_ms=0):
    validar_rango("espera_ms", espera_ms, 0, 10000)
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
            if not adquirir(cerrojo, espera_ms):
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


DIRECTORIO_MODELOS = "/opt/voz/modelos"
MODELO_CPU = "kokoro-v1.0.int8.onnx"
MODELOS_CUDA = {"fp32": "kokoro-v1.0.onnx", "fp16": "kokoro-v1.0.fp16.onnx"}
TEXTO_CALENTAMIENTO = "Hola, soy Vera y estoy lista para ayudarte."
# En cuda se calienta además con dos longitudes más, para que la medición no pague el primer arranque.
TEXTOS_CALENTAMIENTO_CUDA = (
    "Tienes tres movimientos nuevos y un saldo disponible de doscientos mil pesos.",
    "Hoy gastaste más en restaurantes que el promedio de las últimas semanas, así que puedes revisar "
    "tus límites mensuales, ajustar el presupuesto de cada categoría y programar un aviso cuando "
    "te acerques al tope que definiste para el resto del mes.",
)


def opcion_de_entorno(nombre, validos):
    """Lee una opción de texto exacto del entorno. Vacío o ausente da None, cualquier otro valor se rechaza."""
    crudo = os.environ.get(nombre, "")
    if crudo == "":
        return None
    if crudo not in validos:
        raise SystemExit(f"{nombre} debe ser {' o '.join(validos)}")
    return crudo


def dispositivo_de_entorno():
    """Devuelve (dispositivo, precision). cpu no admite precisión, cuda usa fp32 salvo que se pida fp16."""
    dispositivo = opcion_de_entorno("VERA_VOZ_DISPOSITIVO", ("cpu", "cuda")) or "cpu"
    precision = opcion_de_entorno("VERA_VOZ_PRECISION", tuple(MODELOS_CUDA))
    if dispositivo == "cpu":
        if os.environ.get("VERA_VOZ_PRECISION", "") != "":
            raise SystemExit("VERA_VOZ_PRECISION solo aplica con VERA_VOZ_DISPOSITIVO=cuda, en cpu el modelo es int8")
        return "cpu", None
    return "cuda", precision or "fp32"


def crear_sesion(ort, dispositivo, precision, directorio=DIRECTORIO_MODELOS):
    """Crea la sesión de ONNX Runtime. Recibe el módulo ort para poder probarla con un falso."""
    if not (dispositivo == "cpu" and precision is None or dispositivo == "cuda" and precision in MODELOS_CUDA):
        raise SystemExit("Combinación inválida de dispositivo y precisión de la voz")
    opciones = ort.SessionOptions()
    opciones.intra_op_num_threads = 2
    opciones.inter_op_num_threads = 1
    if dispositivo == "cpu":
        return ort.InferenceSession(f"{directorio}/{MODELO_CPU}", sess_options=opciones, providers=["CPUExecutionProvider"])
    # Las librerías de NVIDIA instaladas por pip no están en la ruta del cargador: se precargan antes.
    if hasattr(ort, "preload_dlls"):
        ort.preload_dlls()
    # cudnn_conv_algo_search: EXHAUSTIVE (por defecto) repite una búsqueda costosa por cada forma de
    # entrada y aquí la longitud cambia con cada texto. HEURISTIC es la búsqueda ligera.
    # Fuente: https://onnxruntime.ai/docs/execution-providers/CUDA-ExecutionProvider.html#cudnn_conv_algo_search
    proveedores = [("CUDAExecutionProvider", {"cudnn_conv_algo_search": "HEURISTIC"}), "CPUExecutionProvider"]
    sesion = ort.InferenceSession(f"{directorio}/{MODELOS_CUDA[precision]}", sess_options=opciones, providers=proveedores)
    obtenidos = sesion.get_providers()
    if not obtenidos or obtenidos[0] != "CUDAExecutionProvider":
        # ORT cae a CPU en silencio si CUDA no carga, y eso invalidaría cualquier medición en GPU.
        raise SystemExit("CUDAExecutionProvider no cargó (proveedores: " + ", ".join(obtenidos) + "). "
                         "Revisa el driver y las librerías de requirements-cuda.txt, o usa VERA_VOZ_DISPOSITIVO=cpu")
    return sesion


def calentar(motor_voz, dispositivo):
    """Calienta ONNX antes de declarar el servicio disponible. En cuda usa tres longitudes de texto."""
    textos = (TEXTO_CALENTAMIENTO,) + (TEXTOS_CALENTAMIENTO_CUDA if dispositivo == "cuda" else ())
    for texto in textos:
        motor_voz.create(texto, voice="ef_dora", lang="es")


def main():
    logging.disable(logging.CRITICAL)
    secreto = os.environ.get("VERA_LAB_TOKEN", "")
    if len(secreto) < 32:
        raise SystemExit("Falta token efímero del laboratorio.")
    puerto = entero_de_entorno("VERA_VOZ_PUERTO", 1024, 65535, 8766)
    espera_ms = entero_de_entorno("VERA_VOZ_ESPERA_MS", 0, 10000, 0)
    dispositivo, precision = dispositivo_de_entorno()
    # Importes diferidos: validar el entorno no debe cargar ONNX ni Kokoro.
    import onnxruntime as ort
    from kokoro_onnx import Kokoro

    sesion = crear_sesion(ort, dispositivo, precision)
    motor_voz = Kokoro.from_session(sesion, f"{DIRECTORIO_MODELOS}/voices-v1.0.bin")
    calentar(motor_voz, dispositivo)
    print("Voz neural local lista: Dora · español · sin cuota de Google.", flush=True)
    crear_servidor(motor_voz, secreto, ("127.0.0.1", puerto), espera_ms=espera_ms).serve_forever()


if __name__ == "__main__":
    main()
