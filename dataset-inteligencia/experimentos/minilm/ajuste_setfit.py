"""Fase 3: ajuste del sentence-transformer en la GPU con SetFit.

SetFit ajusta el cuerpo del sentence-transformer de forma contrastiva (pares de la misma categoria y
de categorias distintas) y despues entrena una cabeza logistica sobre los embeddings congelados. Es
el metodo recomendado con pocos ejemplos. Entrena en la GPU NVIDIA, que es la regla de la casa: la
CPU solo se usa para inferir en las fases 1 y 2. El modelo entrenado se cachea en disco para no
reentrenar en cada iteracion. Mide con el mismo corte que `analisis.clasificador_tfidf.medir` y
registra la fila en `resultados.csv`.
"""

from __future__ import annotations

import csv
import json
import os
import time
from pathlib import Path

os.environ.setdefault("TQDM_DISABLE", "1")
os.environ.setdefault("HF_HUB_DISABLE_PROGRESS_BARS", "1")
os.environ.setdefault("TRANSFORMERS_VERBOSITY", "error")

BASE = Path(__file__).resolve().parents[2]
CORPUS = BASE / "salida" / "2026-3000-A" / "verdad"
CACHE = os.environ.get("MINILM_CACHE", str(Path.home() / ".local/share/minilm-exp/models"))
MODELO_DIR = Path(os.environ.get("SETFIT_DIR", str(Path.home() / ".local/share/minilm-exp/setfit-model")))
BASE_MODEL = "sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2"
ITERACION = os.environ.get("ITERACION", "1")

from registro import registrar  # noqa: E402


def leer_csv(nombre: str) -> list[dict[str, str]]:
    with open(CORPUS / nombre, encoding="utf-8") as archivo:
        return list(csv.DictReader(archivo))


def evaluar(preds: list[str], filas: list[dict[str, str]], ficticia: dict[str, str]) -> dict[str, float]:
    aciertos = sum(1 for p, f in zip(preds, filas) if p == f["categoria"])

    def bloque(filtro) -> tuple[int, float]:
        sub = [(p, f) for p, f in zip(preds, filas) if filtro(f)]
        n = sum(1 for f in filas if filtro(f))
        return n, (sum(1 for p, f in sub if p == f["categoria"]) / len(sub) if sub else 0.0)

    n_rev, ex_rev = bloque(lambda f: f["nombre_revela_naturaleza"] == "true")
    n_fic, ex_fic = bloque(lambda f: ficticia.get(f["empresa_id"]) == "true")
    n_rea, ex_rea = bloque(lambda f: ficticia.get(f["empresa_id"]) == "false")
    return {
        "exactitud": round(aciertos / len(filas), 4),
        "cobertura": 1.0,
        "exactitud_revela": round(ex_rev, 4),
        "exactitud_ficticia": round(ex_fic, 4),
        "exactitud_real": round(ex_rea, 4),
        "n_ficticia": n_fic,
        "n_real": n_rea,
    }


def tamano_mb(ruta: Path) -> float:
    return round(sum(p.stat().st_size for p in ruta.rglob("*") if p.is_file()) / 1e6, 1)


def main() -> None:
    import torch
    from datasets import Dataset
    from setfit import SetFitModel, Trainer, TrainingArguments

    assert torch.cuda.is_available(), "la fase 3 exige GPU"

    comercios = leer_csv("comercios.csv")
    ficticia = {e["empresa_id"]: e["ficticia"] for e in leer_csv("empresas.csv")}
    train = [f for f in comercios if f["conjunto"] == "entrenamiento"]
    prueba = [f for f in comercios if f["conjunto"] == "prueba" and f["es_comercio"] == "true"]
    categoria_ord = sorted({f["categoria"] for f in train})

    torch.cuda.reset_peak_memory_stats()
    reusado = MODELO_DIR.exists()
    inicio = time.perf_counter()
    if reusado:
        modelo = SetFitModel.from_pretrained(str(MODELO_DIR))
        tiempo_train = 0.0
    else:
        modelo = SetFitModel.from_pretrained(BASE_MODEL, labels=categoria_ord, cache_folder=CACHE)
        ds = Dataset.from_dict(
            {"text": [f["nombre_canonico"] for f in train], "label": [categoria_ord.index(f["categoria"]) for f in train]}
        )
        args = TrainingArguments(batch_size=8, num_epochs=2, num_iterations=20, seed=42, show_progress_bar=False)
        Trainer(model=modelo, train_dataset=ds, args=args).train()
        tiempo_train = time.perf_counter() - inicio
        MODELO_DIR.parent.mkdir(parents=True, exist_ok=True)
        modelo.save_pretrained(str(MODELO_DIR))
    carga_total_s = time.perf_counter() - inicio
    vram_pico_mb = round(torch.cuda.max_memory_allocated() / 1024**2, 1)

    preds = [str(x) for x in modelo.predict([f["nombre_canonico"] for f in prueba])]
    metricas = evaluar(preds, prueba, ficticia)

    fila = {
        "iteracion": ITERACION,
        "clasificador": "MiniLM SetFit",
        "variante": "modelo reusado" if reusado else "fine-tune GPU",
        "disco_modelo_mb": tamano_mb(MODELO_DIR),
        "rss_mb": "",
        "latencia_ms": "",
        "entrenamiento_s": round(tiempo_train, 1),
        "nota": f"GPU {torch.cuda.get_device_name(0)}, VRAM pico {vram_pico_mb} MB, carga {round(carga_total_s,1)} s",
        **{k: v for k, v in metricas.items() if not k.startswith("n_")},
    }
    registrar(fila)
    print(json.dumps({"reusado": reusado, "tiempo_train_s": round(tiempo_train, 1), "vram_pico_mb": vram_pico_mb, "metricas": metricas}, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
