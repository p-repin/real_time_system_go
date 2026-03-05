-- +goose Up

-- ┌──────────────────────────────────────────────────────────────────────────┐
-- │ ORDERS — ЗАКАЗЫ ПОЛЬЗОВАТЕЛЕЙ                                             │
-- └──────────────────────────────────────────────────────────────────────────┘
--
-- Order — это snapshot корзины на момент оформления.
-- После создания заказа изменяются только status и paid_at.
-- Items заказа неизменяемы (юридическое требование).

CREATE TABLE IF NOT EXISTS orders (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),

    -- Статус заказа (state machine в Go: pending → paid → shipped → delivered)
    -- Также возможен cancelled из pending/paid
    status VARCHAR(20) NOT NULL DEFAULT 'pending',

    -- Общая сумма заказа (вычисляется при создании из order_items)
    -- Хранится денормализованно для быстрого доступа без JOIN
    total_amount BIGINT NOT NULL,
    total_currency VARCHAR(10) NOT NULL DEFAULT 'RUB',

    -- Время оплаты (NULL если не оплачен)
    paid_at TIMESTAMP NULL,

    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

-- Индекс для получения заказов пользователя (частый запрос)
CREATE INDEX idx_orders_user_id ON orders(user_id);

-- Индекс для фильтрации по статусу (аналитика, админка)
CREATE INDEX idx_orders_status ON orders(status);


-- ┌──────────────────────────────────────────────────────────────────────────┐
-- │ ORDER_ITEMS — ПОЗИЦИИ ЗАКАЗА                                              │
-- └──────────────────────────────────────────────────────────────────────────┘
--
-- ПОЧЕМУ price хранится в order_items, а не берётся из products:
-- Цена товара может измениться после оформления заказа.
-- Заказ должен хранить цену НА МОМЕНТ ПОКУПКИ (юридическое требование).

CREATE TABLE IF NOT EXISTS order_items (
    id UUID PRIMARY KEY,
    order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE RESTRICT,

    quantity INTEGER NOT NULL,

    -- Цена за единицу на момент заказа (фиксируется, не меняется)
    price_amount BIGINT NOT NULL,
    price_currency VARCHAR(10) NOT NULL DEFAULT 'RUB',

    created_at TIMESTAMP NOT NULL DEFAULT now()
);

-- Индекс для получения items заказа
CREATE INDEX idx_order_items_order_id ON order_items(order_id);

-- Уникальность: один product в одном order только один раз
-- (если нужно 2 шт — увеличиваем quantity, не создаём вторую запись)
CREATE UNIQUE INDEX idx_order_items_order_product ON order_items(order_id, product_id);


-- +goose Down
DROP TABLE IF EXISTS order_items;
DROP TABLE IF EXISTS orders;
