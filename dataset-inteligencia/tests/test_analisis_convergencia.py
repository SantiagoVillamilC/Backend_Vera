"""Pruebas del replicador de convergencia por multitud, con ejemplos calculados a mano sobre filas diminutas."""

import random
import unittest
from datetime import datetime, timezone
from pathlib import Path

from analisis.configuracion import ConfiguracionInvalida
from analisis.convergencia import Replicador, leer_correcciones, tipos_de_llave
from analisis.convergencia_config import (ConfigConvergencia, Politica, cargar_configuracion_convergencia,
                                          desde_dict_convergencia)

from .test_analisis_cargar import violaciones
from .util_analisis import fila
from .util_convergencia import corr, t

RAIZ = Path(__file__).resolve().parent.parent / "analisis"
LLAVE = "0011111111"
SIN_FILTRO = Politica("sin_filtro", None)
SOLO_COMERCIO = Politica("solo_comercio", ("comercio",))


def ahora(mes_dia: str) -> datetime:
    return datetime.strptime(t(mes_dia), "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)


def replicador(filas: list[dict], k: int, politica: Politica = SIN_FILTRO, tipo: str = "comercio",
               llave: str = LLAVE, deriva: bool = False) -> Replicador:
    movimientos = [fila(1, "x", t("01-01"), 1, llave, tipo_llave=tipo)]
    return Replicador.desde_filas(filas, movimientos, k, politica, None, deriva)


def votos(valores: list[tuple[str, str, str]], llave: str = LLAVE, inicio: int = 1) -> list[dict]:
    """(usuario, nombre, categoria) en orden, un día después de cada uno desde el 05-01."""
    return [corr(inicio + i, u, llave, n, c, f"05-{i + 1:02d}") for i, (u, n, c) in enumerate(valores)]


class PruebasPromocion(unittest.TestCase):
    def test_se_promueve_exactamente_al_llegar_a_k(self):
        r = replicador(votos([("u1", "Tienda Uno", "compras"), ("u2", "Tienda Uno", "compras")]), 2)
        self.assertIsNone(r.estado_en(ahora("05-01")).global_de(LLAVE, "nombre"))
        despues = r.estado_en(ahora("05-02"))
        self.assertEqual(despues.global_de(LLAVE, "nombre"), "TIENDA-UNO")
        self.assertEqual(despues.global_de(LLAVE, "categoria"), "compras")

    def test_con_k_1_el_primer_voto_promueve_y_con_k_3_dos_votos_no(self):
        filas = votos([("u1", "Tienda Uno", "compras"), ("u2", "Tienda Uno", "compras")])
        self.assertEqual(replicador(filas, 1).estado_en(ahora("05-01")).global_de(LLAVE, "nombre"), "TIENDA-UNO")
        self.assertIsNone(replicador(filas, 3).estado_en(ahora("12-31")).global_de(LLAVE, "nombre"))

    def test_la_clave_de_nombre_es_el_nombre_canonizado(self):
        filas = votos([("u1", "Tienda Uno", "compras"), ("u2", "TIENDA_UNO", "compras"), ("u3", "tienda-uno", "compras")])
        self.assertEqual(replicador(filas, 3).estado_en(ahora("12-31")).global_de(LLAVE, "nombre"), "TIENDA-UNO")

    def test_un_nombre_con_tipeo_es_otra_clave(self):
        filas = votos([("u1", "Tienda Uno", "compras"), ("u2", "Tienda Unno", "compras")])
        e = replicador(filas, 2).estado_en(ahora("12-31"))
        self.assertIsNone(e.global_de(LLAVE, "nombre"))
        self.assertEqual(e.global_de(LLAVE, "categoria"), "compras")

    def test_nombre_y_categoria_son_dimensiones_independientes(self):
        filas = votos([("u1", "Tienda Uno", "compras"), ("u2", "Tienda Uno", "salud")])
        e = replicador(filas, 2).estado_en(ahora("12-31"))
        self.assertEqual(e.global_de(LLAVE, "nombre"), "TIENDA-UNO")
        self.assertIsNone(e.global_de(LLAVE, "categoria"))

    def test_los_cambios_registran_instante_dimension_y_valores(self):
        filas = votos([("u1", "Tienda Uno", "compras"), ("u2", "Tienda Uno", "compras")])
        cambios = replicador(filas, 2).cambios()
        self.assertEqual([(c.dimension, c.antes, c.despues, c.instante) for c in cambios],
                         [("nombre", None, "TIENDA-UNO", ahora("05-02")),
                          ("categoria", None, "compras", ahora("05-02"))])


class PruebasUnVotoPorUsuario(unittest.TestCase):
    def test_un_usuario_que_se_corrige_no_cuenta_dos_veces(self):
        mismo_valor = votos([("u1", "Tienda Uno", "compras"), ("u1", "Tienda Uno", "compras")])
        self.assertIsNone(replicador(mismo_valor, 2).estado_en(ahora("12-31")).global_de(LLAVE, "nombre"))

    def test_su_voto_anterior_se_reemplaza(self):
        filas = votos([("u1", "Tienda Uno", "compras"), ("u1", "Tienda Dos", "compras"),
                       ("u2", "Tienda Uno", "compras")])
        e = replicador(filas, 2).estado_en(ahora("12-31"))
        self.assertIsNone(e.global_de(LLAVE, "nombre"))  # u1 ya no apoya TIENDA-UNO, solo u2 lo hace
        self.assertEqual(e.voto_de(LLAVE, "u1", "nombre"), "TIENDA-DOS")

    def test_el_global_no_se_retira_cuando_un_apoyo_cambia_de_voto(self):
        filas = votos([("u1", "A", "compras"), ("u2", "A", "compras"), ("u1", "B", "compras")])
        e = replicador(filas, 2).estado_en(ahora("12-31"))
        self.assertEqual(e.global_de(LLAVE, "nombre"), "A")


class PruebasRelevo(unittest.TestCase):
    BASE = [("u1", "A", "compras"), ("u2", "A", "compras")]

    def test_el_empate_conserva_al_incumbente(self):
        filas = votos(self.BASE + [("u3", "B", "compras"), ("u4", "B", "compras")])
        e = replicador(filas, 2).estado_en(ahora("12-31"))
        self.assertEqual(e.global_de(LLAVE, "nombre"), "A")

    def test_el_relevo_exige_mayoria_estricta(self):
        filas = votos(self.BASE + [("u3", "B", "compras"), ("u4", "B", "compras"), ("u5", "B", "compras")])
        r = replicador(filas, 2)
        self.assertEqual(r.estado_en(ahora("05-04")).global_de(LLAVE, "nombre"), "A")
        self.assertEqual(r.estado_en(ahora("05-05")).global_de(LLAVE, "nombre"), "B")
        nombre = [c for c in r.cambios() if c.dimension == "nombre"]
        self.assertEqual([(c.antes, c.despues) for c in nombre], [(None, "A"), ("A", "B")])

    def test_el_relevo_tambien_exige_al_menos_k(self):
        filas = votos([("u1", "A", "compras"), ("u2", "A", "compras"), ("u3", "A", "compras")]
                      + [("u4", "B", "compras"), ("u5", "B", "compras")])
        self.assertEqual(replicador(filas, 3).estado_en(ahora("12-31")).global_de(LLAVE, "nombre"), "A")

    def test_con_k_1_el_primero_gana_y_el_empate_no_lo_mueve(self):
        filas = votos([("u1", "A", "compras"), ("u2", "B", "compras")])
        self.assertEqual(replicador(filas, 1).estado_en(ahora("12-31")).global_de(LLAVE, "nombre"), "A")


class PruebasDeDeriva(unittest.TestCase):
    """Regla de relevo `reciente`: en el empate releva el candidato posterior a la promoción del incumbente."""

    def test_la_estricta_conserva_al_incumbente_en_el_empate(self):
        filas = votos([("u1", "A", "compras"), ("u2", "A", "compras"),
                       ("u3", "B", "compras"), ("u4", "B", "compras")])
        self.assertEqual(replicador(filas, 2).estado_en(ahora("12-31")).global_de(LLAVE, "nombre"), "A")

    def test_la_reciente_releva_en_el_empate_si_el_candidato_es_posterior(self):
        filas = votos([("u1", "A", "compras"), ("u2", "A", "compras"),
                       ("u3", "B", "compras"), ("u4", "B", "compras")])
        self.assertEqual(replicador(filas, 2, deriva=True).estado_en(ahora("12-31")).global_de(LLAVE, "nombre"), "B")

    def test_la_reciente_registra_el_relevo_en_la_historia(self):
        filas = votos([("u1", "A", "compras"), ("u2", "A", "compras"),
                       ("u3", "B", "compras"), ("u4", "B", "compras")])
        nombre = [c for c in replicador(filas, 2, deriva=True).cambios() if c.dimension == "nombre"]
        self.assertEqual([(c.antes, c.despues) for c in nombre], [(None, "A"), ("A", "B")])

    def test_la_reciente_no_releva_si_el_candidato_no_alcanza_k(self):
        filas = votos([("u1", "A", "compras"), ("u2", "A", "compras"), ("u3", "B", "compras")])
        self.assertEqual(replicador(filas, 2, deriva=True).estado_en(ahora("12-31")).global_de(LLAVE, "nombre"), "A")

    def test_la_reciente_no_releva_a_una_minoria_mas_reciente(self):
        filas = votos([("u1", "A", "compras"), ("u2", "A", "compras"), ("u3", "A", "compras"),
                       ("u4", "B", "compras"), ("u5", "B", "compras")])
        self.assertEqual(replicador(filas, 2, deriva=True).estado_en(ahora("12-31")).global_de(LLAVE, "nombre"), "A")

    def test_la_reciente_conserva_al_incumbente_en_el_empate_simultaneo(self):
        filas = [corr(1, "u1", LLAVE, "A", "compras", "05-01"), corr(2, "u2", LLAVE, "A", "compras", "05-02"),
                 corr(3, "u3", LLAVE, "B", "compras", "05-02"), corr(4, "u4", LLAVE, "B", "compras", "05-02")]
        self.assertEqual(replicador(filas, 2, deriva=True).estado_en(ahora("12-31")).global_de(LLAVE, "nombre"), "A")

    def test_la_reciente_sigue_un_cambio_real_que_empata(self):
        filas = votos([("u1", "Tienda Vieja", "compras"), ("u2", "Tienda Vieja", "compras"),
                       ("u3", "Tienda Nueva", "compras"), ("u4", "Tienda Nueva", "compras")])
        self.assertEqual(replicador(filas, 2).estado_en(ahora("12-31")).global_de(LLAVE, "nombre"), "TIENDA-VIEJA")
        self.assertEqual(replicador(filas, 2, deriva=True).estado_en(ahora("12-31")).global_de(LLAVE, "nombre"),
                         "TIENDA-NUEVA")

    def test_la_reciente_deja_relevar_un_valor_consolidado_por_un_ataque_posterior(self):
        filas = votos([("h1", "Tienda Uno", "compras"), ("h2", "Tienda Uno", "compras"),
                       ("c1", "Tienda Uno", "salud"), ("c2", "Tienda Uno", "salud")])
        self.assertEqual(replicador(filas, 2).estado_en(ahora("12-31")).global_de(LLAVE, "categoria"), "compras")
        self.assertEqual(replicador(filas, 2, deriva=True).estado_en(ahora("12-31")).global_de(LLAVE, "categoria"),
                         "salud")

    def test_la_estricta_no_deja_relevar_por_un_ataque_posterior(self):
        filas = votos([("h1", "Tienda Uno", "compras"), ("h2", "Tienda Uno", "compras"), ("h3", "Tienda Uno", "compras"),
                       ("c1", "Tienda Uno", "salud"), ("c2", "Tienda Uno", "salud"), ("c3", "Tienda Uno", "salud")])
        self.assertEqual(replicador(filas, 2).estado_en(ahora("12-31")).global_de(LLAVE, "categoria"), "compras")


class PruebasElReloj(unittest.TestCase):
    def test_estado_en_no_mira_el_futuro(self):
        filas = votos([("u1", "A", "compras"), ("u2", "A", "compras"), ("u3", "B", "compras")])
        completo = replicador(filas, 2)
        hasta_el_dos = replicador(filas[:2], 2)
        self.assertEqual(completo.estado_en(ahora("05-02")), hasta_el_dos.estado_en(ahora("12-31")))
        self.assertEqual(completo.estado_en(ahora("05-02")).voto_de(LLAVE, "u3", "nombre"), None)

    def test_el_instante_es_inclusivo(self):
        filas = votos([("u1", "A", "compras")])
        self.assertEqual(replicador(filas, 1).estado_en(ahora("05-01")).global_de(LLAVE, "nombre"), "A")

    def test_exige_un_instante_con_zona_horaria(self):
        with self.assertRaises(ValueError):
            replicador(votos([("u1", "A", "compras")]), 1).estado_en(datetime(2026, 5, 1))


class PruebasInvarianza(unittest.TestCase):
    def test_barajar_las_filas_simultaneas_no_cambia_el_resultado(self):
        instante = "05-05"
        filas = [corr(1, "u1", LLAVE, "A", "compras", "05-01"), corr(2, "u2", LLAVE, "A", "compras", "05-02")]
        filas += [corr(10 + i, f"u{3 + i}", LLAVE, nombre, categoria, instante)
                  for i, (nombre, categoria) in enumerate([("B", "salud"), ("C", "compras"), ("B", "salud"),
                                                           ("B", "compras"), ("C", "salud")])]
        referencia = replicador(filas, 2)
        for semilla in range(25):
            baraja = list(filas)
            random.Random(semilla).shuffle(baraja)
            otra = replicador(baraja, 2)
            self.assertEqual(otra.cambios(), referencia.cambios(), semilla)
            self.assertEqual(otra.estado_en(ahora("12-31")), referencia.estado_en(ahora("12-31")), semilla)

    def test_el_orden_de_las_simultaneas_es_el_del_id(self):
        filas = [corr(2, "u2", LLAVE, "B", "compras", "05-01"), corr(1, "u1", LLAVE, "A", "compras", "05-01")]
        self.assertEqual(replicador(filas, 1).estado_en(ahora("05-01")).global_de(LLAVE, "nombre"), "A")


class PruebasDeElegibilidad(unittest.TestCase):
    PERSONAS = votos([("u1", "Arrendadora Ana", "vivienda_servicios"), ("u2", "Arrendadora Ana", "vivienda_servicios")],
                     llave="3001234567")

    def test_sin_filtro_promueve_a_una_persona(self):
        r = replicador(self.PERSONAS, 2, SIN_FILTRO, tipo="celular", llave="3001234567")
        self.assertEqual(r.estado_en(ahora("12-31")).global_de("3001234567", "nombre"), "ARRENDADORA-ANA")
        self.assertEqual({c.llave for c in r.cambios()}, {"3001234567"})

    def test_solo_comercio_no_promueve_a_una_persona_pero_guarda_sus_votos(self):
        r = replicador(self.PERSONAS, 2, SOLO_COMERCIO, tipo="celular", llave="3001234567")
        e = r.estado_en(ahora("12-31"))
        self.assertIsNone(e.global_de("3001234567", "nombre"))
        self.assertEqual(e.voto_de("3001234567", "u1", "nombre"), "ARRENDADORA-ANA")
        self.assertEqual(r.cambios(), ())

    def test_solo_comercio_si_promueve_a_un_comercio(self):
        r = replicador(votos([("u1", "A", "compras"), ("u2", "A", "compras")]), 2, SOLO_COMERCIO)
        self.assertEqual(r.estado_en(ahora("12-31")).global_de(LLAVE, "nombre"), "A")

    def test_una_llave_sin_tipo_observado_no_es_elegible_con_filtro(self):
        r = Replicador.desde_filas(votos([("u1", "A", "compras")]), [], 1, SOLO_COMERCIO)
        self.assertIsNone(r.estado_en(ahora("12-31")).global_de(LLAVE, "nombre"))

    def test_tipos_de_llave_sale_de_los_movimientos(self):
        movimientos = [fila(1, "u", t("05-01"), 1, "3001234567", tipo_llave="celular"),
                       fila(2, "u", t("05-02"), 1, LLAVE), fila(3, "u", t("05-03"), 1, "", tipo_llave="")]
        self.assertEqual(tipos_de_llave(movimientos), {"3001234567": "celular", LLAVE: "comercio"})


class PruebasDeLaVista(unittest.TestCase):
    FILAS = votos([("u1", "Pepe Supermercado", "alimentos_mercado"), ("u2", "Supermercado Don Pepe", "compras"),
                   ("u3", "Supermercado Don Pepe", "compras")])

    def test_el_privado_gana_en_la_vista_propia_aun_con_global(self):
        v = replicador(self.FILAS, 2).vista_en(ahora("12-31"), "u1", LLAVE, "")
        self.assertEqual((v.nombre, v.categoria, v.origen), ("PEPE-SUPERMERCADO", "alimentos_mercado", "privado"))

    def test_otro_usuario_ve_el_global(self):
        v = replicador(self.FILAS, 2).vista_en(ahora("12-31"), "u9", LLAVE, "")
        self.assertEqual((v.nombre, v.categoria, v.origen), ("SUPERMERCADO-DON-PEPE", "compras", "global"))

    def test_sin_global_ni_privado_ve_el_crudo_canonizado_y_sin_categoria(self):
        r = replicador(self.FILAS, 2)
        v = r.vista_en(ahora("05-02"), "u9", LLAVE, "Don_Pepe")
        self.assertEqual((v.nombre, v.categoria, v.origen), ("DON-PEPE", "sin_categoria", "crudo"))
        vacio = r.vista_en(ahora("05-02"), "u9", LLAVE, "")
        self.assertEqual((vacio.nombre, vacio.categoria, vacio.origen), ("", "sin_categoria", "crudo"))

    def test_el_privado_empieza_cuando_el_usuario_corrige(self):
        v = replicador(self.FILAS, 2).vista_en(ahora("04-30"), "u1", LLAVE, "")
        self.assertEqual(v.origen, "crudo")


class PruebasDeLectura(unittest.TestCase):
    def test_leer_correcciones_ordena_por_instante_e_id_sin_mutar_la_entrada(self):
        filas = [corr(2, "u2", LLAVE, "B", "compras", "05-01"), corr(1, "u1", LLAVE, "Tienda_Uno", "compras", "05-01")]
        copia = [dict(f) for f in filas]
        leidas = leer_correcciones(filas)
        self.assertEqual([c.correccion_id for c in leidas], ["c001", "c002"])
        self.assertEqual(leidas[0].nombre, "TIENDA-UNO")
        self.assertEqual(filas, copia)


class PruebasDeConfiguracion(unittest.TestCase):
    BUENA = {"ks": [1, 2, 3], "politicas": {"sin_filtro": None, "solo_comercio": ["comercio"]},
             "politica_principal": "solo_comercio", "k_referencia": 2, "umbral_similitud": None}

    def test_la_configuracion_por_defecto_carga(self):
        cfg = cargar_configuracion_convergencia(RAIZ / "config" / "convergencia.json")
        self.assertEqual(cfg.ks, (1, 2, 3))
        self.assertEqual([p.nombre for p in cfg.politicas], ["sin_filtro", "solo_comercio"])
        self.assertIsNone(cfg.umbral_similitud)
        self.assertEqual(cfg.politica("solo_comercio").tipos_elegibles, ("comercio",))

    def test_el_umbral_de_similitud_esta_reservado_y_desactivado(self):
        with self.assertRaises(ConfiguracionInvalida) as cm:
            desde_dict_convergencia({**self.BUENA, "umbral_similitud": 0.8})
        self.assertIn("umbral_similitud", str(cm.exception))

    def test_se_juntan_todos_los_problemas(self):
        malo = {**self.BUENA, "ks": [0, 2, 2], "politica_principal": "otra", "k_referencia": 5}
        with self.assertRaises(ConfiguracionInvalida) as cm:
            desde_dict_convergencia(malo)
        mensaje = str(cm.exception)
        for pista in ("ks", "politica_principal", "k_referencia"):
            self.assertIn(pista, mensaje)

    def test_claves_faltantes_y_desconocidas(self):
        with self.assertRaises(ConfiguracionInvalida) as cm:
            desde_dict_convergencia({"ks": [1], "sobra": 1})
        self.assertIn("faltan", str(cm.exception))
        self.assertIn("sobra", str(cm.exception))

    def test_hash_estable_y_a_dict_ida_y_vuelta(self):
        cfg = desde_dict_convergencia(self.BUENA)
        self.assertEqual(desde_dict_convergencia(cfg.a_dict()).hash(), cfg.hash())
        self.assertIsInstance(cfg, ConfigConvergencia)

    def test_regla_deriva_por_defecto_y_validacion(self):
        cfg = desde_dict_convergencia(self.BUENA)
        self.assertEqual(cfg.regla_deriva, "estricta")
        self.assertNotIn("regla_deriva", cfg.a_dict())
        reciente = desde_dict_convergencia({**self.BUENA, "regla_deriva": "reciente"})
        self.assertEqual(reciente.regla_deriva, "reciente")
        self.assertEqual(reciente.a_dict()["regla_deriva"], "reciente")
        self.assertEqual(desde_dict_convergencia(reciente.a_dict()).hash(), reciente.hash())
        with self.assertRaises(ConfiguracionInvalida) as cm:
            desde_dict_convergencia({**self.BUENA, "regla_deriva": "otra"})
        self.assertIn("regla_deriva", str(cm.exception))


class PruebaEstaticaDelReplicador(unittest.TestCase):
    def test_el_replicador_no_toca_la_verdad(self):
        for nombre in ("convergencia.py", "convergencia_config.py"):
            ruta = RAIZ / nombre
            self.assertTrue(ruta.exists(), nombre)
            self.assertEqual(violaciones(ruta.read_text(encoding="utf-8")), [], nombre)


if __name__ == "__main__":
    unittest.main()
