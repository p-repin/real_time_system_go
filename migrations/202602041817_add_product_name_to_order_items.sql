-- +goose Up

-- ┌──────────────────────────────────────────────────────────────────────────┐
-- │ SNAPSHOT PATTERN: Сохраняем название товара на момент заказа              │
-- └──────────────────────────────────────────────────────────────────────────┘
--
-- ЗАЧЕМ:
-- Заказ — это юридический документ. Если товар переименуют или удалят,
-- клиент должен видеть то название, которое было при покупке.
--
-- PRODUCTION MIGRATION (если есть данные):
-- В реальном проекте миграция была бы в 3 шага:
--   1. ALTER TABLE ADD COLUMN product_name VARCHAR(255) NULL;
--   2. UPDATE order_items SET product_name = (SELECT name FROM products WHERE id = product_id);
--   3. ALTER TABLE ALTER COLUMN product_name SET NOT NULL;
--
-- Для нового проекта (нет данных) — упрощённый вариант с временным DEFAULT:

ALTER TABLE order_items ADD COLUMN product_name VARCHAR(255) NOT NULL DEFAULT '';

-- Убираем DEFAULT после создания (новые записи должны явно указывать название)
ALTER TABLE order_items ALTER COLUMN product_name DROP DEFAULT;


-- +goose Down

ALTER TABLE order_items DROP COLUMN product_name;
