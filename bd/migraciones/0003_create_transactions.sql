-- +goose Up
CREATE TABLE transactions (id UUID PRIMARY KEY, from_wallet_id UUID NOT NULL REFERENCES wallets(id), to_wallet_id UUID NOT NULL REFERENCES wallets(id), amount NUMERIC(14,2) NOT NULL CHECK (amount > 0), status TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now());

-- +goose Down
DROP TABLE transactions;
