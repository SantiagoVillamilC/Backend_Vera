-- regla_limite del capítulo 11; tope_uvt_centesimas reemplaza al monto_maximo en centavos del diseño, que se calcula al leer (HU-NF-06).

-- +goose Up
CREATE TABLE nucleo.regla_limite (
    id                    text PRIMARY KEY,
    nivel_kyc             text NOT NULL,
    ventana               text NOT NULL CHECK (ventana IN ('operacion', 'diaria', 'mensual')),
    tope_uvt_centesimas   bigint NOT NULL CHECK (tope_uvt_centesimas > 0),
    conteo_maximo         integer,
    vigente_desde         date NOT NULL,
    vigente_hasta         date,
    fuente                text NOT NULL,
    CONSTRAINT regla_limite_vigencia_coherente CHECK (vigente_hasta IS NULL OR vigente_hasta > vigente_desde)
);

CREATE TABLE nucleo.valor_uvt (
    anio            integer PRIMARY KEY,
    valor_centavos  bigint NOT NULL CHECK (valor_centavos > 0),
    fuente          text NOT NULL
);

GRANT SELECT ON nucleo.regla_limite, nucleo.valor_uvt TO rol_nucleo;
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON nucleo.regla_limite, nucleo.valor_uvt FROM rol_nucleo;

-- Semilla del único nivel de KYC del alcance: 210,50 UVT por operación y por mes, sin conteo máximo (HU-NF-06, criterios 10 y 11).
INSERT INTO nucleo.regla_limite (id, nivel_kyc, ventana, tope_uvt_centesimas, conteo_maximo, vigente_desde, fuente) VALUES
    ('deposito_bajo_monto_operacion', 'deposito_bajo_monto', 'operacion', 21050, NULL, DATE '2026-01-01',
     'Artículo 2.1.15.1.2 del Decreto 2555 de 2010, Reglamento del Depósito de Bajo Monto de una SEDPE'),
    ('deposito_bajo_monto_mensual', 'deposito_bajo_monto', 'mensual', 21050, NULL, DATE '2026-01-01',
     'Artículo 2.1.15.1.2 del Decreto 2555 de 2010, Reglamento del Depósito de Bajo Monto de una SEDPE');

INSERT INTO nucleo.valor_uvt (anio, valor_centavos, fuente) VALUES
    (2026, 5237400, 'Resolución DIAN 000238 del 15 de diciembre de 2025');

-- +goose Down
DROP TABLE nucleo.valor_uvt;
DROP TABLE nucleo.regla_limite;
