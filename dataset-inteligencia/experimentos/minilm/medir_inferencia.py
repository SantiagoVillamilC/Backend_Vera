"""Mide el costo de inferencia aislado de cada configuracion, en su propio proceso.

`ru_maxrss` no es confiable en este entorno y el arena de onnxruntime crece al repetir corridas, asi
que el RSS se lee del kernel (VmHWM) en un proceso dedicado por modo. Modos: onnx_int8, onnx_fp32,
setfit. Imprime un JSON con disco, RSS pico, tiempo de carga y milisegundos por nombre.
"""

from __future__ import annotations

import csv
import json
import os
import sys
import time
from pathlib import Path

os.environ.setdefault("TQDM_DISABLE", "1")
os.environ.setdefault("HF_HUB_DISABLE_PROGRESS_BARS", "1")
os.environ.setdefault("TRANSFORMERS_VERBOSITY", "error")

DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(DIR))
BASE = DIR.parents[1]
CORPUS = BASE / "salida" / "2026-3000-A" / "verdad"
CACHE = os.environ.get("MINILM_CACHE", str(Path.home() / ".local/share/minilm-exp/models"))
MODELO_DIR = Path(os.environ.get("SETFIT_DIR", str(Path.home() / ".local/share/minilm-exp/setfit-model")))
ONNX = {"onnx_int8": "onnx/model_quint8_avx2.onnx", "onnx_fp32": "onnx/model.onnx"}


def vmhwm_mb() -> float:
    with open("/proc/self/status", encoding="utf-8") as archivo:
        for linea in archivo:
            if linea.startswith("VmHWM:"):
                return round(int(linea.split()[1]) / 1024, 1)
    return -1.0


def textos_prueba() -> list[str]:
    with open(CORPUS / "comercios.csv", encoding="utf-8") as archivo:
        filas = list(csv.DictReader(archivo))
    return [f["nombre_canonico"] for f in filas if f["conjunto"] == "prueba" and f["es_comercio"] == "true"]


def main() -> None:
    modo = sys.argv[1]
    textos = textos_prueba()
    resultado: dict[str, object] = {"modo": modo, "nombres": len(textos)}

    if modo in ONNX:
        os.environ["MINILM_ONNX"] = ONNX[modo]
        import clasificador_minilm as cm

        emb = cm.Embedder()
        resultado["disco_modelo_mb"] = round(emb.bytes_disco / 1e6, 1)
        resultado["carga_s"] = round(emb.carga_s, 2)
        inicio = time.perf_counter()
        emb.codificar(textos)
        resultado["latencia_ms_por_nombre"] = round((time.perf_counter() - inicio) / len(textos) * 1000, 2)
    elif modo == "setfit":
        from setfit import SetFitModel

        inicio = time.perf_counter()
        modelo = SetFitModel.from_pretrained(str(MODELO_DIR))
        resultado["carga_s"] = round(time.perf_counter() - inicio, 2)
        resultado["disco_modelo_mb"] = round(sum(p.stat().st_size for p in MODELO_DIR.rglob("*") if p.is_file()) / 1e6, 1)
        inicio = time.perf_counter()
        modelo.predict(textos)
        resultado["latencia_ms_por_nombre"] = round((time.perf_counter() - inicio) / len(textos) * 1000, 2)
    else:
        raise SystemExit(f"modo desconocido: {modo}")

    resultado["rss_pico_mb"] = vmhwm_mb()
    print(json.dumps(resultado, ensure_ascii=False))


if __name__ == "__main__":
    main()
