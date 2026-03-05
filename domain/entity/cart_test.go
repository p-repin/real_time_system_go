package entity

import (
	"errors"
	"real_time_system/domain"
	"real_time_system/domain/value_objects"
	"sync"
	"testing"
)

func TestNewCart(t *testing.T) {
	userID := NewUserID()
	cart := NewCart(userID, value_objects.RUB)

	if cart.ID.IsZero() {
		t.Error("ID should not be zero")
	}
	if cart.UserID != userID {
		t.Error("UserID mismatch")
	}
	if !cart.TotalPrice.IsZero() {
		t.Errorf("TotalPrice should be zero, got %d", cart.TotalPrice.Amount)
	}
	if cart.Items == nil {
		t.Error("Items map should be initialized")
	}
	if !cart.IsEmpty() {
		t.Error("new cart should be empty")
	}
}

func TestCart_AddItem(t *testing.T) {
	cart := NewCart(NewUserID(), value_objects.RUB)
	productID := NewProductID()
	price := value_objects.Money{Amount: 1000, Currency: value_objects.RUB} // 10.00₽

	err := cart.AddItem(productID, NewCartItemID(), 2, price)
	if err != nil {
		t.Fatalf("AddItem() error: %v", err)
	}

	// TotalPrice = 1000 * 2 = 2000
	if cart.TotalPrice.Amount != 2000 {
		t.Errorf("TotalPrice = %d, want 2000", cart.TotalPrice.Amount)
	}
	if cart.ItemCount() != 1 {
		t.Errorf("ItemCount = %d, want 1", cart.ItemCount())
	}

	// Добавляем ещё 3 штуки того же товара
	err = cart.AddItem(productID, NewCartItemID(), 3, price)
	if err != nil {
		t.Fatalf("AddItem() second call error: %v", err)
	}

	// TotalPrice = 2000 + (1000 * 3) = 5000
	if cart.TotalPrice.Amount != 5000 {
		t.Errorf("TotalPrice after second add = %d, want 5000", cart.TotalPrice.Amount)
	}

	// Количество товара = 2 + 3 = 5
	item := cart.Items[productID]
	if item.Quantity != 5 {
		t.Errorf("item Quantity = %d, want 5", item.Quantity)
	}

	// Позиция всё ещё одна (тот же productID)
	if cart.ItemCount() != 1 {
		t.Errorf("ItemCount = %d, want 1 (same product)", cart.ItemCount())
	}
}

func TestCart_AddItem_InvalidQuantity(t *testing.T) {
	cart := NewCart(NewUserID(), value_objects.RUB)
	price := value_objects.Money{Amount: 1000, Currency: value_objects.RUB}

	tests := []struct {
		name     string
		quantity int
	}{
		{"zero quantity", 0},
		{"negative quantity", -5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cart.AddItem(NewProductID(), NewCartItemID(), tt.quantity, price)
			if !errors.Is(err, domain.ErrInvalidQuantity) {
				t.Errorf("AddItem(qty=%d) error = %v, want %v", tt.quantity, err, domain.ErrInvalidQuantity)
			}
		})
	}
}

func TestCart_AddItem_CurrencyMismatch(t *testing.T) {
	cart := NewCart(NewUserID(), value_objects.RUB)
	usdPrice := value_objects.Money{Amount: 1000, Currency: value_objects.USD}

	err := cart.AddItem(NewProductID(), NewCartItemID(), 1, usdPrice)
	if !errors.Is(err, domain.ErrCurrencyMismatch) {
		t.Errorf("AddItem(USD to RUB cart) error = %v, want %v", err, domain.ErrCurrencyMismatch)
	}
}

func TestCart_RemoveItem(t *testing.T) {
	cart := NewCart(NewUserID(), value_objects.RUB)
	productID := NewProductID()
	price := value_objects.Money{Amount: 500, Currency: value_objects.RUB}

	// Добавляем 3 штуки по 500 копеек
	_ = cart.AddItem(productID, NewCartItemID(), 3, price)

	// TotalPrice = 500 * 3 = 1500
	if cart.TotalPrice.Amount != 1500 {
		t.Fatalf("setup: TotalPrice = %d, want 1500", cart.TotalPrice.Amount)
	}

	// Удаляем
	err := cart.RemoveItem(productID)
	if err != nil {
		t.Fatalf("RemoveItem() error: %v", err)
	}

	// TotalPrice должен вернуться к 0
	if cart.TotalPrice.Amount != 0 {
		t.Errorf("TotalPrice after remove = %d, want 0", cart.TotalPrice.Amount)
	}
	if !cart.IsEmpty() {
		t.Error("cart should be empty after removing the only item")
	}
}

