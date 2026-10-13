"""Iteracion 3: evalua el SetFit ajustado exportado a ONNX int8, sin torch.

Carga el ONNX (int8 o fp32 segun ONNX_KIND), tokeniza con el tokenizador Rust directo, hace mean
pooling y aplica la cabeza logistica guardada en head.npz. Mide exactitud con el mismo corte que
`medir`, mas el disco, el RSS pico del kernel y la latencia por nombre, y registra la fila.
"""

from __future__ import annotations

import csv
import json
import os
import sys
import time
from pathlib import Path

os.environ.setdefault("HF_HUB_DISABLE_PROGRESS_BARS", "1")

DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(DIR))
BASE = DIR.parents[1]
CORPUS = BASE / "salida" / "2026-3000-A" / "verdad"
CACHE = os.environ.get("MINILM_CACHE", str(Path.home() / ".local/share/minilm-exp/models"))
ONNX_DIR = Path(os.environ.get("ONNX_DIR", str(Path.home() / ".local/share/minilm-exp/setfit-onnx")))
KIND = os.environ.get("ONNX_KIND", "int8")
ITERACION = os.environ.get("ITERACION", "3")
REPO = "sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2"

import numpy as np  # noqa: E402
import onnxruntime as ort  # noqa: E402

from ajuste_setfit import evaluar  # noqa: E402
from registro import registrar  # noqa: E402


def vmhwm_mb() -> float:
    with open("/proc/self/status", encoding="utf-8") as archivo:
        for linea in archivo:
            if linea.startswith("VmHWM:"):
                return round(int(linea.split()[1]) / 1024, 1)
    return -1.0


def leer_csv(nombre: str) -> list[dict[str, str]]:
    with open(CORPUS / nombre, encoding="utf-8") as archivo:
        return list(csv.DictReader(archivo))


def main() -> None:
    from huggingface_hub import hf_hub_download
    from tokenizers import Tokenizer

    ruta = ONNX_DIR / f"model_{KIND}.onnx"
    sesion = ort.InferenceSession(str(ruta), providers=["CPUExecutionProvider"])
    tok = Tokenizer.from_file(hf_hub_download(REPO, "tokenizer.json", cache_dir=CACHE))
    tok.enable_truncation(64)
    tok.enable_padding(pad_id=tok.token_to_id("<pad>"), pad_token="<pad>")
    cabeza = np.load(ONNX_DIR / "head.npz", allow_pickle=True)
    coef, intercepto, clases = cabeza["coef"], cabeza["intercept"], cabeza["classes"]

    comercios = leer_csv("comercios.csv")
    ficticia = {e["empresa_id"]: e["ficticia"] for e in leer_csv("empresas.csv")}
    prueba = [f for f in comercios if f["conjunto"] == "prueba" and f["es_comercio"] == "true"]
    textos = [f["nombre_canonico"] for f in prueba]

    inicio = time.perf_counter()
    encs = tok.encode_batch(textos)
    ids = np.array([e.ids for e in encs], dtype="int64")
    mascara = np.array([e.attention_mask for e in encs], dtype="int64")
    salida = sesion.run(None, {"input_ids": ids, "attention_mask": mascara, "token_type_ids": np.zeros_like(ids)})[0]
    mascara_f = mascara[..., None].astype("float32")
    vector = (salida * mascara_f).sum(1) / np.clip(mascara_f.sum(1), 1e-9, None)
    # El SetFit de esta tarea se entreno con normalize_embeddings=False, asi que la cabeza logistica
    # espera el vector crudo del mean pooling. Normalizar aqui bajaba la exactitud de 0,82 a 0,77.
    logits = vector @ coef.T + intercepto
    preds = [str(clases[j]) for j in logits.argmax(1)]
    latencia = (time.perf_counter() - inicio) / len(textos) * 1000

    metricas = evaluar(preds, prueba, ficticia)
    registrar({
        "iteracion": ITERACION,
        "clasificador": "MiniLM SetFit ONNX",
        "variante": f"SetFit exportado ONNX {KIND}",
        "exactitud": metricas["exactitud"],
        "cobertura": metricas["cobertura"],
        "exactitud_revela": metricas["exactitud_revela"],
        "exactitud_ficticia": metricas["exactitud_ficticia"],
        "exactitud_real": metricas["exactitud_real"],
        "disco_modelo_mb": round(ruta.stat().st_size / 1e6, 1),
        "rss_mb": vmhwm_mb(),
        "latencia_ms": round(latencia, 2),
        "nota": f"sin torch. tokenizador Rust. head.npz {KIND}",
    })
    print(json.dumps({
        "kind": KIND,
        "disco_mb": round(ruta.stat().st_size / 1e6, 1),
        "rss_pico_mb": vmhwm_mb(),
        "latencia_ms_por_nombre": round(latencia, 2),
        "metricas": metricas,
    }, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
