-- +goose Up
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    surname VARCHAR(255),
    email VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP NULL DEFAULT NULL
);

-- Partial UNIQUE INDEX: уникальность email только для НЕ удалённых пользователей
-- PostgreSQL не поддерживает CONSTRAINT ... WHERE, но поддерживает UNIQUE INDEX ... WHERE
-- Это позволяет повторно использовать email после soft delete
CREATE UNIQUE INDEX idx_users_email_unique ON users(email) WHERE deleted_at IS NULL;

-- Индекс для фильтрации удалённых пользователей (если понадобится их искать)
CREATE INDEX idx_users_deleted_at ON users(deleted_at);

-- +goose Down
DROP TABLE IF EXISTS users;
