"""Pruebas del clasificador TF-IDF de categoria por nombre y MCC de comercio, con casos a mano.

El clasificador vectoriza sobre el texto ya limpio de genericos legales: combina tokens de palabra
(`w:`) con trigramas de caracter (`c:`) y aplica tf sublineal `1 + log(tf)`. Estas pruebas comprueban
que reutiliza ese vectorizador de `analisis.similitud` (`ajustar_idf_ampliado` y
`vector_tfidf_ampliado`), que `vector_tfidf` (trigramas puros, sin tf sublineal) sigue intacto, que el
MCC entra al texto del vector como `mcc:<codigo>`, que la medicion sobre un corpus minimo da
numeros calculables a mano, y que el lexico semilla refuerza un coseno debil sin sobreescribir uno
fuerte.
"""

from __future__ import annotations

import csv
import json
import math
import tempfile
import unittest
from collections import Counter
from pathlib import Path

from analisis.clasificador_tfidf import (
    LEXICO_SEMILLA,
    UMBRAL_COSENO_FUERTE,
    UMBRAL_JW_LEXICO,
    cargar_lexico,
    entrenar,
    medir,
    predecir,
)
from analisis.similitud import (
    ajustar_idf,
    ajustar_idf_ampliado,
    coseno_tfidf,
    coseno_vectores,
    jaro_winkler,
    palabras,
    quitar_genericos,
    trigramas,
    vector_tfidf,
    vector_tfidf_ampliado,
)

FILAS = [
    {"nombre_canonico": "MERCADO-UNO", "categoria": "alimentos"},
    {"nombre_canonico": "FARMACIA-DOS", "categoria": "salud"},
]

CAMPOS_COMERCIOS = (
    "llave_destino", "clase_llave", "empresa_id", "nombre_canonico", "categoria", "mcc_categoria",
    "es_comercio", "nombre_revela_naturaleza", "conjunto",
)


def _vector_manual(nombre: str, idf) -> dict[str, float]:
    """Vector TF-IDF de trigramas puros armado con `trigramas` e `idf`: lo de `vector_tfidf`."""
    maximos = max(idf.values(), default=1.0)
    return {gram: tf * idf.get(gram, maximos) for gram, tf in Counter(trigramas(nombre)).items()}


def _vector_ampliado_manual(nombre: str, idf) -> dict[str, float]:
    """Vector de palabras mas trigramas con tf sublineal, armado con las piezas del modulo."""
    maximos = max(idf.values(), default=1.0)
    conteo: Counter[str] = Counter(f"w:{p}" for p in palabras(nombre))
    conteo.update(f"c:{g}" for g in trigramas(quitar_genericos(nombre)))
    return {g: (1.0 + math.log(tf)) * idf.get(g, maximos) for g, tf in conteo.items()}


def _texto_manual(nombre: str, mcc: str | None = None) -> str:
    """Texto del vector: el nombre mas el token `mcc:<codigo>` cuando hay MCC."""
    return f"{nombre} mcc:{mcc}" if mcc else nombre


def _escribir_corpus_minimo(base: Path) -> Path:
    """Corpus diminuto con dos categorias separables y una fila deliberadamente errada."""
    verdad = base / "verdad"
    verdad.mkdir(parents=True)
    with open(verdad / "empresas.csv", "w", newline="", encoding="utf-8") as f:
        escritor = csv.writer(f)
        escritor.writerow(("empresa_id", "razon_social", "ficticia"))
        escritor.writerow(("E1", "Salud Ficticia", "true"))
        escritor.writerow(("E2", "Alimentos Real", "false"))
    filas = [
        ("001", "comercio", "E1", "MERCADO-UNO", "alimentos", "5411", "true", "true", "entrenamiento"),
        ("002", "comercio", "E2", "FARMACIA-DOS", "salud", "5912", "true", "false", "entrenamiento"),
        ("003", "comercio", "E1", "MERCADO-UNO", "alimentos", "5411", "true", "true", "prueba"),
        ("004", "comercio", "E2", "FARMACIA-DOS", "salud", "5912", "true", "false", "prueba"),
        ("005", "comercio", "E1", "TIENDA-RARA", "salud", "5912", "true", "false", "prueba"),
        ("006", "comercio", "E2", "SIN-CATEGORIA", "sin_categoria", "0000", "false", "false", ""),
    ]
    with open(verdad / "comercios.csv", "w", newline="", encoding="utf-8") as f:
        escritor = csv.writer(f)
        escritor.writerow(CAMPOS_COMERCIOS)
        escritor.writerows(filas)
    return base


