package dto

import (
	"real_time_system/domain/entity"
	"real_time_system/domain/value_objects"
)

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ CART DTO: ИЗОЛЯЦИЯ API ОТ DOMAIN                                          │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Корзина — сложный aggregate, содержащий связанные данные:
// - Cart (корзина)
// - CartItems (позиции)
// - Product info (название, цена — для отображения в UI)
//
// DTO должен предоставить клиенту всё необходимое для отображения,
// чтобы клиент не делал дополнительные запросы за названиями товаров.

// ──────────────────────────────────────────────────────────────────────────
// REQUEST DTOs
// ──────────────────────────────────────────────────────────────────────────

// AddToCartRequest — запрос на добавление товара в корзину.
//
// ПОЧЕМУ ProductID string, а не entity.ProductID:
// HTTP handler получает JSON со строкой. Парсинг string → entity.ProductID
// происходит в Service слое, где мы можем вернуть понятную ошибку валидации.
//
// ПОЧЕМУ нет CartID:
// Корзина привязана к пользователю (UserID из контекста авторизации).
// Клиент не должен указывать CartID — это деталь реализации.
type AddToCartRequest struct {
	ProductID string `json:"product_id" validate:"required,uuid"`
	Quantity  int    `json:"quantity" validate:"required,min=1"`
}

// UpdateCartItemRequest — запрос на изменение количества товара.
//
// ПОЧЕМУ Quantity НЕ pointer (в отличие от UpdateUserRequest):
// Для корзины partial update не имеет смысла — мы всегда меняем количество.
// Если Quantity = 0, это означает "удалить товар" (можно обработать в Service).
//
// АЛЬТЕРНАТИВА: отдельный endpoint DELETE /cart/items/{product_id}
// Мы используем оба подхода: UpdateQuantity(quantity=0) и RemoveFromCart.
type UpdateCartItemRequest struct {
	ProductID string `json:"product_id" validate:"required,uuid"`
	Quantity  int    `json:"quantity" validate:"min=0"` // 0 = удалить
}

// ──────────────────────────────────────────────────────────────────────────
// RESPONSE DTOs
// ──────────────────────────────────────────────────────────────────────────

// CartResponse — полное представление корзины для API.
//
// КЛЮЧЕВОЙ ПАТТЕРН: Response содержит всё для отображения UI.
// Клиенту не нужно делать дополнительные запросы за названиями товаров.
type CartResponse struct {
	ID         string             `json:"id"`
	UserID     string             `json:"user_id"`
	Items      []CartItemResponse `json:"items"`       // slice, не map (JSON-friendly)
	TotalPrice MoneyResponse      `json:"total_price"` // структурированный Money
	ItemsCount int                `json:"items_count"` // количество уникальных позиций
	CreatedAt  string             `json:"created_at"`  // ISO8601
	UpdatedAt  string             `json:"updated_at"`
}

// CartItemResponse — позиция в корзине с информацией о товаре.
//
// ПОЧЕМУ включаем ProductName и Subtotal:
// - ProductName: клиент показывает "iPhone 15" вместо UUID
// - Subtotal: вычисляемое поле (Quantity × Price), избавляем клиента от расчётов
//
// ОТКУ|ДА БЕРЁТСЯ ProductName:
// CartItem хранит только ProductID. Service должен обогатить данные,
// получив Products из ProductRepository. Это называется "data enrichment".
type CartItemResponse struct {
	ProductID   string        `json:"product_id"`
	ProductName string        `json:"product_name"` // обогащённые данные из Product
	Quantity    int           `json:"quantity"`
	Price       MoneyResponse `json:"price"`    // цена за единицу
	Subtotal    MoneyResponse `json:"subtotal"` // Quantity × Price
}

// MoneyResponse — представление денег для API.
//
// ПОЧЕМУ отдельная структура, а не value_objects.Money:
// 1. Изоляция: изменение Money в domain не ломает API
// 2. Формат: можем добавить Formatted ("1 500,00 ₽") для UI
// 3. Контроль: явно указываем, что отдаём клиенту
type MoneyResponse struct {
	Amount    int64  `json:"amount"`    // в минимальных единицах (копейки)
	Currency  string `json:"currency"`  // "RUB", "USD"
	Formatted string `json:"formatted"` // "1 500,00 ₽" — для отображения
}

// ──────────────────────────────────────────────────────────────────────────
// MAPPER FUNCTIONS
// ──────────────────────────────────────────────────────────────────────────

