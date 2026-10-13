"""Validacion cruzada estratificada de 5 particiones sobre los 130 comercios etiquetados.

El corte de prueba oficial tiene solo 39 filas, asi que un unico numero es ruidoso. Esta validacion
cruzada da media y desviacion sobre el conjunto etiquetado completo (91 de entrenamiento mas 39 de
prueba), con la misma particion reproducible, para comparar iteraciones con un intervalo. Los metodos
congelados (TF-IDF y MiniLM sin ajustar) corren en segundos. SetFit se activa con SETFIT_CV=1 porque
reentrena en la GPU en cada particion.
"""

from __future__ import annotations

import csv
import os
import sys
from pathlib import Path

os.environ.setdefault("TQDM_DISABLE", "1")
os.environ.setdefault("TRANSFORMERS_VERBOSITY", "error")

DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(DIR))
BASE = DIR.parents[1]
CORPUS = BASE / "salida" / "2026-3000-A" / "verdad"
sys.path.insert(0, str(BASE))

import numpy as np  # noqa: E402

from analisis.clasificador_tfidf import UMBRAL_COSENO, entrenar, predecir  # noqa: E402
from registro import registrar  # noqa: E402

K = 5
SEMILLA = 42
ITERACION = os.environ.get("ITERACION", "1")


def leer_csv(nombre: str) -> list[dict[str, str]]:
    with open(CORPUS / nombre, encoding="utf-8") as archivo:
        return list(csv.DictReader(archivo))


def particiones_estratificadas(etiquetas: list[str], k: int, semilla: int) -> list[list[int]]:
    rng = np.random.default_rng(semilla)
    por_clase: dict[str, list[int]] = {}
    for i, e in enumerate(etiquetas):
        por_clase.setdefault(e, []).append(i)
    folds: list[list[int]] = [[] for _ in range(k)]
    for _, indices in por_clase.items():
        orden = list(rng.permutation(indices))
        for pos, idx in enumerate(orden):
            folds[pos % k].append(idx)
    return folds


def cv_tfidf(filas: list[dict[str, str]], con_mcc: bool) -> tuple[float, float]:
    etiquetas = [f["categoria"] for f in filas]
    folds = particiones_estratificadas(etiquetas, K, SEMILLA)
    exactitudes = []
    for prueba_idx in folds:
        prueba = set(prueba_idx)
        train = [f for i, f in enumerate(filas) if i not in prueba]
        modelo = entrenar([{**f, "mcc_categoria": (f["mcc_categoria"] if con_mcc else None)} for f in train])
        aciertos = 0
        evaluadas = 0
        for i in prueba_idx:
            f = filas[i]
            pred = predecir(modelo, f["nombre_canonico"], f["mcc_categoria"] if con_mcc else None, UMBRAL_COSENO)
            if pred is not None:
                evaluadas += 1
                aciertos += pred == f["categoria"]
        exactitudes.append(aciertos / evaluadas if evaluadas else 0.0)
    return round(float(np.mean(exactitudes)), 4), round(float(np.std(exactitudes)), 4)


def cv_minilm(filas, embeddings, metodo) -> tuple[float, float]:
    import clasificador_minilm as cm

    etiquetas = [f["categoria"] for f in filas]
    folds = particiones_estratificadas(etiquetas, K, SEMILLA)
    exactitudes = []
    for prueba_idx in folds:
        prueba = set(prueba_idx)
        train_idx = [i for i in range(len(filas)) if i not in prueba]
        e_train = embeddings[train_idx]
        etq_train = [etiquetas[i] for i in train_idx]
        e_test = embeddings[list(prueba_idx)]
        etq_test = [etiquetas[i] for i in prueba_idx]
        if metodo == "centroide":
            cats, cent = cm.centroides(e_train, etq_train)
            preds = [cats[j] for j in (e_test @ cent.T).argmax(1)]
        else:
            cats, w = cm.entrenar_logreg(e_train, etq_train)
            preds = cm.predicciones_logreg(cats, w, e_test)
        exactitudes.append(sum(1 for p, y in zip(preds, etq_test) if p == y) / len(etq_test))
    return round(float(np.mean(exactitudes)), 4), round(float(np.std(exactitudes)), 4)


def main() -> None:
    comercios = leer_csv("comercios.csv")
    filas = [f for f in comercios if f["conjunto"] == "entrenamiento" or (f["conjunto"] == "prueba" and f["es_comercio"] == "true")]
    print(f"etiquetados: {len(filas)}")

    for nombre, con_mcc in (("TF-IDF produccion con MCC", True), ("TF-IDF nombre solo", False)):
        media, desv = cv_tfidf(filas, con_mcc)
        registrar({"iteracion": ITERACION, "clasificador": "TF-IDF", "variante": f"{nombre} (CV{K})",
                   "exactitud": media, "nota": f"CV{K} estratificada, desviacion {desv}"})
        print(f"{nombre} CV{K}: {media} +/- {desv}")

    os.environ["MINILM_ONNX"] = "onnx/model.onnx"
    import clasificador_minilm as cm

    emb = cm.Embedder()
    embeddings = emb.codificar([f["nombre_canonico"] for f in filas])
    for metodo in ("centroide", "logreg"):
        media, desv = cv_minilm(filas, embeddings, metodo)
        registrar({"iteracion": ITERACION, "clasificador": "MiniLM congelado fp32", "variante": f"raw / {metodo} (CV{K})",
                   "exactitud": media, "disco_modelo_mb": round(emb.bytes_disco / 1e6, 1),
                   "nota": f"CV{K} estratificada, desviacion {desv}"})
        print(f"MiniLM fp32 {metodo} CV{K}: {media} +/- {desv}")


if __name__ == "__main__":
    main()
