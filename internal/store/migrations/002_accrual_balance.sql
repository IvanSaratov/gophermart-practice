-- +goose Up
-- Баланс и начисления хранятся в сотых долях.
ALTER TABLE users
    ADD COLUMN balance BIGINT NOT NULL DEFAULT 0 CHECK (balance >= 0);

ALTER TABLE orders
    ADD COLUMN accrual BIGINT CHECK (accrual >= 0),
    ADD CONSTRAINT orders_accrual_processed_check
        CHECK (accrual IS NULL OR status = 'PROCESSED');
