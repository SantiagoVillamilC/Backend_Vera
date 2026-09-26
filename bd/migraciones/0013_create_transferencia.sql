-- +goose Up
CREATE TABLE nucleo.transferencia (
    id              uuid PRIMARY KEY,
    usuario_id      text NOT NULL,
    cuenta_origen   text NOT NULL REFERENCES nucleo.cuenta(id),
    llave_destino   text NOT NULL,
    monto           bigint NOT NULL CHECK (monto > 0),
    moneda          char(3) NOT NULL DEFAULT 'COP',
    idempotency_key text NOT NULL,
    estado          text NOT NULL
        CHECK (estado IN ('creada', 'reservada', 'despachada', 'confirmada',
                          'fallida', 'indeterminada', 'compensada')),
    creada_en       timestamptz NOT NULL DEFAULT now(),
    actualizada_en  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT transferencia_idempotencia_unica UNIQUE (usuario_id, idempotency_key)
);

-- transferencia es máquina de estados, no diario: UPDATE acotado a las dos columnas que mueve el CAS.
GRANT SELECT, INSERT ON nucleo.transferencia TO rol_nucleo;
GRANT UPDATE (estado, actualizada_en) ON nucleo.transferencia TO rol_nucleo;
REVOKE DELETE, TRUNCATE ON nucleo.transferencia FROM rol_nucleo;

-- +goose Down
DROP TABLE nucleo.transferencia;
