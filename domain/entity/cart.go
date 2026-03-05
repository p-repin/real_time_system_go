package entity

import (
	"database/sql/driver"
	"fmt"
	"github.com/google/uuid"
	"real_time_system/domain"
	"real_time_system/domain/value_objects"
	"sync"
	"time"
)

type CartID uuid.UUID
type CartItemID uuid.UUID

func NewCartID() CartID {
	return CartID(uuid.New())
}

// ParseCartID парсит строку в CartID.
// Используется при маппинге из БД (nullable поля в LEFT JOIN).
func ParseCartID(s string) (CartID, error) {
	parsed, err := uuid.Parse(s)
	if err != nil {
		return CartID{}, fmt.Errorf("invalid CartID: %w", err)
	}
	return CartID(parsed), nil
}

func (id CartID) String() string {
	return uuid.UUID(id).String()
}

func (id CartID) IsZero() bool {
	return uuid.UUID(id) == uuid.Nil
}

func NewCartItemID() CartItemID {
	return CartItemID(uuid.New())
}

// ParseCartItemID парсит строку в CartItemID.
// Используется при маппинге из БД (nullable поля в LEFT JOIN).
func ParseCartItemID(s string) (CartItemID, error) {
	parsed, err := uuid.Parse(s)
	if err != nil {
		return CartItemID{}, fmt.Errorf("invalid CartItemID: %w", err)
	}
	return CartItemID(parsed), nil
}

func (id CartItemID) String() string {
	return uuid.UUID(id).String()
}

func (id CartItemID) IsZero() bool {
	return uuid.UUID(id) == uuid.Nil
}

