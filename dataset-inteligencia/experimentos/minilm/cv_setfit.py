"""Validacion cruzada de 5 particiones para SetFit (fase 3).

Reentrena en la GPU en cada particion, porque el modelo de una particion no puede ver el pliegue de
prueba. Da media y desviacion de la exactitud sobre los 130 etiquetados, comparable con la CV de los
metodos congelados de `evaluar_cv.py`. Registra una fila en `resultados.csv`.
"""

from __future__ import annotations

import csv
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
K = 5
SEMILLA = 42
ITERACION = os.environ.get("ITERACION", "1")
LORA = os.environ.get("LORA") == "1"

import numpy as np  # noqa: E402

from evaluar_cv import leer_csv, particiones_estratificadas  # noqa: E402
from registro import registrar  # noqa: E402


def main() -> None:
    import torch
    from datasets import Dataset
    from setfit import SetFitModel, Trainer, TrainingArguments

    assert torch.cuda.is_available(), "SetFit CV exige GPU"
    comercios = leer_csv("comercios.csv")
    filas = [f for f in comercios if f["conjunto"] == "entrenamiento" or (f["conjunto"] == "prueba" and f["es_comercio"] == "true")]
    etiquetas = [f["categoria"] for f in filas]
    categorias = sorted(set(etiquetas))
    folds = particiones_estratificadas(etiquetas, K, SEMILLA)

    exactitudes = []
    vram_pico = 0.0
    for numero, prueba_idx in enumerate(folds, 1):
        prueba = set(prueba_idx)
        train = [f for i, f in enumerate(filas) if i not in prueba]
        ds = Dataset.from_dict(
            {"text": [f["nombre_canonico"] for f in train], "label": [categorias.index(f["categoria"]) for f in train]}
        )
        torch.cuda.reset_peak_memory_stats()
        modelo = SetFitModel.from_pretrained(BASE_MODEL, labels=categorias, cache_folder=CACHE)
        if LORA:
            from peft import LoraConfig

            modelo.model_body.add_adapter(
                LoraConfig(r=16, lora_alpha=32, lora_dropout=0.05, bias="none", target_modules=["query", "value"])
            )
        args = TrainingArguments(batch_size=8, num_epochs=2, num_iterations=20, seed=SEMILLA, show_progress_bar=False)
        Trainer(model=modelo, train_dataset=ds, args=args).train()
        vram_pico = max(vram_pico, round(torch.cuda.max_memory_allocated() / 1024**2, 1))
        preds = [str(x) for x in modelo.predict([filas[i]["nombre_canonico"] for i in prueba_idx])]
        exactitud = sum(1 for p, i in zip(preds, prueba_idx) if p == filas[i]["categoria"]) / len(prueba_idx)
        exactitudes.append(exactitud)
        print(f"particion {numero}/{K}: {round(exactitud, 4)}")
        del modelo
        torch.cuda.empty_cache()

    media = round(float(np.mean(exactitudes)), 4)
    desv = round(float(np.std(exactitudes)), 4)
    registrar({
        "iteracion": ITERACION,
        "clasificador": "MiniLM SetFit+LoRA" if LORA else "MiniLM SetFit",
        "variante": f"{'fine-tune LoRA' if LORA else 'fine-tune GPU'} (CV{K})",
        "exactitud": media,
        "nota": f"CV{K} estratificada, desviacion {desv}, VRAM pico {vram_pico} MB",
    })
    print(f"{'SetFit+LoRA' if LORA else 'SetFit'} CV{K}: {media} +/- {desv}")


if __name__ == "__main__":
    main()
