package repository

import (
	"context"
	"real_time_system/domain/entity"
)

type CartItemRepository interface {
	AddItem(ctx context.Context, item *entity.CartItem) error
	UpdateQuantity(ctx context.Context, itemID entity.CartItemID, quantity int) error
	RemoveItem(ctx context.Context, itemID entity.CartItemID) error
	GetItemsByCartID(ctx context.Context, cartID entity.CartID) ([]*entity.CartItem, error)
	Clear(ctx context.Context, cartID entity.CartID) error
}
