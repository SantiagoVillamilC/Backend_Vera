"""Iteracion 2: SetFit con LoRA (adaptadores) sobre la misma tarea.

En vez de ajustar los 118 M de parametros del cuerpo, agrega un adaptador LoRA de bajo rango y
congela el resto, asi que el optimizador solo guarda estado de una fraccion minima. Entrena en la
GPU. Reporta la fraccion de parametros entrenables, el tamano del adaptador solo, la VRAM y el
tiempo, y registra el resultado junto al de SetFit completo para comparar.

Nota honesta de costo: el adaptador solo pesa unos MB, pero para usarlo en produccion hace falta el
modelo base (471 MB) mas el adaptador. El ahorro de LoRA es de entrenamiento (memoria y tiempo) y de
flexibilidad (varios adaptadores sobre una base), no de disco en despliegue.
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
BASE_MODEL = "sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2"
ITERACION = os.environ.get("ITERACION", "2")

from ajuste_setfit import evaluar, leer_csv  # noqa: E402
from registro import registrar  # noqa: E402


def main() -> None:
    import torch
    from datasets import Dataset
    from peft import LoraConfig
    from setfit import SetFitModel, Trainer, TrainingArguments

    assert torch.cuda.is_available(), "la iteracion 2 exige GPU"
    comercios = leer_csv("comercios.csv")
    ficticia = {e["empresa_id"]: e["ficticia"] for e in leer_csv("empresas.csv")}
    train = [f for f in comercios if f["conjunto"] == "entrenamiento"]
    prueba = [f for f in comercios if f["conjunto"] == "prueba" and f["es_comercio"] == "true"]
    categorias = sorted({f["categoria"] for f in train})

    modelo = SetFitModel.from_pretrained(BASE_MODEL, labels=categorias, cache_folder=CACHE)
    lora = LoraConfig(r=16, lora_alpha=32, lora_dropout=0.05, bias="none", target_modules=["query", "value"])
    modelo.model_body.add_adapter(lora)

    total = sum(p.numel() for p in modelo.model_body.parameters())
    entrenables = sum(p.numel() for p in modelo.model_body.parameters() if p.requires_grad)
    fraccion = round(entrenables / total * 100, 2)

    ds = Dataset.from_dict(
        {"text": [f["nombre_canonico"] for f in train], "label": [categorias.index(f["categoria"]) for f in train]}
    )
    args = TrainingArguments(batch_size=8, num_epochs=2, num_iterations=20, seed=42, show_progress_bar=False)
    torch.cuda.reset_peak_memory_stats()
    inicio = time.perf_counter()
    Trainer(model=modelo, train_dataset=ds, args=args).train()
    tiempo_train = time.perf_counter() - inicio
    vram_pico_mb = round(torch.cuda.max_memory_allocated() / 1024**2, 1)

    adaptador_mb = round(
        sum(p.numel() * p.element_size() for p in modelo.model_body.parameters() if p.requires_grad) / 1e6, 2
    )

    preds = [str(x) for x in modelo.predict([f["nombre_canonico"] for f in prueba])]
    metricas = evaluar(preds, prueba, ficticia)

    registrar({
        "iteracion": ITERACION,
        "clasificador": "MiniLM SetFit+LoRA",
        "variante": "fine-tune GPU",
        "exactitud": metricas["exactitud"],
        "cobertura": metricas["cobertura"],
        "exactitud_revela": metricas["exactitud_revela"],
        "exactitud_ficticia": metricas["exactitud_ficticia"],
        "exactitud_real": metricas["exactitud_real"],
        "entrenamiento_s": round(tiempo_train, 1),
        "nota": f"entrenables {entrenables}/{total} ({fraccion}%), adaptador {adaptador_mb} MB, VRAM pico {vram_pico_mb} MB",
    })
    print(json.dumps({
        "entrenables_pct": fraccion,
        "adaptador_mb": adaptador_mb,
        "vram_pico_mb": vram_pico_mb,
        "entrenamiento_s": round(tiempo_train, 1),
        "metricas": metricas,
    }, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
