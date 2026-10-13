"""Ninguna fila de entrada/ puede ser posterior a --fecha-fin."""

import unittest
from datetime import date

from generador.parametros import Parametros

from .util import carpeta, leer

FECHAS = (date(2026, 10, 20), date(2026, 10, 21), date(2026, 11, 1), date(2026, 11, 2),
          date(2026, 12, 1), date(2026, 12, 31))
SEMILLAS = (2026, 1, 7)


def limite(fecha: date) -> str:
    return f"{fecha.isoformat()}T23:59:59Z"


class PruebasConsistenciaTemporal(unittest.TestCase):
    def test_el_maximo_de_entrada_no_supera_fecha_fin_para_varias_fechas_y_semillas(self):
        for fecha in FECHAS:
            for semilla in SEMILLAS:
                base = carpeta(filas=1500, semilla=semilla, fecha_fin=fecha)
                for archivo in ("entrada/movimientos.csv", "entrada/correcciones.csv"):
                    maximo = max(f["ocurrido_en"] for f in leer(base, archivo))
                    self.assertLessEqual(maximo, limite(fecha), (fecha, semilla, archivo))

    def test_con_los_parametros_por_defecto(self):
        base = carpeta(filas=3000)
        for archivo in ("entrada/movimientos.csv", "entrada/correcciones.csv"):
            self.assertLessEqual(max(f["ocurrido_en"] for f in leer(base, archivo)), limite(date(2026, 10, 21)))

    def test_el_perfil_4_sigue_llegando_al_20_de_octubre(self):
        base = carpeta(filas=3000)
        maximo = max(f["ocurrido_en"] for f in leer(base, "entrada/movimientos.csv"))
        self.assertGreaterEqual(maximo[:10], "2026-10-20")

    def test_la_fecha_fin_por_defecto_es_el_21_de_octubre(self):
        self.assertEqual(Parametros(filas=1).fecha_fin, date(2026, 10, 21))

    def test_fecha_fin_anterior_al_20_de_octubre_se_rechaza(self):
        with self.assertRaises(ValueError):
            Parametros(filas=3000, fecha_fin=date(2026, 10, 19)).validar()

    def test_ningun_aviso_se_evalua_despues_de_fecha_fin(self):
        for fecha in (date(2026, 10, 20), date(2026, 10, 21), date(2026, 12, 31)):
            base = carpeta(filas=1500, fecha_fin=fecha)
            for a in leer(base, "verdad/avisos_esperados.csv"):
                self.assertLessEqual(a["fecha_evaluacion"], fecha.isoformat(), a)

    def test_el_manifiesto_registra_la_fecha_por_defecto(self):
        import json
        base = carpeta(filas=3000)
        manifiesto = json.loads((base / "manifiesto.json").read_text(encoding="utf-8"))
        self.assertEqual(manifiesto["parametros"]["fecha_fin"], "2026-10-21")


if __name__ == "__main__":
    unittest.main()
