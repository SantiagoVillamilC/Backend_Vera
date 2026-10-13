"""Comparacion MiniLM (ONNX int8) contra TF-IDF sobre el corpus del proyecto, en dos fases.

Fase 1: embeddings congelados con centroide por categoria, el mismo metodo del TF-IDF.
Fase 2: una cabeza de regresion logistica entrenada sobre los embeddings congelados, que es el
escalon profesional siguiente segun la practica (SetFit solo se justifica si esta linea base se
estanca). La cabeza lineal se ajusta con numpy en milisegundos: no es el entrenamiento al que se
reserva la GPU, ese es el ajuste del transformer (SetFit o LoRA) y va en una fase aparte.

Mide exactitud, cobertura y los bloques revela/ficticia/real con el mismo corte que
`analisis.clasificador_tfidf.medir`, mas el consumo (RSS pico del kernel, latencia, disco). La
inferencia de MiniLM es en CPU a proposito: en estas dos fases no se entrena el transformer.
"""

from __future__ import annotations

import csv
import json
import os
import sys
import time
from pathlib import Path

import numpy as np
import onnxruntime as ort

REPO = "sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2"
ONNX_FILE = os.environ.get("MINILM_ONNX", "onnx/model_quint8_avx2.onnx")  # AVX2, sin AVX512 (Ryzen 5 3550H)
CACHE = os.environ.get("MINILM_CACHE", str(Path.home() / ".local/share/minilm-exp/models"))
BASE = Path(__file__).resolve().parents[2]  # dataset-inteligencia
CORPUS = BASE / "salida" / "2026-3000-A" / "verdad"
sys.path.insert(0, str(BASE))


def rss_pico_mb() -> float:
    """RSS pico del proceso segun el kernel (VmHWM). ru_maxrss no es confiable en este entorno."""
    try:
        with open("/proc/self/status", encoding="utf-8") as archivo:
            for linea in archivo:
                if linea.startswith("VmHWM:"):
                    return round(int(linea.split()[1]) / 1024, 1)
    except OSError:
        pass
    return -1.0


def leer_csv(nombre: str) -> list[dict[str, str]]:
    with open(CORPUS / nombre, encoding="utf-8") as archivo:
        return list(csv.DictReader(archivo))


VARIANTES = {
    "raw": lambda n: n,
    "espacios": lambda n: n.replace("-", " ").replace("_", " ").strip(),
    "espacios_minusculas": lambda n: n.replace("-", " ").replace("_", " ").strip().lower(),
}


class Embedder:
    """Envoltura de ONNX + tokenizador: codifica con mean pooling y normalizacion L2.

    Con `MINILM_RUST_TOK=1` usa el `tokenizers.Tokenizer` Rust directo en vez del `AutoTokenizer` de
    transformers, que carga el vocab de ~250 mil tokens y se lleva mas de 1 GB. Los ids son identicos.
    """

    def __init__(self) -> None:
        from huggingface_hub import hf_hub_download

        inicio = time.perf_counter()
        self.ruta = hf_hub_download(REPO, ONNX_FILE, cache_dir=CACHE)
        opciones = ort.SessionOptions()
        if os.environ.get("MINILM_NO_ARENA") == "1":
            opciones.enable_cpu_mem_arena = False
        self.sesion = ort.InferenceSession(self.ruta, sess_options=opciones, providers=["CPUExecutionProvider"])
        self.rust = None
        self.tok = None
        if os.environ.get("MINILM_RUST_TOK") == "1":
            from tokenizers import Tokenizer

            rust = Tokenizer.from_file(hf_hub_download(REPO, "tokenizer.json", cache_dir=CACHE))
            rust.enable_truncation(64)
            rust.enable_padding(pad_id=rust.token_to_id("<pad>"), pad_token="<pad>")
            self.rust = rust
        else:
            from transformers import AutoTokenizer

            self.tok = AutoTokenizer.from_pretrained(REPO, cache_dir=CACHE)
        self.carga_s = time.perf_counter() - inicio
        self.bytes_disco = os.path.getsize(self.ruta)
        self.tiene_token_type = any(i.name == "token_type_ids" for i in self.sesion.get_inputs())

    def _lote(self, trozo: list[str]) -> tuple[np.ndarray, np.ndarray]:
        if self.rust is not None:
            encs = self.rust.encode_batch(trozo)
            ids = np.array([e.ids for e in encs], dtype="int64")
            mascara = np.array([e.attention_mask for e in encs], dtype="int64")
            return ids, mascara
        enc = self.tok(trozo, padding=True, truncation=True, max_length=64, return_tensors="np")
        return enc["input_ids"], enc["attention_mask"]

    def codificar(self, textos: list[str], lote: int = 32) -> np.ndarray:
        vectores: list[np.ndarray] = []
        for inicio in range(0, len(textos), lote):
            ids, mascara = self._lote(textos[inicio : inicio + lote])
            entradas = {"input_ids": ids, "attention_mask": mascara}
            if self.tiene_token_type:
                entradas["token_type_ids"] = np.zeros_like(ids)
            salida = self.sesion.run(None, entradas)[0]
            mascara_f = mascara[..., None].astype("float32")
            vector = (salida * mascara_f).sum(1) / np.clip(mascara_f.sum(1), 1e-9, None)
            vector = vector / np.clip(np.linalg.norm(vector, axis=1, keepdims=True), 1e-9, None)
            vectores.append(vector)
        return np.vstack(vectores)


