package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"real_time_system/domain"
	"real_time_system/domain/entity"
	"real_time_system/domain/repository"
	"real_time_system/domain/value_objects"
	"real_time_system/internal/logger"
	"real_time_system/pkg/client"
)

// Compile-time check: проверяем, что CartRepositoryPg реализует интерфейс
var _ repository.CartRepository = (*CartRepositoryPg)(nil)

type CartRepositoryPg struct {
	db client.Querier
}

// NewCartRepository создаёт репозиторий корзин.
// Принимает Querier — может быть *pgxpool.Pool (обычная работа) или pgx.Tx (транзакция).
func NewCartRepository(db client.Querier) *CartRepositoryPg {
	return &CartRepositoryPg{db: db}
}

func (r *CartRepositoryPg) Create(ctx context.Context, cart *entity.Cart) error {
	l := logger.FromContext(ctx)

	q := `
		INSERT INTO carts (id, user_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4) 
	`

	_, err := r.db.Exec(ctx, q,
		cart.ID,
		cart.UserID,
		cart.CreatedAt,
		cart.UpdatedAt,
	)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code == "23505" {
				return domain.NewConflictError("cart user_id already exists")
			}
		}

		l.Errorw("failed to create cart", "error", err, "cart_id", cart.ID)
		return domain.NewInternalError("failed to create cart", err)
	}
	l.Infow("cart created", "cart_id", cart.ID)
	return nil
}

func (r *CartRepositoryPg) FindByID(ctx context.Context, id entity.CartID) (*entity.Cart, error) {
	l := logger.FromContext(ctx)

	q := `
		SELECT id, user_id, created_at, updated_at, deleted_at
		FROM carts WHERE id = $1 AND deleted_at IS NULL
	`

	var cart entity.Cart

	err := r.db.QueryRow(ctx, q, id).Scan(
		&cart.ID,
		&cart.UserID,
		&cart.CreatedAt,
		&cart.UpdatedAt,
		&cart.DeletedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.NewNotFoundError("cart")
		}

		l.Errorw("failed to find cart by id", "error", err, "cart_id", id)
		return nil, domain.NewInternalError("failed to find cart by id", err)
	}

	// ВАЖНО: инициализируем Items map и TotalPrice.
	// При сканировании из БД создаётся zero-value struct:
	// - Items = nil → panic при AddItem
	// - TotalPrice.Currency = "" → ошибка "different currencies" при Add
	cart.Items = make(map[entity.ProductID]entity.CartItem)
	cart.TotalPrice = value_objects.Zero(value_objects.RUB)

	return &cart, nil
}

func (r *CartRepositoryPg) FindByUserID(ctx context.Context, userID entity.UserID) (*entity.Cart, error) {
	l := logger.FromContext(ctx)

	q := `
		SELECT id, user_id, created_at, updated_at, deleted_at
		FROM carts WHERE user_id = $1 AND deleted_at IS NULL
	`

	var cart entity.Cart

	err := r.db.QueryRow(ctx, q, userID).Scan(
		&cart.ID,
		&cart.UserID,
		&cart.CreatedAt,
		&cart.UpdatedAt,
		&cart.DeletedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.NewNotFoundError("cart")
		}

		l.Errorw("failed to find cart by user_id", "error", err, "user_id", userID)
		return nil, domain.NewInternalError("failed to find cart by user_id", err)
	}

	// ВАЖНО: инициализируем Items map и TotalPrice.
	// При сканировании из БД создаётся zero-value struct:
	// - Items = nil → panic при AddItem
	// - TotalPrice.Currency = "" → ошибка "different currencies" при Add
	cart.Items = make(map[entity.ProductID]entity.CartItem)
	cart.TotalPrice = value_objects.Zero(value_objects.RUB)

	return &cart, nil
}

