-- El catálogo curado solo admite las categorías que se ofrecen al usuario.
--
-- La regla vive una sola vez, en categoria_es_visible: la usan el cargador para rechazar el archivo
-- antes de escribir, el disparador de catalogo_marca para que la clave foránea no admita
-- sin_categoria por la puerta de atrás y la consulta que recalcula la categoría desde la memoria. La
-- cascada decide en memoria y no puede consultar la base por fila, así que su lista de 14 códigos es
-- una copia declarada que una prueba de integración compara contra esta función.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION inteligencia.categoria_es_visible(codigo text) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT EXISTS (
        SELECT 1
          FROM inteligencia.categoria c
         WHERE c.codigo = $1
           AND c.grupo <> 'ninguno'
    )
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION inteligencia.catalogo_marca_categoria_visible() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT inteligencia.categoria_es_visible(NEW.categoria) THEN
        RAISE EXCEPTION 'la categoría % del catálogo curado no es visible', NEW.categoria
            USING ERRCODE = 'foreign_key_violation';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER catalogo_marca_solo_categoria_visible
    BEFORE INSERT OR UPDATE OF categoria ON inteligencia.catalogo_marca
    FOR EACH ROW EXECUTE FUNCTION inteligencia.catalogo_marca_categoria_visible();

-- +goose Down
DROP TRIGGER catalogo_marca_solo_categoria_visible ON inteligencia.catalogo_marca;
DROP FUNCTION inteligencia.catalogo_marca_categoria_visible();
DROP FUNCTION inteligencia.categoria_es_visible(text);
