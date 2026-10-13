"""Iteracion 4: clasificacion zero-shot por API (item 5, la tercera pata).

El LLM no entrena: se le da el nombre del comercio y las 8 categorias y responde una. Se mide sobre
los 130 etiquetados y sobre el corte de prueba de 39, con los bloques ficticia/real/revela, igual que
`medir`. Mide tokens y latencia por llamada. Registra una fila por modelo en `resultados.csv`.

El modelo se pasa por la variable MODELO. La key vive en ~/.local/share/opencode/auth.json.
No usa GPU: no hay entrenamiento, solo inferencia por API.
"""

from __future__ import annotations

import csv
import json
import os
import sys
import time
import urllib.request
from pathlib import Path

DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(DIR))
BASE = DIR.parents[1]
CORPUS = BASE / "salida" / "2026-3000-A" / "verdad"

from registro import registrar  # noqa: E402

MODELO = os.environ.get("MODELO", "deepseek-v4.1-flash")
ITERACION = os.environ.get("ITERACION", "4")
URL = "https://amr-link.open-design.ai/v1/chat/completions"
CATEGORIAS = [
    "alimentos_mercado",
    "compras",
    "educacion",
    "entretenimiento",
    "restaurantes_domicilios",
    "salud",
    "transporte",
    "vivienda_servicios",
]

PROMPT = (
    "Eres un clasificador de comercios de una billetera digital en Colombia. Asigna el nombre del "
    "comercio a UNA de estas categorias:\n"
    + ", ".join(CATEGORIAS)
    + "\nResponde SOLO con la categoria exacta, sin explicacion ni texto extra.\n"
    "Comercio: \"{}\""
)


def leer_csv(nombre: str) -> list[dict[str, str]]:
    with open(CORPUS / nombre, encoding="utf-8") as archivo:
        return list(csv.DictReader(archivo))


def parsear(texto: str) -> str | None:
    plano = texto.strip().lower()
    for categoria in CATEGORIAS:
        if categoria in plano:
            return categoria
    return None


def clasificar(nombre: str) -> tuple[str | None, float, int]:
    clave = json.load(open(Path.home() / ".local/share/opencode/auth.json", encoding="utf-8"))["opendesign"]["key"]
    cuerpo = json.dumps({"model": MODELO, "messages": [{"role": "user", "content": PROMPT.format(nombre)}], "max_tokens": 512}).encode()
    peticion = urllib.request.Request(
        URL, data=cuerpo, headers={"Authorization": f"Bearer {clave}", "Content-Type": "application/json"}
    )
    inicio = time.perf_counter()
    try:
        with urllib.request.urlopen(peticion, timeout=120) as respuesta:
            datos = json.loads(respuesta.read())
        texto = datos["choices"][0]["message"]["content"]
        tokens = int(datos.get("usage", {}).get("total_tokens", 0))
    except Exception as error:  # noqa: BLE001
        return None, time.perf_counter() - inicio, 0
    return parsear(texto), time.perf_counter() - inicio, tokens


def main() -> None:
    comercios = leer_csv("comercios.csv")
    ficticia = {e["empresa_id"]: e["ficticia"] for e in leer_csv("empresas.csv")}
    filas = [f for f in comercios if f["conjunto"] == "entrenamiento" or (f["conjunto"] == "prueba" and f["es_comercio"] == "true")]

    preds: list[str | None] = []
    tiempos: list[float] = []
    tokens_total = 0
    for i, fila in enumerate(filas, 1):
        pred, dt, tokens = clasificar(fila["nombre_canonico"])
        preds.append(pred)
        tiempos.append(dt)
        tokens_total += tokens
        if i % 20 == 0:
            print(f"  {i}/{len(filas)}...", file=sys.stderr)

    def exactitud(subfilas: list[tuple[str | None, dict[str, str]]]) -> float:
        validas = [(p, f) for p, f in subfilas if p is not None]
        return round(sum(1 for p, f in validas if p == f["categoria"]) / len(validas), 4) if validas else 0.0

    todo = list(zip(preds, filas))
    prueba = [(p, f) for p, f in todo if f["conjunto"] == "prueba"]
    bloque = lambda pred: exactitud([(p, f) for p, f in todo if pred(f)])
    resultado = {
        "modelo": MODELO,
        "n": len(filas),
        "exactitud_130": exactitud(todo),
        "exactitud_39_prueba": exactitud(prueba),
        "abstenciones": sum(1 for p in preds if p is None),
        "revela": round(bloque(lambda f: f["nombre_revela_naturaleza"] == "true"), 4),
        "ficticia": round(bloque(lambda f: ficticia.get(f["empresa_id"]) == "true"), 4),
        "real": round(bloque(lambda f: ficticia.get(f["empresa_id"]) == "false"), 4),
        "tokens_total": tokens_total,
        "tokens_por_llamada": round(tokens_total / len(filas), 1),
        "latencia_ms": round(sum(tiempos) / len(tiempos) * 1000, 1),
    }
    registrar({
        "iteracion": ITERACION,
        "clasificador": "LLM API",
        "variante": MODELO,
        "exactitud": resultado["exactitud_130"],
        "cobertura": round((len(filas) - resultado["abstenciones"]) / len(filas), 4),
        "exactitud_revela": resultado["revela"],
        "exactitud_ficticia": resultado["ficticia"],
        "exactitud_real": resultado["real"],
        "latencia_ms": resultado["latencia_ms"],
        "nota": f"zero-shot sobre 130; 39 de prueba {resultado['exactitud_39_prueba']}; {resultado['tokens_por_llamada']} tokens/llamada",
    })
    print(json.dumps(resultado, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
