package dto

import (
	"real_time_system/domain/entity"
	"real_time_system/domain/value_objects"
)

type UpdateStatusRequest struct {
	Status string `json:"status"`
}

type OrderResponse struct {
	ID          string              `json:"id"`
	UserID      string              `json:"user_id"`
	Items       []OrderItemResponse `json:"items"`
	TotalAmount MoneyResponse       `json:"total_amount"`
	Status      string              `json:"status"`
	PaidAt      string              `json:"paid_at,omitempty"`
	CreatedAt   string              `json:"created_at"`
	UpdatedAt   string              `json:"updated_at"`
}

type OrderItemResponse struct {
	ProductID   string        `json:"product_id"`
	ProductName string        `json:"product_name"`
	Quantity    int           `json:"quantity"`
	Price       MoneyResponse `json:"price"`
	Subtotal    MoneyResponse `json:"subtotal"`
}

// ToOrderItemResponse конвертирует OrderItem в DTO.
//
// SNAPSHOT PATTERN: ProductName уже хранится в entity (не нужен lookup).
// Название было сохранено при создании заказа и не зависит от текущего Product.
func ToOrderItemResponse(item entity.OrderItem) OrderItemResponse {
	subtotal, err := item.Price.Multiply(int64(item.Quantity))
	if err != nil {
		subtotal = value_objects.Zero(item.Price.Currency)
	}

	return OrderItemResponse{
		ProductID:   item.ProductID.String(),
		ProductName: item.ProductName, // snapshot: из entity, не из Product
		Quantity:    item.Quantity,
		Price:       ToMoneyResponse(item.Price),
		Subtotal:    ToMoneyResponse(subtotal),
	}
}

// ToOrderResponse конвертирует Order в DTO.
//
// SNAPSHOT PATTERN: не требует productNames map — всё уже в entity.
// Это упрощает код и убирает зависимость от ProductRepository при чтении заказов.
func ToOrderResponse(order *entity.Order) OrderResponse {
	items := make([]OrderItemResponse, 0, len(order.Items))

	for _, item := range order.Items {
		items = append(items, ToOrderItemResponse(item))
	}

	// PaidAt — nullable (*time.Time), нужна проверка перед Format
	// Если nil — оставляем пустую строку (omitempty в JSON уберёт поле)
	var paidAt string
	if order.PaidAt != nil {
		paidAt = order.PaidAt.Format("2006-01-02T15:04:05Z07:00")
	}

	return OrderResponse{
		ID:          order.ID.String(),
		UserID:      order.UserID.String(),
		Items:       items,
		TotalAmount: ToMoneyResponse(order.TotalAmount),
		Status:      string(order.Status), // entity.OrderStatus → string для изоляции API от domain
		PaidAt:      paidAt,
		CreatedAt:   order.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:   order.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}
