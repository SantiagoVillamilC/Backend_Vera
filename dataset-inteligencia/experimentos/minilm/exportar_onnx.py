"""Iteracion 3: exporta el cuerpo del SetFit ajustado a ONNX int8 y guarda la cabeza.

Exporta el transformer ya ajustado (no el base) a ONNX y lo cuantiza dinamicamente a int8 para AVX2,
y guarda los pesos de la cabeza logistica en un npz. Asi la evaluacion de la iteracion 3 corre sin
torch: ONNX + tokenizador Rust + la cabeza en numpy. Mide el disco de cada artefacto.
"""

from __future__ import annotations

import os
from pathlib import Path

os.environ.setdefault("TQDM_DISABLE", "1")
os.environ.setdefault("TRANSFORMERS_VERBOSITY", "error")

SETFIT_DIR = Path(os.environ.get("SETFIT_DIR", str(Path.home() / ".local/share/minilm-exp/setfit-model")))
OUT = Path(os.environ.get("ONNX_DIR", str(Path.home() / ".local/share/minilm-exp/setfit-onnx")))
OUT.mkdir(parents=True, exist_ok=True)


def main() -> None:
    import numpy as np
    import torch
    import torch.nn as nn
    from onnxruntime.quantization import QuantType, quantize_dynamic
    from setfit import SetFitModel

    modelo = SetFitModel.from_pretrained(str(SETFIT_DIR))
    body = modelo.model_body
    modulo = body[0]
    auto = getattr(modulo, "auto_model", None) or getattr(modulo, "model", None)
    head = modelo.model_head
    print("body[0]:", type(modulo).__name__, "| auto:", type(auto).__name__)
    print("head:", type(head).__name__)

    class Enc(nn.Module):
        def __init__(self, auto):
            super().__init__()
            self.auto = auto

        def forward(self, input_ids, attention_mask, token_type_ids):
            salida = self.auto(input_ids=input_ids, attention_mask=attention_mask, token_type_ids=token_type_ids)
            return salida.last_hidden_state

    enc = Enc(auto).eval().cpu()
    ids = torch.ones((1, 16), dtype=torch.long)
    mask = torch.ones((1, 16), dtype=torch.long)
    tt = torch.zeros((1, 16), dtype=torch.long)
    fp32 = OUT / "model_fp32.onnx"
    torch.onnx.export(
        enc,
        (ids, mask, tt),
        str(fp32),
        input_names=["input_ids", "attention_mask", "token_type_ids"],
        output_names=["last_hidden_state"],
        dynamic_axes={
            "input_ids": {0: "b", 1: "s"},
            "attention_mask": {0: "b", 1: "s"},
            "token_type_ids": {0: "b", 1: "s"},
            "last_hidden_state": {0: "b", 1: "s"},
        },
        opset_version=17,
        dynamo=False,
    )
    int8 = OUT / "model_int8.onnx"
    quantize_dynamic(str(fp32), str(int8), weight_type=QuantType.QUInt8)

    coef = getattr(head, "coef_", None)
    intercept = getattr(head, "intercept_", None)
    etiquetas = getattr(modelo, "labels", None)
    if coef is not None and intercept is not None and etiquetas is not None:
        # La cabeza predice indices enteros; se traducen a las categorias con las etiquetas del modelo.
        mapa = np.array([etiquetas[int(c)] for c in head.classes_])
        np.savez(
            OUT / "head.npz",
            coef=np.asarray(coef, dtype="float32"),
            intercept=np.asarray(intercept, dtype="float32"),
            classes=mapa,
        )
        print("cabeza guardada, coef:", np.asarray(coef).shape, "| etiquetas:", list(mapa))
    else:
        print("la cabeza no expone coef_/intercept_/etiquetas; habra que reajustarla")

    print("fp32 MB:", round(fp32.stat().st_size / 1e6, 1), "| int8 MB:", round(int8.stat().st_size / 1e6, 1))


if __name__ == "__main__":
    main()
