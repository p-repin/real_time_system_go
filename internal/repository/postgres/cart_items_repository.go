package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"real_time_system/domain"
	"real_time_system/domain/entity"
	"real_time_system/domain/repository"
	"real_time_system/internal/logger"
	"real_time_system/pkg/client"
)

// Compile-time check: проверяем, что CartItemsRepositoryPg реализует интерфейс
var _ repository.CartItemRepository = (*CartItemsRepositoryPg)(nil)

type CartItemsRepositoryPg struct {
	db client.Querier
}

// NewCartItemsRepository создаёт репозиторий элементов корзины.
// Принимает Querier — может быть *pgxpool.Pool (обычная работа) или pgx.Tx (транзакция).
func NewCartItemsRepository(db client.Querier) *CartItemsRepositoryPg {
	return &CartItemsRepositoryPg{db: db}
}

// AddItem добавляет товар в корзину или увеличивает количество существующего.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ PRODUCTION ПАТТЕРН: UPSERT (INSERT ... ON CONFLICT DO UPDATE)            │
// └──────────────────────────────────────────────────────────────────────────┘
//
// ПРОБЛЕМА БЕЗ UPSERT:
//   1. SELECT * FROM cart_items WHERE cart_id = $1 AND product_id = $2
//   2. IF exists: UPDATE ... ELSE: INSERT ...
//   Race condition: два запроса одновременно видят "не существует" → два INSERT → ошибка.
//
// РЕШЕНИЕ — UPSERT (атомарная операция):
//   INSERT ... ON CONFLICT (cart_id, product_id) DO UPDATE SET quantity = quantity + EXCLUDED.quantity
//
// EXCLUDED — специальная таблица PostgreSQL, содержащая значения, которые пытались вставить.
//   - EXCLUDED.quantity = quantity из VALUES (новое количество)
//   - cart_items.quantity = текущее количество в БД
//   - Результат: cart_items.quantity + EXCLUDED.quantity (суммируем)
//
// ПОЧЕМУ (cart_id, product_id), А НЕ (id):
//   - id — это первичный ключ, он всегда уникален (новый UUID при каждом вызове)
//   - Бизнес-уникальность: "один товар в одной корзине" = (cart_id, product_id)
//   - ON CONFLICT работает с UNIQUE constraint, который мы создали в миграции
func (r *CartItemsRepositoryPg) AddItem(ctx context.Context, item *entity.CartItem) error {
	l := logger.FromContext(ctx)

	q := `
		INSERT INTO cart_items (id, cart_id, product_id, quantity, price_amount, price_currency, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (cart_id, product_id) DO UPDATE SET
			quantity = cart_items.quantity + EXCLUDED.quantity,
			updated_at = EXCLUDED.updated_at
	`

	_, err := r.db.Exec(ctx, q,
		item.ID,
		item.CartID,
		item.ProductID,
		item.Quantity,
		item.Price.Amount,
		item.Price.Currency,
		item.CreatedAt,
		item.UpdatedAt,
	)

	if err != nil {
		// 23503 = foreign_key_violation (cart_id или product_id не существует)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code == "23503" {
				return domain.NewNotFoundError("cart or product")
			}
		}
		l.Errorw("failed to add item to cart", "error", err, "cart_item_id", item.ID)
		return domain.NewInternalError("failed to add item to cart", err)
	}

	l.Infow("item added to cart", "cart_id", item.CartID, "product_id", item.ProductID, "quantity", item.Quantity)
	return nil
}

// UpdateQuantity обновляет количество товара в корзине.
//
// ПОЧЕМУ ОТДЕЛЬНЫЙ МЕТОД, А НЕ UPSERT:
//   - AddItem увеличивает quantity (добавление в корзину)
//   - UpdateQuantity устанавливает точное значение (редактирование корзины)
//   - Разная семантика: +5 vs =5
//
// ВАЛИДАЦИЯ quantity <= 0:
//   Должна происходить в Service-слое, не здесь. Repository — это "тупое" хранилище,
//   оно не знает бизнес-правил. Если Service решил установить quantity = 0,
//   значит это осознанное решение (например, перед удалением).
func (r *CartItemsRepositoryPg) UpdateQuantity(ctx context.Context, itemID entity.CartItemID, quantity int) error {
	l := logger.FromContext(ctx)

	q := `
		UPDATE cart_items
		SET quantity = $1, updated_at = NOW()
		WHERE id = $2
	`

	result, err := r.db.Exec(ctx, q, quantity, itemID)
	if err != nil {
		l.Errorw("failed to update item quantity", "error", err, "cart_item_id", itemID)
		return domain.NewInternalError("failed to update item quantity", err)
	}

	if result.RowsAffected() == 0 {
		return domain.NewNotFoundError("cart_item")
	}

	l.Infow("cart item quantity updated", "cart_item_id", itemID, "new_quantity", quantity)
	return nil
}

