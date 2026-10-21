-- +goose Up
ALTER TABLE auth.textos_consentimiento DROP CONSTRAINT textos_consentimiento_tipo_check;
ALTER TABLE auth.textos_consentimiento ADD CONSTRAINT textos_consentimiento_tipo_check
CHECK (tipo IN ('tratamiento_datos','whatsapp_optin','biometricos','asistente_ia'));
ALTER TABLE auth.consentimientos DROP CONSTRAINT consentimientos_tipo_check;
ALTER TABLE auth.consentimientos ADD CONSTRAINT consentimientos_tipo_check
CHECK (tipo IN ('tratamiento_datos','whatsapp_optin','biometricos','asistente_ia'));

-- Orden total del registro incluso si dos eventos comparten fecha.
ALTER TABLE auth.consentimientos ADD COLUMN orden bigint GENERATED ALWAYS AS IDENTITY;
GRANT USAGE, SELECT ON SEQUENCE auth.consentimientos_orden_seq TO rol_auth;
CREATE INDEX consentimientos_asistente_ultimo ON auth.consentimientos (parte_id, orden DESC)
WHERE tipo = 'asistente_ia';

INSERT INTO auth.textos_consentimiento (tipo, version, texto) VALUES
('asistente_ia', 'v1', '[BORRADOR PARA REVISIÓN DEL EQUIPO Y ASESOR; NO ES UN TEXTO LEGAL APROBADO] Autorizo de forma opcional el uso del asistente Vera para consultar mi saldo, límites y movimientos y preparar transferencias que requieren mi confirmación. Para responder, el backend podrá enviar mis mensajes, audio y los fragmentos necesarios de información financiera a un proveedor externo de inteligencia artificial, inicialmente Gemini de Google, que puede procesarlos fuera de Colombia. Se minimizan los datos y se enmascaran nombres y celulares. El proveedor y su modalidad deben informarse antes del uso: en modalidades gratuitas el contenido podría utilizarse para mejorar modelos; esta autorización no garantiza su exclusión de entrenamiento. Las conversaciones se conservarán durante 30 días sin actividad cuando se habilite la política de retención; el registro mínimo de seguridad y los movimientos financieros tienen retención independiente. Puedo revocar esta autorización desde mi perfil y dejar de utilizar el asistente. Revocar no borra automáticamente mi historial; puedo solicitar su borrado por separado. Vera no mueve dinero sin mi confirmación explícita. Un cambio de proveedor o de estas condiciones requiere una nueva versión y autorización.');

-- +goose Down
-- No eliminar evidencia para permitir rollback. Si ya hubo eventos IA, falla cerrado.
ALTER TABLE auth.consentimientos DROP CONSTRAINT consentimientos_tipo_check;
ALTER TABLE auth.consentimientos ADD CONSTRAINT consentimientos_tipo_check
CHECK (tipo IN ('tratamiento_datos','whatsapp_optin','biometricos'));
DELETE FROM auth.textos_consentimiento WHERE tipo = 'asistente_ia';
ALTER TABLE auth.textos_consentimiento DROP CONSTRAINT textos_consentimiento_tipo_check;
ALTER TABLE auth.textos_consentimiento ADD CONSTRAINT textos_consentimiento_tipo_check
CHECK (tipo IN ('tratamiento_datos','whatsapp_optin','biometricos'));
DROP INDEX auth.consentimientos_asistente_ultimo;
ALTER TABLE auth.consentimientos DROP COLUMN orden;
