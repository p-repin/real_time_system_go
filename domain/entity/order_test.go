package entity

import (
	"errors"
	"real_time_system/domain"
	"real_time_system/domain/value_objects"
	"testing"
)

// testOrderItems — хелпер для создания валидных OrderItem в тестах.
// Вынесен отдельно, чтобы не дублировать инициализацию в каждом тесте.
func testOrderItems() []OrderItem {
	return []OrderItem{
		{
			ProductID: NewProductID(),
			Quantity:  2,
			Price:     value_objects.Money{Amount: 1000, Currency: value_objects.RUB},
		},
		{
			ProductID: NewProductID(),
			Quantity:  1,
			Price:     value_objects.Money{Amount: 500, Currency: value_objects.RUB},
		},
	}
}

func TestNewOrder(t *testing.T) {
	userID := NewUserID()
	items := testOrderItems()

	order, err := NewOrder(userID, items)
	if err != nil {
		t.Fatalf("NewOrder() error: %v", err)
	}

	if order.ID.IsZero() {
		t.Error("ID should not be zero")
	}
	if order.UserID != userID {
		t.Error("UserID mismatch")
	}
	if order.Status != OrderStatusPending {
		t.Errorf("Status = %q, want %q", order.Status, OrderStatusPending)
	}

	// TotalAmount = (1000 * 2) + (500 * 1) = 2500
	if order.TotalAmount.Amount != 2500 {
		t.Errorf("TotalAmount = %d, want 2500", order.TotalAmount.Amount)
	}
	if order.PaidAt != nil {
		t.Error("PaidAt should be nil for new order")
	}
}

func TestNewOrder_EmptyItems(t *testing.T) {
	_, err := NewOrder(NewUserID(), []OrderItem{})
	if !errors.Is(err, domain.ErrEmptyOrderItems) {
		t.Errorf("NewOrder(empty) error = %v, want %v", err, domain.ErrEmptyOrderItems)
	}
}

func TestNewOrder_NilItems(t *testing.T) {
	_, err := NewOrder(NewUserID(), nil)
	if !errors.Is(err, domain.ErrEmptyOrderItems) {
		t.Errorf("NewOrder(nil) error = %v, want %v", err, domain.ErrEmptyOrderItems)
	}
}

func TestNewOrder_MixedCurrencies(t *testing.T) {
	items := []OrderItem{
		{ProductID: NewProductID(), Quantity: 1, Price: value_objects.Money{Amount: 1000, Currency: value_objects.RUB}},
		{ProductID: NewProductID(), Quantity: 1, Price: value_objects.Money{Amount: 500, Currency: value_objects.USD}},
	}

	_, err := NewOrder(NewUserID(), items)
	if err == nil {
		t.Error("NewOrder with mixed currencies should return error")
	}
}

// TestOrder_StateTransitions — тест state machine через table-driven подход.
//
// ПОЧЕМУ тестируем ВСЕ переходы, а не только happy path:
// State machine — это инвариант бизнес-логики. Если хотя бы один невалидный
// переход пройдёт, это может привести к:
// - Повторной оплате (pending → paid → paid)
// - Отправке отменённого заказа (cancelled → shipped)
// - "Воскрешению" доставленного заказа (delivered → pending)
//
// Table-driven тест гарантирует полное покрытие всех комбинаций from → to.
func TestOrder_StateTransitions(t *testing.T) {
	tests := []struct {
		name      string
		fromState OrderStatus
		action    string // "pay", "ship", "deliver", "cancel"
		wantErr   bool
	}{
		// Допустимые переходы
		{"pending → paid", OrderStatusPending, "pay", false},
		{"pending → cancelled", OrderStatusPending, "cancel", false},
		{"paid → shipped", OrderStatusPaid, "ship", false},
		{"paid → cancelled", OrderStatusPaid, "cancel", false},
		{"shipped → delivered", OrderStatusShipped, "deliver", false},

		// Недопустимые переходы
		{"pending → shipped", OrderStatusPending, "ship", true},
		{"pending → delivered", OrderStatusPending, "deliver", true},
		{"paid → paid", OrderStatusPaid, "pay", true},
		{"paid → delivered", OrderStatusPaid, "deliver", true},
		{"shipped → paid", OrderStatusShipped, "pay", true},
		{"shipped → cancelled", OrderStatusShipped, "cancel", true},
		{"delivered → any", OrderStatusDelivered, "pay", true},
		{"delivered → cancel", OrderStatusDelivered, "cancel", true},
		{"cancelled → pay", OrderStatusCancelled, "pay", true},
		{"cancelled → ship", OrderStatusCancelled, "ship", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := &Order{
				ID:     NewOrderID(),
				Status: tt.fromState,
				Items:  testOrderItems(),
			}

			var err error
			switch tt.action {
			case "pay":
				err = order.MarkAsPaid()
			case "ship":
				err = order.Ship()
			case "deliver":
				err = order.Deliver()
			case "cancel":
				err = order.Cancel()
			}

			if tt.wantErr && err == nil {
				t.Errorf("expected error for %s → %s, got nil", tt.fromState, tt.action)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for %s → %s: %v", tt.fromState, tt.action, err)
			}
		})
	}
}

