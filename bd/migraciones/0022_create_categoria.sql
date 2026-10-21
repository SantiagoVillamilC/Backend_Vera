-- Catálogo cerrado de categorías, firmado en el punto 349 bis de SUPUESTOS.md.
--
-- Los 14 códigos visibles coinciden con CATALOGO_14 de analisis/cascada_mcc.py. sin_categoria no se
-- ofrece al usuario y existe para que la clave foránea de la copia enriquecida no admita nulo.
-- El rol de la aplicación solo lee: el catálogo lo cura el equipo y el usuario no lo crea ni lo
-- renombra.

-- +goose Up
CREATE TABLE inteligencia.categoria (
    codigo text PRIMARY KEY CHECK (btrim(codigo) <> ''),
    nombre text NOT NULL CHECK (btrim(nombre) <> ''),
    grupo  text NOT NULL CHECK (grupo IN ('ingreso', 'gasto', 'ninguno')),
    orden  smallint NOT NULL CHECK (orden > 0),
    CONSTRAINT categoria_orden_unico UNIQUE (orden)
);

GRANT SELECT ON inteligencia.categoria TO rol_inteligencia;
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON inteligencia.categoria FROM rol_inteligencia;

INSERT INTO inteligencia.categoria (codigo, nombre, grupo, orden) VALUES
    ('salario', 'Salario', 'ingreso', 1),
    ('transferencia_recibida', 'Transferencia recibida', 'ingreso', 2),
    ('reembolso', 'Reembolso', 'ingreso', 3),
    ('otros_ingresos', 'Otros ingresos', 'ingreso', 4),
    ('alimentos_mercado', 'Alimentos y mercado', 'gasto', 5),
    ('restaurantes_domicilios', 'Restaurantes y domicilios', 'gasto', 6),
    ('transporte', 'Transporte', 'gasto', 7),
    ('vivienda_servicios', 'Vivienda y servicios', 'gasto', 8),
    ('salud', 'Salud', 'gasto', 9),
    ('educacion', 'Educación', 'gasto', 10),
    ('entretenimiento', 'Entretenimiento', 'gasto', 11),
    ('compras', 'Compras', 'gasto', 12),
    ('comisiones_costos_financieros', 'Comisiones y costos financieros', 'gasto', 13),
    ('otros_gastos', 'Otros gastos', 'gasto', 14),
    ('sin_categoria', 'Sin categoría', 'ninguno', 15);

-- +goose Down
DROP TABLE inteligencia.categoria;