func TestCart_RemoveItem_NotFound(t *testing.T) {
	cart := NewCart(NewUserID(), value_objects.RUB)

	err := cart.RemoveItem(NewProductID())
	if !errors.Is(err, domain.ErrItemNotFound) {
		t.Errorf("RemoveItem(unknown) error = %v, want %v", err, domain.ErrItemNotFound)
	}
}

func TestCart_Clear(t *testing.T) {
	cart := NewCart(NewUserID(), value_objects.RUB)
	price := value_objects.Money{Amount: 1000, Currency: value_objects.RUB}

	_ = cart.AddItem(NewProductID(), NewCartItemID(), 1, price)
	_ = cart.AddItem(NewProductID(), NewCartItemID(), 2, price)

	cart.Clear()

	if !cart.IsEmpty() {
		t.Error("cart should be empty after Clear()")
	}
	if !cart.TotalPrice.IsZero() {
		t.Errorf("TotalPrice after Clear() = %d, want 0", cart.TotalPrice.Amount)
	}
}

// TestCart_ConcurrentAddItem — тест на потокобезопасность.
//
// ПОЧЕМУ этот тест критически важен:
// Без него мы не можем быть уверены, что мьютекс работает корректно.
// go test -race (race detector) поймает data race только если он реально произошёл,
// а для этого нужна конкуренция — множество горутин, работающих параллельно.
//
// ЧТО ПРОВЕРЯЕМ:
// - Нет panic от concurrent map write
// - TotalPrice консистентен после всех операций
// - Количество позиций корректно
//
// ЗАПУСК С RACE DETECTOR: go test -race ./domain/entity/
// Если мьютекс убрать, race detector сразу поймает проблему.
func TestCart_ConcurrentAddItem(t *testing.T) {
	cart := NewCart(NewUserID(), value_objects.RUB)
	price := value_objects.Money{Amount: 100, Currency: value_objects.RUB}

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)

	// Каждая горутина добавляет свой уникальный товар (1 шт по 100 копеек)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_ = cart.AddItem(NewProductID(), NewCartItemID(), 1, price)
		}()
	}

	wg.Wait()

	if cart.ItemCount() != goroutines {
		t.Errorf("ItemCount = %d, want %d", cart.ItemCount(), goroutines)
	}

	// TotalPrice = 100 * 100 горутин = 10000
	expectedTotal := int64(goroutines) * price.Amount
	if cart.TotalPrice.Amount != expectedTotal {
		t.Errorf("TotalPrice = %d, want %d", cart.TotalPrice.Amount, expectedTotal)
	}
}

// TestCart_ConcurrentAddAndRemove — более сложный сценарий конкурентности:
// одновременное добавление и удаление.
func TestCart_ConcurrentAddAndRemove(t *testing.T) {
	cart := NewCart(NewUserID(), value_objects.RUB)
	price := value_objects.Money{Amount: 200, Currency: value_objects.RUB}

	// Добавляем 50 товаров заранее, чтобы было что удалять
	productIDs := make([]ProductID, 50)
	for i := range productIDs {
		productIDs[i] = NewProductID()
		_ = cart.AddItem(productIDs[i], NewCartItemID(), 1, price)
	}

	var wg sync.WaitGroup

	// 50 горутин добавляют новые товары
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = cart.AddItem(NewProductID(), NewCartItemID(), 1, price)
		}()
	}

	// 50 горутин удаляют существующие
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id ProductID) {
			defer wg.Done()
			_ = cart.RemoveItem(id)
		}(productIDs[i])
	}

	wg.Wait()

	// Добавлено 50 (заранее) + 50 (горутины) - 50 (удалено) = 50
	if cart.ItemCount() != 50 {
		t.Errorf("ItemCount = %d, want 50", cart.ItemCount())
	}
}

// TestCart_ConcurrentClear — Clear не должен вызывать panic при одновременном AddItem.
func TestCart_ConcurrentClear(t *testing.T) {
	cart := NewCart(NewUserID(), value_objects.RUB)
	price := value_objects.Money{Amount: 100, Currency: value_objects.RUB}

	var wg sync.WaitGroup

	// Горутины добавляют товары
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = cart.AddItem(NewProductID(), NewCartItemID(), 1, price)
		}()
	}

	// Параллельно кто-то вызывает Clear
	wg.Add(1)
	go func() {
		defer wg.Done()
		cart.Clear()
	}()

	// Тест проходит, если нет panic (concurrent map write)
	wg.Wait()
}