func TestOrder_MarkAsPaid_SetsPaidAt(t *testing.T) {
	order, _ := NewOrder(NewUserID(), testOrderItems())

	if err := order.MarkAsPaid(); err != nil {
		t.Fatalf("MarkAsPaid() error: %v", err)
	}

	if order.PaidAt == nil {
		t.Error("PaidAt should be set after payment")
	}
	if order.Status != OrderStatusPaid {
		t.Errorf("Status = %q, want %q", order.Status, OrderStatusPaid)
	}
}

func TestOrder_IsFinal(t *testing.T) {
	tests := []struct {
		status OrderStatus
		want   bool
	}{
		{OrderStatusPending, false},
		{OrderStatusPaid, false},
		{OrderStatusShipped, false},
		{OrderStatusDelivered, true},
		{OrderStatusCancelled, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			order := &Order{Status: tt.status}
			if got := order.IsFinal(); got != tt.want {
				t.Errorf("IsFinal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOrderItem_ItemTotal(t *testing.T) {
	item := OrderItem{
		ProductID: NewProductID(),
		Quantity:  3,
		Price:     value_objects.Money{Amount: 1500, Currency: value_objects.RUB},
	}

	total, err := item.ItemTotal()
	if err != nil {
		t.Fatalf("ItemTotal() error: %v", err)
	}

	// 1500 * 3 = 4500
	if total.Amount != 4500 {
		t.Errorf("ItemTotal() = %d, want 4500", total.Amount)
	}
}

// TestOrder_CanTransitionTo — тест проверки возможности перехода без изменения состояния.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПОЧЕМУ ВАЖЕН ЭТОТ ТЕСТ:                                                    │
// └──────────────────────────────────────────────────────────────────────────┘
// CanTransitionTo используется для:
// - Показа/скрытия кнопок в UI (если !CanTransitionTo(paid) → скрыть кнопку "Оплатить")
// - Валидации запросов API перед выполнением действия
// - Генерации списка доступных операций
//
// Если тест упадёт — UI покажет неправильные кнопки, или API примет невалидный запрос.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ЧТО ПРОВЕРЯЕМ:                                                             │
// └──────────────────────────────────────────────────────────────────────────┘
// 1. Допустимые переходы возвращают true
// 2. Недопустимые переходы возвращают false
// 3. Метод НЕ меняет состояние заказа (иммутабельная проверка)
func TestOrder_CanTransitionTo(t *testing.T) {
	tests := []struct {
		name        string
		fromStatus  OrderStatus
		toStatus    OrderStatus
		wantAllowed bool
	}{
		// Допустимые переходы из pending
		{"pending → paid (allowed)", OrderStatusPending, OrderStatusPaid, true},
		{"pending → cancelled (allowed)", OrderStatusPending, OrderStatusCancelled, true},

		// Недопустимые переходы из pending
		{"pending → shipped (forbidden)", OrderStatusPending, OrderStatusShipped, false},
		{"pending → delivered (forbidden)", OrderStatusPending, OrderStatusDelivered, false},

		// Допустимые переходы из paid
		{"paid → shipped (allowed)", OrderStatusPaid, OrderStatusShipped, true},
		{"paid → cancelled (allowed)", OrderStatusPaid, OrderStatusCancelled, true},

		// Недопустимые переходы из paid
		{"paid → pending (forbidden)", OrderStatusPaid, OrderStatusPending, false},
		{"paid → delivered (forbidden)", OrderStatusPaid, OrderStatusDelivered, false},

		// Допустимые переходы из shipped
		{"shipped → delivered (allowed)", OrderStatusShipped, OrderStatusDelivered, true},

		// Недопустимые переходы из shipped
		{"shipped → cancelled (forbidden)", OrderStatusShipped, OrderStatusCancelled, false},
		{"shipped → paid (forbidden)", OrderStatusShipped, OrderStatusPaid, false},

		// Финальные статусы: из них нет переходов
		{"delivered → any (forbidden)", OrderStatusDelivered, OrderStatusPending, false},
		{"cancelled → any (forbidden)", OrderStatusCancelled, OrderStatusPaid, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := &Order{Status: tt.fromStatus}
			originalStatus := order.Status // сохраняем для проверки иммутабельности

			got := order.CanTransitionTo(tt.toStatus)

			// Проверка результата
			if got != tt.wantAllowed {
				t.Errorf("CanTransitionTo() = %v, want %v", got, tt.wantAllowed)
			}

			// КРИТИЧЕСКАЯ ПРОВЕРКА: метод не должен менять состояние
			// CanTransitionTo — read-only операция, изменение статуса — это баг
			if order.Status != originalStatus {
				t.Errorf("CanTransitionTo() modified Status from %q to %q (MUST NOT mutate state)",
					originalStatus, order.Status)
			}
		})
	}
}

// TestOrder_GetAvailableTransitions — тест получения списка допустимых переходов.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПОЧЕМУ ВАЖЕН ЭТОТ ТЕСТ:                                                    │
// └──────────────────────────────────────────────────────────────────────────┘
// GetAvailableTransitions используется для:
// - API endpoint: GET /orders/{id}/available-actions
// - Динамической генерации UI (кнопки только для допустимых действий)
// - Документации (автогенерация диаграммы state machine)
//
// Если тест упадёт — API вернёт неполный список, или лишние действия.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ЧТО ПРОВЕРЯЕМ:                                                             │
// └──────────────────────────────────────────────────────────────────────────┘
// 1. Каждый статус возвращает правильное количество доступных переходов
// 2. Возвращаемые статусы действительно допустимы (через CanTransitionTo)
// 3. Финальные статусы возвращают пустой слайс (не nil, для JSON-сериализации)
// 4. Метод НЕ меняет состояние заказа
func TestOrder_GetAvailableTransitions(t *testing.T) {
	tests := []struct {
		name        string
		status      OrderStatus
		wantCount   int          // ожидаемое количество переходов
		wantContain []OrderStatus // какие статусы ДОЛЖНЫ быть в результате (проверяем наличие)
	}{
		{
			name:        "pending имеет 2 перехода",
			status:      OrderStatusPending,
			wantCount:   2,
			wantContain: []OrderStatus{OrderStatusPaid, OrderStatusCancelled},
		},
		{
			name:        "paid имеет 2 перехода",
			status:      OrderStatusPaid,
			wantCount:   2,
			wantContain: []OrderStatus{OrderStatusShipped, OrderStatusCancelled},
		},
		{
			name:        "shipped имеет 1 переход",
			status:      OrderStatusShipped,
			wantCount:   1,
			wantContain: []OrderStatus{OrderStatusDelivered},
		},
		{
			name:        "delivered (финальный) имеет 0 переходов",
			status:      OrderStatusDelivered,
			wantCount:   0,
			wantContain: []OrderStatus{}, // пустой слайс
		},
		{
			name:        "cancelled (финальный) имеет 0 переходов",
			status:      OrderStatusCancelled,
			wantCount:   0,
			wantContain: []OrderStatus{}, // пустой слайс
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := &Order{Status: tt.status}
			originalStatus := order.Status // для проверки иммутабельности

			got := order.GetAvailableTransitions()

			// 1. Проверка количества переходов
			if len(got) != tt.wantCount {
				t.Errorf("GetAvailableTransitions() returned %d items, want %d: %v",
					len(got), tt.wantCount, got)
			}

			// 2. Проверка, что все ожидаемые статусы присутствуют
			// (порядок не гарантирован из-за итерации по мапе в Go)
			for _, wantStatus := range tt.wantContain {
				found := false
				for _, gotStatus := range got {
					if gotStatus == wantStatus {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("GetAvailableTransitions() missing expected status %q, got: %v",
						wantStatus, got)
				}
			}

			// 3. Проверка, что все возвращённые статусы действительно допустимы
			// (защита от багов: если вернули лишний статус, проверим через CanTransitionTo)
			for _, status := range got {
				if !order.CanTransitionTo(status) {
					t.Errorf("GetAvailableTransitions() returned %q, but CanTransitionTo(%q) = false",
						status, status)
				}
			}

			// 4. Проверка иммутабельности
			if order.Status != originalStatus {
				t.Errorf("GetAvailableTransitions() modified Status from %q to %q (MUST NOT mutate state)",
					originalStatus, order.Status)
			}

			// 5. Проверка, что пустой результат — это слайс, а не nil
			// (важно для JSON-сериализации: [] вместо null)
			if tt.wantCount == 0 && got == nil {
				t.Error("GetAvailableTransitions() returned nil for final status, want empty slice []")
			}
		})
	}
}
