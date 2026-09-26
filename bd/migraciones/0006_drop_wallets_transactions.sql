-- +goose Up
DROP TABLE IF EXISTS public.transactions;
DROP TABLE IF EXISTS public.wallets;

-- +goose Down
-- Restaura en public las tablas tal como las dejaron 0002 y 0003; el NUMERIC(14,2) es el esquema viejo, no diseño.
CREATE TABLE public.wallets (id UUID PRIMARY KEY, user_id UUID NOT NULL REFERENCES public.users(id), name TEXT NOT NULL, balance NUMERIC(14,2) NOT NULL DEFAULT 0);
CREATE TABLE public.transactions (id UUID PRIMARY KEY, from_wallet_id UUID NOT NULL REFERENCES public.wallets(id), to_wallet_id UUID NOT NULL REFERENCES public.wallets(id), amount NUMERIC(14,2) NOT NULL CHECK (amount > 0), status TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now());
