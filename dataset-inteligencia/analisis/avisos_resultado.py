"""Resultado de evaluar una corrida: etiquetas con categoría del oráculo, etiquetas con categoría inferida y fondo."""

from dataclasses import dataclass

from .avisos_config import ConfiguracionAvisos
from .avisos_corrida import CorridaAvisos
from .avisos_evaluacion import FilaEvaluada, evaluar_corrida
from .avisos_fondo import FilaFondo, filas_sin_etiqueta


@dataclass(frozen=True)
class ResultadoCorrida:
    corrida: CorridaAvisos
    filas: tuple[FilaEvaluada, ...]
    inferidas: tuple[FilaEvaluada, ...]
    fondo: tuple[FilaFondo, ...]


def evaluar_resultado(corrida: CorridaAvisos, cfg: ConfiguracionAvisos) -> ResultadoCorrida:
    return ResultadoCorrida(corrida, evaluar_corrida(corrida, cfg), evaluar_corrida(corrida, cfg, True),
                            filas_sin_etiqueta(corrida, cfg))
