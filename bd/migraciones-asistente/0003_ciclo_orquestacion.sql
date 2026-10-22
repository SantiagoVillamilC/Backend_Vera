-- SCRUM-124: exclusión temporal sin mantener una transacción durante llamadas a IA.
-- +goose Up
ALTER TABLE asistente.conversacion
    ADD COLUMN ciclo_token uuid,
    ADD COLUMN ciclo_expira_en timestamptz,
    ADD CONSTRAINT conversacion_ciclo_completo CHECK ((ciclo_token IS NULL) = (ciclo_expira_en IS NULL));
GRANT UPDATE (ciclo_token, ciclo_expira_en) ON asistente.conversacion TO rol_asistente;

-- +goose Down
ALTER TABLE asistente.conversacion DROP CONSTRAINT conversacion_ciclo_completo,
    DROP COLUMN ciclo_token, DROP COLUMN ciclo_expira_en;
