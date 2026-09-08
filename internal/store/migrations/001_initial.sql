-- +goose Up
CREATE TABLE users (
    id BIGSERIAL PRIMARY KEY,
    login TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE orders (
    -- Хеш позволяет индексировать номера произвольной длины.
    number_hash BYTEA PRIMARY KEY,
    number TEXT NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id),
    status TEXT NOT NULL DEFAULT 'NEW'
        CHECK (status IN ('NEW', 'PROCESSING', 'INVALID', 'PROCESSED')),
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX orders_user_uploaded_at_idx ON orders (user_id, uploaded_at DESC);