// RemoveItem удаляет товар из корзины (физическое удаление).
//
// ПОЧЕМУ HARD DELETE, А НЕ SOFT DELETE:
//   - cart_items — это не самостоятельная сущность, а часть Cart aggregate
//   - Удаление item из корзины — это бизнес-операция "убрать товар", не "пометить удалённым"
//   - Для аналитики ("что удаляли из корзин") используются события, а не soft delete
//   - Soft delete cart_items усложнил бы все запросы (WHERE deleted_at IS NULL везде)
func (r *CartItemsRepositoryPg) RemoveItem(ctx context.Context, itemID entity.CartItemID) error {
	l := logger.FromContext(ctx)

	q := `DELETE FROM cart_items WHERE id = $1`

	result, err := r.db.Exec(ctx, q, itemID)
	if err != nil {
		l.Errorw("failed to remove cart item", "error", err, "item_id", itemID)
		return domain.NewInternalError("failed to remove cart item", err)
	}

	if result.RowsAffected() == 0 {
		return domain.NewNotFoundError("cart_item")
	}

	l.Infow("cart item removed", "item_id", itemID)
	return nil
}

// GetItemsByCartID возвращает все items корзины.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ PRODUCTION ПАТТЕРН: ПУСТОЙ РЕЗУЛЬТАТ ≠ ОШИБКА                            │
// └──────────────────────────────────────────────────────────────────────────┘
//
// ПРОБЛЕМА:
//   Пустая корзина — это нормальный сценарий (новый пользователь, очистил корзину).
//   Если возвращать NotFoundError, вызывающий код не отличит:
//   - "корзина существует, но пуста" (HTTP 200, пустой массив)
//   - "корзина не существует" (HTTP 404)
//
// РЕШЕНИЕ:
//   Возвращаем ([]CartItem{}, nil) для пустой корзины.
//   Вызывающий код получает пустой slice — это валидный результат.
//
// КАК ПРОВЕРИТЬ "КОРЗИНА НЕ СУЩЕСТВУЕТ":
//   Это задача CartRepository.FindByID(), а не CartItemsRepository.
//   Сначала проверяем существование корзины, потом запрашиваем items.
func (r *CartItemsRepositoryPg) GetItemsByCartID(ctx context.Context, cartID entity.CartID) ([]*entity.CartItem, error) {
	l := logger.FromContext(ctx)

	q := `
		SELECT id, cart_id, product_id, quantity, price_amount, price_currency, created_at, updated_at
		FROM cart_items
		WHERE cart_id = $1
	`

	rows, err := r.db.Query(ctx, q, cartID)
	if err != nil {
		l.Errorw("failed to get items by cart_id", "error", err, "cart_id", cartID)
		return nil, domain.NewInternalError("failed to get items by cart_id", err)
	}
	defer rows.Close()

	// Pre-allocation: если знаем примерное количество items, можно указать capacity.
	// Для корзины обычно 1-20 items, но мы не знаем заранее → начинаем с 0.
	cartItems := make([]*entity.CartItem, 0)

	for rows.Next() {
		var item entity.CartItem

		if err := rows.Scan(
			&item.ID,
			&item.CartID,
			&item.ProductID,
			&item.Quantity,
			&item.Price.Amount,
			&item.Price.Currency,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			l.Errorw("failed to scan cart item", "error", err)
			return nil, domain.NewInternalError("failed to scan cart item", err)
		}

		cartItems = append(cartItems, &item)
	}

	if err := rows.Err(); err != nil {
		l.Errorw("error during rows iteration", "error", err)
		return nil, domain.NewInternalError("error during rows iteration", err)
	}

	// Пустой slice — это OK, не ошибка
	return cartItems, nil
}

// Clear удаляет все items из корзины.
//
// КОГДА ИСПОЛЬЗУЕТСЯ:
//   1. Пользователь нажал "Очистить корзину"
//   2. Перед soft delete корзины (CartRepository.Delete)
//   3. После успешного оформления заказа (OrderService.PlaceOrder)
//
// ПОЧЕМУ НЕ ПРОВЕРЯЕМ RowsAffected:
//   Очистка пустой корзины — не ошибка. Результат одинаковый: корзина пуста.
//   Если бы мы возвращали NotFound при RowsAffected == 0, то:
//   - Два вызова Clear() подряд: первый OK, второй NotFound — неконсистентно
//   - Идемпотентность нарушена (повторный вызов даёт другой результат)
func (r *CartItemsRepositoryPg) Clear(ctx context.Context, cartID entity.CartID) error {
	l := logger.FromContext(ctx)

	q := `DELETE FROM cart_items WHERE cart_id = $1`

	_, err := r.db.Exec(ctx, q, cartID)
	if err != nil {
		l.Errorw("failed to clear cart items", "error", err, "cart_id", cartID)
		return domain.NewInternalError("failed to clear cart items", err)
	}

	l.Infow("cart items cleared", "cart_id", cartID)
	return nil
}
