"""Los avisos esperados se verifican contra los datos con una regla de referencia independiente.

Regla de referencia (la misma que documenta ESPECIFICACION.md):
- Gasto: salidas por mes local, sin GMF ni envíos a personas.
- Meses previos: meses completos (que empiezan en o después del primer movimiento del usuario) anteriores
  al mes de la fecha de evaluación. Con menos de 3, no se evalúa (no dispara).
- presupuesto_anticipado: gasto total acumulado del mes hasta la fecha supera la mediana o la media de
  los meses previos.
- categoria_sobre_media: lo mismo por categoría, para las categorías con 3 o más meses previos con gasto.
"""

import statistics
import unittest
from collections import defaultdict
from datetime import date

from .util import carpeta, etiquetas_por_id, fecha_local, leer

EXCLUIDAS = {"comisiones_costos_financieros", "sin_categoria"}
FECHA_POR_DEFECTO = date(2026, 10, 21)
TIPOS = ("presupuesto_anticipado", "categoria_sobre_media")


def gasto_por_mes_y_categoria(filas, etiquetas):
    gasto = defaultdict(lambda: defaultdict(list))
    for m in filas:
        categoria = etiquetas[m["movimiento_id"]]["categoria_real"]
        if m["direccion"] == "salida" and categoria not in EXCLUIDAS:
            f = fecha_local(m["ocurrido_en"])
            gasto[(f.year, f.month)][categoria].append((f, int(m["monto_centavos"])))
    return gasto


def _centrales(mensuales):
    if len(mensuales) < 3:
        return None
    return statistics.median(mensuales), statistics.mean(mensuales)


def evaluar(filas, etiquetas, tipo, fecha):
    """(dispara_con_mediana, dispara_con_media) según la regla de referencia."""
    gasto = gasto_por_mes_y_categoria(filas, etiquetas)
    primer = min(fecha_local(m["ocurrido_en"]) for m in filas)
    mes_actual = (fecha.year, fecha.month)
    previos = [k for k in gasto if k < mes_actual and date(k[0], k[1], 1) >= primer]
    categorias = ({c for k in previos for c in gasto[k]} | {c for c in gasto[mes_actual]}
                  if tipo == "categoria_sobre_media" else {None})
    mediana = media = False
    for categoria in categorias:
        def total(k, hasta=None):
            return sum(v for c, lst in gasto[k].items() if categoria in (None, c)
                       for f, v in lst if hasta is None or f <= hasta)
        mensuales = [total(k) for k in sorted(previos) if total(k) > 0]
        centrales = _centrales(mensuales)
        if centrales is None:
            continue
        actual = total(mes_actual, fecha)
        mediana |= actual > centrales[0]
        media |= actual > centrales[1]
    return mediana, media


class PruebasAvisosContraLosDatos(unittest.TestCase):
    def setUp(self):
        self.base = carpeta()
        self.etiquetas = etiquetas_por_id(self.base)
        self.avisos = leer(self.base, "verdad/avisos_esperados.csv")
        self.por_usuario = defaultdict(list)
        for m in leer(self.base, "entrada/movimientos.csv"):
            self.por_usuario[m["usuario_id"]].append(m)

    def test_cada_aviso_de_gasto_coincide_con_la_regla_de_referencia(self):
        revisados = 0
        for a in self.avisos:
            if a["tipo"] not in TIPOS:
                continue
            fecha = date.fromisoformat(a["fecha_evaluacion"])
            esperado = (a["debe_disparar_con_mediana"] == "true", a["debe_disparar_con_media"] == "true")
            real = evaluar(self.por_usuario[a["usuario_id"]], self.etiquetas, a["tipo"], fecha)
            self.assertEqual(real, esperado, a)
            revisados += 1
        self.assertGreaterEqual(revisados, 8)

    def test_ningun_perfil_dispara_un_aviso_de_gasto_sin_etiqueta(self):
        etiquetados = {(a["usuario_id"], a["tipo"]) for a in self.avisos}
        for usuario in {a["usuario_id"] for a in self.avisos}:
            for tipo in TIPOS:
                if (usuario, tipo) in etiquetados:
                    continue
                real = evaluar(self.por_usuario[usuario], self.etiquetas, tipo, FECHA_POR_DEFECTO)
                self.assertEqual(real, (False, False), (usuario, tipo))

    def test_la_regla_de_referencia_distingue_mediana_y_media_en_el_perfil_1(self):
        p1 = next(a for a in self.avisos
                  if a["tipo"] == "categoria_sobre_media" and a["debe_disparar_con_media"] == "false"
                  and a["debe_disparar_con_mediana"] == "true")
        real = evaluar(self.por_usuario[p1["usuario_id"]], self.etiquetas, "categoria_sobre_media",
                       date.fromisoformat(p1["fecha_evaluacion"]))
        self.assertEqual(real, (True, False))


if __name__ == "__main__":
    unittest.main()
