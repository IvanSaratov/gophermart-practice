CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY,
    login TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS orders (
    -- Длинный TEXT нельзя безопасно использовать как ключ при индексации: размер элемента индекса ограничен.
    -- он имеет фиксированные 32 байта, а исходный номер хранится рядом для проверки коллизий
    -- нашел такую проблему при генерации тестов с большими числами
    number_hash BYTEA PRIMARY KEY,
    number TEXT NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id),
    status TEXT NOT NULL DEFAULT 'NEW'
        CHECK (status IN ('NEW', 'PROCESSING', 'INVALID', 'PROCESSED')),
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS orders_user_uploaded_at_idx
    ON orders (user_id, uploaded_at DESC);
