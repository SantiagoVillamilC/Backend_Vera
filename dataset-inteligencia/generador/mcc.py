"""MCC (ISO 18245) derivado siempre de la categoría, nunca de la marca."""

from .constantes import MCC_POR_CATEGORIA


def mcc_de(categoria: str, llave_destino: str) -> str:
    """Código MCC de la categoría. Se escoge de la lista por los 3 últimos dígitos de la llave."""
    lista = MCC_POR_CATEGORIA.get(categoria)
    if not lista:
        return ""
    return lista[int(llave_destino[-3:]) % len(lista)]
