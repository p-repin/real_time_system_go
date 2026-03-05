package dto

import (
	"real_time_system/domain/entity"
	"real_time_system/domain/value_objects"
)

type CreateProductRequest struct {
	Name        string
	Description string
	Price       value_objects.Money
	Stock       int
}

type UpdateProductRequest struct {
	Name        *string
	Description *string
	Price       *value_objects.Money
	Stock       *int
}

type ProductResponse struct {
	ID          string
	Name        string
	Description string
	Price       value_objects.Money
	Stock       int
	CreatedAt   string
	UpdatedAt   string
}

// ToProductResponse преобразует entity.Product в DTO для API.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ PRODUCTION ПАТТЕРН: DTO ИЗОЛЯЦИЯ                                           │
// └──────────────────────────────────────────────────────────────────────────┘
//
// ПОЧЕМУ НЕ ОТДАЁМ entity.Product НАПРЯМУЮ В API:
// 1. Изоляция слоёв:
//    - Entity может меняться (добавим поля для внутренней логики)
//    - API остаётся стабильным (backward compatibility)
//
// 2. Контроль над данными:
//    - В entity может быть DeletedAt — не нужен клиенту
//    - В entity может быть internal ID для связей — не отдаём
//
// 3. Формат данных:
//    - Entity хранит time.Time → API хочет ISO8601 string
//    - Entity хранит Money struct → API может хотеть "15.00 RUB"
func ToProductResponse(product *entity.Product) ProductResponse {
	return ProductResponse{
		ID:          product.ID.String(),
		Name:        product.Name,
		Description: product.Description,

		// БЫЛО: value_objects.Money{Amount: ..., Currency: ...} ❌
		//
		// ПОЧЕМУ ИЗБЫТОЧНО:
		// Money — это struct (value type), он передаётся по значению (копируется).
		// product.Price уже безопасная копия, не нужно создавать новый Money вручную.
		//
		// ЧТО БЫЛО БЫ С POINTER:
		// Если бы Price был *Money, то нужно копировать:
		//   Price: &value_objects.Money{Amount: product.Price.Amount, ...}
		// Иначе изменение Price в DTO повлияло бы на entity.
		//
		// ✅ ПРАВИЛЬНО: просто копируем struct
		Price: product.Price,

		Stock:     product.Stock,
		CreatedAt: product.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), // ISO8601
		UpdatedAt: product.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}
