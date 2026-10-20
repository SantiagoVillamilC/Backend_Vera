-- Nombres visibles para la contraparte del enriquecido.
--
-- La inteligencia solo puede leer el nombre y el alias de una parte, nunca su documento, su NIT ni
-- sus credenciales. El clasificador no recibe estos nombres: la resolución es para mostrar la
-- contraparte, no para categorizar.

-- +goose Up
GRANT USAGE ON SCHEMA auth TO rol_inteligencia;
GRANT SELECT (parte_id, nombre, perfil_nombre) ON auth.personas TO rol_inteligencia;
GRANT SELECT (parte_id, razon_social) ON auth.empresas TO rol_inteligencia;

-- +goose Down
REVOKE SELECT (parte_id, nombre, perfil_nombre) ON auth.personas FROM rol_inteligencia;
REVOKE SELECT (parte_id, razon_social) ON auth.empresas FROM rol_inteligencia;
REVOKE USAGE ON SCHEMA auth FROM rol_inteligencia;
