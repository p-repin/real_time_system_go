package entity

import (
	"fmt"
	"github.com/google/uuid"
	"real_time_system/domain"
	"real_time_system/domain/value_objects"
	"time"
)

type OrderID uuid.UUID

func NewOrderID() OrderID {
	return OrderID(uuid.New())
}

func (id OrderID) String() string {
	return uuid.UUID(id).String()
}

func (id OrderID) IsZero() bool {
	return uuid.UUID(id) == uuid.Nil
}

func ParseOrderID(s string) (OrderID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return OrderID{}, err
	}
	return OrderID(id), nil
}

// Scan реализует sql.Scanner для чтения из БД.
func (id *OrderID) Scan(src interface{}) error {
	switch v := src.(type) {
	case string:
		parsed, err := uuid.Parse(v)
		if err != nil {
			return fmt.Errorf("invalid UUID string: %w", err)
		}
		*id = OrderID(parsed)
		return nil
	case []byte:
		parsed, err := uuid.ParseBytes(v)
		if err != nil {
			return fmt.Errorf("invalid UUID bytes: %w", err)
		}
		*id = OrderID(parsed)
		return nil
	case [16]byte:
		*id = OrderID(uuid.UUID(v))
		return nil
	default:
		return fmt.Errorf("cannot scan %T into OrderID", src)
	}
}

// Value реализует driver.Valuer для записи в БД.
func (id OrderID) Value() (interface{}, error) {
	return uuid.UUID(id).String(), nil
}

// OrderItemID — идентификатор позиции заказа.
type OrderItemID uuid.UUID

func NewOrderItemID() OrderItemID {
	return OrderItemID(uuid.New())
}

func (id OrderItemID) String() string {
	return uuid.UUID(id).String()
}

type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusPaid      OrderStatus = "paid"
	OrderStatusShipped   OrderStatus = "shipped"
	OrderStatusDelivered OrderStatus = "delivered"
	OrderStatusCancelled OrderStatus = "cancelled"
)

// ╔══════════════════════════════════════════════════════════════════════════╗
// ║ ПРИМЕРЫ ИСПОЛЬЗОВАНИЯ STATE MACHINE API                                   ║
// ╚══════════════════════════════════════════════════════════════════════════╝
//
// 1. ПРОВЕРКА ВОЗМОЖНОСТИ ПЕРЕХОДА (без изменения состояния):
//
//    order := &Order{Status: OrderStatusPending}
//    if order.CanTransitionTo(OrderStatusPaid) {
//        fmt.Println("✓ Можно оплатить заказ")
//    }
//    if !order.CanTransitionTo(OrderStatusShipped) {
//        fmt.Println("✗ Нельзя отправить неоплаченный заказ")
//    }
//
// 2. ПОЛУЧЕНИЕ СПИСКА ДОСТУПНЫХ ДЕЙСТВИЙ (для UI/API):
//
//    available := order.GetAvailableTransitions()
//    // available = [OrderStatusPaid, OrderStatusCancelled]
//
//    for _, status := range available {
//        fmt.Printf("Доступная кнопка: %s\n", status)
//    }
//    // Вывод: "Доступная кнопка: paid"
//    //        "Доступная кнопка: cancelled"
//
// 3. ВЫПОЛНЕНИЕ ПЕРЕХОДА (через бизнес-методы):
//
//    // Правильно: через бизнес-методы с дополнительной логикой
//    if err := order.MarkAsPaid(); err != nil {
//        log.Errorf("Не удалось оплатить: %v", err)
//    }
//
//    // Неправильно: прямое изменение статуса
//    // order.Status = OrderStatusPaid // ❌ обходит проверку переходов и не обновляет UpdatedAt
//
// 4. ОБРАБОТКА ОШИБКИ НЕДОПУСТИМОГО ПЕРЕХОДА:
//
//    order := &Order{Status: OrderStatusShipped}
//    err := order.Cancel() // попытка отменить отправленный заказ
//    if errors.Is(err, domain.ErrInvalidStatusTransition) {
//        // Обработка: показать пользователю "Отменить можно только до отправки"
//    }
//
// 5. ПРОВЕРКА ФИНАЛЬНОГО СТАТУСА:
//
//    if order.IsFinal() {
//        fmt.Println("Заказ завершён, действия недоступны")
//        // Скрыть кнопки "Отменить", "Изменить"
//    }
//
// 6. ДИНАМИЧЕСКАЯ ГЕНЕРАЦИЯ ФОРМ (паттерн для админки):
//
//    type Action struct {
//        Label  string
//        Status OrderStatus
//    }
//
//    actions := []Action{}
//    for _, status := range order.GetAvailableTransitions() {
//        switch status {
//        case OrderStatusPaid:
//            actions = append(actions, Action{"Оплатить", status})
//        case OrderStatusShipped:
//            actions = append(actions, Action{"Отправить", status})
//        case OrderStatusCancelled:
//            actions = append(actions, Action{"Отменить", status})
//        }
//    }
//    // Отрисовать кнопки в UI динамически
//
// 7. API ENDPOINT: GET /orders/{id}/available-actions
//
//    func (h *OrderHandler) GetAvailableActions(w http.ResponseWriter, r *http.Request) {
//        order, _ := h.repo.FindByID(orderID)
//        available := order.GetAvailableTransitions()
//        json.NewEncoder(w).Encode(map[string]interface{}{
//            "current": order.Status,
//            "available": available,
//        })
//    }
//    // Response: {"current": "pending", "available": ["paid", "cancelled"]}

