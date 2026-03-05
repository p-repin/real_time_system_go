-- +goose Up
CREATE TABLE IF NOT EXISTS carts (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now(),
    deleted_at TIMESTAMP NULL DEFAULT NULL
);

-- Один пользователь = одна активная корзина
-- PostgreSQL не поддерживает CONSTRAINT ... WHERE, используем UNIQUE INDEX
-- UNIQUE INDEX также обеспечивает быстрый поиск по user_id (отдельный INDEX не нужен)
CREATE UNIQUE INDEX idx_carts_user_id_unique ON carts(user_id) WHERE deleted_at IS NULL;

CREATE INDEX idx_carts_deleted_at ON carts(deleted_at);

-- +goose Down
DROP TABLE IF EXISTS carts;