package repository

import (
	"context"
	"real_time_system/domain/entity"
)

type CartRepository interface {
	Create(ctx context.Context, cart *entity.Cart) error
	FindByID(ctx context.Context, id entity.CartID) (*entity.Cart, error)
	FindByUserID(ctx context.Context, userID entity.UserID) (*entity.Cart, error)
	Update(ctx context.Context, cart *entity.Cart) error
	Delete(ctx context.Context, id entity.CartID) error
	GetCartWithItems(ctx context.Context, cartID entity.CartID) (*entity.Cart, error)
}
