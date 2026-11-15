"""Descarga únicamente los pesos publicados por el mantenedor y verifica SHA256 y tamaño.

Por defecto baja sólo los dos pesos de la imagen CPU (int8 y voces). Con VERA_VOZ_DESCARGAR_GPU=1
baja además kokoro-v1.0.onnx (fp32) y kokoro-v1.0.fp16.onnx para la variante en GPU de servidor.py.
VERA_VOZ_MODELOS_DESTINO cambia el directorio de destino (por defecto /opt/voz/modelos).
"""
import hashlib
import os
from pathlib import Path
from urllib.request import urlopen

BASE = "https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.1/"
# Huella y tamaño de cada asset de la release. Los mismos valores viven en modelos/lock.json y una
# prueba de cmd/verificar-modelos los compara, así que este manifiesto no puede cambiarlos solo.
ARCHIVOS = {
    "kokoro-v1.0.int8.onnx": (
        "ae315a79b623f244700e4afb9246c46a26066782e049ba174bf3ba433970ee9c",
        114119327,
    ),
    "voices-v1.0.bin": (
        "bca610b8308e8d99f32e6fe4197e7ec01679264efed0cac9140fe9c29f1fbf7d",
        28214398,
    ),
}
# Pesos opcionales para la variante en GPU. Siguen el mismo formato porque la prueba de
# cmd/verificar-modelos lee todo el archivo y exige que cada uno esté también en modelos/lock.json.
ARCHIVOS_GPU = {
    "kokoro-v1.0.onnx": (
        "beb0d1848dee9a49da392cc3df26958d46cfa35d321edf434f52949153f0df3a",
        325505369,
    ),
    "kokoro-v1.0.fp16.onnx": (
        "f3a290d384fbb27966d462905c71a46cef9e5fd00516b40df32a0b4afe77ac96",
        163527961,
    ),
}


def archivos_a_bajar():
    """Los pesos de CPU siempre. Los de GPU sólo con VERA_VOZ_DESCARGAR_GPU=1 (vacío y 0 son apagado)."""
    gpu = os.environ.get("VERA_VOZ_DESCARGAR_GPU", "")
    if gpu not in ("", "0", "1"):
        raise SystemExit("VERA_VOZ_DESCARGAR_GPU debe ser 0 o 1")
    return {**ARCHIVOS, **ARCHIVOS_GPU} if gpu == "1" else dict(ARCHIVOS)


def directorio_destino():
    return Path(os.environ.get("VERA_VOZ_MODELOS_DESTINO", "") or "/opt/voz/modelos")


def descargar(nombre, esperado, tamano, destino):
    huella = hashlib.sha256()
    recibidos = 0
    with urlopen(BASE + nombre, timeout=60) as respuesta, (destino / nombre).open("wb") as archivo:
        while bloque := respuesta.read(1024 * 1024):
            huella.update(bloque)
            archivo.write(bloque)
            recibidos += len(bloque)
    if recibidos != tamano:
        raise RuntimeError("El tamaño del modelo no coincide: " + nombre)
    if huella.hexdigest() != esperado:
        raise RuntimeError("La huella del modelo no coincide: " + nombre)
    print("Modelo verificado:", nombre, flush=True)


def main():
    archivos = archivos_a_bajar()
    destino = directorio_destino()
    destino.mkdir(parents=True, exist_ok=True)
    for nombre, (esperado, tamano) in archivos.items():
        descargar(nombre, esperado, tamano, destino)


if __name__ == "__main__":
    main()
