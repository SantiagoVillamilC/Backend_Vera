-- Nivel de KYC por parte y su evidencia de verificacion (AUT-A4-01).

-- +goose Up
CREATE TABLE auth.kyc_estados (
    parte_id      uuid PRIMARY KEY REFERENCES auth.partes(id),
    producto      text NOT NULL CHECK (producto IN ('bajo_monto','pleno','deposito_ordinario')),
    estado_kyc    text NOT NULL,
    actualizado_en timestamptz NOT NULL DEFAULT now()
);

-- Evidencia append-only: fecha, medio, proveedor, resultado y referencia de cada verificacion.
CREATE TABLE auth.kyc_verificaciones (
    id                   bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
    parte_id             uuid NOT NULL REFERENCES auth.partes(id),
    nivel_solicitado     text NOT NULL CHECK (nivel_solicitado IN ('bajo_monto','pleno')),
    medio                text NOT NULL,
    proveedor            text,
    resultado            text NOT NULL,
    referencia_proveedor text,
    ocurrida_en          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX kyc_verificaciones_parte ON auth.kyc_verificaciones (parte_id, ocurrida_en);

GRANT SELECT, INSERT, UPDATE ON auth.kyc_estados TO rol_auth;
GRANT SELECT, INSERT ON auth.kyc_verificaciones TO rol_auth;

-- La evidencia es append-only: ni UPDATE, ni DELETE, ni TRUNCATE para rol_auth.
REVOKE UPDATE, DELETE, TRUNCATE ON auth.kyc_verificaciones FROM rol_auth;

-- +goose StatementBegin
CREATE FUNCTION auth.kyc_verificaciones_inmutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'auth.kyc_verificaciones es append-only: % no permitido', TG_OP
        USING ERRCODE = 'restrict_violation';
END $$;
-- +goose StatementEnd

CREATE TRIGGER kyc_verificaciones_inmutable
BEFORE UPDATE OR DELETE ON auth.kyc_verificaciones
FOR EACH ROW EXECUTE FUNCTION auth.kyc_verificaciones_inmutable();

-- +goose Down
DROP TRIGGER IF EXISTS kyc_verificaciones_inmutable ON auth.kyc_verificaciones;
DROP FUNCTION IF EXISTS auth.kyc_verificaciones_inmutable();
DROP TABLE auth.kyc_verificaciones;
DROP TABLE auth.kyc_estados;