class VectorizadorReutilizadoTest(unittest.TestCase):
    def test_vector_tfidf_reproduce_el_calculo_manual(self) -> None:
        corpus = ("FARMACIA-CENTRAL", "MERCADO-CENTRAL")
        idf = ajustar_idf(corpus)
        self.assertEqual(vector_tfidf("FARMACIA-CENTRAL", idf), _vector_manual("FARMACIA-CENTRAL", idf))

    def test_coseno_tfidf_delega_en_el_coseno_de_vectores(self) -> None:
        idf = ajustar_idf(("FARMACIA-CENTRAL", "MERCADO-CENTRAL"))
        a, b = "FARMACIA-CENTRAL", "MERCADO-CENTRAL"
        esperado = coseno_vectores(vector_tfidf(a, idf), vector_tfidf(b, idf))
        self.assertAlmostEqual(coseno_tfidf(a, b, idf), esperado)

    def test_vector_tfidf_vacio_sin_trigramas(self) -> None:
        self.assertEqual(vector_tfidf("", ajustar_idf(("A", "B"))), {})

    def test_vector_tfidf_ampliado_reproduce_el_calculo_manual(self) -> None:
        corpus = ("FARMACIA-CENTRAL", "MERCADO-CENTRAL")
        idf = ajustar_idf_ampliado(corpus)
        self.assertEqual(vector_tfidf_ampliado("FARMACIA-CENTRAL", idf),
                         _vector_ampliado_manual("FARMACIA-CENTRAL", idf))

    def test_vector_tfidf_ampliado_vacio_sin_caracteristicas(self) -> None:
        self.assertEqual(vector_tfidf_ampliado("", ajustar_idf_ampliado(("A", "B"))), {})


class VectorizadorAmpliadoTest(unittest.TestCase):
    """Casos a mano para el tf sublineal y el descarte de sufijos y genericos legales."""

    def test_tf_sublineal_una_aparicion_pesa_uno(self) -> None:
        # Solo la palabra GAS tiene idf propio, y el resto de caracteristicas usa el idf maximo.
        idf = {"w:GAS": 1.0}
        self.assertAlmostEqual(vector_tfidf_ampliado("GAS", idf)["w:GAS"], 1.0)

    def test_tf_sublineal_dos_apariciones_pesan_uno_mas_log_dos(self) -> None:
        idf = {"w:GAS": 1.0}
        # "GAS GAS": la palabra aparece dos veces, su tf sublineal es 1 + log(2).
        vector = vector_tfidf_ampliado("GAS GAS", idf)
        self.assertAlmostEqual(vector["w:GAS"], 1.0 + math.log(2))

    def test_tf_sublineal_amortigua_la_repeticion_frente_al_tf_crudo(self) -> None:
        # Con tf crudo una repeticion duplicaria el peso, y con sublineal crece menos que el doble.
        idf = {"w:GAS": 1.0}
        con_sublineal = vector_tfidf_ampliado("GAS GAS", idf)["w:GAS"]
        self.assertLess(con_sublineal, 2.0)

    def test_palabras_descarta_sufijos_y_genericos(self) -> None:
        self.assertEqual(palabras("ALMACEN EL SOL SAS LTDA"), ("EL", "SOL"))
        self.assertEqual(palabras("DROGUERIA LA 80 SUCURSAL"), ("DROGUERIA", "LA", "80"))

    def test_palabras_conserva_la_palabra_que_contiene_un_generico(self) -> None:
        # TIENDAS y ALMACENES contienen un generico como subcadena, pero no son esa palabra.
        self.assertEqual(palabras("TIENDAS ALMACENES SA"), ("TIENDAS", "ALMACENES"))

    def test_quitar_genericos_no_corta_subcadenas(self) -> None:
        self.assertEqual(quitar_genericos("ALMACENES SA"), "ALMACENES")
        self.assertEqual(quitar_genericos("TIENDA  LA  80"), "LA 80")

    def test_las_caracteristicas_prefijan_palabras_y_trigramas(self) -> None:
        caracteristicas = vector_tfidf_ampliado("MERCADO UNO", {})
        self.assertIn("w:MERCADO", caracteristicas)
        self.assertIn("c:MER", caracteristicas)
        # Son espacios de nombres distintos: la palabra MERCADO no colisiona con sus trigramas.
        self.assertNotIn("MERCADO", caracteristicas)


