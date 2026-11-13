-- Montos del hecho que disparó el aviso.
--
-- El aviso guardaba solo la severidad, que es la diferencia contra la referencia, y el texto llevaba
-- las cifras. Para que la app pueda pintar la proyección sin leer el texto, el aviso guarda el monto
-- del hecho y la línea base contra la que se compara. La referencia es nula cuando el tipo de aviso no
-- tiene línea base (cobro nuevo y pago que no alcanza no comparan contra un histórico).
--
-- El monto es la proyección, no la severidad: en presupuesto anticipado el monto es el gasto del mes
-- en curso y la referencia es lo habitual en los meses completos, mientras la severidad es la
-- diferencia entre los dos. Las filas anteriores a esta migración no tienen el dato y quedan en cero
-- sin referencia, que es lo que ya afirmaba su texto.

-- +goose Up
ALTER TABLE inteligencia.aviso
    ADD COLUMN monto_centavos bigint NOT NULL DEFAULT 0
        CONSTRAINT aviso_monto_no_negativo CHECK (monto_centavos >= 0),
    ADD COLUMN referencia_centavos bigint
        CONSTRAINT aviso_referencia_no_negativa CHECK (referencia_centavos >= 0);

-- El privilegio de tabla de 0024 alcanza a las columnas nuevas. Se repite la revocación de TRUNCATE
-- para dejar explícito que el rol no puede vaciar la tabla.
REVOKE TRUNCATE ON inteligencia.aviso FROM rol_inteligencia;

-- +goose Down
ALTER TABLE inteligencia.aviso
    DROP CONSTRAINT IF EXISTS aviso_referencia_no_negativa,
    DROP CONSTRAINT IF EXISTS aviso_monto_no_negativo,
    DROP COLUMN IF EXISTS referencia_centavos,
    DROP COLUMN IF EXISTS monto_centavos;