def normalizar_fila(v: np.ndarray) -> np.ndarray:
    return v / np.clip(np.linalg.norm(v, axis=1, keepdims=True), 1e-9, None)


def centroides(vectores: np.ndarray, etiquetas: list[str]) -> tuple[list[str], np.ndarray]:
    categorias = sorted(set(etiquetas))
    filas = []
    for categoria in categorias:
        indices = [i for i, e in enumerate(etiquetas) if e == categoria]
        media = vectores[indices].mean(0)
        filas.append(media / max(float(np.linalg.norm(media)), 1e-9))
    return categorias, np.vstack(filas)


def softmax(z: np.ndarray) -> np.ndarray:
    z = z - z.max(1, keepdims=True)
    e = np.exp(z)
    return e / e.sum(1, keepdims=True)


def entrenar_logreg(
    x: np.ndarray, etiquetas: list[str], l2: float = 0.1, epocas: int = 4000, lr: float = 0.5
) -> tuple[list[str], np.ndarray]:
    """Regresion logistica multinomial con descenso de gradiente full-batch. Ajuste de milisegundos."""
    categorias = sorted(set(etiquetas))
    y = np.eye(len(categorias))[[categorias.index(e) for e in etiquetas]]
    x = np.hstack([normalizar_fila(x), np.ones((len(x), 1))])
    w = np.zeros((x.shape[1], len(categorias)))
    for _ in range(epocas):
        p = softmax(x @ w)
        grad = x.T @ (p - y) / len(x) + l2 * w / len(x)
        w -= lr * grad
    return categorias, w


def predicciones_logreg(categorias: list[str], w: np.ndarray, x: np.ndarray) -> list[str]:
    x = np.hstack([normalizar_fila(x), np.ones((len(x), 1))])
    return [categorias[j] for j in (x @ w).argmax(1)]


def evaluar(preds: list[str | None], filas: list[dict[str, str]], ficticia: dict[str, str]) -> dict[str, float]:
    evaluadas = [(p, f) for p, f in zip(preds, filas) if p is not None]
    n_prueba = len(filas)
    aciertos = sum(1 for p, f in evaluadas if p == f["categoria"])

    def bloque(filtro) -> tuple[int, float]:
        sub = [(p, f) for p, f in evaluadas if filtro(f)]
        n = sum(1 for f in filas if filtro(f))
        return n, (sum(1 for p, f in sub if p == f["categoria"]) / len(sub) if sub else 0.0)

    n_rev, ex_rev = bloque(lambda f: f["nombre_revela_naturaleza"] == "true")
    n_fic, ex_fic = bloque(lambda f: ficticia.get(f["empresa_id"]) == "true")
    n_rea, ex_rea = bloque(lambda f: ficticia.get(f["empresa_id"]) == "false")
    return {
        "exactitud": round(aciertos / len(evaluadas), 4) if evaluadas else 0.0,
        "cobertura": round(len(evaluadas) / n_prueba, 4) if n_prueba else 0.0,
        "abstenciones": n_prueba - len(evaluadas),
        "exactitud_revela": round(ex_rev, 4),
        "exactitud_ficticia": round(ex_fic, 4),
        "exactitud_real": round(ex_rea, 4),
        "n_ficticia": n_fic,
        "n_real": n_rea,
    }