class EntrenarPredecirTest(unittest.TestCase):
    def test_el_idf_del_modelo_es_el_de_los_nombres_de_entrenamiento(self) -> None:
        modelo = entrenar(FILAS)
        esperado = ajustar_idf_ampliado([f["nombre_canonico"] for f in FILAS])
        self.assertEqual(dict(modelo.idf), dict(esperado))

    def test_el_centroide_es_el_promedio_de_los_vectores(self) -> None:
        modelo = entrenar(FILAS)
        self.assertEqual(set(modelo.centroides), {"alimentos", "salud"})
        idf = ajustar_idf_ampliado([f["nombre_canonico"] for f in FILAS])
        for categoria, nombre in (("alimentos", "MERCADO-UNO"), ("salud", "FARMACIA-DOS")):
            esperado = _vector_ampliado_manual(nombre, idf)
            obtenido = modelo.centroides[categoria]
            self.assertEqual(set(obtenido), set(esperado))
            for gram, peso in esperado.items():
                self.assertAlmostEqual(obtenido[gram], peso)

    def test_predecir_acierta_los_nombres_de_entrenamiento(self) -> None:
        modelo = entrenar(FILAS)
        self.assertEqual(predecir(modelo, "MERCADO-UNO"), "alimentos")
        self.assertEqual(predecir(modelo, "FARMACIA-DOS"), "salud")

    def test_predecir_sigue_el_prefijo_compartido(self) -> None:
        filas = [
            {"nombre_canonico": "TIENDAS-D1-SUBA", "categoria": "alimentos"},
            {"nombre_canonico": "FARMACIA-CENTRAL", "categoria": "salud"},
        ]
        modelo = entrenar(filas)
        self.assertEqual(predecir(modelo, "TIENDAS-D1-BOSA"), "alimentos")

    def test_nombre_vacio_no_produce_vector(self) -> None:
        self.assertIsNone(predecir(entrenar(FILAS), ""))

    def test_modelo_sin_categorias_da_none(self) -> None:
        self.assertIsNone(predecir(entrenar([]), "MERCADO-UNO"))

    def test_determinista(self) -> None:
        self.assertEqual(predecir(entrenar(FILAS), "MERCADO-UNO"),
                         predecir(entrenar(FILAS), "MERCADO-UNO"))


class AbstencionTest(unittest.TestCase):
    """`predecir` se abstiene (None) cuando el mejor coseno queda bajo el umbral."""

    @staticmethod
    def _mejor_coseno(modelo, nombre: str, mcc: str | None = None) -> float:
        """Coseno del centroide mas cercano, calculado a mano con las piezas del modulo."""
        vector = vector_tfidf_ampliado(_texto_manual(nombre, mcc), modelo.idf)
        return max((coseno_vectores(vector, centroide) for centroide in modelo.centroides.values()),
                   default=0.0)

    def test_predecir_se_abstiene_bajo_el_umbral(self) -> None:
        modelo = entrenar(FILAS)
        nombre, umbral = "XILOFONO-ZZZ", 0.5
        self.assertLess(self._mejor_coseno(modelo, nombre), umbral)
        self.assertIsNone(predecir(modelo, nombre, umbral=umbral))

    def test_predecir_predice_por_encima_del_umbral(self) -> None:
        modelo = entrenar(FILAS)
        umbral = 0.5
        self.assertGreaterEqual(self._mejor_coseno(modelo, "MERCADO-UNO"), umbral)
        self.assertEqual(predecir(modelo, "MERCADO-UNO", umbral=umbral), "alimentos")

    def test_umbral_por_defecto_no_abstiene_una_prediccion_clara(self) -> None:
        self.assertEqual(predecir(entrenar(FILAS), "MERCADO-UNO"), "alimentos")