// Update обновляет метаданные корзины.
//
// ПРИМЕЧАНИЕ: Cart имеет только user_id как изменяемое поле (кроме timestamps).
// В реальности user_id корзины не меняется — корзина привязана к пользователю навсегда.
// Этот метод нужен для обновления updated_at при изменении items через CartItemsRepository.
func (r *CartRepositoryPg) Update(ctx context.Context, cart *entity.Cart) error {
	l := logger.FromContext(ctx)

	q := `
		UPDATE carts
		SET updated_at = $1
		WHERE id = $2 AND deleted_at IS NULL
	`

	result, err := r.db.Exec(ctx, q,
		cart.UpdatedAt,
		cart.ID,
	)

	if err != nil {
		l.Errorw("failed to update cart", "error", err, "cart_id", cart.ID)
		return domain.NewInternalError("failed to update cart", err)
	}

	if result.RowsAffected() == 0 {
		return domain.NewNotFoundError("cart")
	}

	l.Infow("cart updated", "cart_id", cart.ID)
	return nil
}

// Delete выполняет soft delete корзины.
//
// ВАЖНО О CASCADE:
// ON DELETE CASCADE в cart_items срабатывает только при физическом DELETE.
// При soft delete (UPDATE deleted_at) items остаются в БД.
//
// ДВА ПОДХОДА в production:
// 1. Clear() + Delete() — сначала удаляем items, потом корзину
// 2. Оставляем items — для истории/аналитики (что было в корзине при удалении)
//
// Мы используем подход 1 — CartService должен вызвать Clear() перед Delete().
func (r *CartRepositoryPg) Delete(ctx context.Context, id entity.CartID) error {
	l := logger.FromContext(ctx)

	q := `
		UPDATE carts
		SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`

	result, err := r.db.Exec(ctx, q, id)

	if err != nil {
		l.Errorw("failed to delete cart", "error", err, "cart_id", id)
		return domain.NewInternalError("failed to delete cart", err)
	}

	if result.RowsAffected() == 0 {
		return domain.NewNotFoundError("cart")
	}

	l.Infow("cart soft deleted", "cart_id", id)
	return nil
}