// allowedTransitions — state machine допустимых переходов статусов заказа.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ АРХИТЕКТУРА: ВЛОЖЕННАЯ МАПА ДЛЯ O(1) ПРОВЕРКИ ПЕРЕХОДОВ                   │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Структура: map[ИзСтатуса]map[ВКакойСтатус]bool
// Пример доступа: allowedTransitions[OrderStatusPending][OrderStatusPaid] → true
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПОЧЕМУ ВЛОЖЕННАЯ МАПА, А НЕ СЛАЙС []OrderStatus?                          │
// └──────────────────────────────────────────────────────────────────────────┘
//
// 1. ПРОИЗВОДИТЕЛЬНОСТЬ: O(1) vs O(n)
//
//   - Слайс:    []OrderStatus → нужен линейный поиск for _, s := range allowed
//
//   - Мапа:     map[OrderStatus]bool → хэш-лукап за константное время
//
//     Для 2 переходов разница незаметна, но если добавим статусы Refunded,
//     Returned, OnHold, Disputed → разница станет ощутимой.
//
// 2. МАСШТАБИРУЕМОСТЬ:
//   - Добавление нового перехода: одна строка, производительность не меняется
//   - Слайс при 10+ переходах из одного статуса начинает тормозить
//
// 3. ИДИОМАТИЧНОСТЬ:
//   - В Go проверка "элемент принадлежит множеству" = map[T]bool (или struct{})
//   - Вложенная мапа — стандартный паттерн для графов переходов
//
// 4. ЧИТАЕМОСТЬ:
//   - Явно видно "из Pending можно в {Paid: true, Cancelled: true}"
//   - Проверка: if allowedTransitions[from][to] { ... } — одна строка
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ КАК РАБОТАТЬ С ЭТОЙ СТРУКТУРОЙ?                                            │
// └──────────────────────────────────────────────────────────────────────────┘
//
// ПРОВЕРИТЬ ВОЗМОЖНОСТЬ ПЕРЕХОДА:
//
//	canTransition := allowedTransitions[currentStatus][targetStatus]
//	if !canTransition {
//	    return errors.New("переход запрещён")
//	}
//
// ПОЛУЧИТЬ ВСЕ РАЗРЕШЁННЫЕ ПЕРЕХОДЫ ИЗ ТЕКУЩЕГО СТАТУСА:
//
//	for targetStatus := range allowedTransitions[currentStatus] {
//	    fmt.Println("можно перейти в:", targetStatus)
//	}
//
// ДОБАВИТЬ НОВЫЙ ПЕРЕХОД (например, из Paid в Refunded):
//
//	allowedTransitions[OrderStatusPaid][OrderStatusRefunded] = true
//
// УДАЛИТЬ ПЕРЕХОД:
//
//	delete(allowedTransitions[OrderStatusPaid], OrderStatusCancelled)
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПОЧЕМУ State Machine ВООБЩЕ, А НЕ if/else В МЕТОДАХ?                      │
// └──────────────────────────────────────────────────────────────────────────┘
//
// БЕЗ STATE MACHINE (анти-паттерн):
//
//	func (o *Order) MarkAsPaid() error {
//	    if o.Status != "pending" { return errors.New("только pending → paid") }
//	    o.Status = "paid"
//	}
//	func (o *Order) Ship() error {
//	    if o.Status != "paid" && o.Status != "pending" { return err }
//	    o.Status = "shipped"
//	}
//	// Проблема: правила размазаны по методам, легко забыть обновить везде
//
// С STATE MACHINE (правильный подход):
//   - Единый источник правды: все переходы в одном месте
//   - Изменение правила = правка одной строки в allowedTransitions
//   - Невозможно случайно разрешить запрещённый переход
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ВИЗУАЛИЗАЦИЯ ГРАФА ПЕРЕХОДОВ                                               │
// └──────────────────────────────────────────────────────────────────────────┘
//
//	pending ──────┬──→ paid ──────┬──→ shipped ──→ delivered
//	              │                │
//	              └──→ cancelled ←─┘
//
// Стрелка "A → B" = allowedTransitions[A][B] == true
var allowedTransitions = map[OrderStatus]map[OrderStatus]bool{
	OrderStatusPending: {
		OrderStatusPaid:      true, // оплата заказа
		OrderStatusCancelled: true, // отмена до оплаты
	},
	OrderStatusPaid: {
		OrderStatusShipped:   true, // отправка товара
		OrderStatusCancelled: true, // отмена после оплаты (возврат денег)
	},
	OrderStatusShipped: {
		OrderStatusDelivered: true, // успешная доставка
	},
	// Финальные статусы: из них нет переходов (пустая мапа или nil)
	OrderStatusDelivered: {}, // заказ завершён успешно
	OrderStatusCancelled: {}, // заказ отменён
}