class MccTest(unittest.TestCase):
    """El MCC de la fila entra al texto del vector como un token propio `mcc:<codigo>`."""

    CON_MCC = [
        {"nombre_canonico": "COMERCIO", "categoria": "alimentos", "mcc_categoria": "5411"},
        {"nombre_canonico": "COMERCIO", "categoria": "salud", "mcc_categoria": "5912"},
    ]

    def test_el_idf_usa_el_texto_con_mcc(self) -> None:
        modelo = entrenar(self.CON_MCC)
        esperado = ajustar_idf_ampliado(
            [_texto_manual("COMERCIO", "5411"), _texto_manual("COMERCIO", "5912")]
        )
        self.assertEqual(dict(modelo.idf), dict(esperado))

    def test_el_centroide_promedia_el_texto_con_mcc(self) -> None:
        modelo = entrenar(self.CON_MCC)
        idf = ajustar_idf_ampliado(
            [_texto_manual("COMERCIO", "5411"), _texto_manual("COMERCIO", "5912")]
        )
        for categoria, mcc in (("alimentos", "5411"), ("salud", "5912")):
            esperado = _vector_ampliado_manual(_texto_manual("COMERCIO", mcc), idf)
            obtenido = modelo.centroides[categoria]
            self.assertEqual(set(obtenido), set(esperado))
            for gram, peso in esperado.items():
                self.assertAlmostEqual(obtenido[gram], peso)

    def test_predecir_usa_el_mcc_para_desempatar_nombres_iguales(self) -> None:
        modelo = entrenar(self.CON_MCC)
        self.assertEqual(predecir(modelo, "COMERCIO", "5411"), "alimentos")
        self.assertEqual(predecir(modelo, "COMERCIO", "5912"), "salud")

    def test_fila_sin_el_campo_mcc_entrena_y_predice(self) -> None:
        # FILAS no trae la clave `mcc_categoria`: el texto del vector es solo el nombre.
        modelo = entrenar(FILAS)
        self.assertEqual(dict(modelo.idf),
                         dict(ajustar_idf_ampliado([f["nombre_canonico"] for f in FILAS])))
        self.assertEqual(predecir(modelo, "MERCADO-UNO"), "alimentos")

    def test_mcc_vacio_equivale_a_sin_mcc(self) -> None:
        modelo = entrenar(FILAS)
        self.assertEqual(predecir(modelo, "MERCADO-UNO", ""), predecir(modelo, "MERCADO-UNO"))


class MedirTest(unittest.TestCase):
    def setUp(self) -> None:
        self._temporal = tempfile.TemporaryDirectory()
        self.corpus = _escribir_corpus_minimo(Path(self._temporal.name) / "corrida")

    def tearDown(self) -> None:
        self._temporal.cleanup()

    def test_medir_sobre_corpus_minimo(self) -> None:
        # `medir` pasa el `mcc_categoria` de cada fila: con el, TIENDA-RARA (MCC 5912) cae en salud,
        # que es la categoria de FARMACIA-DOS, y el corpus queda separable.
        resultado = medir(self.corpus)
        self.assertEqual(resultado["n_prueba"], 3)
        self.assertAlmostEqual(resultado["exactitud"], 1.0)
        self.assertEqual(resultado["abstenciones"], 0)
        self.assertAlmostEqual(resultado["cobertura"], 1.0)
        self.assertEqual(resultado["n_revela"], 1)
        self.assertAlmostEqual(resultado["exactitud_revela"], 1.0)
        self.assertEqual(resultado["n_ficticia"], 2)
        self.assertAlmostEqual(resultado["exactitud_ficticia"], 1.0)
        self.assertEqual(resultado["n_real"], 1)
        self.assertAlmostEqual(resultado["exactitud_real"], 1.0)

    def test_medir_reporta_cobertura_y_abstenciones_con_umbral_alto(self) -> None:
        # TIENDA-RARA tiene un coseno bajo (nombre sin prefijo compartido con el entrenamiento): con
        # un umbral alto se abstiene, la cobertura baja y la exactitud se calcula sobre lo no abstenido.
        resultado = medir(self.corpus, umbral=0.5)
        self.assertEqual(resultado["n_prueba"], 3)
        self.assertEqual(resultado["abstenciones"], 1)
        self.assertAlmostEqual(resultado["cobertura"], 2 / 3)
        self.assertAlmostEqual(resultado["exactitud"], 1.0)