def main() -> None:
    comercios = leer_csv("comercios.csv")
    ficticia = {e["empresa_id"]: e["ficticia"] for e in leer_csv("empresas.csv")}
    train = [f for f in comercios if f["conjunto"] == "entrenamiento"]
    prueba = [f for f in comercios if f["conjunto"] == "prueba" and f["es_comercio"] == "true"]
    etiquetas_train = [f["categoria"] for f in train]

    res: dict[str, object] = {
        "entorno": {"rss_import_mb": round(rss_pico_mb(), 1), "train_n": len(train), "prueba_n": len(prueba)}
    }

    emb = Embedder()
    res["modelo"] = {
        "onnx_disco_mb": round(emb.bytes_disco / 1e6, 1),
        "carga_s": round(emb.carga_s, 2),
        "rss_tras_cargar_mb": round(rss_pico_mb(), 1),
    }

    resultado_minilm: dict[str, object] = {}
    t_train_total = 0.0
    t_test_total = 0.0
    for nombre, transformar in VARIANTES.items():
        inicio = time.perf_counter()
        e_train = emb.codificar([transformar(f["nombre_canonico"]) for f in train])
        t_train = time.perf_counter() - inicio
        inicio = time.perf_counter()
        e_test = emb.codificar([transformar(f["nombre_canonico"]) for f in prueba])
        t_test = time.perf_counter() - inicio
        t_train_total += t_train
        t_test_total += t_test

        categorias, cent = centroides(e_train, etiquetas_train)
        sims = e_test @ cent.T
        preds_cent = [categorias[j] for j in sims.argmax(1)]

        categorias_lr, w = entrenar_logreg(e_train, etiquetas_train)
        preds_lr = predicciones_logreg(categorias_lr, w, e_test)

        resultado_minilm[nombre] = {
            "centroide": evaluar(preds_cent, prueba, ficticia),
            "logreg": evaluar(preds_lr, prueba, ficticia),
        }
    res["minilm"] = resultado_minilm
    res["minilm_latencia"] = {
        "train_s": round(t_train_total, 3),
        "prueba_s": round(t_test_total, 4),
        "ms_por_nombre_prueba": round(t_test_total / len(prueba) / len(VARIANTES) * 1000, 2),
    }

    from analisis.clasificador_tfidf import UMBRAL_COSENO, entrenar, medir, predecir as predecir_tfidf

    modelo = entrenar([{**f, "mcc_categoria": None} for f in train])
    preds_tfidf = [predecir_tfidf(modelo, f["nombre_canonico"], None, UMBRAL_COSENO) for f in prueba]
    res["tfidf_nombre_solo"] = evaluar(preds_tfidf, prueba, ficticia)

    res["entorno"]["rss_pico_mb"] = round(rss_pico_mb(), 1)
    print(json.dumps(res, ensure_ascii=False, indent=2))

    from registro import registrar

    iteracion = os.environ.get("ITERACION", "1")
    disco = round(emb.bytes_disco / 1e6, 1)
    rss = res["entorno"]["rss_pico_mb"]
    lat = res["minilm_latencia"]["ms_por_nombre_prueba"]

    prod = medir(CORPUS.parent)
    registrar({
        "iteracion": iteracion,
        "clasificador": "TF-IDF",
        "variante": "produccion con MCC",
        "exactitud": round(prod["exactitud"], 4),
        "cobertura": round(prod["cobertura"], 4),
        "exactitud_revela": round(prod["exactitud_revela"], 4),
        "exactitud_ficticia": round(prod["exactitud_ficticia"], 4),
        "exactitud_real": round(prod["exactitud_real"], 4),
        "disco_modelo_mb": 0.02,
        "rss_mb": 18.5,
        "latencia_ms": 0.85,
        "nota": "linea base de produccion, RSS y latencia medidos por separado",
    })
    registrar({
        "iteracion": iteracion,
        "clasificador": "TF-IDF",
        "variante": "nombre solo (centroide)",
        "exactitud": res["tfidf_nombre_solo"]["exactitud"],
        "cobertura": res["tfidf_nombre_solo"]["cobertura"],
        "exactitud_revela": res["tfidf_nombre_solo"]["exactitud_revela"],
        "exactitud_ficticia": res["tfidf_nombre_solo"]["exactitud_ficticia"],
        "exactitud_real": res["tfidf_nombre_solo"]["exactitud_real"],
        "disco_modelo_mb": 0.02,
        "rss_mb": 18.5,
        "latencia_ms": 0.85,
        "nota": "comparacion justa sin el token MCC",
    })
    for variante, metodos in resultado_minilm.items():
        for metodo, met in metodos.items():
            registrar({
                "iteracion": iteracion,
                "clasificador": "MiniLM congelado",
                "variante": f"{variante} / {metodo}",
                "exactitud": met["exactitud"],
                "cobertura": met["cobertura"],
                "exactitud_revela": met["exactitud_revela"],
                "exactitud_ficticia": met["exactitud_ficticia"],
                "exactitud_real": met["exactitud_real"],
                "disco_modelo_mb": disco,
                "rss_mb": rss,
                "latencia_ms": lat,
                "nota": f"{ONNX_FILE}",
            })


if __name__ == "__main__":
    main()