// OrderItem — позиция заказа: товар + количество + зафиксированная цена.
//
// ПОЧЕМУ Price хранится в OrderItem, а не берётся из Product:
// Цена товара может измениться после оформления заказа (акция закончилась,
// поставщик поднял цену). Но заказ должен хранить цену на момент покупки.
// Это юридическое требование: клиент заплатил 1000₽, значит в заказе 1000₽,
// даже если завтра товар стоит 1500₽.
//
// ПОЧЕМУ Quantity — int, а не int64:
// В реальном e-com никто не заказывает 2 миллиарда единиц товара.
// int (минимум 32 бита в Go) покрывает любые разумные значения.
// int64 для количества — over-engineering без пользы.
type OrderItem struct {
	ProductID   ProductID
	ProductName string
	Quantity    int
	Price       value_objects.Money
}

// ItemTotal возвращает стоимость позиции: цена * количество.
func (item OrderItem) ItemTotal() (value_objects.Money, error) {
	return item.Price.Multiply(int64(item.Quantity))
}

type Order struct {
	ID          OrderID
	UserID      UserID
	Items       []OrderItem
	TotalAmount value_objects.Money
	Status      OrderStatus
	PaidAt      *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NewOrder — фабрика заказа. Принимает UserID и список позиций.
//
// ПОЧЕМУ items передаётся готовым слайсом, а не добавляется по одному:
// Заказ — это снимок (snapshot) корзины на момент оформления. Он не должен
// модифицироваться после создания (кроме смены статуса). Если бы мы разрешали
// AddItem после создания, то нарушился бы инвариант TotalAmount — сумма
// перестала бы соответствовать позициям.
//
// ПОЧЕМУ считаем TotalAmount здесь, а не принимаем параметром:
// Если бы TotalAmount передавался снаружи, вызывающий код мог бы ошибиться
// в расчёте. Заказ сам считает свой итог из позиций — это гарантия консистентности.
// Принцип: entity владеет своими инвариантами и защищает их.
func NewOrder(userID UserID, items []OrderItem) (*Order, error) {
	if len(items) == 0 {
		return nil, domain.ErrEmptyOrderItems
	}

	// Вычисляем общую сумму заказа из позиций.
	// Берём валюту первого товара как эталонную — все позиции должны быть в одной валюте.
	totalAmount := value_objects.Zero(items[0].Price.Currency)

	for _, item := range items {
		itemTotal, err := item.ItemTotal()
		if err != nil {
			return nil, fmt.Errorf("calculate item total for product %s: %w", item.ProductID, err)
		}

		newTotal, err := totalAmount.Add(itemTotal)
		if err != nil {
			// Ошибка здесь означает, что в заказе товары с разными валютами.
			// Это бизнес-ограничение: один заказ — одна валюта.
			return nil, fmt.Errorf("sum order items: %w", err)
		}
		totalAmount = newTotal
	}

	now := time.Now()

	return &Order{
		ID:          NewOrderID(),
		UserID:      userID,
		Items:       items,
		TotalAmount: totalAmount,
		Status:      OrderStatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// CanTransitionTo проверяет возможность перехода в целевой статус БЕЗ изменения состояния.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ КОГДА ИСПОЛЬЗОВАТЬ:                                                        │
// └──────────────────────────────────────────────────────────────────────────┘
// - UI: показать/скрыть кнопки действий (если !order.CanTransitionTo(paid) → спрятать "Оплатить")
// - Валидация API: проверить допустимость перехода ДО вызова бизнес-метода
// - Генерация доступных действий: for _, status := range allStatuses { if order.CanTransitionTo(status) {...} }
//
// ПРИМЕР:
//
//	if order.CanTransitionTo(OrderStatusShipped) {
//	    fmt.Println("Можно отправить заказ")
//	}
func (o *Order) CanTransitionTo(target OrderStatus) bool {
	// Проверяем, что для текущего статуса есть список разрешённых переходов
	allowedFromCurrent, exists := allowedTransitions[o.Status]
	if !exists {
		return false // неизвестный статус (не должно случиться, но защита на всякий случай)
	}

	// Проверяем наличие целевого статуса во вложенной мапе — O(1) хэш-лукап
	return allowedFromCurrent[target]
}

// GetAvailableTransitions возвращает список всех допустимых статусов из текущего.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ КОГДА ИСПОЛЬЗОВАТЬ:                                                        │
// └──────────────────────────────────────────────────────────────────────────┘
// - API endpoint: GET /orders/{id}/available-actions → ["paid", "cancelled"]
// - UI: динамическая генерация кнопок действий
// - Документация: автогенерация диаграммы state machine
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ КАК РАБОТАЕТ ИТЕРАЦИЯ ПО ВЛОЖЕННОЙ МАПЕ:                                   │
// └──────────────────────────────────────────────────────────────────────────┘
//
//	allowedTransitions[OrderStatusPending] = map[OrderStatus]bool{
//	    OrderStatusPaid: true,
//	    OrderStatusCancelled: true,
//	}
//
//	for status := range allowedTransitions[OrderStatusPending] {
//	    // status будет OrderStatusPaid, затем OrderStatusCancelled
//	}
//
// ВАЖНО: порядок итерации по мапе в Go НЕ гарантирован (специально рандомизирован).
// Если нужен стабильный порядок → собрать в слайс и отсортировать.
//
// ПРИМЕР:
//
//	available := order.GetAvailableTransitions()
//	fmt.Printf("Можно перейти в: %v\n", available)
//	// Вывод для pending: [paid cancelled] (порядок может меняться)
func (o *Order) GetAvailableTransitions() []OrderStatus {
	allowedFromCurrent, exists := allowedTransitions[o.Status]
	if !exists {
		return []OrderStatus{} // пустой слайс, а не nil — удобнее для JSON сериализации
	}

	// Резервируем слайс нужной длины сразу (оптимизация выделения памяти)
	// len(allowedFromCurrent) = количество ключей в мапе = количество допустимых переходов
	result := make([]OrderStatus, 0, len(allowedFromCurrent))

	// Итерируемся по ключам вложенной мапы
	// (значения bool всегда true, поэтому не проверяем)
	for status := range allowedFromCurrent {
		result = append(result, status)
	}

	return result
}

// transitionTo — внутренний метод для перехода между статусами.
// Проверяет допустимость перехода по карте allowedTransitions и изменяет состояние.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПОЧЕМУ ЭТО ПРИВАТНЫЙ МЕТОД?                                                │
// └──────────────────────────────────────────────────────────────────────────┘
// Публичные методы (MarkAsPaid, Ship, Deliver, Cancel) — это бизнес-операции
// с дополнительной логикой:
//
//	MarkAsPaid → устанавливает PaidAt
//	Ship       → может отправить email клиенту (в будущем)
//	Deliver    → может начислить баллы лояльности (в будущем)
//
// transitionTo — это только проверка + изменение Status и UpdatedAt.
// Разделение позволяет:
//  1. Не дублировать проверку allowedTransitions в каждом методе
//  2. Добавлять бизнес-логику в публичные методы, не трогая проверку переходов
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ КАК РАБОТАЕТ ПРОВЕРКА ПЕРЕХОДА (O(1) ХЭШ-ЛУКАП):                           │
// └──────────────────────────────────────────────────────────────────────────┘
// 1. allowedTransitions[o.Status] → получаем map[OrderStatus]bool для текущего статуса
// 2. allowedFromCurrent[target]   → проверяем наличие целевого статуса в мапе
//
// Пример для pending → paid:
//
//	allowedTransitions[OrderStatusPending][OrderStatusPaid] → true ✓
//
// Пример для shipped → pending:
//
//	allowedTransitions[OrderStatusShipped][OrderStatusPending] → false (ключа нет) ✗
//
// Go-особенность: обращение к несуществующему ключу мапы возвращает zero-value (false для bool),
// а не панику. Поэтому проверка allowedFromCurrent[target] безопасна даже если target не в мапе.
//
// ЧТО БУДЕТ, ЕСЛИ ПОПРОБОВАТЬ НЕДОПУСТИМЫЙ ПЕРЕХОД:
//
//	order := &Order{Status: OrderStatusShipped}
//	err := order.transitionTo(OrderStatusPending)
//	// err = "invalid status transition: cannot transition from shipped to pending"
func (o *Order) transitionTo(target OrderStatus) error {
	// Получаем мапу разрешённых переходов из текущего статуса
	allowedFromCurrent, exists := allowedTransitions[o.Status]
	if !exists {
		// Это не должно случиться, если Status всегда инициализируется через константы,
		// но защита на случай ручной установки невалидного статуса.
		return domain.ErrInvalidStatusTransition
	}

	// Проверяем допустимость перехода через хэш-лукап O(1)
	// БЫЛО (линейный поиск O(n)):
	//   for _, s := range allowedFromCurrent { if s == target { ... } }
	// СТАЛО (хэш-лукап O(1)):
	//   if allowedFromCurrent[target] { ... }
	if !allowedFromCurrent[target] {
		// Переход запрещён state machine
		return fmt.Errorf("%w: cannot transition from %s to %s",
			domain.ErrInvalidStatusTransition, o.Status, target)
	}

	// Переход разрешён — меняем статус и обновляем timestamp
	o.Status = target
	o.UpdatedAt = time.Now()
	return nil
}

// MarkAsPaid фиксирует оплату заказа.
// Устанавливает PaidAt — момент фактической оплаты (нужен для бухгалтерии и аналитики).
func (o *Order) MarkAsPaid() error {
	if err := o.transitionTo(OrderStatusPaid); err != nil {
		return err
	}
	now := time.Now()
	o.PaidAt = &now
	return nil
}

// Ship отмечает заказ как отправленный.
// Только оплаченный заказ может быть отправлен.
func (o *Order) Ship() error {
	return o.transitionTo(OrderStatusShipped)
}

// Deliver отмечает заказ как доставленный.
// Только отправленный заказ может быть доставлен.
func (o *Order) Deliver() error {
	return o.transitionTo(OrderStatusDelivered)
}

// Cancel отменяет заказ.
//
// ПОЧЕМУ отмена возможна только из pending и paid, но не из shipped:
// Если товар уже отправлен, отмена — это другой бизнес-процесс (возврат),
// который требует логистики, проверки состояния товара и т.д.
// Это отдельный use-case, который не стоит смешивать с простой отменой.
func (o *Order) Cancel() error {
	return o.transitionTo(OrderStatusCancelled)
}

// IsFinal проверяет, находится ли заказ в финальном статусе.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ЧТО ТАКОЕ ФИНАЛЬНЫЙ СТАТУС?                                                │
// └──────────────────────────────────────────────────────────────────────────┘
// Статус, из которого нет переходов: allowedTransitions[status] пустая мапа.
// Для текущей state machine: delivered и cancelled.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ДВА ПОДХОДА К РЕАЛИЗАЦИИ:                                                  │
// └──────────────────────────────────────────────────────────────────────────┘
//
//  1. ХАРДКОД (текущий вариант):
//     return o.Status == OrderStatusDelivered || o.Status == OrderStatusCancelled
//     + Простота, нулевые накладные расходы
//     - При добавлении нового финального статуса (например, Refunded) легко забыть обновить IsFinal
//
//  2. ДИНАМИЧЕСКАЯ ПРОВЕРКА ЧЕРЕЗ STATE MACHINE:
//     return len(allowedTransitions[o.Status]) == 0
//     + Автоматически работает для любых финальных статусов
//     - Дополнительный вызов функции len() и доступ к мапе
//
// ВЫБОР:
// Для production-систем с часто меняющимися статусами → динамическая проверка безопаснее.
// Для учебного проекта → оставляем хардкод для читаемости (но помним про альтернативу).
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ИСПОЛЬЗОВАНИЕ:                                                             │
// └──────────────────────────────────────────────────────────────────────────┘
// - UI: if order.IsFinal() { скрыть кнопки "Отменить", "Изменить" }
// - Валидация: if order.IsFinal() { return errors.New("нельзя изменить завершённый заказ") }
// - Аналитика: SELECT COUNT(*) WHERE NOT is_final → активных заказов
func (o *Order) IsFinal() bool {
	// ХАРДКОД-ВАРИАНТ (выбран для явности):
	return o.Status == OrderStatusDelivered || o.Status == OrderStatusCancelled

	// ДИНАМИЧЕСКИЙ ВАРИАНТ (раскомментируй, если нужна гибкость):
	// return len(allowedTransitions[o.Status]) == 0
}