class LexicoSemillaTest(unittest.TestCase):
    """El lexico semilla refuerza un coseno debil, sin sobreescribir uno fuerte."""

    # Dos categorias separables cuyos nombres no comparten raiz con las semillas que se probaran.
    FILAS_DEBIL = [
        {"nombre_canonico": "CONSULTORIO-ANDES", "categoria": "salud"},
        {"nombre_canonico": "ABASTOS-LA-80", "categoria": "alimentos_mercado"},
    ]

    @staticmethod
    def _mejor_coseno(modelo, nombre: str, mcc: str | None = None) -> float:
        """Coseno del centroide mas cercano, con las piezas del modulo."""
        vector = vector_tfidf_ampliado(_texto_manual(nombre, mcc), modelo.idf)
        return max((coseno_vectores(vector, centroide) for centroide in modelo.centroides.values()),
                   default=0.0)

    def test_el_lexico_es_dato_editable_con_las_semillas_pedidas(self) -> None:
        self.assertEqual(LEXICO_SEMILLA["FARMACIA"], "salud")
        self.assertEqual(LEXICO_SEMILLA["MERCADO"], "alimentos_mercado")
        self.assertEqual(LEXICO_SEMILLA["RESTAURANTE"], "restaurantes_domicilios")
        self.assertEqual(LEXICO_SEMILLA["UNIVERSIDAD"], "educacion")

    def test_una_palabra_del_lexico_rescata_un_coseno_debil(self) -> None:
        # FARMACIA no comparte caracteristicas con el entrenamiento: el coseno es 0.0 y sin el lexico
        # el clasificador se abstendria. La semilla FARMACIA -> salud lo rescata.
        modelo = entrenar(self.FILAS_DEBIL)
        nombre = "FARMACIA-NUEVA"
        self.assertLess(self._mejor_coseno(modelo, nombre), UMBRAL_COSENO_FUERTE)
        self.assertEqual(predecir(modelo, nombre), "salud")

    def test_jaro_winkler_acepta_la_variante_en_plural(self) -> None:
        # FARMACIAS no es la semilla literal, pero Jaro-Winkler con FARMACIA supera el umbral.
        modelo = entrenar(self.FILAS_DEBIL)
        self.assertGreaterEqual(jaro_winkler("FARMACIAS", "FARMACIA"), UMBRAL_JW_LEXICO)
        self.assertLess(self._mejor_coseno(modelo, "FARMACIAS-DEL-NORTE"), UMBRAL_COSENO_FUERTE)
        self.assertEqual(predecir(modelo, "FARMACIAS-DEL-NORTE"), "salud")

    def test_una_semilla_sin_categoria_en_el_modelo_no_predice(self) -> None:
        # DROGUERIA es semilla de salud, pero el modelo no tiene esa categoria: el lexico no la inventa.
        modelo = entrenar([{"nombre_canonico": "ABASTOS-LA-80", "categoria": "alimentos_mercado"}])
        self.assertIsNone(predecir(modelo, "DROGUERIA-NUEVA"))

    def test_un_coseno_alto_no_lo_sobreescribe_el_lexico(self) -> None:
        # MERCADO-CENTRAL es identico al unico texto de entrenamiento de compras: coseno 1.0. Aunque
        # MERCADO sea semilla de alimentos_mercado (categoria presente en el modelo), el coseno alto
        # manda y el lexico no sobreescribe.
        modelo = entrenar([
            {"nombre_canonico": "MERCADO-CENTRAL", "categoria": "compras"},
            {"nombre_canonico": "PLAZA-ABC", "categoria": "alimentos_mercado"},
        ])
        nombre = "MERCADO-CENTRAL"
        self.assertGreaterEqual(self._mejor_coseno(modelo, nombre), UMBRAL_COSENO_FUERTE)
        self.assertEqual(predecir(modelo, nombre), "compras")