// GetCartWithItems возвращает корзину со всеми items.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ PRODUCTION ПАТТЕРН: LEFT JOIN ДЛЯ СВЯЗАННЫХ ДАННЫХ                        │
// └──────────────────────────────────────────────────────────────────────────┘
//
// ПОЧЕМУ LEFT JOIN, А НЕ JOIN:
//   - JOIN (INNER JOIN) возвращает строки только если есть совпадение в обеих таблицах
//   - Если корзина пустая (нет items), JOIN вернёт 0 строк → "корзина не найдена"
//   - LEFT JOIN вернёт корзину даже если items нет (ci.* будут NULL)
//
// ПРОБЛЕМА LEFT JOIN — NULLABLE ПОЛЯ:
//   Когда items нет, все ci.* колонки = NULL. Нельзя сканировать NULL в non-pointer типы.
//   Решение: сканируем ci.id в *uuid.UUID, проверяем != nil перед добавлением item.
//
// АЛЬТЕРНАТИВНЫЙ ПОДХОД — ДВА ЗАПРОСА:
//   1. SELECT * FROM carts WHERE id = $1
//   2. SELECT * FROM cart_items WHERE cart_id = $1
//   Плюсы: проще код, нет nullable-проблем
//   Минусы: 2 round-trip к БД вместо 1
//
// Мы используем LEFT JOIN для демонстрации паттерна.
func (r *CartRepositoryPg) GetCartWithItems(ctx context.Context, cartID entity.CartID) (*entity.Cart, error) {
	l := logger.FromContext(ctx)

	// LEFT JOIN: вернёт корзину даже если items пустые
	// c.deleted_at IS NULL: не возвращаем удалённые корзины
	q := `
		SELECT c.id, c.user_id, c.created_at, c.updated_at, c.deleted_at,
		       ci.id, ci.cart_id, ci.product_id, ci.quantity,
		       ci.price_amount, ci.price_currency, ci.created_at, ci.updated_at
		FROM carts c
		LEFT JOIN cart_items ci ON c.id = ci.cart_id
		WHERE c.id = $1 AND c.deleted_at IS NULL
	`

	rows, err := r.db.Query(ctx, q, cartID)
	if err != nil {
		l.Errorw("failed to get cart with items", "error", err, "cart_id", cartID)
		return nil, domain.NewInternalError("failed to get cart with items", err)
	}
	defer rows.Close()

	var cart entity.Cart
	cart.Items = make(map[entity.ProductID]entity.CartItem)

	// Флаг: была ли хотя бы одна строка (корзина существует)
	found := false

	for rows.Next() {
		found = true

		// Nullable поля для LEFT JOIN: если items нет, ci.* = NULL
		var (
			ciID            *string    // uuid как string, nullable
			ciCartID        *string
			ciProductID     *string
			ciQuantity      *int
			ciPriceAmount   *int64
			ciPriceCurrency *string
			ciCreatedAt     *time.Time // timestamp → *time.Time (не *string!)
			ciUpdatedAt     *time.Time
		)

		if err := rows.Scan(
			&cart.ID, &cart.UserID, &cart.CreatedAt, &cart.UpdatedAt, &cart.DeletedAt,
			&ciID, &ciCartID, &ciProductID, &ciQuantity,
			&ciPriceAmount, &ciPriceCurrency, &ciCreatedAt, &ciUpdatedAt,
		); err != nil {
			l.Errorw("failed to scan cart row", "error", err)
			return nil, domain.NewInternalError("failed to scan cart row", err)
		}

		// Если ciID != nil, значит есть item (LEFT JOIN вернул данные из cart_items)
		if ciID != nil {
			productID, _ := entity.ParseProductID(*ciProductID)
			cartItemID, _ := entity.ParseCartItemID(*ciID)
			cartID, _ := entity.ParseCartID(*ciCartID)

			item := entity.CartItem{
				ID:        cartItemID,
				CartID:    cartID,
				ProductID: productID,
				Quantity:  *ciQuantity,
			}
			item.Price.Amount = *ciPriceAmount
			item.Price.Currency = value_objects.Currency(*ciPriceCurrency)

			cart.Items[productID] = item
		}
	}

	if err := rows.Err(); err != nil {
		l.Errorw("error during rows iteration", "error", err)
		return nil, domain.NewInternalError("error during rows iteration", err)
	}

	// Корзина не найдена (0 строк от LEFT JOIN = cart не существует)
	if !found {
		return nil, domain.NewNotFoundError("cart")
	}

	// Пересчитываем TotalPrice из items
	// ПОЧЕМУ ЗДЕСЬ, А НЕ В БД:
	//   - Можно было бы SELECT SUM(price_amount * quantity) в SQL
	//   - Но TotalPrice — это Money (Amount + Currency), нужна валидация
	//   - Domain logic остаётся в Go, не размазывается по SQL
	for _, item := range cart.Items {
		itemCost, err := item.Price.Multiply(int64(item.Quantity))
		if err != nil {
			return nil, domain.NewInternalError("failed to calculate item cost", err)
		}

		// Первый item: TotalPrice ещё не инициализирован (Currency пустая)
		// Просто присваиваем, а не складываем — избегаем ErrCurrencyMismatch
		if cart.TotalPrice.Currency == "" {
			cart.TotalPrice = itemCost
			continue
		}

		cart.TotalPrice, err = cart.TotalPrice.Add(itemCost)
		if err != nil {
			return nil, domain.NewInternalError("failed to calculate total price", err)
		}
	}

	// Пустая корзина: TotalPrice не был инициализирован в цикле.
	// Устанавливаем дефолтную валюту, чтобы избежать ошибки при AddItem.
	if cart.TotalPrice.Currency == "" {
		cart.TotalPrice = value_objects.Zero(value_objects.RUB)
	}

	return &cart, nil
}
