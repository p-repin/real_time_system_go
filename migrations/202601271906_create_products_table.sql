-- +goose Up
CREATE TABLE IF NOT EXISTS products (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description VARCHAR(255),
    -- ПОЧЕМУ INT, а не DECIMAL/NUMERIC:
    -- Money.Amount хранится в минимальных единицах валюты (копейки/центы).
    -- 1500 = 15.00₽. Все операции целочисленные, без потери точности.
    -- INT быстрее и не имеет проблем округления float.
    price_amount INTEGER NOT NULL,

    -- DEFAULT 'RUB' соответствует domain/value_objects/money.go:11 (const RUB)
    -- БЫЛО: 'RU' — это ОШИБКА! 'RU' — это код страны, а не валюта.
    price_currency VARCHAR(10) NOT NULL DEFAULT 'RUB',

    stock INTEGER NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP NULL DEFAULT NULL
);

-- SOFT DELETE: уникальность name только для активных товаров
-- PostgreSQL не поддерживает CONSTRAINT ... WHERE, используем UNIQUE INDEX
CREATE UNIQUE INDEX idx_products_name_unique ON products(name) WHERE deleted_at IS NULL;

-- Partial index: индексируем только активные товары для быстрого поиска
-- WHERE deleted_at IS NULL исключает удалённые записи из индекса → экономия места
CREATE INDEX idx_products_price_amount ON products(price_amount) WHERE deleted_at IS NULL;

-- Индекс для поиска товаров в наличии (stock > 0)
CREATE INDEX idx_products_stock ON products(stock) WHERE deleted_at IS NULL;

-- Индекс для фильтрации удалённых товаров (если понадобится их искать для аудита)
CREATE INDEX idx_products_deleted_at ON products(deleted_at);

-- +goose Down
-- КРИТИЧЕСКИ ВАЖНО: удаляем ПРАВИЛЬНУЮ таблицу!
-- БЫЛО: DROP TABLE IF EXISTS users; — это уничтожило бы всех пользователей в production!
DROP TABLE IF EXISTS products;