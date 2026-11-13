"""Descarga únicamente los pesos publicados por el mantenedor y verifica SHA256 y tamaño."""
import hashlib
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
destino = Path("/opt/voz/modelos")
destino.mkdir(parents=True, exist_ok=True)
for nombre, (esperado, tamano) in ARCHIVOS.items():
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
