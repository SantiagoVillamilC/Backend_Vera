"""Descarga únicamente los pesos publicados por el mantenedor y verifica SHA256 y tamaño.

Por defecto baja sólo los dos pesos de la imagen CPU (int8 y voces). Con VERA_VOZ_DESCARGAR_GPU=1
baja además kokoro-v1.0.onnx (fp32) para la variante en GPU de servidor.py. El fp16
(kokoro-v1.0.fp16.onnx, 163 MB) es un opt-in aparte con VERA_VOZ_DESCARGAR_FP16=1, porque en CUDA
produjo audio vacío y la variante en GPU no lo usa por defecto. Las dos variables aceptan 0 o 1.
VERA_VOZ_MODELOS_DESTINO cambia el directorio de destino (por defecto /opt/voz/modelos).

Cada peso se escribe en <nombre>.parcial dentro del mismo directorio y sólo pasa a su nombre final
con os.replace después de verificar tamaño y SHA256. Ante cualquier fallo no queda nada con el
nombre final, y un peso ya presente con huella y tamaño correctos no se vuelve a bajar.
"""
import hashlib
import os
from pathlib import Path
from urllib.request import urlopen

BASE = "https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.1/"
BLOQUE = 1024 * 1024
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
# Peso opcional para la variante en GPU (VERA_VOZ_DESCARGAR_GPU=1). Sigue el mismo formato porque la
# prueba de cmd/verificar-modelos lee todo el archivo y exige que cada uno esté también en modelos/lock.json.
ARCHIVOS_GPU = {
    "kokoro-v1.0.onnx": (
        "beb0d1848dee9a49da392cc3df26958d46cfa35d321edf434f52949153f0df3a",
        325505369,
    ),
}
# Peso fp16, opt-in separado (VERA_VOZ_DESCARGAR_FP16=1). Mismo formato y mismo cruce con el lockfile.
ARCHIVOS_FP16 = {
    "kokoro-v1.0.fp16.onnx": (
        "f3a290d384fbb27966d462905c71a46cef9e5fd00516b40df32a0b4afe77ac96",
        163527961,
    ),
}


def _activo(variable):
    valor = os.environ.get(variable, "")
    if valor not in ("", "0", "1"):
        raise SystemExit(variable + " debe ser 0 o 1")
    return valor == "1"


def archivos_a_bajar():
    """Los pesos de CPU siempre. El fp32 sólo con VERA_VOZ_DESCARGAR_GPU=1 y el fp16 sólo con
    VERA_VOZ_DESCARGAR_FP16=1 (vacío y 0 son apagado en ambas)."""
    gpu = _activo("VERA_VOZ_DESCARGAR_GPU")
    fp16 = _activo("VERA_VOZ_DESCARGAR_FP16")
    return {**ARCHIVOS, **(ARCHIVOS_GPU if gpu else {}), **(ARCHIVOS_FP16 if fp16 else {})}


def directorio_destino():
    return Path(os.environ.get("VERA_VOZ_MODELOS_DESTINO", "") or "/opt/voz/modelos")


def _ya_verificado(ruta, esperado, tamano):
    """True si ruta existe con el tamaño y la huella esperados. Falso si falta o no coincide."""
    if not ruta.is_file() or ruta.stat().st_size != tamano:
        return False
    huella = hashlib.sha256()
    with ruta.open("rb") as archivo:
        while bloque := archivo.read(BLOQUE):
            huella.update(bloque)
    return huella.hexdigest() == esperado


def _bajar_al_parcial(nombre, esperado, tamano, parcial):
    huella = hashlib.sha256()
    recibidos = 0
    with urlopen(BASE + nombre, timeout=60) as respuesta, parcial.open("wb") as archivo:
        # Nunca pide más de un byte por encima de lo esperado, así un servidor hostil no llena el disco.
        while bloque := respuesta.read(min(BLOQUE, tamano - recibidos + 1)):
            recibidos += len(bloque)
            if recibidos > tamano:
                raise RuntimeError("El tamaño del modelo supera el esperado (" + str(tamano) + " bytes): " + nombre)
            huella.update(bloque)
            archivo.write(bloque)
        archivo.flush()
        os.fsync(archivo.fileno())
    if recibidos != tamano:
        raise RuntimeError("El tamaño del modelo no coincide: " + nombre)
    if huella.hexdigest() != esperado:
        raise RuntimeError("La huella del modelo no coincide: " + nombre)


def descargar(nombre, esperado, tamano, destino):
    final = destino / nombre
    if _ya_verificado(final, esperado, tamano):
        print("Modelo ya presente y verificado:", nombre, flush=True)
        return
    # Un archivo previo que no coincide nunca debe sobrevivir a un intento fallido.
    final.unlink(missing_ok=True)
    parcial = destino / (nombre + ".parcial")
    try:
        _bajar_al_parcial(nombre, esperado, tamano, parcial)
        os.replace(parcial, final)
    except BaseException:
        parcial.unlink(missing_ok=True)
        raise
    print("Modelo verificado:", nombre, flush=True)


def main():
    archivos = archivos_a_bajar()
    destino = directorio_destino()
    destino.mkdir(parents=True, exist_ok=True)
    for nombre, (esperado, tamano) in archivos.items():
        descargar(nombre, esperado, tamano, destino)


if __name__ == "__main__":
    main()