// CartItem — позиция в корзине: товар + количество + цена на момент добавления.
type CartItem struct {
	ID        CartItemID
	CartID    CartID
	ProductID ProductID
	Quantity  int
	Price     value_objects.Money
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (id *CartItemID) Scan(src interface{}) error {
	switch v := src.(type) {
	case string:
		// Парсим UUID из строки
		parsed, err := uuid.Parse(v)
		if err != nil {
			return fmt.Errorf("invalid UUID string: %w", err)
		}
		*id = CartItemID(parsed)
		return nil

	case []byte:
		// Парсим UUID из байтов
		parsed, err := uuid.ParseBytes(v)
		if err != nil {
			return fmt.Errorf("invalid UUID bytes: %w", err)
		}
		*id = CartItemID(parsed)
		return nil

	case nil:
		// NULL из БД → zero-value
		*id = CartItemID(uuid.Nil)
		return nil

	default:
		// Неожиданный тип → ошибка
		return fmt.Errorf("cannot scan %T into CartItemID", src)
	}
}

// Cart — корзина покупателя.
//
// ПОЧЕМУ используем sync.RWMutex:
// Корзина — это mutable entity, к которой могут обращаться несколько горутин:
// - HTTP handler добавляет товар
// - WebSocket уведомляет об изменении цены
// - Background job проверяет наличие товара на складе
//
// RWMutex (а не обычный Mutex) позволяет нескольким читателям работать параллельно,
// блокируя только при записи. Это важно для real-time системы: чтение корзины
// (отображение в UI) не должно блокироваться другими чтениями.
//
// КАКИЕ ПРОБЛЕМЫ БЫЛИ БЫ БЕЗ МЬЮТЕКСА:
// - Data race: два запроса одновременно добавляют товар → TotalPrice считается неверно
// - Panic: concurrent map write в Go вызывает панику (не просто неверные данные, а крэш)
// - Race detector (go test -race) поймает это при тестировании, но на проде будет крэш
//
// ПОЧЕМУ mu — приватное поле (маленькая буква):
// Мьютекс — это деталь реализации. Внешний код не должен управлять блокировкой напрямую.
// Если бы mu был публичным, кто-то мог бы вызвать cart.Mu.Lock() и забыть Unlock() — deadlock.
// Entity сама управляет своей потокобезопасностью.
type Cart struct {
	ID         CartID
	UserID     UserID
	Items      map[ProductID]CartItem
	TotalPrice value_objects.Money
	CreatedAt  time.Time
	UpdatedAt  time.Time
	DeletedAt  *time.Time
	mu         sync.RWMutex
}

func (id *CartID) Scan(src interface{}) error {
	switch v := src.(type) {
	case string:
		// Парсим UUID из строки
		parsed, err := uuid.Parse(v)
		if err != nil {
			return fmt.Errorf("invalid UUID string: %w", err)
		}
		*id = CartID(parsed)
		return nil

	case []byte:
		// Парсим UUID из байтов
		parsed, err := uuid.ParseBytes(v)
		if err != nil {
			return fmt.Errorf("invalid UUID bytes: %w", err)
		}
		*id = CartID(parsed)
		return nil

	case nil:
		// NULL из БД → zero-value
		*id = CartID(uuid.Nil)
		return nil

	default:
		// Неожиданный тип → ошибка
		return fmt.Errorf("cannot scan %T into CartID", src)
	}
}

func (id CartID) Value() (driver.Value, error) {
	// Возвращаем строковое представление
	return id.String(), nil
}

// NewCart — фабрика корзины.
//
// ПОЧЕМУ принимаем currency параметром, а не хардкодим "RUB":
// Система может поддерживать несколько валют (RUB, USD). Валюта корзины
// определяется при создании и все товары в ней должны быть в той же валюте.
// Хардкод "RUB" нарушал бы Open/Closed principle — при добавлении новой валюты
// пришлось бы менять фабрику.
func NewCart(userID UserID, currency value_objects.Currency) *Cart {
	return &Cart{
		ID:         CartID(uuid.New()),
		UserID:     userID,
		Items:      make(map[ProductID]CartItem),
		TotalPrice: value_objects.Zero(currency),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
}

// AddItem добавляет товар в корзину или увеличивает количество существующего.
//
// ИСПРАВЛЕННЫЕ БАГИ (по сравнению с оригиналом):
//
//  1. ПОТЕРЯННАЯ ОШИБКА: раньше было fmt.Errorf("quantity must be positive") без return.
//     Код продолжал выполняться с невалидным quantity (0 или отрицательным).
//     Последствия: товар с quantity=-5 попадал в корзину, TotalPrice уменьшался,
//     а при оформлении заказа — отрицательная сумма или паника.
//
//  2. НЕВЕРНЫЙ РАСЧЁТ TOTAL: раньше прибавлялся price (цена за 1 шт) независимо от quantity.
//     Если добавить 5 штук по 100₽, TotalPrice увеличивался на 100₽ вместо 500₽.
//     Теперь: price * quantity через Money.Multiply().
//
//  3. ПРОВЕРКА quantity <= 0, а не < 0: добавление 0 штук — бессмысленная операция.
//     Она не должна проходить молча, потому что указывает на баг в вызывающем коде.
func (c *Cart) AddItem(productID ProductID, itemID CartItemID, quantity int, price value_objects.Money) error {
	// ИСПРАВЛЕНИЕ #1: возвращаем ошибку вместо игнорирования.
	if quantity <= 0 {
		return domain.ErrInvalidQuantity
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if item, exists := c.Items[productID]; exists {
		item.Quantity += quantity
		c.Items[productID] = item
	} else {
		c.Items[productID] = CartItem{
			ID:        itemID,
			ProductID: productID,
			Quantity:  quantity,
			Price:     price,
		}
	}

	// ИСПРАВЛЕНИЕ #2: умножаем цену на количество перед прибавлением к итогу.
	// price — это цена за 1 единицу, а добавляем мы quantity единиц.
	itemCost, err := price.Multiply(int64(quantity))
	if err != nil {
		return err
	}

	newTotal, err := c.TotalPrice.Add(itemCost)
	if err != nil {
		return err
	}

	c.TotalPrice = newTotal
	c.UpdatedAt = time.Now()

	return nil
}

// RemoveItem удаляет товар из корзины и пересчитывает TotalPrice.
//
// ИСПРАВЛЕННЫЙ БАГ: раньше TotalPrice не пересчитывался при удалении.
// Последствия: пользователь добавил товар на 500₽, удалил его, а TotalPrice
// показывает 500₽ при пустой корзине. При оформлении заказа — несоответствие суммы.
//
// ПОЧЕМУ возвращаем error, а не bool:
// bool не объясняет, почему операция не удалась. error позволяет
// вызывающему коду понять причину (товара нет в корзине) и обработать корректно
// (например, вернуть 404, а не 500).
func (c *Cart) RemoveItem(productID ProductID) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, exists := c.Items[productID]
	if !exists {
		return domain.ErrItemNotFound
	}

	// ИСПРАВЛЕНИЕ: вычитаем стоимость удаляемой позиции из TotalPrice.
	// Стоимость позиции = цена за единицу * количество в корзине.
	itemCost, err := item.Price.Multiply(int64(item.Quantity))
	if err != nil {
		return err
	}

	newTotal, err := c.TotalPrice.Subtract(itemCost)
	if err != nil {
		return err
	}

	delete(c.Items, productID)
	c.TotalPrice = newTotal
	c.UpdatedAt = time.Now()

	return nil
}

// Clear очищает корзину полностью.
//
// ИСПРАВЛЕННЫЙ БАГ: раньше Clear() не использовал мьютекс.
// Последствия: если горутина A вызывает Clear(), а горутина B одновременно
// вызывает AddItem(), получаем:
// - concurrent map write → panic (крэш приложения)
// - или Items очищается, но TotalPrice содержит сумму от AddItem — неконсистентность
//
// ПОЧЕМУ Lock(), а не RLock(): Clear — это операция записи (модификация Items и TotalPrice).
// RLock разрешил бы нескольким Clear выполняться параллельно, что тоже data race.
func (c *Cart) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.Items = make(map[ProductID]CartItem)
	c.TotalPrice = value_objects.Zero(c.TotalPrice.Currency)
	c.UpdatedAt = time.Now()
}

// ItemCount возвращает количество уникальных позиций в корзине.
// Потокобезопасен — использует RLock (чтение не блокирует другие чтения).
func (c *Cart) ItemCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.Items)
}

// IsEmpty проверяет, пуста ли корзина.
func (c *Cart) IsEmpty() bool {
	return c.ItemCount() == 0
}
