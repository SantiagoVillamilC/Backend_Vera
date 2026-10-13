"""Pruebas de las medidas de exposición latente y fusión falsa, con verdad escrita a mano."""

import unittest
from datetime import datetime, timezone

from analisis.convergencia import Replicador
from analisis.convergencia_agrupacion import crear_agrupador
from analisis.convergencia_agrupacion_medidas import medidas_de_agrupacion
from analisis.convergencia_config import Politica

from .util_analisis import fila
from .util_convergencia import corr, t

LLAVE = "0011111111"
CORTE = datetime(2026, 12, 31, tzinfo=timezone.utc)
HISTORIAL = {LLAVE: [{"nombre_canonico": "TIENDA-UNO", "categoria": "compras", "vigente_desde": "2026-01-01",
                      "vigente_hasta": ""}]}


def correr(filas: list[dict], metodo: str, umbral: float | None, ruido: dict | None = None) -> dict:
    movimientos = [fila(1, "x", t("01-01"), 1, LLAVE, tipo_llave="comercio")]
    r = Replicador.desde_filas(filas, movimientos, 2, Politica("sin_filtro", None),
                               crear_agrupador(metodo, umbral, ["TIENDA-UNO", "TIENDA-UNNO"]))
    return medidas_de_agrupacion(r, [LLAVE], HISTORIAL, ruido or {}, CORTE)


class PruebasMedidasDeAgrupacion(unittest.TestCase):
    def test_exposicion_latente_cuenta_un_nombre_equivocado_con_dos_votantes(self):
        filas = [corr(1, "u1", LLAVE, "TIENDA-UNNO", "compras", "05-01"),
                 corr(2, "u2", LLAVE, "TIENDA-UNNO", "compras", "05-02")]
        self.assertEqual(correr(filas, "ninguno", None)["exposicion_latente_comercios"], 1)

    def test_con_agrupacion_el_tipeo_se_une_al_nombre_correcto_y_no_expone(self):
        filas = [corr(1, "u1", LLAVE, "TIENDA-UNO", "compras", "05-01"),
                 corr(2, "u2", LLAVE, "TIENDA-UNNO", "compras", "05-02")]
        self.assertEqual(correr(filas, "ninguno", None)["exposicion_latente_comercios"], 0)
        m = correr(filas, "jaro_winkler", 0.97)
        self.assertEqual((m["exposicion_latente_comercios"], m["fusion_falsa_grupos"]), (0, 0))

    def test_un_tipeo_primero_se_vuelve_representante_y_expone(self):
        filas = [corr(1, "u1", LLAVE, "TIENDA-UNNO", "compras", "05-01"),
                 corr(2, "u2", LLAVE, "TIENDA-UNO", "compras", "05-02")]
        m = correr(filas, "jaro_winkler", 0.97)
        self.assertEqual(m["exposicion_latente_comercios"], 1)

    def test_fusion_falsa_cuando_se_mezcla_colusion_con_un_voto_honesto(self):
        filas = [corr(1, "u1", LLAVE, "TIENDA-UNO", "compras", "05-01"),
                 corr(2, "u2", LLAVE, "TIENDA-UNNO", "compras", "05-02")]
        m = correr(filas, "jaro_winkler", 0.97, {"c002": "colusion"})
        self.assertEqual((m["fusion_falsa_grupos"], m["fusion_falsa_comercios_con_grupo"]), (1, 1))
        self.assertEqual(m["fusion_falsa_comercios_global_equivocado"], 0)


if __name__ == "__main__":
    unittest.main()