// ToMoneyResponse конвертирует value_objects.Money в DTO.
//
// ПОЧЕМУ выносим в отдельную функцию:
// Money используется в нескольких местах (Price, Subtotal, TotalPrice).
// DRY: одна функция форматирования, легко изменить формат везде.
func ToMoneyResponse(money value_objects.Money) MoneyResponse {
	return MoneyResponse{
		Amount:   money.Amount,
		Currency: string(money.Currency),
		// TODO: добавить форматирование "1 500,00 ₽" когда понадобится
		Formatted: formatMoney(money),
	}
}

// formatMoney форматирует Money для отображения.
// Пока простая реализация, в production — использовать i18n библиотеку.
func formatMoney(money value_objects.Money) string {
	// Простое форматирование: "150.00 RUB"
	// В production: учитывать locale, разделители тысяч, символ валюты
	return money.String()
}

// ToCartItemResponse конвертирует CartItem + Product в DTO.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПАТТЕРН: DATA ENRICHMENT                                                  │
// └──────────────────────────────────────────────────────────────────────────┘
//
// CartItem содержит только ProductID, но клиенту нужно название товара.
// Есть два подхода:
//
//  1. Mapper принимает дополнительные данные (текущий подход):
//     ToCartItemResponse(item, productName) — простой, явный
//
//  2. Mapper принимает map всех продуктов:
//     ToCartItemResponse(item, products map[ProductID]*Product)
//     — удобнее для batch-конвертации, но менее явный
//
// Мы используем подход #1 для простоты. Service получает Products
// и передаёт название в mapper.
func ToCartItemResponse(item entity.CartItem, productName string) CartItemResponse {
	// Вычисляем subtotal: количество × цена за единицу
	// Используем Money.Multiply для корректной арифметики
	subtotal, err := item.Price.Multiply(int64(item.Quantity))
	if err != nil {
		// В production: логировать ошибку, использовать zero value
		// Ошибка маловероятна (overflow), но обрабатываем gracefully
		subtotal = value_objects.Zero(item.Price.Currency)
	}

	return CartItemResponse{
		ProductID:   item.ProductID.String(),
		ProductName: productName,
		Quantity:    item.Quantity,
		Price:       ToMoneyResponse(item.Price),
		Subtotal:    ToMoneyResponse(subtotal),
	}
}

// ToCartResponse конвертирует Cart + Products в полный DTO.
//
// ПОЧЕМУ принимаем productNames map:
// Cart.Items содержит только ProductID. Чтобы вернуть ProductName,
// нужна информация из Product entity. Service получает её из ProductRepository
// и передаёт сюда как map[ProductID]string.
//
// АЛЬТЕРНАТИВА: передавать []*entity.Product и искать по ID.
// Map эффективнее: O(1) lookup вместо O(n) для каждого item.
func ToCartResponse(cart *entity.Cart, productNames map[entity.ProductID]string) CartResponse {
	// Pre-allocate slice: знаем точное количество items
	items := make([]CartItemResponse, 0, len(cart.Items))

	for _, item := range cart.Items {
		// Получаем название продукта из map
		// Если продукт не найден (удалён?), используем fallback
		productName, ok := productNames[item.ProductID]
		if !ok {
			productName = "Unknown Product" // или item.ProductID.String()
		}

		items = append(items, ToCartItemResponse(item, productName))
	}

	return CartResponse{
		ID:         cart.ID.String(),
		UserID:     cart.UserID.String(),
		Items:      items,
		TotalPrice: ToMoneyResponse(cart.TotalPrice),
		ItemsCount: len(cart.Items),
		CreatedAt:  cart.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:  cart.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// ToCartResponseSimple конвертирует Cart без обогащения данными.
//
// КОГДА ИСПОЛЬЗОВАТЬ:
// - Когда не нужны названия товаров (внутренние операции)
// - Когда Products уже удалены и недоступны
// - Для быстрого ответа без дополнительных запросов к БД
//
// В Items[].ProductName будет UUID продукта вместо названия.
func ToCartResponseSimple(cart *entity.Cart) CartResponse {
	items := make([]CartItemResponse, 0, len(cart.Items))

	for _, item := range cart.Items {
		// Без обогащения: используем ProductID как fallback для названия
		items = append(items, ToCartItemResponse(item, item.ProductID.String()))
	}

	return CartResponse{
		ID:         cart.ID.String(),
		UserID:     cart.UserID.String(),
		Items:      items,
		TotalPrice: ToMoneyResponse(cart.TotalPrice),
		ItemsCount: len(cart.Items),
		CreatedAt:  cart.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:  cart.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}
