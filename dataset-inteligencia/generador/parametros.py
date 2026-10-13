"""Parámetros de una corrida y su validación."""

from dataclasses import dataclass
from datetime import date

from .constantes import (FECHA_FIN_MAXIMA, FECHA_FIN_MINIMA, FECHA_FIN_POR_DEFECTO, TAMANOS_COLUSION_PERMITIDOS,
                         TAMANOS_COLUSION_POR_DEFECTO)


@dataclass(frozen=True)
class Parametros:
    filas: int
    semilla: int = 2026
    variante: str = "A"
    usuarios: int | None = None
    tasa_correccion: float = 0.35
    ruido_nombre: float = 0.30
    tasa_etiqueta_erronea: float = 0.08
    fecha_fin: date = FECHA_FIN_POR_DEFECTO
    fraccion_qr: float = 0.60
    colusion_comercios: int = 0
    colusion_tamanos: tuple[int, ...] = TAMANOS_COLUSION_POR_DEFECTO
    colusion_tardia_comercios: int = 0
    gasto_usuarios: int = 0

    def validar(self) -> None:
        if self.filas < 0:
            raise ValueError("--filas no puede ser negativo")
        if self.variante not in ("A", "B"):
            raise ValueError("--variante debe ser A o B")
        if self.usuarios is not None and self.usuarios < 1:
            raise ValueError("--usuarios debe ser al menos 1")
        for nombre in ("tasa_correccion", "ruido_nombre", "tasa_etiqueta_erronea", "fraccion_qr"):
            if not 0.0 <= getattr(self, nombre) <= 1.0:
                raise ValueError(f"{nombre} debe estar entre 0 y 1")
        if not FECHA_FIN_MINIMA <= self.fecha_fin <= FECHA_FIN_MAXIMA:
            raise ValueError(
                f"--fecha-fin debe estar entre {FECHA_FIN_MINIMA.isoformat()} y {FECHA_FIN_MAXIMA.isoformat()}"
            )
        self._validar_colusion()

    def _validar_colusion(self) -> None:
        if self.colusion_comercios < 0:
            raise ValueError("--colusion-comercios debe ser un entero mayor o igual a 0")
        if self.colusion_tardia_comercios < 0:
            raise ValueError("--colusion-tardia-comercios debe ser un entero mayor o igual a 0")
        if self.gasto_usuarios < 0:
            raise ValueError("--gasto-usuarios debe ser un entero mayor o igual a 0")
        if not self.colusion_tamanos or len(set(self.colusion_tamanos)) != len(self.colusion_tamanos) or any(
                t not in TAMANOS_COLUSION_PERMITIDOS for t in self.colusion_tamanos):
            raise ValueError("--colusion-tamanos solo admite los valores 2 y 3, sin repetir (por ejemplo 2,3)")

    def a_dict(self) -> dict:
        return {
            "filas": self.filas,
            "semilla": self.semilla,
            "variante": self.variante,
            "usuarios": self.usuarios,
            "tasa_correccion": self.tasa_correccion,
            "ruido_nombre": self.ruido_nombre,
            "tasa_etiqueta_erronea": self.tasa_etiqueta_erronea,
            "fecha_fin": self.fecha_fin.isoformat(),
            "fraccion_qr": self.fraccion_qr,
            "colusion_comercios": self.colusion_comercios,
            "colusion_tamanos": list(self.colusion_tamanos),
            "colusion_tardia_comercios": self.colusion_tardia_comercios,
            "gasto_usuarios": self.gasto_usuarios,
        }
