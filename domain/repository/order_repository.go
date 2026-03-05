package repository

import (
	"context"
	"real_time_system/domain/entity"
)

type OrderRepository interface {
	// Create создаёт заказ с его items в одной транзакции.
	Create(ctx context.Context, order *entity.Order) error

	// FindByID возвращает заказ со всеми items.
	FindByID(ctx context.Context, id entity.OrderID) (*entity.Order, error)

	// FindByUserID возвращает все заказы пользователя (без items для списка).
	FindByUserID(ctx context.Context, userID entity.UserID) ([]*entity.Order, error)

	// Update обновляет статус заказа (и paid_at при оплате).
	Update(ctx context.Context, order *entity.Order) error

	// Delete удаляет заказ (обычно не используется в production — заказы архивируются).
	Delete(ctx context.Context, id entity.OrderID) error
}
