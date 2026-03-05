package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"real_time_system/domain"
	"real_time_system/domain/entity"
	"real_time_system/domain/repository"
	"real_time_system/domain/value_objects"
	"real_time_system/internal/logger"
	"real_time_system/pkg/client"
)

// Compile-time check: проверяем, что OrderRepositoryPg реализует интерфейс.
var _ repository.OrderRepository = (*OrderRepositoryPg)(nil)

type OrderRepositoryPg struct {
	db client.Querier
}

// NewOrderRepository создаёт репозиторий заказов.
// Принимает Querier — может быть *pgxpool.Pool (обычная работа) или pgx.Tx (транзакция).
func NewOrderRepository(db client.Querier) *OrderRepositoryPg {
	return &OrderRepositoryPg{db: db}
}

// Create создаёт заказ и все его items.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ВАЖНО: АТОМАРНОСТЬ СОЗДАНИЯ ЗАКАЗА                                        │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Заказ и его items должны создаваться атомарно:
//   - Если db — это pgx.Tx, то всё в одной транзакции (правильно)
//   - Если db — это Pool, каждый INSERT — отдельная операция (опасно!)
//
// Рекомендация: для Create всегда передавать pgx.Tx из сервиса.
func (r *OrderRepositoryPg) Create(ctx context.Context, order *entity.Order) error {
	l := logger.FromContext(ctx)

	// 1. Создаём заказ
	orderQuery := `
		INSERT INTO orders (id, user_id, status, total_amount, total_currency, paid_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err := r.db.Exec(ctx, orderQuery,
		order.ID,
		order.UserID,
		order.Status,
		order.TotalAmount.Amount,
		order.TotalAmount.Currency,
		order.PaidAt,
		order.CreatedAt,
		order.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code == "23503" { // foreign_key_violation
				return domain.NewNotFoundError("user")
			}
		}
		l.Errorw("failed to create order", "error", err, "order_id", order.ID)
		return domain.NewInternalError("failed to create order", err)
	}

	// 2. Создаём items заказа
	// SNAPSHOT PATTERN: сохраняем product_name на момент заказа
	itemQuery := `
		INSERT INTO order_items (id, order_id, product_id, product_name, quantity, price_amount, price_currency, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
	`

	for _, item := range order.Items {
		_, err := r.db.Exec(ctx, itemQuery,
			entity.NewOrderItemID(), // генерируем ID для каждого item
			order.ID,
			item.ProductID,
			item.ProductName, // snapshot: название товара на момент заказа
			item.Quantity,
			item.Price.Amount,
			item.Price.Currency,
		)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) {
				if pgErr.Code == "23503" { // foreign_key_violation
					return domain.NewNotFoundError("product")
				}
			}
			l.Errorw("failed to create order item", "error", err, "order_id", order.ID, "product_id", item.ProductID)
			return domain.NewInternalError("failed to create order item", err)
		}
	}

	l.Infow("order created", "order_id", order.ID, "items_count", len(order.Items))
	return nil
}

// FindByID возвращает заказ со всеми items.
//
// Используем LEFT JOIN — заказ вернётся даже если items пусты (хотя это аномалия).
func (r *OrderRepositoryPg) FindByID(ctx context.Context, id entity.OrderID) (*entity.Order, error) {
	l := logger.FromContext(ctx)

	query := `
		SELECT o.id, o.user_id, o.status, o.total_amount, o.total_currency,
		       o.paid_at, o.created_at, o.updated_at,
		       oi.product_id, oi.product_name, oi.quantity, oi.price_amount, oi.price_currency
		FROM orders o
		LEFT JOIN order_items oi ON o.id = oi.order_id
		WHERE o.id = $1
	`

	rows, err := r.db.Query(ctx, query, id)
	if err != nil {
		l.Errorw("failed to find order by id", "error", err, "order_id", id)
		return nil, domain.NewInternalError("failed to find order", err)
	}
	defer rows.Close()

	var order *entity.Order
	items := make([]entity.OrderItem, 0)

	for rows.Next() {
		var (
			oID            entity.OrderID
			oUserID        entity.UserID
			oStatus        entity.OrderStatus
			oTotalAmount   int64
			oTotalCurrency string
			oPaidAt        *time.Time // nullable timestamp
			oCreatedAt     time.Time
			oUpdatedAt     time.Time
			// Nullable поля для LEFT JOIN (order_items может быть пустым)
			oiProductID     *string
			oiProductName   *string // snapshot: название товара на момент заказа
			oiQuantity      *int
			oiPriceAmount   *int64
			oiPriceCurrency *string
		)

		if err := rows.Scan(
			&oID, &oUserID, &oStatus, &oTotalAmount, &oTotalCurrency,
			&oPaidAt, &oCreatedAt, &oUpdatedAt,
			&oiProductID, &oiProductName, &oiQuantity, &oiPriceAmount, &oiPriceCurrency,
		); err != nil {
			l.Errorw("failed to scan order row", "error", err)
			return nil, domain.NewInternalError("failed to scan order", err)
		}

		// Инициализируем order только один раз (первая строка)
		if order == nil {
			order = &entity.Order{
				ID:     oID,
				UserID: oUserID,
				Status: oStatus,
				TotalAmount: value_objects.Money{
					Amount:   oTotalAmount,
					Currency: value_objects.Currency(oTotalCurrency),
				},
				PaidAt:    oPaidAt,
				CreatedAt: oCreatedAt,
				UpdatedAt: oUpdatedAt,
				Items:     []entity.OrderItem{},
			}
		}

		// Если есть item (LEFT JOIN вернул данные)
		if oiProductID != nil {
			productID, _ := entity.ParseProductID(*oiProductID)
			item := entity.OrderItem{
				ProductID:   productID,
				ProductName: *oiProductName, // snapshot: название из БД
				Quantity:    *oiQuantity,
				Price: value_objects.Money{
					Amount:   *oiPriceAmount,
					Currency: value_objects.Currency(*oiPriceCurrency),
				},
			}
			items = append(items, item)
		}
	}

	if err := rows.Err(); err != nil {
		l.Errorw("error iterating order rows", "error", err)
		return nil, domain.NewInternalError("failed to iterate order rows", err)
	}

	if order == nil {
		return nil, domain.NewNotFoundError("order")
	}

	order.Items = items
	return order, nil
}

// FindByUserID возвращает все заказы пользователя (без items для списка).
func (r *OrderRepositoryPg) FindByUserID(ctx context.Context, userID entity.UserID) ([]*entity.Order, error) {
	l := logger.FromContext(ctx)

	query := `
		SELECT id, user_id, status, total_amount, total_currency, paid_at, created_at, updated_at
		FROM orders
		WHERE user_id = $1
		ORDER BY created_at DESC
	`

	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		l.Errorw("failed to find orders by user_id", "error", err, "user_id", userID)
		return nil, domain.NewInternalError("failed to find orders", err)
	}
	defer rows.Close()

	orders := make([]*entity.Order, 0)

	for rows.Next() {
		var order entity.Order
		var totalCurrency string

		if err := rows.Scan(
			&order.ID,
			&order.UserID,
			&order.Status,
			&order.TotalAmount.Amount,
			&totalCurrency,
			&order.PaidAt,
			&order.CreatedAt,
			&order.UpdatedAt,
		); err != nil {
			l.Errorw("failed to scan order", "error", err)
			return nil, domain.NewInternalError("failed to scan order", err)
		}

		order.TotalAmount.Currency = value_objects.Currency(totalCurrency)
		orders = append(orders, &order)
	}

	if err := rows.Err(); err != nil {
		l.Errorw("error iterating orders", "error", err)
		return nil, domain.NewInternalError("failed to iterate orders", err)
	}

	return orders, nil
}

// Update обновляет статус заказа и paid_at.
func (r *OrderRepositoryPg) Update(ctx context.Context, order *entity.Order) error {
	l := logger.FromContext(ctx)

	query := `
		UPDATE orders
		SET status = $1, paid_at = $2, updated_at = $3
		WHERE id = $4
	`

	result, err := r.db.Exec(ctx, query,
		order.Status,
		order.PaidAt,
		order.UpdatedAt,
		order.ID,
	)
	if err != nil {
		l.Errorw("failed to update order", "error", err, "order_id", order.ID)
		return domain.NewInternalError("failed to update order", err)
	}

	if result.RowsAffected() == 0 {
		return domain.NewNotFoundError("order")
	}

	l.Infow("order updated", "order_id", order.ID, "status", order.Status)
	return nil
}

// Delete удаляет заказ (физическое удаление).
// В production обычно используется soft delete или архивация.
func (r *OrderRepositoryPg) Delete(ctx context.Context, id entity.OrderID) error {
	l := logger.FromContext(ctx)

	query := `DELETE FROM orders WHERE id = $1`

	result, err := r.db.Exec(ctx, query, id)
	if err != nil {
		l.Errorw("failed to delete order", "error", err, "order_id", id)
		return domain.NewInternalError("failed to delete order", err)
	}

	if result.RowsAffected() == 0 {
		return domain.NewNotFoundError("order")
	}

	l.Infow("order deleted", "order_id", id)
	return nil
}