class LexicoConfiguracionTest(unittest.TestCase):
    """El lexico es dato editable tambien desde un archivo JSON, sin tocar la logica."""

    FILAS_DEBIL = [
        {"nombre_canonico": "CONSULTORIO-ANDES", "categoria": "salud"},
        {"nombre_canonico": "ABASTOS-LA-80", "categoria": "alimentos_mercado"},
    ]

    @staticmethod
    def _mejor_coseno(modelo, nombre: str, mcc: str | None = None) -> float:
        """Coseno del centroide mas cercano, con las piezas del modulo."""
        vector = vector_tfidf_ampliado(_texto_manual(nombre, mcc), modelo.idf)
        return max((coseno_vectores(vector, centroide) for centroide in modelo.centroides.values()),
                   default=0.0)

    def test_cargar_lexico_sin_ruta_devuelve_el_builtin(self) -> None:
        self.assertEqual(dict(cargar_lexico()), dict(LEXICO_SEMILLA))

    def test_cargar_lexico_con_ruta_ausente_devuelve_el_builtin(self) -> None:
        with tempfile.TemporaryDirectory() as temporal:
            ausente = Path(temporal) / "no-existe.json"
            self.assertEqual(dict(cargar_lexico(ausente)), dict(LEXICO_SEMILLA))

    def test_cargar_lexico_extiende_el_builtin_desde_un_archivo_json(self) -> None:
        with tempfile.TemporaryDirectory() as temporal:
            ruta = Path(temporal) / "lexico.json"
            ruta.write_text(json.dumps({"verduleria": "alimentos_mercado"}), encoding="utf-8")
            lexico = cargar_lexico(ruta)
        # La semilla nueva entra normalizada a mayusculas y el built-in se conserva.
        self.assertEqual(lexico["VERDULERIA"], "alimentos_mercado")
        self.assertEqual(lexico["FARMACIA"], "salud")

    def test_el_lexico_de_configuracion_rescata_un_coseno_debil(self) -> None:
        # VERDULERIA no esta en el built-in ni se parece a ninguna semilla suya: solo el lexico del
        # archivo la puede rescatar.
        modelo = entrenar(self.FILAS_DEBIL)
        nombre = "VERDULERIA-NUEVA"
        self.assertLess(self._mejor_coseno(modelo, nombre), UMBRAL_COSENO_FUERTE)
        self.assertNotEqual(predecir(modelo, nombre), "alimentos_mercado")
        with tempfile.TemporaryDirectory() as temporal:
            ruta = Path(temporal) / "lexico.json"
            ruta.write_text(json.dumps({"verduleria": "alimentos_mercado"}), encoding="utf-8")
            lexico = cargar_lexico(ruta)
        self.assertEqual(predecir(modelo, nombre, lexico=lexico), "alimentos_mercado")

    def test_el_lexico_de_configuracion_no_sobreescribe_un_coseno_alto(self) -> None:
        # MERCADO-CENTRAL tiene coseno 1.0 con compras: ni un lexico de configuracion que lo mande a
        # alimentos_mercado lo sobreescribe.
        modelo = entrenar([
            {"nombre_canonico": "MERCADO-CENTRAL", "categoria": "compras"},
            {"nombre_canonico": "PLAZA-ABC", "categoria": "alimentos_mercado"},
        ])
        with tempfile.TemporaryDirectory() as temporal:
            ruta = Path(temporal) / "lexico.json"
            ruta.write_text(json.dumps({"mercado": "alimentos_mercado"}), encoding="utf-8")
            lexico = cargar_lexico(ruta)
        nombre = "MERCADO-CENTRAL"
        self.assertGreaterEqual(self._mejor_coseno(modelo, nombre), UMBRAL_COSENO_FUERTE)
        self.assertEqual(predecir(modelo, nombre, lexico=lexico), "compras")


if __name__ == "__main__":
    unittest.main()
