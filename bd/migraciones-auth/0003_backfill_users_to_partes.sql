-- Backfill de public.users al modelo de partes de auth y congelamiento de la tabla (AUT-A1-02).

-- +goose Up
INSERT INTO auth.partes (id, tipo, creada_en)
SELECT u.id, 'persona', u.created_at
FROM public.users u
ON CONFLICT (id) DO NOTHING;

-- El celular de relleno deriva del uuid y nunca se verifica: la cuenta sin_login no autentica ni recibe por celular.
INSERT INTO auth.credenciales (id, parte_id, correo, celular, valor_derivado, estado, creada_en, actualizada_en)
SELECT gen_random_uuid(), u.id, u.email, '+0' || replace(u.id::text, '-', ''), NULL, 'sin_login', u.created_at, u.created_at
FROM public.users u
ON CONFLICT DO NOTHING;

REVOKE INSERT ON public.users FROM rol_api;

-- +goose Down
GRANT INSERT ON public.users TO rol_api;

DELETE FROM auth.credenciales c
USING public.users u
WHERE c.parte_id = u.id
  AND c.estado = 'sin_login';
