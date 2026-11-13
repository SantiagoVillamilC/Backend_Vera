"""Humo del servicio: manda un WAV y muestra la transcripción.

Uso: python probar.py ruta.wav [http://127.0.0.1:8767]
Requiere WHISPER_TOKEN en el entorno, el mismo del servidor.
"""
import os
import sys
import urllib.request

ruta = sys.argv[1]
base = sys.argv[2] if len(sys.argv) > 2 else "http://127.0.0.1:8767"
datos = open(ruta, "rb").read()
peticion = urllib.request.Request(
    base + "/transcribir",
    data=datos,
    headers={"Content-Type": "audio/wav", "X-Vera-Token": os.environ.get("WHISPER_TOKEN", "")},
    method="POST",
)
with urllib.request.urlopen(peticion, timeout=120) as respuesta:
    print(respuesta.read().decode("utf-8"))
