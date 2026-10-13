"""Constantes del generador: catálogo, MCC, zona horaria y periodo de datos."""

import uuid
from datetime import date, timedelta, timezone

VERSION_GENERADOR = "1.4.0"
ESPACIO_UUID = uuid.uuid5(uuid.NAMESPACE_DNS, "dataset-inteligencia.tesis-billetera.invalid")

ZONA_COLOMBIA = timezone(timedelta(hours=-5))
INICIO_PERIODO = date(2026, 4, 1)
INICIO_HISTORIAL = date(2026, 1, 1)
# El perfil 4 necesita datos hasta el 20 de octubre, así que ese es el corte mínimo posible.
FECHA_FIN_MINIMA = date(2026, 10, 20)
FECHA_FIN_POR_DEFECTO = date(2026, 10, 21)
FECHA_FIN_MAXIMA = date(2026, 12, 31)
MESES_FIJOS = (4, 5, 6, 7, 8, 9)

CATEGORIAS = (
    "salario", "transferencia_recibida", "reembolso", "otros_ingresos", "alimentos_mercado",
    "restaurantes_domicilios", "transporte", "vivienda_servicios", "salud", "educacion",
    "entretenimiento", "compras", "comisiones_costos_financieros", "otros_gastos", "sin_categoria",
)
SIN_CATEGORIA = "sin_categoria"
CATEGORIA_GMF = "comisiones_costos_financieros"

# Mapeo categoría a MCC (ISO 18245): decisión del equipo, ver ESPECIFICACION.md.
MCC_POR_CATEGORIA = {
    "alimentos_mercado": ("5411", "5499"),
    "restaurantes_domicilios": ("5812", "5813", "5814"),
    "transporte": ("4111", "4121", "4131", "5541", "5542"),
    "vivienda_servicios": ("4814", "4899", "4900"),
    "salud": ("5912", "8011", "8062", "8099"),
    "educacion": ("8211", "8220", "8299", "5942"),
    "entretenimiento": ("7832", "7922", "5815", "5816", "5817", "5818"),
    "compras": ("5311", "5310", "5651", "5691", "5732", "5964"),
}
CATEGORIAS_COMERCIO = tuple(MCC_POR_CATEGORIA)
CATEGORIAS_ERRONEAS = CATEGORIAS_COMERCIO + ("otros_gastos",)

# Tamaños de grupo de colusión permitidos en --colusion-tamanos y su valor por defecto.
TAMANOS_COLUSION_PERMITIDOS = (2, 3)
TAMANOS_COLUSION_POR_DEFECTO = (2, 3)

TIPOS_LLAVE = ("documento", "celular", "correo", "alfanumerico", "comercio")
TIPOS_RUIDO_NOMBRE = ("mayusculas", "tildes", "abreviatura", "tipeo")

MONTO_EXENTO_CENTAVOS = 100_000 * 100
CENTAVOS_POR_PESO = 100
CIUDAD_QR = "BOGOTA"
LIMITE_NOMBRE_QR = 25
PROBABILIDAD_NOMBRE_VACIO = 0.08
PROBABILIDAD_NOMBRE_GENERICO = 0.25
TEXTO_GENERICO = "Pago a comercio"
