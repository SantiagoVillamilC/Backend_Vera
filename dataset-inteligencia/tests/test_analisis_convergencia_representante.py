"""Pruebas de la política de representante `mas_votado` de la agrupación por similitud, con casos a mano.

Con `primero` el representante es el primer nombre que creó el grupo. Con `mas_votado` el nombre visible del grupo es
el de más votantes distintos (vigentes), con desempate por el voto que primero alcanzó ese conteo, y se reetiqueta
todo el historial: la clave estable del conteo es el ancla del grupo, que no cambia.
"""

import unittest
from datetime import datetime, timezone

from analisis.configuracion import ConfiguracionInvalida
from analisis.convergencia import Replicador
from analisis.convergencia_agrupacion import crear_agrupador
from analisis.convergencia_config import Politica, desde_dict_convergencia

from .util_analisis import fila
from .util_convergencia import corr, t

LLAVE = "0011111111"
POLITICA = Politica("sin_filtro", None)
BASE = {"ks": [1, 2, 3], "politicas": {"sin_filtro": None}, "politica_principal": "sin_filtro",
        "k_referencia": 2, "umbral_similitud": None}
TIPEO, HONESTO = "TIENDA-UNNO", "TIENDA-UNO"


def ahora() -> datetime:
    return datetime.strptime(t("12-31"), "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)


def votos(nombres: list[str], llave: str = LLAVE) -> list[dict]:
    return [corr(i + 1, f"u{i + 1}", llave, n, "compras", f"05-{i + 1:02d}") for i, n in enumerate(nombres)]


def replicador(filas: list[dict], representante: str | None = None, k: int = 2) -> Replicador:
    movimientos = [fila(1, "x", t("01-01"), 1, LLAVE, tipo_llave="comercio")]
    extra = {} if representante is None else {"representante": representante}
    agrupador = crear_agrupador("jaro_winkler", 0.97, [], **extra)
    return Replicador.desde_filas(filas, movimientos, k, POLITICA, agrupador)


def global_nombre(r: Replicador) -> str | None:
    return r.estado_en(ahora()).global_de(LLAVE, "nombre")


class PruebasMasVotado(unittest.TestCase):
    def test_un_tipeo_primero_y_dos_honestos_hacen_que_el_representante_sea_el_honesto(self):
        filas = votos([TIPEO, HONESTO, HONESTO])
        self.assertEqual(global_nombre(replicador(filas, "primero")), TIPEO)
        self.assertEqual(global_nombre(replicador(filas, "mas_votado")), HONESTO)

    def test_el_default_es_primero_y_es_identico_al_explicito(self):
        filas = votos([TIPEO, HONESTO, HONESTO])
        self.assertEqual(crear_agrupador("jaro_winkler", 0.97, []).representante, "primero")
        self.assertEqual(replicador(filas).cambios(), replicador(filas, "primero").cambios())
        self.assertEqual(replicador(filas).correcciones_efectivas(), replicador(filas, "primero").correcciones_efectivas())

    def test_el_empate_lo_decide_quien_llego_primero_a_ese_conteo(self):
        filas = votos([TIPEO, HONESTO])
        self.assertEqual(global_nombre(replicador(filas, "mas_votado")), TIPEO)
        invertido = votos([HONESTO, TIPEO])
        self.assertEqual(global_nombre(replicador(invertido, "mas_votado")), HONESTO)

    def test_el_cambio_de_representante_queda_como_cambio_y_reetiqueta_los_votos_ya_contados(self):
        filas = votos([TIPEO, HONESTO, HONESTO])
        r = replicador(filas, "mas_votado")
        resumen = [(c.antes, c.despues, c.correccion_id) for c in r.cambios() if c.dimension == "nombre"]
        self.assertEqual(resumen, [(None, TIPEO, "c002"), (TIPEO, HONESTO, "c003")])
        estado = r.estado_en(ahora())
        self.assertEqual(estado.voto_de(LLAVE, "u1", "nombre"), HONESTO)  # el voto del tipeo se reetiqueta
        self.assertEqual(estado.global_de(LLAVE, "nombre"), HONESTO)

    def test_el_estado_a_un_instante_anterior_solo_usa_los_votos_hasta_ese_instante(self):
        r = replicador(votos([TIPEO, HONESTO, HONESTO]), "mas_votado")
        corte = datetime.strptime(t("05-02"), "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)
        self.assertEqual(r.estado_en(corte).global_de(LLAVE, "nombre"), TIPEO)

    def test_la_promocion_no_depende_de_la_politica_de_representante(self):
        filas = votos([TIPEO, HONESTO])
        for k, esperado in ((2, True), (3, False)):
            with self.subTest(k=k):
                self.assertEqual(global_nombre(replicador(filas, "mas_votado", k)) is not None, esperado)
                self.assertEqual(global_nombre(replicador(filas, "primero", k)) is not None, esperado)

    def test_determinismo_con_filas_barajadas(self):
        filas = votos([TIPEO, HONESTO, HONESTO, TIPEO, HONESTO])
        a, b = replicador(filas, "mas_votado"), replicador(list(reversed(filas)), "mas_votado")
        self.assertEqual(a.cambios(), b.cambios())
        self.assertEqual(a.correcciones_efectivas(), b.correcciones_efectivas())
        self.assertEqual(a.estado_en(ahora()), b.estado_en(ahora()))

    def test_un_usuario_que_corrige_mueve_su_voto_y_el_conteo(self):
        filas = votos([TIPEO, HONESTO]) + [corr(3, "u1", LLAVE, HONESTO, "compras", "05-03")]
        self.assertEqual(global_nombre(replicador(filas, "mas_votado")), HONESTO)

    def test_la_etiqueta_efectiva_es_la_final_del_grupo(self):
        efectivas = replicador(votos([TIPEO, HONESTO, HONESTO]), "mas_votado").correcciones_efectivas()
        self.assertEqual([c.nombre for c in efectivas], [HONESTO] * 3)
        primero = replicador(votos([TIPEO, HONESTO, HONESTO]), "primero").correcciones_efectivas()
        self.assertEqual([c.nombre for c in primero], [TIPEO] * 3)


class PruebasConfiguracionRepresentante(unittest.TestCase):
    ACTIVO = {"metodo_similitud": "jaro_winkler", "umbral_similitud": 0.97}

    def test_el_default_no_cambia_el_diccionario_ni_el_hash(self):
        base = desde_dict_convergencia({**BASE, **self.ACTIVO})
        explicito = desde_dict_convergencia({**BASE, **self.ACTIVO, "representante": "primero"})
        self.assertEqual((base.representante, base.hash()), ("primero", explicito.hash()))
        self.assertNotIn("representante", base.a_dict())

    def test_mas_votado_cambia_el_hash_y_es_ida_y_vuelta(self):
        base = desde_dict_convergencia({**BASE, **self.ACTIVO})
        cfg = desde_dict_convergencia({**BASE, **self.ACTIVO, "representante": "mas_votado"})
        self.assertNotEqual(cfg.hash(), base.hash())
        self.assertEqual(desde_dict_convergencia(cfg.a_dict()).hash(), cfg.hash())

    def test_rechaza_representante_desconocido_o_sin_metodo(self):
        for malo in ({**self.ACTIVO, "representante": "ultimo"},
                     {"metodo_similitud": "ninguno", "umbral_similitud": None, "representante": "mas_votado"}):
            with self.subTest(malo=malo), self.assertRaises(ConfiguracionInvalida):
                desde_dict_convergencia({**BASE, **malo})


if __name__ == "__main__":
    unittest.main()
