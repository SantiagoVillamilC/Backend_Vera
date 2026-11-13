"""Mide el WER del STT local y lo compara con un tope.

Norma de comparacion: minusculas, sin tildes, sin puntuacion, espacios colapsados y
cifras tal como salen del modelo. El WER se calcula por palabras con distancia de
Levenshtein sobre los totales (ediciones / palabras de referencia), no como promedio
de tasas por clip.

Uso: python medir_wer.py --manifiesto clips/manifiesto.json [--tope 0.10]
Sale con 1 cuando el WER supera el tope, para que la compuerta de CI falle.
"""

from __future__ import annotations

import argparse
import json
import sys
import unicodedata
import wave
from pathlib import Path


def cargar_16k(ruta: Path):
    """Lee el WAV mono de 16 bits y lo lleva a 16 kHz en coma flotante."""
    import numpy as np

    with wave.open(str(ruta)) as w:
        if w.getsampwidth() != 2 or w.getnchannels() != 1:
            raise SystemExit("el clip no es PCM16 mono: " + str(ruta))
        tasa = w.getframerate()
        datos = np.frombuffer(w.readframes(w.getnframes()), dtype="<i2").astype("float32") / 32768.0
    if tasa != 16000:
        n = int(round(len(datos) * 16000 / tasa))
        datos = np.interp(np.linspace(0, len(datos) - 1, n), np.arange(len(datos)), datos).astype("float32")
    return datos


def normalizar(texto: str) -> list[str]:
    descompuesto = unicodedata.normalize("NFD", texto)
    sin_marcas = "".join(c for c in descompuesto if unicodedata.category(c) != "Mn")
    limpio = "".join(c if c.isalnum() or c.isspace() else " " for c in sin_marcas.lower())
    return limpio.split()


def ediciones(referencia: list[str], hipotesis: list[str]) -> int:
    previa = list(range(len(hipotesis) + 1))
    for i, palabra in enumerate(referencia, start=1):
        actual = [i]
        for j, otra in enumerate(hipotesis, start=1):
            costo = 0 if palabra == otra else 1
            actual.append(min(previa[j] + 1, actual[j - 1] + 1, previa[j - 1] + costo))
        previa = actual
    return previa[-1]


def main() -> int:
    p = argparse.ArgumentParser(description="WER del STT local sobre un manifiesto de clips.")
    p.add_argument("--manifiesto", type=Path, required=True)
    p.add_argument("--tope", type=float, default=0.10)
    args = p.parse_args()

    if not args.manifiesto.is_file():
        raise SystemExit("falta el manifiesto: " + str(args.manifiesto))
    clips = json.loads(args.manifiesto.read_text(encoding="utf-8"))
    if not clips:
        raise SystemExit("el manifiesto no trae clips: " + str(args.manifiesto))
    base = args.manifiesto.parent

    from faster_whisper import WhisperModel

    modelo = WhisperModel("small", device="cpu", compute_type="int8")

    total_ediciones = total_palabras = 0
    for clip in clips:
        ruta = base / clip["archivo"]
        if not ruta.is_file():
            raise SystemExit("falta el clip: " + str(ruta))
        segmentos, _info = modelo.transcribe(
            cargar_16k(ruta), language="es", beam_size=1, vad_filter=True
        )
        hipotesis = " ".join(s.text for s in segmentos).strip()
        ref = normalizar(clip["texto"])
        total_ediciones += ediciones(ref, normalizar(hipotesis))
        total_palabras += len(ref)

    if not total_palabras:
        raise SystemExit("el manifiesto no trae palabras de referencia")

    wer = round(total_ediciones / total_palabras, 4)
    print(json.dumps({"n": len(clips), "wer": wer, "tope": args.tope, "pass": wer <= args.tope}))
    return 0 if wer <= args.tope else 1


if __name__ == "__main__":
    raise SystemExit(main())
