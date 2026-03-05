package repository

import (
	"context"
	"real_time_system/domain/entity"
	"real_time_system/domain/value_objects"
)

type ProductRepository interface {
	Create(ctx context.Context, product *entity.Product) error
	FindByID(ctx context.Context, id entity.ProductID) (*entity.Product, error)
	Update(ctx context.Context, product *entity.Product) error
	Delete(ctx context.Context, id entity.ProductID) error
	FindByPriceRange(ctx context.Context, min, max value_objects.Money) ([]*entity.Product, error)

	// FindByIDs возвращает продукты по списку ID.
	//
	// ЗАЧЕМ НУЖЕН ЭТОТ МЕТОД:
	// CartService при формировании CartResponse должен обогатить данные —
	// добавить ProductName к каждому CartItem. Без batch-метода пришлось бы
	// делать N запросов FindByID (N+1 problem).
	//
	// ПОВЕДЕНИЕ:
	// - Возвращает только найденные продукты (удалённые/несуществующие пропускаются)
	// - Пустой slice ids → пустой результат (не ошибка)
	// - Порядок результата не гарантирован (используй map для lookup)
	FindByIDs(ctx context.Context, ids []entity.ProductID) ([]*entity.Product, error)

	// DecrementStock уменьшает stock продукта на указанное количество.
	//
	// ИСПОЛЬЗУЕТСЯ: при создании заказа (PlaceOrder) для резервирования товара.
	//
	// ОШИБКИ:
	// - ErrNotFound: продукт не найден
	// - ErrInsufficientStock: недостаточно товара на складе
	DecrementStock(ctx context.Context, productID entity.ProductID, quantity int) error
}
