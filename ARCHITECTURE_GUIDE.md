# Architecture Guide — Путь к Senior

> Справочник архитектурных решений, принципов и паттернов проекта `real_time_system`.
> Все примеры взяты из реального кода. Каждое правило объясняет **"почему так"** и показывает **"что было бы, если иначе"**.

---

## 📚 Оглавление

1. [Clean Architecture: Domain vs Service](#clean-architecture-domain-vs-service)
2. [Инварианты Entity](#инварианты-entity)
3. [State Machine: Управление состояниями](#state-machine-управление-состояниями)
4. [Конкурентность в Go](#конкурентность-в-go)
5. [Value Objects: Иммутабельность](#value-objects-иммутабельность)
6. [Typed IDs: Безопасность типов](#typed-ids-безопасность-типов)
7. [Error Handling: Sentinel Errors](#error-handling-sentinel-errors)
8. [Factory Methods: Валидация при создании](#factory-methods-валидация-при-создании)
9. [Repository Patterns: Работа с PostgreSQL](#repository-patterns-работа-с-postgresql)
10. [Связанные таблицы (1:N): Cart + CartItems](#связанные-таблицы-1n-cart--cartitems)
11. [UPSERT Pattern: INSERT ON CONFLICT](#upsert-pattern-insert-on-conflict)
12. [ON DELETE CASCADE vs Soft Delete](#on-delete-cascade-vs-soft-delete)
13. [Идемпотентность операций](#идемпотентность-операций)
14. [Data Enrichment Pattern](#data-enrichment-pattern)
15. [Snapshot Pattern: Исторические данные](#snapshot-pattern-исторические-данные)
16. [DTO для составных типов (MoneyResponse)](#dto-для-составных-типов-moneyresponse)
17. [Service Layer: Оркестрация и Helper-методы](#service-layer-оркестрация-и-helper-методы)
18. [Querier Interface: Поддержка транзакций](#querier-interface-поддержка-транзакций)
19. [Атомарное обновление stock](#атомарное-обновление-stock)
20. [Action-based API: Production подход](#action-based-api-production-подход)
21. [PlaceOrder: Транзакция оформления заказа](#placeorder-транзакция-оформления-заказа)

---

## Clean Architecture: Domain vs Service

### Золотое правило разделения

```
┌─────────────────────────────────────────────────────────────────┐
│ DOMAIN LOGIC (entity)                                            │
│ • Правила, истинные ВСЕГДА для entity                            │
│ • Защита инвариантов                                             │
│ • НЕ зависит от внешних систем (БД, API, файлы)                  │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│ APPLICATION LOGIC (service)                                      │
│ • Оркестрация между entities                                     │
│ • Вызовы БД, внешних API, email                                  │
│ • Транзакции, события                                            │
└─────────────────────────────────────────────────────────────────┘
```

### ✅ Правильно: Entity защищает инвариант

```go
// domain/entity/order.go
func NewOrder(userID UserID, items []OrderItem) (*Order, error) {
    if len(items) == 0 {
        return nil, ErrEmptyOrderItems // инвариант: заказ без товаров невалиден
    }

    // Entity САМА считает TotalAmount — гарантия консистентности
    totalAmount := value_objects.Zero(items[0].Price.Currency)
    for _, item := range items {
        itemTotal, _ := item.ItemTotal()
        totalAmount, _ = totalAmount.Add(itemTotal)
    }

    return &Order{
        Items:       items,
        TotalAmount: totalAmount, // ВСЕГДА соответствует Items
        Status:      OrderStatusPending,
    }, nil
}
```

**Почему в entity, а не в service?**
```
Инвариант: TotalAmount = сумма OrderItem

Если считает service → ничто не мешает создать невалидный заказ:
  order := &Order{
      Items: []OrderItem{{Price: 1000}},
      TotalAmount: Money{Amount: 500}, // БАГ!
  }

Если считает entity → невалидный заказ создать НЕВОЗМОЖНО.
```

---

### ❌ Неправильно: Service дублирует валидацию

```go
// ❌ АНТИ-ПАТТЕРН
func (s *CartService) AddItem(cart *Cart, quantity int) error {
    // Service проверяет quantity
    if quantity <= 0 {
        return errors.New("invalid quantity")
    }
    // Entity ТОЖЕ проверяет — дублирование!
    return cart.AddItem(productID, quantity, price)
}
```

**Проблема:**
- Дублирование логики
- Если забыть проверку в service → баг
- Если изменить правило (например, `quantity > 5`) → править в двух местах

**✅ Правильно:**
```go
func (s *CartService) AddItem(cart *Cart, quantity int) error {
    // Service доверяет entity
    return cart.AddItem(productID, quantity, price)
}

// domain/entity/cart.go
func (c *Cart) AddItem(..., quantity int, ...) error {
    if quantity <= 0 {
        return ErrInvalidQuantity // ЕДИНСТВЕННОЕ место проверки
    }
    // ...
}
```

---

### ❌ Неправильно: Entity знает про БД

```go
// ❌ АНТИ-ПАТТЕРН
func (o *Order) MarkAsPaid() error {
    // БАГ: entity обращается к БД!
    user, _ := database.FindUserByID(o.UserID)
    if user.Balance < o.TotalAmount {
        return errors.New("insufficient funds")
    }
    o.Status = OrderStatusPaid
}
```

**Проблема:**
- Entity зависит от БД → нельзя протестировать без БД
- Нарушение Clean Architecture: domain зависит от infrastructure

**✅ Правильно:**
```go
// domain/entity/order.go — чистая логика
func (o *Order) MarkAsPaid() error {
    return o.transitionTo(OrderStatusPaid) // только проверка state machine
}

// service/order_service.go — оркестрация
func (s *OrderService) PayOrder(orderID OrderID) error {
    order, _ := s.orderRepo.FindByID(orderID)    // БД
    user, _ := s.userRepo.FindByID(order.UserID) // БД

    if user.Balance < order.TotalAmount {
        return errors.New("insufficient funds") // бизнес-правило service-уровня
    }

    order.MarkAsPaid()                    // domain logic
    s.orderRepo.Update(order)             // БД
    s.emailService.SendConfirmation(...)  // внешняя система
}
```

---

### 📋 Чеклист: куда поместить логику?

```
❓ Правило истинно ВСЕГДА, даже в unit-тесте без БД?
   → ДА: в entity
   → НЕТ: в service

❓ Правило зависит от внешних систем (БД, API)?
   → ДА: в service
   → НЕТ: может быть в entity

❓ Правило координирует несколько entities?
   → ДА: в service
   → НЕТ: может быть в entity

❓ Можно ли нарушить правило, обойдя service?
   → ДА: переместить в entity (защитить инвариант)
   → НЕТ: может остаться в service
```

---

## Инварианты Entity

### Что такое инвариант?

**Инвариант** = бизнес-правило, которое **ВСЕГДА** истинно для entity, в любом состоянии, в любом месте кода.

```
Если инвариант нарушен → entity в невалидном состоянии → БАГ
```

### Примеры инвариантов из нашего кода

#### 1. Cart: синхронизация Items и TotalPrice

```go
// domain/entity/cart.go
func (c *Cart) AddItem(productID ProductID, quantity int, price Money) error {
    c.mu.Lock()
    defer c.mu.Unlock()

    // Добавляем товар
    c.Items[productID] = CartItem{...}

    // СРАЗУ обновляем TotalPrice (в одной транзакции)
    itemCost, _ := price.Multiply(int64(quantity))
    c.TotalPrice, _ = c.TotalPrice.Add(itemCost)

    return nil
}
```

**Инвариант:**
```
TotalPrice ВСЕГДА = сумма стоимости всех Items
```

**Что было бы без защиты инварианта:**
```go
// ❌ АНТИ-ПАТТЕРН
func (c *Cart) AddItem(...) {
    c.Items[productID] = item
    // Забыли обновить TotalPrice → инвариант нарушен!
}

// Теперь:
cart.Items = [товар 500₽, товар 300₽]
cart.TotalPrice = 0₽  // БАГ! Должно быть 800₽
```

---

#### 2. Order: проверка пустого списка товаров

```go
func NewOrder(userID UserID, items []OrderItem) (*Order, error) {
    if len(items) == 0 {
        return nil, ErrEmptyOrderItems // защита инварианта
    }
    // ...
}
```

**Инвариант:**
```
Заказ ВСЕГДА содержит минимум 1 товар
```

**Что было бы без проверки:**
```go
order := &Order{Items: []OrderItem{}} // пустой заказ
order.TotalAmount.Amount // паника при расчёте!
```

---

#### 3. Money: проверка совпадения валюты

```go
// domain/value_objects/money.go
func (m Money) Add(other Money) (Money, error) {
    if m.Currency != other.Currency {
        return Money{}, ErrCurrencyMismatch // защита инварианта
    }
    return Money{
        Amount:   m.Amount + other.Amount,
        Currency: m.Currency,
    }, nil
}
```

**Инвариант:**
```
Нельзя складывать деньги в разных валютах
```

**Что было бы без проверки:**
```go
rub := Money{Amount: 1000, Currency: RUB}
usd := Money{Amount: 10, Currency: USD}
total := rub.Add(usd) // 1010 в какой валюте?! БАГ!
```

---

## State Machine: Управление состояниями

### Вложенная мапа для O(1) проверки переходов

```go
// domain/entity/order.go
var allowedTransitions = map[OrderStatus]map[OrderStatus]bool{
    OrderStatusPending: {
        OrderStatusPaid:      true,
        OrderStatusCancelled: true,
    },
    OrderStatusPaid: {
        OrderStatusShipped:   true,
        OrderStatusCancelled: true,
    },
    OrderStatusShipped: {
        OrderStatusDelivered: true,
    },
    OrderStatusDelivered: {},
    OrderStatusCancelled: {},
}
```

### Почему вложенная мапа, а не слайс?

```go
// ❌ БЫЛО (линейный поиск O(n))
var allowedTransitions = map[OrderStatus][]OrderStatus{
    OrderStatusPending: {OrderStatusPaid, OrderStatusCancelled},
}

func (o *Order) CanTransitionTo(target OrderStatus) bool {
    for _, s := range allowedTransitions[o.Status] { // O(n)
        if s == target {
            return true
        }
    }
    return false
}

// ✅ СТАЛО (хэш-лукап O(1))
func (o *Order) CanTransitionTo(target OrderStatus) bool {
    return allowedTransitions[o.Status][target] // O(1)
}
```

**Разница:**
- Слайс `[]OrderStatus` → O(n), для 10 переходов = 10 проверок
- Мапа `map[OrderStatus]bool` → O(1), константное время

**Масштабируемость:**
```go
// Добавление 5 новых статусов — производительность не меняется
OrderStatusPaid: {
    OrderStatusShipped:   true,
    OrderStatusCancelled: true,
    OrderStatusRefunded:  true,  // +1
    OrderStatusOnHold:    true,  // +1
    OrderStatusDisputed:  true,  // +1
}
```

---

### API для работы с state machine

#### 1. Проверка возможности перехода

```go
if order.CanTransitionTo(OrderStatusShipped) {
    // Показать кнопку "Отправить"
}
```

#### 2. Получение списка доступных действий

```go
// API endpoint: GET /orders/{id}/available-actions
available := order.GetAvailableTransitions()
// → ["paid", "cancelled"]
```

#### 3. Выполнение перехода

```go
// ✅ Правильно: через бизнес-методы
if err := order.MarkAsPaid(); err != nil {
    // обработка ошибки
}

// ❌ Неправильно: прямое изменение
order.Status = OrderStatusPaid // обходит state machine!
```

---

### Визуализация state machine

```
pending ──────┬──→ paid ──────┬──→ shipped ──→ delivered
              │                │
              └──→ cancelled ←─┘
```

**Правило:** стрелка "A → B" существует = `allowedTransitions[A][B] == true`

---

## Конкурентность в Go

### sync.RWMutex для защиты от race conditions

```go
// domain/entity/cart.go
type Cart struct {
    Items      map[ProductID]CartItem
    TotalPrice Money
    mu         sync.RWMutex // защита от data race
}

func (c *Cart) AddItem(...) error {
    c.mu.Lock()         // блокируем на запись
    defer c.mu.Unlock() // гарантированно разблокируем

    // Атомарно: Items + TotalPrice
    c.Items[productID] = item
    c.TotalPrice, _ = c.TotalPrice.Add(itemCost)
}

func (c *Cart) GetTotalPrice() Money {
    c.mu.RLock()         // блокируем только чтение
    defer c.mu.RUnlock()
    return c.TotalPrice
}
```

### Почему RWMutex, а не Mutex?

```
sync.Mutex:
  - Lock() блокирует ВСЕ (чтение + запись)
  - Два параллельных чтения — блокируются друг другом

sync.RWMutex:
  - RLock() — несколько читателей параллельно ✅
  - Lock() — эксклюзивная блокировка при записи
```

**Пример:**
```go
// 3 горутины одновременно:
go cart.GetTotalPrice() // RLock() — ОК
go cart.GetTotalPrice() // RLock() — ОК (параллельно с первой)
go cart.AddItem(...)    // Lock() — ждёт завершения чтений
```

---

### ❌ Что было бы без мьютекса

```go
// ❌ АНТИ-ПАТТЕРН (без мьютекса)
func (c *Cart) AddItem(...) {
    c.Items[productID] = item       // запись в мапу
    c.TotalPrice = c.TotalPrice + x // чтение + запись
}
```

**Сценарий с data race:**
```
Горутина A                      Горутина B
────────────────────────────────────────────────────
Читает TotalPrice = 1000₽
                                Читает TotalPrice = 1000₽
Пишет TotalPrice = 1500₽
                                Пишет TotalPrice = 1200₽
───────────────────────────────────────────────────
Результат: TotalPrice = 1200₽ (должен быть 1700₽!)
```

**С мьютексом:**
```
Горутина A                      Горутина B
────────────────────────────────────────────────────
Lock()
Читает + пишет TotalPrice
Unlock()
                                Lock() ← ждёт разблокировки
                                Читает + пишет TotalPrice
                                Unlock()
───────────────────────────────────────────────────
Результат: TotalPrice = 1700₽ ✅
```

---

### Почему mu — приватное поле

```go
type Cart struct {
    mu sync.RWMutex // маленькая буква — приватное
}
```

**Причина:** мьютекс — деталь реализации, не API entity.

**❌ Если сделать публичным:**
```go
type Cart struct {
    Mu sync.RWMutex // публичный
}

// В коде:
cart.Mu.Lock()
doSomething()
// Забыли Unlock() → deadlock!
```

**✅ С приватным:**
```go
// Единственный способ изменить cart — через методы
cart.AddItem(...) // внутри гарантированно Lock/Unlock
```

---

## Value Objects: Иммутабельность

### Почему Money возвращает новый экземпляр

```go
// domain/value_objects/money.go
func (m Money) Add(other Money) (Money, error) {
    return Money{
        Amount:   m.Amount + other.Amount, // создаём НОВЫЙ Money
        Currency: m.Currency,
    }, nil
}
```

**Использование:**
```go
price := Money{Amount: 1000, Currency: RUB}
newPrice, _ := price.Add(Money{Amount: 500, Currency: RUB})

// price    = 1000₽ (не изменился!)
// newPrice = 1500₽ (новый объект)
```

---

### ❌ Что было бы с мутацией

```go
// ❌ АНТИ-ПАТТЕРН (мутация value object)
func (m *Money) Add(other Money) error {
    m.Amount += other.Amount // меняем исходный объект
    return nil
}
```

**Проблема:**
```go
price := Money{Amount: 1000}
cart.TotalPrice = price

price.Add(Money{Amount: 500}) // меняем price

// БАГ: cart.TotalPrice ТОЖЕ изменился! (та же ссылка)
```

**С иммутабельностью:**
```go
price := Money{Amount: 1000}
cart.TotalPrice = price

newPrice := price.Add(Money{Amount: 500})

// price            = 1000₽ (не изменился)
// cart.TotalPrice  = 1000₽ (не изменился)
// newPrice         = 1500₽ (новый объект)
```

---

### Преимущества иммутабельности

1. **Thread-safety:** можно читать из нескольких горутин без мьютексов
2. **Предсказуемость:** функция не меняет аргументы
3. **История:** можно хранить снапшоты состояния

```go
// Хранение истории цен
priceHistory := []Money{}
price := Money{Amount: 1000}
priceHistory = append(priceHistory, price)

price = price.Add(Money{Amount: 100}) // новый объект
priceHistory = append(priceHistory, price)

// priceHistory[0] = 1000₽ (не изменился!)
// priceHistory[1] = 1100₽
```

---

## Typed IDs: Безопасность типов

### Почему не string/uuid.UUID

```go
// ❌ БЕЗ ТИПИЗАЦИИ (string)
func ProcessOrder(userID string, orderID string) {
    // ...
}

// БАГ: перепутали аргументы
ProcessOrder(orderID, userID) // компилятор не поймал!
```

**✅ С ТИПИЗАЦИЕЙ:**
```go
// domain/entity/user.go
type UserID uuid.UUID

// domain/entity/order.go
type OrderID uuid.UUID

func ProcessOrder(userID UserID, orderID OrderID) {
    // ...
}

// ProcessOrder(orderID, userID) // ошибка компиляции! ✅
```

---

### API для работы с typed IDs

```go
// Создание
userID := NewUserID() // генерирует новый UUID

// Парсинг из строки
userID, err := ParseUserID("550e8400-e29b-41d4-a716-446655440000")

// Конвертация в строку (для JSON, логов)
str := userID.String() // "550e8400-..."

// Проверка на zero-value
if userID.IsZero() {
    return errors.New("invalid user ID")
}
```

---

### Преимущества

1. **Защита от опечаток:** `userID` ≠ `orderID` на уровне типов
2. **Читаемость:** `func GetUser(id UserID)` понятнее, чем `func GetUser(id string)`
3. **Рефакторинг:** IDE найдёт все использования `UserID`

---

## Error Handling: Sentinel Errors

### Что такое sentinel error

**Sentinel error** = глобальная переменная с ошибкой для программной обработки.

```go
// domain/errors.go
var (
    ErrInvalidQuantity       = errors.New("quantity must be positive")
    ErrItemNotFound          = errors.New("item not found in cart")
    ErrInvalidStatusTransition = errors.New("invalid status transition")
)
```

---

### Почему не просто errors.New в коде?

```go
// ❌ АНТИ-ПАТТЕРН
func (c *Cart) RemoveItem(productID ProductID) error {
    if _, exists := c.Items[productID]; !exists {
        return errors.New("item not found") // новая ошибка каждый раз
    }
}

// В коде:
err := cart.RemoveItem(id)
if err.Error() == "item not found" { // сравнение строк! хрупко
    // ...
}
```

**Проблемы:**
- Сравнение строк хрупко (опечатка = баг)
- Изменение текста ошибки ломает код

---

### ✅ С sentinel errors

```go
// domain/entity/cart.go
func (c *Cart) RemoveItem(productID ProductID) error {
    if _, exists := c.Items[productID]; !exists {
        return ErrItemNotFound // возвращаем глобальную переменную
    }
}

// В коде:
err := cart.RemoveItem(id)
if errors.Is(err, domain.ErrItemNotFound) { // сравнение по значению ✅
    return httpStatus(404) // "не найдено"
}
```

---

### Когда использовать sentinel errors

```
✅ Используй для ошибок, которые нужно ОБРАБАТЫВАТЬ программно:
   - ErrItemNotFound → возвращаем HTTP 404
   - ErrInvalidStatusTransition → возвращаем HTTP 400

❌ НЕ используй для ошибок, которые просто логируются:
   - return fmt.Errorf("invalid email format: %s", email)
```

---

## Factory Methods: Валидация при создании

### Почему не создаём entity через &Order{...}

```go
// ❌ АНТИ-ПАТТЕРН
order := &Order{
    Items:       items,
    TotalAmount: Money{}, // забыли посчитать!
    Status:      "pending", // опечатка в статусе
}
```

**Проблемы:**
- Забыли посчитать `TotalAmount` → инвариант нарушен
- Опечатка в статусе → некорректная state machine
- Нет валидации `items` → пустой список

---

### ✅ Фабричный метод с валидацией

```go
// domain/entity/order.go
func NewOrder(userID UserID, items []OrderItem) (*Order, error) {
    // 1. Валидация
    if len(items) == 0 {
        return nil, ErrEmptyOrderItems
    }

    // 2. Расчёт инвариантов
    totalAmount := calculateTotal(items)

    // 3. Гарантированно валидное состояние
    return &Order{
        ID:          NewOrderID(),
        UserID:      userID,
        Items:       items,
        TotalAmount: totalAmount, // всегда корректный
        Status:      OrderStatusPending, // константа, не опечатка
        CreatedAt:   time.Now(),
        UpdatedAt:   time.Now(),
    }, nil
}
```

**Преимущества:**
- Невозможно создать невалидный Order
- Все инварианты защищены
- Единственная точка создания = единое место валидации

---

### Правило

```
✅ Всегда используй фабрики для entities с инвариантами:
   - NewOrder()
   - NewCart()
   - NewUser()

❌ Можно использовать &Struct{} для простых DTO без правил:
   - &HTTPRequest{}
   - &Config{}
```

---

## 🎯 Итоговый чеклист senior-разработчика

### Domain Entity

- ✅ Защищает инварианты (TotalPrice = сумма Items)
- ✅ Валидация в фабричных методах (NewOrder)
- ✅ НЕ зависит от внешних систем (БД, API)
- ✅ Thread-safe при необходимости (sync.RWMutex)
- ✅ Использует typed IDs (UserID, OrderID)
- ✅ Возвращает sentinel errors для программной обработки

### Service Layer

- ✅ Оркестрация между entities
- ✅ Вызовы repository (БД)
- ✅ Интеграция с внешними API
- ✅ Транзакции, события, email
- ❌ НЕ дублирует валидацию entity
- ❌ НЕ обходит методы entity (напрямую меняя поля)

### State Machine

- ✅ Единый источник правды (allowedTransitions)
- ✅ O(1) проверка через вложенную мапу
- ✅ API: CanTransitionTo, GetAvailableTransitions
- ✅ Приватный метод transitionTo для централизации логики

### Value Objects

- ✅ Иммутабельны (возвращают новый экземпляр)
- ✅ Валидируют инварианты (валюта совпадает)
- ✅ Thread-safe (нет мутаций)

---

---

## DTO Pattern: Изоляция слоёв

### Что такое DTO?

**DTO (Data Transfer Object)** = объект для передачи данных между слоями, **без бизнес-логики**.

```
Entity:  User { ID, Name, Email, Password, DeletedAt }
DTO:     UserResponse { ID, Name, Email } // без Password, DeletedAt
```

---

### Почему НЕ использовать Entity напрямую в API?

```go
// ❌ БЕЗ DTO (анти-паттерн)
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
    var user entity.User // API завязан на структуру entity
    json.NewDecoder(r.Body).Decode(&user)
    // Проблемы:
    // 1. Клиент может установить ID, CreatedAt (должны генерироваться сервером)
    // 2. Добавили поле Password в entity → автоматически попало в API
    // 3. Изменили поле в entity → breaking change для клиентов
}
```

**✅ С DTO:**

```go
// internal/service/dto/user_dto.go
type CreateUserRequest struct {
    Name    string `json:"name"`
    Surname string `json:"surname"`
    Email   string `json:"email"`
    // ID, CreatedAt НЕ здесь — генерируются сервером
}

type UserResponse struct {
    ID        string `json:"id"`
    Name      string `json:"name"`
    Email     string `json:"email"`
    CreatedAt string `json:"created_at"`
    // Password НЕ здесь — не экспозим чувствительные данные
}

// Mapper function
func ToUserResponse(user *entity.User) UserResponse {
    return UserResponse{
        ID:        user.ID.String(),
        Name:      user.Name,
        Email:     user.Email,
        CreatedAt: user.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
    }
}
```

---

### Преимущества DTO

1. **Изоляция слоёв:** API не зависит от entity структуры
2. **Безопасность:** не экспозим внутренние поля (Password, DeletedAt)
3. **Гибкость:** можем вернуть computed fields (FullName), которых нет в entity
4. **Обратная совместимость:** изменения в entity не ломают API

---

### Когда использовать DTO?

```
✅ ВСЕГДА для:
   - HTTP API (REST, GraphQL)
   - gRPC (protobuf → DTO → entity)
   - WebSocket messages
   - Внешние интеграции

❌ НЕ нужно для:
   - Внутренних вызовов (service → repository)
   - Unit-тестов entity
```

---

## SOFT DELETE: Production подход

### HARD DELETE vs SOFT DELETE

```go
// HARD DELETE (физическое удаление)
DELETE FROM users WHERE id = $1

// SOFT DELETE (логическое удаление)
UPDATE users SET deleted_at = NOW() WHERE id = $1
```

---

### Почему в production используют SOFT DELETE?

#### 1. Восстановление данных

```go
// Пользователь удалил аккаунт по ошибке
// HARD DELETE: данные потеряны навсегда ❌
// SOFT DELETE: можно восстановить ✅
UPDATE users SET deleted_at = NULL WHERE id = $1
```

#### 2. Аудит и GDPR Compliance

```sql
-- Кто и когда удалился?
SELECT id, email, deleted_at FROM users WHERE deleted_at IS NOT NULL;

-- Сколько пользователей ушло за месяц?
SELECT COUNT(*) FROM users WHERE deleted_at > NOW() - INTERVAL '30 days';
```

#### 3. Foreign Keys остаются валидными

```sql
-- Order ссылается на User
-- HARD DELETE: foreign key violation ❌
-- SOFT DELETE: ссылка остаётся валидной ✅
SELECT * FROM orders o JOIN users u ON o.user_id = u.id;
```

#### 4. Уникальность email

```sql
-- Constraint с WHERE: удалённый email не блокирует регистрацию
CONSTRAINT unique_active_email UNIQUE (email) WHERE (deleted_at IS NULL)

-- user@mail.com удалился → новый user@mail.com может зарегистрироваться
```

---

### Реализация в entity

```go
type User struct {
    ID        UserID
    Email     string
    DeletedAt *time.Time // nil = активный, !nil = удалён
}

func (u *User) IsDeleted() bool {
    return u.DeletedAt != nil
}

func (u *User) Delete() error {
    if u.IsDeleted() {
        return errors.New("user already deleted")
    }
    now := time.Now()
    u.DeletedAt = &now
    return nil
}
```

---

### Реализация в repository

```go
// ВАЖНО: все запросы должны включать WHERE deleted_at IS NULL

func (r *UserRepository) FindByID(ctx, id) (*User, error) {
    q := `
        SELECT id, email, deleted_at
        FROM users
        WHERE id = $1 AND deleted_at IS NULL
    `
    // БЕЗ "deleted_at IS NULL" вернём удалённого пользователя → баг!
}

func (r *UserRepository) Delete(ctx, id) error {
    q := `
        UPDATE users
        SET deleted_at = NOW()
        WHERE id = $1 AND deleted_at IS NULL
    `
    // SOFT DELETE: обновляем, а не удаляем
}
```

---

### Когда использовать HARD DELETE?

```
SOFT DELETE (production default):
✅ User, Order, Product — бизнес-данные
✅ Audit trail нужен
✅ Foreign keys есть

HARD DELETE (исключения):
✅ Session tokens (короткоживущие)
✅ Rate limit counters
✅ Тестовые данные
```

---

## sql.Scanner и driver.Valuer: Интеграция с БД

### Проблема: typed IDs и pgx

```go
type UserID uuid.UUID // custom type

// БЕЗ Scanner/Valuer: ручной парсинг
var idStr string
db.QueryRow(...).Scan(&idStr) // сканируем в строку
user.ID, _ = entity.ParseUserID(idStr) // парсим вручную

// С Scanner/Valuer: автоматическая конвертация
var user entity.User
db.QueryRow(...).Scan(&user.ID) // pgx автоматически вызовет Scan()
```

---

### Реализация

```go
// domain/entity/user.go
import "database/sql/driver"

// Scan реализует sql.Scanner для ЧТЕНИЯ из БД
func (id *UserID) Scan(src interface{}) error {
    switch v := src.(type) {
    case string:
        parsed, err := uuid.Parse(v)
        if err != nil {
            return err
        }
        *id = UserID(parsed)
        return nil
    case []byte:
        parsed, err := uuid.ParseBytes(v)
        if err != nil {
            return err
        }
        *id = UserID(parsed)
        return nil
    case nil:
        *id = UserID(uuid.Nil) // NULL из БД
        return nil
    default:
        return fmt.Errorf("cannot scan %T into UserID", src)
    }
}

// Value реализует driver.Valuer для ЗАПИСИ в БД
func (id UserID) Value() (driver.Value, error) {
    return id.String(), nil // возвращаем строку
}
```

---

### Как это работает

```go
// ЧТЕНИЕ: БД → UserID
var user entity.User
db.QueryRow("SELECT id FROM users WHERE id = $1", userID).Scan(
    &user.ID, // pgx вызовет user.ID.Scan(src)
)

// ЗАПИСЬ: UserID → БД
db.Exec("INSERT INTO users (id) VALUES ($1)",
    user.ID, // pgx вызовет user.ID.Value()
)
```

---

### Преимущества

1. **Меньше кода:** не нужно парсить вручную
2. **Меньше ошибок:** нельзя забыть проверить err от ParseUserID
3. **Единообразие:** все typed IDs работают одинаково
4. **Производительность:** pgx может оптимизировать конвертацию

---

## Partial Update: Pointer-based DTO

### Проблема: как обновить только часть полей?

```go
// Клиент хочет обновить ТОЛЬКО email, не трогая name/surname
PATCH /users/{id}
{
    "email": "new@example.com"
}

// ❌ Неправильно: struct с обычными полями
type UpdateUserRequest struct {
    Name    string // как отличить "не передано" от "передана пустая строка"?
    Email   string
}
```

---

### Решение: pointer fields

```go
// ✅ Правильно: pointer fields
type UpdateUserRequest struct {
    Name    *string `json:"name,omitempty"`
    Surname *string `json:"surname,omitempty"`
    Email   *string `json:"email,omitempty"`
}

// nil = поле не передано, не обновляем
// !nil = поле передано, обновляем на новое значение (даже если это "")
```

---

### Использование в service

```go
func (s *UserService) UpdateUser(ctx, id, req UpdateUserRequest) error {
    user, _ := s.userRepo.FindByID(ctx, id)

    // Обновляем только переданные поля
    if req.Name != nil {
        user.Name = *req.Name
    }
    if req.Surname != nil {
        user.Surname = *req.Surname
    }
    if req.Email != nil {
        user.Email = *req.Email
    }

    user.UpdatedAt = time.Now()
    return s.userRepo.Update(ctx, user)
}
```

---

### Сценарии использования

```json
// Обновить только email
{"email": "new@mail.com"}
// Name, Surname не обновятся

// Обновить name и surname
{"name": "John", "surname": "Doe"}
// Email не обновится

// Обновить все поля
{"name": "John", "surname": "Doe", "email": "john@mail.com"}
```

---

## Production Logging

### Что логировать?

```go
// ✅ ЛОГИРОВАТЬ (уровень Error):

// 1. Ошибки БД (connection, deadlock)
l.Errorw("failed to create user", "error", err, "user_id", user.ID)

// 2. Неожиданные ошибки парсинга
l.Errorw("corrupted UUID in database", "id", idStr, "error", err)

// ✅ ЛОГИРОВАТЬ (уровень Info):

// 3. Важные бизнес-события
l.Infow("user created", "user_id", user.ID, "email", user.Email)
l.Infow("order placed", "order_id", order.ID, "amount", order.Total)

// ❌ НЕ ЛОГИРОВАТЬ:

// 4. NotFound (404) — нормальный сценарий
if err == pgx.ErrNoRows {
    // НЕ логируем
    return nil, domain.NewNotFoundError("user")
}

// 5. ValidationError (400) — пользователь ввёл неверные данные
if user.Email == "" {
    // НЕ логируем
    return domain.NewValidationError("email required")
}
```

---

### Правило: логируй то, что поможет найти баг в production

```
❓ Если это случится в 3 часа ночи — мне нужно проснуться?
   ДА → l.Errorw()
   НЕТ → не логируем
```

---

## PostgreSQL Error Handling

### Коды ошибок PostgreSQL

```go
// github.com/jackc/pgx/v5/pgconn
type PgError struct {
    Code string // PostgreSQL error code
    // ...
}

// Основные коды:
// 23505 → unique_violation (duplicate key)
// 23503 → foreign_key_violation
// 23514 → check_violation
```

---

### Конвертация в domain errors

```go
func (r *UserRepository) Create(ctx, user) error {
    _, err := r.db.Exec(ctx, query, user.ID, user.Email)

    if err != nil {
        var pgErr *pgconn.PgError
        if errors.As(err, &pgErr) {
            // 23505 = unique_violation
            if pgErr.Code == "23505" {
                // Конвертируем в domain error с HTTP 409
                return domain.NewConflictError("email already exists")
            }
            // 23503 = foreign_key_violation
            if pgErr.Code == "23503" {
                return domain.NewValidationError("referenced entity not found")
            }
        }

        // Любая другая ошибка → 500
        return domain.NewInternalError("database error", err)
    }

    return nil
}
```

---

### Почему это важно?

1. **Handler не зависит от PostgreSQL**
   - Можем заменить на MySQL → handler не меняется

2. **Правильные HTTP коды**
   - 409 Conflict → клиент знает, что email занят
   - 500 Internal Server Error → клиент знает, что проблема на сервере

3. **Тестируемость**
   - `errors.Is(err, domain.ConflictError)` в unit-тестах

---

## Repository Patterns: Работа с PostgreSQL

### Money в БД: Два поля вместо JSON

**Проблема:**
Value Object `Money` содержит два поля: `Amount` и `Currency`. Как хранить в PostgreSQL?

**❌ Вариант 1: JSON field**
```sql
CREATE TABLE products (
    id UUID PRIMARY KEY,
    price JSONB NOT NULL -- {"amount": 1500, "currency": "RUB"}
);
```

**Проблемы JSON:**
1. **Медленный поиск по цене:**
   ```sql
   WHERE (price::jsonb->>'amount')::int BETWEEN 1000 AND 5000
   ```
   → нужно парсить JSON для каждой строки → нельзя использовать индекс

2. **Сложная агрегация:**
   ```sql
   SUM((price::jsonb->>'amount')::int) -- выручка за месяц
   ```

3. **Индексы сложнее:**
   ```sql
   CREATE INDEX idx_price ON products USING GIN(price); -- медленнее B-tree
   ```

---

**✅ Вариант 2: Два отдельных поля**
```sql
CREATE TABLE products (
    id UUID PRIMARY KEY,
    price_amount INTEGER NOT NULL,      -- 1500 (копейки)
    price_currency VARCHAR(10) NOT NULL -- 'RUB'
);

CREATE INDEX idx_price_amount ON products(price_amount);
```

**Преимущества:**
1. **Быстрый поиск:**
   ```sql
   WHERE price_amount BETWEEN 1000 AND 5000 -- использует B-tree индекс
   ```

2. **Простая агрегация:**
   ```sql
   SUM(price_amount) WHERE price_currency = 'RUB'
   ```

3. **B-tree индекс** (быстрее GIN для числовых данных)

**Когда JSON лучше:**
Если Money содержит >3 полей (Amount, Currency, ExchangeRate, Tax, Fee) → JSON удобнее.

**Код (repository):**
```go
// Create
_, err := r.db.Exec(ctx, q,
    product.Price.Amount,   // int64 → INTEGER
    product.Price.Currency, // Currency → VARCHAR
)

// FindByID
err := r.db.QueryRow(ctx, q).Scan(
    &product.Price.Amount,   // INTEGER → int64
    &product.Price.Currency, // VARCHAR → Currency
)
```

---

### created_at — Иммутабельность

**Правило:**
`created_at` устанавливается **один раз** при создании и **никогда** не обновляется.

**✅ Правильно:**
```go
func (r *ProductRepository) Update(ctx context.Context, product *entity.Product) error {
    q := `
        UPDATE products
        SET name = $1, price_amount = $2, updated_at = $3
        WHERE id = $4
    `
    // created_at НЕ ТРОГАЕМ!
}
```

**❌ Неправильно:**
```go
func (r *ProductRepository) Update(ctx context.Context, product *entity.Product) error {
    q := `
        UPDATE products
        SET name = $1, created_at = $2, updated_at = $3 -- ❌ обновляем created_at!
        WHERE id = $4
    `
}
```

**Почему это критично:**

1. **Аудит:**
   Теряем информацию о реальном времени создания записи.

2. **Compliance (GDPR):**
   Должны знать, когда данные появились в системе.

3. **Отчёты:**
   ```sql
   -- "Товары, добавленные в январе"
   SELECT * FROM products WHERE created_at BETWEEN '2026-01-01' AND '2026-02-01';
   -- Если обновляли created_at → отчёт неверен!
   ```

4. **Бухгалтерия:**
   Не сойдётся с реальными датами создания документов.

**Альтернативный подход (триггер):**
```sql
CREATE TRIGGER set_updated_at
BEFORE UPDATE ON products
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();
```

**Плюсы триггера:**
- Нельзя забыть обновить `updated_at`

**Минусы триггера:**
- Entity не знает финальное значение `updated_at`
- Нужен отдельный SELECT после UPDATE для получения актуального времени

**Наш подход:**
Service устанавливает `updated_at = time.Now()` перед вызовом repository.

---

### FindByPriceRange: BETWEEN vs >=/<

**Задача:**
Найти товары в ценовом диапазоне [min, max].

**✅ Правильно: BETWEEN**
```go
q := `
    SELECT * FROM products
    WHERE price_amount BETWEEN $1 AND $2 AND deleted_at IS NULL
    ORDER BY price_amount ASC
`
```

**❌ Неправильно: WHERE id = $1**
```go
q := `
    SELECT * FROM products
    WHERE id = $1 -- ❌ ищем по ID, а не по цене!
    ORDER BY price_amount
`
```

**Почему BETWEEN лучше >= AND <=:**

1. **Читаемость:**
   ```sql
   BETWEEN 1000 AND 5000  -- понятно сразу
   >= 1000 AND <= 5000    -- нужно вдумываться
   ```

2. **Включение границ:**
   `BETWEEN` включает `min` и `max` (inclusive).

3. **Индекс:**
   PostgreSQL оптимизирует `BETWEEN` для B-tree индексов.

**ORDER BY:**
```sql
ORDER BY price_amount ASC -- от дешёвых к дорогим
```

Логично для ценового фильтра.

---

### Пустой результат vs Ошибка

**Проблема:**
Как различать "нет товаров в диапазоне" и "ошибка БД"?

**❌ Неправильно: NotFoundError для пустого результата**
```go
rows, err := r.db.Query(ctx, q, min.Amount, max.Amount)
if err != nil {
    return []*entity.Product{}, domain.NewNotFoundError("products") // ❌
}

if len(products) == 0 {
    return nil, domain.NewNotFoundError("products") // ❌
}
```

**Проблема:**
Пустой результат — это **нормально**, а не ошибка. В диапазоне просто нет товаров.

**✅ Правильно: пустой slice + nil error**
```go
rows, err := r.db.Query(ctx, q, min.Amount, max.Amount)
if err != nil {
    // НАСТОЯЩАЯ ошибка БД (connection lost, syntax error)
    return nil, domain.NewInternalError("failed to query products", err)
}

products := make([]*entity.Product, 0) // пустой slice

for rows.Next() {
    // ... scan
    products = append(products, &product)
}

// Если пустой — возвращаем [] + nil
return products, nil
```

**Caller проверяет:**
```go
products, err := repo.FindByPriceRange(ctx, min, max)
if err != nil {
    // Ошибка БД → retry, fallback
    return err
}

if len(products) == 0 {
    // Нет товаров → показать "Ничего не найдено"
    return nil
}
```

**Когда NotFoundError уместен:**
```go
// FindByID — товар ДОЛЖЕН существовать, если ищем по ID
product, err := repo.FindByID(ctx, id)
if errors.Is(err, pgx.ErrNoRows) {
    return nil, domain.NewNotFoundError("product") // ✅
}
```

**Аналогия в REST API:**
```
GET /products?min=1000&max=5000
→ 200 OK + []  (пустой результат)

GET /products/550e8400
→ 404 Not Found  (товар не существует)
```

---

### Pre-allocation для Slice

**Проблема:**
При `append` в цикле Go может много раз реаллоцировать массив.

**❌ Неправильно: без capacity**
```go
products := make([]*entity.Product, 0)

for rows.Next() {
    var product entity.Product
    rows.Scan(&product)
    products = append(products, &product) // append может реаллоцировать
}
```

**Что происходит:**
```
cap=0 → append → cap=1 → realloc
cap=1 → append → cap=2 → realloc
cap=2 → append → cap=4 → realloc
cap=4 → append → cap=8 → realloc
...
```

**✅ Правильно: pre-allocate с capacity**
```go
rows, _ := r.db.Query(ctx, q, min, max)

// Вариант 1: если не знаем размер заранее
products := make([]*entity.Product, 0, 10) // capacity=10

// Вариант 2: если знаем точный размер
sourceProducts := []Product{ /* ... */ }
dtos := make([]*dto.ProductResponse, 0, len(sourceProducts)) // ✅

for _, p := range sourceProducts {
    response := dto.ToProductResponse(p)
    dtos = append(dtos, &response) // НЕТ реаллокаций
}
```

**Когда это критично:**

1. **Большие списки** (>1000 элементов)
   Экономия на реаллокациях → меньше нагрузка на GC.

2. **High-load API**
   Миллионы запросов в секунду → каждая аллокация важна.

3. **Latency-critical code**
   Реаллокация может вызвать GC pause.

**Когда можно не делать:**
Маленькие списки (<100 элементов) + low traffic → оптимизация не критична.

---

### rows.Err() — Проверка после итерации

**Проблема:**
Ошибка может возникнуть **во время** `rows.Next()`, а не только в `Query()`.

**❌ Неправильно: не проверяем rows.Err()**
```go
rows, err := r.db.Query(ctx, q)
if err != nil {
    return nil, err
}

for rows.Next() {
    rows.Scan(&product)
    products = append(products, &product)
}

return products, nil // ❌ пропускаем ошибку!
```

**Проблема:**
Если соединение упало во время чтения → `rows.Err()` содержит ошибку, но мы её игнорируем.

**✅ Правильно: проверяем rows.Err()**
```go
rows, err := r.db.Query(ctx, q)
if err != nil {
    return nil, err
}
defer rows.Close()

for rows.Next() {
    rows.Scan(&product)
    products = append(products, &product)
}

// ВАЖНО: проверяем ошибку после итерации
if err := rows.Err(); err != nil {
    return nil, domain.NewInternalError("error during iteration", err)
}

return products, nil
```

**Когда это критично:**
- Connection timeout во время чтения большого результата
- Network glitch между БД и приложением
- Deadlock detection (PostgreSQL прервал запрос)

---

## Связанные таблицы (1:N): Cart + CartItems

### LEFT JOIN для nullable данных

**Проблема:**
Как получить корзину вместе с её товарами, если корзина может быть пустой?

**❌ Неправильно: INNER JOIN**
```sql
SELECT c.*, ci.*
FROM carts c
JOIN cart_items ci ON c.id = ci.cart_id -- ❌
WHERE c.id = $1
```

**Проблема:**
INNER JOIN возвращает строки только если есть совпадение в обеих таблицах.
Пустая корзина (нет items) → 0 строк → "корзина не найдена" → БАГ!

**✅ Правильно: LEFT JOIN**
```sql
SELECT c.id, c.user_id, c.created_at, c.updated_at, c.deleted_at,
       ci.id, ci.cart_id, ci.product_id, ci.quantity,
       ci.price_amount, ci.price_currency
FROM carts c
LEFT JOIN cart_items ci ON c.id = ci.cart_id
WHERE c.id = $1 AND c.deleted_at IS NULL
```

**Как это работает:**
- LEFT JOIN возвращает все строки из левой таблицы (carts)
- Если нет соответствия в правой таблице (cart_items) → поля ci.* = NULL
- Пустая корзина вернёт 1 строку с NULL в полях ci.*

---

### Nullable поля при сканировании

**Проблема:**
Когда LEFT JOIN возвращает NULL, нельзя сканировать в non-pointer типы.

**❌ Неправильно: scan в non-pointer**
```go
var ciID entity.CartItemID // non-pointer
rows.Scan(&ciID) // ❌ panic при NULL!
```

**✅ Правильно: scan в pointer**
```go
var ciID *string // pointer → может быть nil
var ciQuantity *int
var ciPriceCurrency *string

rows.Scan(&ciID, &ciQuantity, &ciPriceCurrency)

// Проверяем: если ciID != nil, значит item существует
if ciID != nil {
    productID, _ := entity.ParseProductID(*ciProductID)
    item := entity.CartItem{
        ProductID: productID,
        Quantity:  *ciQuantity,
    }
    item.Price.Currency = value_objects.Currency(*ciPriceCurrency)
    cart.Items[productID] = item
}
```

**Паттерн:**
1. Сканируем nullable поля в `*string`, `*int`, `*int64`
2. Проверяем `!= nil` перед использованием
3. Используем Parse-функции для конвертации строки в typed ID

---

### Parse-функции для typed IDs

**Проблема:**
LEFT JOIN возвращает ID как nullable string. Нужно конвертировать в typed ID.

**Решение:**
```go
// domain/entity/product.go
func ParseProductID(s string) (ProductID, error) {
    parsed, err := uuid.Parse(s)
    if err != nil {
        return ProductID{}, fmt.Errorf("invalid ProductID: %w", err)
    }
    return ProductID(parsed), nil
}

// domain/entity/cart.go
func ParseCartID(s string) (CartID, error) {
    parsed, err := uuid.Parse(s)
    if err != nil {
        return CartID{}, fmt.Errorf("invalid CartID: %w", err)
    }
    return CartID(parsed), nil
}
```

**Использование в repository:**
```go
if ciID != nil {
    productID, _ := entity.ParseProductID(*ciProductID)
    cartItemID, _ := entity.ParseCartItemID(*ciID)

    item := entity.CartItem{
        ID:        cartItemID,
        ProductID: productID,
        // ...
    }
}
```

**Почему Parse, а не Scan:**
- `Scan()` работает автоматически для non-nullable полей (pgx вызывает его)
- `Parse()` нужен для nullable полей, которые мы сначала сканируем в `*string`

---

## UPSERT Pattern: INSERT ON CONFLICT

### Проблема race condition без UPSERT

**Сценарий:**
Два запроса одновременно добавляют товар в корзину.

**❌ Без UPSERT (read-modify-write):**
```go
// Запрос A                          // Запрос B
item := repo.FindByCartAndProduct()  item := repo.FindByCartAndProduct()
// item = nil                        // item = nil
if item == nil {
    repo.Insert(newItem)             if item == nil {
}                                        repo.Insert(newItem) // ❌ unique violation!
                                     }
```

**Проблема:**
Оба запроса видят "item не существует" → оба пытаются INSERT → ошибка.

**✅ С UPSERT (атомарная операция):**
```sql
INSERT INTO cart_items (id, cart_id, product_id, quantity, price_amount, price_currency)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (cart_id, product_id) DO UPDATE SET
    quantity = cart_items.quantity + EXCLUDED.quantity,
    updated_at = EXCLUDED.updated_at
```

**Как это работает:**
1. PostgreSQL пытается INSERT
2. Если есть conflict по (cart_id, product_id) → выполняет UPDATE
3. Всё атомарно — race condition невозможен

---

### EXCLUDED — специальная таблица PostgreSQL

**Что такое EXCLUDED:**
Виртуальная таблица, содержащая значения, которые пытались вставить.

```sql
INSERT INTO cart_items (quantity) VALUES (5)  -- пытаемся вставить 5
ON CONFLICT DO UPDATE SET
    quantity = cart_items.quantity + EXCLUDED.quantity
    --         текущее в БД (3)   +  новое (5) = 8
```

**Примеры использования:**

```sql
-- Увеличить quantity (добавление в корзину)
quantity = cart_items.quantity + EXCLUDED.quantity

-- Заменить quantity (редактирование корзины)
quantity = EXCLUDED.quantity

-- Обновить timestamp
updated_at = EXCLUDED.updated_at

-- Взять максимум (для price updates)
price = GREATEST(cart_items.price, EXCLUDED.price)
```

---

### Почему ON CONFLICT (cart_id, product_id), а не (id)

**id** — это первичный ключ, он всегда уникален (новый UUID при каждом вызове).
ON CONFLICT по id никогда не сработает → всегда INSERT.

**cart_id + product_id** — это бизнес-уникальность: "один товар в одной корзине".

```sql
-- Миграция: создаём UNIQUE constraint
CREATE TABLE cart_items (
    id UUID PRIMARY KEY,
    cart_id UUID NOT NULL REFERENCES carts(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id),
    quantity INTEGER NOT NULL,
    UNIQUE (cart_id, product_id)  -- бизнес-уникальность
);
```

---

## ON DELETE CASCADE vs Soft Delete

### Когда использовать CASCADE

```sql
CREATE TABLE cart_items (
    cart_id UUID NOT NULL REFERENCES carts(id) ON DELETE CASCADE
);
```

**ON DELETE CASCADE работает только при HARD DELETE:**
```sql
DELETE FROM carts WHERE id = $1  -- cart_items автоматически удаляются
```

**Soft Delete (UPDATE deleted_at) НЕ триггерит CASCADE:**
```sql
UPDATE carts SET deleted_at = NOW() WHERE id = $1
-- cart_items остаются в БД!
```

---

### Production подход для Cart

**Вариант 1: Clear() + Delete()**
```go
func (s *CartService) DeleteCart(ctx context.Context, cartID CartID) error {
    // Сначала удаляем items
    if err := s.cartItemsRepo.Clear(ctx, cartID); err != nil {
        return err
    }
    // Потом soft delete корзины
    return s.cartRepo.Delete(ctx, cartID)
}
```

**Вариант 2: Оставляем items для аналитики**
```go
// Что было в корзине при удалении? (для аналитики abandoned carts)
SELECT * FROM cart_items WHERE cart_id = $1
```

**Наш подход:** Clear() + Delete() — не храним orphan items.

---

## Идемпотентность операций

### Что такое идемпотентность?

**Идемпотентная операция** = повторный вызов даёт тот же результат.

```
f(x) = f(f(x))

Clear() → пустая корзина
Clear() → пустая корзина (тот же результат)
```

---

### Применение в CartItemsRepository.Clear()

**❌ Неправильно: ошибка при пустой корзине**
```go
func (r *CartItemsRepo) Clear(ctx context.Context, cartID CartID) error {
    result, _ := r.db.Exec(ctx, "DELETE FROM cart_items WHERE cart_id = $1", cartID)

    if result.RowsAffected() == 0 {
        return domain.NewNotFoundError("cart_items") // ❌
    }
    return nil
}

// Проблема:
Clear(cartID)  // OK
Clear(cartID)  // NotFoundError ❌ — должен быть OK!
```

**✅ Правильно: пустая корзина — не ошибка**
```go
func (r *CartItemsRepo) Clear(ctx context.Context, cartID CartID) error {
    _, err := r.db.Exec(ctx, "DELETE FROM cart_items WHERE cart_id = $1", cartID)
    if err != nil {
        return domain.NewInternalError("failed to clear cart", err)
    }
    // НЕ проверяем RowsAffected — пустой результат OK
    return nil
}

// Теперь:
Clear(cartID)  // OK
Clear(cartID)  // OK (идемпотентно)
```

---

### Когда идемпотентность важна

1. **Retry логика:**
   Запрос упал по таймауту → клиент повторяет → должен получить тот же результат.

2. **Distributed systems:**
   Сообщение в очереди обработано дважды → система остаётся консистентной.

3. **UI кнопки:**
   Пользователь кликнул "Очистить корзину" дважды → не должен видеть ошибку.

---

### Какие операции должны быть идемпотентными

```
✅ ИДЕМПОТЕНТНЫЕ:
   - Clear() — очистить корзину
   - Delete() — удалить (soft delete)
   - SetQuantity(5) — установить точное значение

❌ НЕ ИДЕМПОТЕНТНЫЕ:
   - AddItem() — добавить товар (увеличивает quantity)
   - IncrementQuantity() — увеличить количество
```

---

## Data Enrichment Pattern

### Проблема: Entity не содержит всех данных для UI

**Сценарий:**
CartItem хранит только `ProductID`, но клиенту нужно показать название товара.

```go
// entity/cart.go
type CartItem struct {
    ProductID ProductID  // только ID!
    Quantity  int
    Price     Money
}

// Клиент хочет видеть:
// {
//   "product_id": "...",
//   "product_name": "iPhone 15",  // откуда взять?
//   "quantity": 2
// }
```

---

### Решение: Service обогащает данные

```go
// service/cart/service.go
func (s *CartService) GetCart(ctx context.Context, userID UserID) (*dto.CartResponse, error) {
    // 1. Получаем корзину с items
    cart, err := s.cartRepo.GetWithItems(ctx, userID)
    if err != nil {
        return nil, err
    }

    // 2. Собираем все ProductID
    productIDs := make([]ProductID, 0, len(cart.Items))
    for productID := range cart.Items {
        productIDs = append(productIDs, productID)
    }

    // 3. Получаем Products из ProductRepository (batch запрос)
    products, err := s.productRepo.FindByIDs(ctx, productIDs)
    if err != nil {
        return nil, err
    }

    // 4. Создаём map для O(1) lookup
    productNames := make(map[ProductID]string, len(products))
    for _, p := range products {
        productNames[p.ID] = p.Name
    }

    // 5. Передаём в mapper
    return dto.ToCartResponse(cart, productNames), nil
}
```

---

### Mapper принимает обогащённые данные

```go
// dto/cart_dto.go
func ToCartResponse(cart *entity.Cart, productNames map[entity.ProductID]string) CartResponse {
    items := make([]CartItemResponse, 0, len(cart.Items))

    for _, item := range cart.Items {
        // O(1) lookup названия
        productName, ok := productNames[item.ProductID]
        if !ok {
            productName = "Unknown Product"  // fallback
        }

        items = append(items, ToCartItemResponse(item, productName))
    }

    return CartResponse{
        ID:    cart.ID.String(),
        Items: items,
        // ...
    }
}
```

---

### Альтернативы

**1. JOIN в SQL (N+1 проблема решена):**
```sql
SELECT ci.*, p.name as product_name
FROM cart_items ci
JOIN products p ON ci.product_id = p.id
WHERE ci.cart_id = $1
```
- ✅ Один запрос
- ❌ Repository возвращает "грязные" данные (смесь entities)

**2. Lazy loading (N+1 проблема!):**
```go
for _, item := range cart.Items {
    product := productRepo.FindByID(item.ProductID)  // N запросов!
}
```
- ❌ N+1 запросов к БД
- ❌ Медленно

**3. Batch loading (наш подход):**
```go
productIDs := collectIDs(cart.Items)
products := productRepo.FindByIDs(productIDs)  // 1 запрос
```
- ✅ Всего 2 запроса (cart + products)
- ✅ Repository остаётся чистым
- ✅ Service контролирует обогащение

---

### Когда использовать Data Enrichment

```
✅ ИСПОЛЬЗОВАТЬ:
   - Клиенту нужны данные из связанных entities
   - Хотим избежать N+1 запросов
   - Repository должен оставаться чистым (без JOIN на другие entities)

❌ НЕ ИСПОЛЬЗОВАТЬ:
   - Данные уже есть в entity (не нужно обогащать)
   - Внутренние операции (service → repository)
```

---

## DTO для составных типов (MoneyResponse)

### Проблема: Value Object в API

```go
// value_objects/money.go
type Money struct {
    Amount   int64     // 1500 (копейки)
    Currency Currency  // "RUB"
}

// Клиент хочет видеть:
// {
//   "amount": 1500,
//   "currency": "RUB",
//   "formatted": "15,00 ₽"  // для UI
// }
```

---

### Решение: MoneyResponse DTO

```go
// dto/cart_dto.go
type MoneyResponse struct {
    Amount    int64  `json:"amount"`     // в минимальных единицах
    Currency  string `json:"currency"`   // "RUB"
    Formatted string `json:"formatted"`  // "15,00 ₽"
}

func ToMoneyResponse(money value_objects.Money) MoneyResponse {
    return MoneyResponse{
        Amount:    money.Amount,
        Currency:  string(money.Currency),
        Formatted: formatMoney(money),  // "15,00 ₽"
    }
}

func formatMoney(money value_objects.Money) string {
    // Простая реализация
    // В production: i18n библиотека (golang.org/x/text)
    return money.String()
}
```

---

### Почему отдельный DTO, а не Money напрямую

**1. Изоляция:**
Изменение Money в domain не ломает API.

**2. Дополнительные поля:**
`Formatted` — вычисляемое поле, которого нет в Value Object.

**3. Типы данных:**
- Money.Currency = `value_objects.Currency` (custom type)
- MoneyResponse.Currency = `string` (JSON-friendly)

**4. Локализация:**
Можно добавить `Symbol` ("₽"), `Locale` ("ru-RU").

---

### Использование в других DTO

```go
type CartItemResponse struct {
    ProductID   string        `json:"product_id"`
    ProductName string        `json:"product_name"`
    Quantity    int           `json:"quantity"`
    Price       MoneyResponse `json:"price"`     // цена за единицу
    Subtotal    MoneyResponse `json:"subtotal"`  // quantity × price
}

type CartResponse struct {
    ID         string             `json:"id"`
    Items      []CartItemResponse `json:"items"`
    TotalPrice MoneyResponse      `json:"total_price"`
}
```

---

### Вычисляемые поля в DTO

```go
func ToCartItemResponse(item entity.CartItem, productName string) CartItemResponse {
    // Вычисляем subtotal
    subtotal, err := item.Price.Multiply(int64(item.Quantity))
    if err != nil {
        subtotal = value_objects.Zero(item.Price.Currency)
    }

    return CartItemResponse{
        ProductID:   item.ProductID.String(),
        ProductName: productName,
        Quantity:    item.Quantity,
        Price:       ToMoneyResponse(item.Price),
        Subtotal:    ToMoneyResponse(subtotal),  // вычисляемое
    }
}
```

**Почему вычисляем в mapper:**
- Клиенту не нужно считать самому
- Единая логика расчёта
- Можно добавить округление, скидки

---

## Service Layer: Оркестрация и Helper-методы

### Проблема: дублирование кода в Service

**Сценарий:**
Несколько методов CartService используют одинаковый паттерн:

```go
// ❌ Дублирование в каждом методе
func (s *CartService) AddToCart(...) {
    cart, err := s.getOrCreateCartEntity(ctx, userID)
    if err != nil { return nil, err }
    cart, err = s.cartRepo.GetCartWithItems(ctx, cart.ID)
    if err != nil { return nil, err }
    // ... логика
}

func (s *CartService) RemoveFromCart(...) {
    cart, err := s.getOrCreateCartEntity(ctx, userID)
    if err != nil { return nil, err }
    cart, err = s.cartRepo.GetCartWithItems(ctx, cart.ID)
    if err != nil { return nil, err }
    // ... логика
}
```

---

### Решение: DRY через helper-методы

```go
// ✅ Выносим в приватные helpers
func (s *CartService) getCartWithItems(ctx context.Context, userID entity.UserID) (*entity.Cart, error) {
    cart, err := s.getOrCreateCartEntity(ctx, userID)
    if err != nil {
        return nil, err
    }
    return s.cartRepo.GetCartWithItems(ctx, cart.ID)
}

func (s *CartService) toResponse(ctx context.Context, cart *entity.Cart) (*dto.CartResponse, error) {
    // Data Enrichment: получаем ProductNames
    if len(cart.Items) == 0 {
        response := dto.ToCartResponse(cart, nil)
        return &response, nil
    }

    productIDs := make([]entity.ProductID, 0, len(cart.Items))
    for productID := range cart.Items {
        productIDs = append(productIDs, productID)
    }

    products, err := s.productRepo.FindByIDs(ctx, productIDs)
    if err != nil {
        return nil, err
    }

    productNames := make(map[entity.ProductID]string, len(products))
    for _, product := range products {
        productNames[product.ID] = product.Name
    }

    response := dto.ToCartResponse(cart, productNames)
    return &response, nil
}
```

---

### Использование в публичных методах

```go
func (s *CartService) RemoveFromCart(ctx, userID, productIDStr) (*dto.CartResponse, error) {
    // ... валидация

    cart, err := s.getCartWithItems(ctx, userID)  // ✅ DRY
    if err != nil {
        return nil, err
    }

    // ... бизнес-логика

    return s.toResponse(ctx, cart)  // ✅ DRY
}
```

---

### Правило "Rule of Three"

```
Если код повторяется 3 раза — выноси в функцию.

1 раз: оставь как есть
2 раза: подумай о выносе
3 раза: обязательно выноси
```

---

### Структура Service с helpers

```go
// ── PUBLIC METHODS ──────────────────────────────────────
// API для handlers, можно вызывать извне

func (s *CartService) GetCart(...)
func (s *CartService) AddToCart(...)
func (s *CartService) UpdateQuantity(...)
func (s *CartService) RemoveFromCart(...)
func (s *CartService) ClearCart(...)

// ── PRIVATE HELPERS ─────────────────────────────────────
// Внутренние методы, уменьшают дублирование

func (s *CartService) getOrCreateCartEntity(...)  // Get-Or-Create
func (s *CartService) getCartWithItems(...)       // Получить с items
func (s *CartService) toResponse(...)             // DTO + Data Enrichment
```

---

## Querier Interface: Поддержка транзакций

### Проблема: репозиторий не работает внутри транзакции

**Сценарий:**
PlaceOrder должен атомарно выполнить несколько операций:
1. Создать Order
2. Уменьшить stock продуктов
3. Очистить корзину

**❌ Без Querier:**
```go
type OrderRepositoryPg struct {
    db *pgxpool.Pool  // конкретный тип
}

func (s *OrderService) PlaceOrder(ctx, userID) error {
    tx, _ := s.db.Begin(ctx)  // tx — это pgx.Tx

    // Проблема: репозиторий ожидает *pgxpool.Pool, не pgx.Tx!
    s.orderRepo.Create(ctx, order)  // ❌ вызывает Pool.Exec(), не tx.Exec()
}
```

Репозиторий жёстко привязан к `*pgxpool.Pool`. `pgx.Tx` — другой тип, хотя имеет те же методы.

---

### Решение: Querier interface

```go
// pkg/client/querier.go
type Querier interface {
    Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
    Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
    QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// И *pgxpool.Pool, и pgx.Tx реализуют эти методы
// Go автоматически считает их реализующими Querier
```

**Репозиторий принимает интерфейс:**
```go
type OrderRepositoryPg struct {
    db Querier  // интерфейс вместо конкретного типа
}

func NewOrderRepository(db Querier) *OrderRepositoryPg {
    return &OrderRepositoryPg{db: db}
}
```

---

### Использование в транзакции

```go
func (s *OrderService) PlaceOrder(ctx context.Context, userID entity.UserID) (*dto.OrderResponse, error) {
    tx, err := s.db.Begin(ctx)
    if err != nil {
        return nil, err
    }
    defer tx.Rollback(ctx)  // откатится, если не сделаем Commit

    // Создаём репозитории с tx — все операции в одной транзакции
    orderRepo := postgres.NewOrderRepository(tx)
    productRepo := postgres.NewProductRepository(tx)
    cartItemRepo := postgres.NewCartItemRepository(tx)

    // 1. Создать заказ
    orderRepo.Create(ctx, order)  // tx.Exec()

    // 2. Уменьшить stock
    for _, item := range order.Items {
        productRepo.DecrementStock(ctx, item.ProductID, item.Quantity)  // tx.Exec()
    }

    // 3. Очистить корзину
    cartItemRepo.Clear(ctx, cartID)  // tx.Exec()

    // Если дошли сюда без ошибок — коммитим
    return response, tx.Commit(ctx)
}
```

---

### Почему транзакция в Service, а не в Repository

**Repository:** работает с одним агрегатом (Order или Product)

**Service:** оркестрирует несколько агрегатов в одной транзакции

```
┌─────────────────────────────────────────────────────────┐
│ OrderService.PlaceOrder()                               │
│   ┌───────────────────────────────────────────────────┐ │
│   │ BEGIN TRANSACTION                                 │ │
│   │   OrderRepository.Create()      → tx.Exec()       │ │
│   │   ProductRepository.DecrementStock() → tx.Exec()  │ │
│   │   CartItemRepository.Clear()    → tx.Exec()       │ │
│   │ COMMIT                                            │ │
│   └───────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────┘
```

---

### Сравнение с распределёнными транзакциями

| | Локальная TX | 2PC | SAGA |
|---|---|---|---|
| **Когда** | Одна БД | Несколько БД | Микросервисы |
| **ACID** | ✅ Полный | ✅ Но сложно | ❌ Eventual |
| **Откат** | Автоматический | Автоматический | Ручной |
| **Сложность** | Низкая | Высокая | Средняя |

Наш случай — локальная транзакция (одна PostgreSQL БД). SAGA/2PC нужны только при переходе на микросервисы с отдельными БД.

---

## Атомарное обновление stock

### Проблема: race condition при уменьшении stock

**❌ SELECT + UPDATE (read-modify-write):**
```go
// Запрос A                          // Запрос B
stock := SELECT stock WHERE id=$1    stock := SELECT stock WHERE id=$1
// stock = 10                        // stock = 10

UPDATE SET stock = 10 - 2            UPDATE SET stock = 10 - 3
// stock = 8                         // stock = 7 ← перезаписали!

// Итого: должно быть 5, а стало 7!
```

**✅ Атомарный UPDATE:**
```sql
UPDATE products
SET stock = stock - $2, updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL AND stock >= $2
```

**Почему это работает:**
1. `stock - $2` выполняется атомарно внутри PostgreSQL
2. `stock >= $2` гарантирует, что не уйдём в минус
3. Если stock недостаточен → 0 affected rows → возвращаем ErrInsufficientStock

---

### Реализация в репозитории

```go
func (r *ProductRepositoryPg) DecrementStock(ctx context.Context, productID entity.ProductID, quantity int) error {
    q := `
        UPDATE products
        SET stock = stock - $2, updated_at = NOW()
        WHERE id = $1 AND deleted_at IS NULL AND stock >= $2
    `

    result, err := r.db.Exec(ctx, q, productID, quantity)
    if err != nil {
        return domain.NewInternalError("failed to decrement stock", err)
    }

    if result.RowsAffected() == 0 {
        // Либо продукт не найден, либо недостаточно stock
        // Проверяем существование
        var exists bool
        r.db.QueryRow(ctx, `SELECT EXISTS(...)`, productID).Scan(&exists)

        if !exists {
            return domain.NewNotFoundError("product")
        }
        return domain.ErrInsufficientStock
    }

    return nil
}
```

---

## Snapshot Pattern: Исторические данные

### Проблема: данные меняются после создания записи

**Сценарий:**
Клиент заказал "iPhone 15 Pro" за 999$. Через неделю:
- Товар переименовали в "iPhone 15 Pro Max"
- Цену подняли до 1099$
- Товар удалили из каталога

Что показывать в истории заказа?

---

### Два подхода

#### Data Enrichment (для Cart)
```go
// CartItem хранит только ProductID
type CartItem struct {
    ProductID ProductID
    Quantity  int
    Price     Money
}

// Service подгружает название при запросе
func (s *CartService) GetCart(ctx, userID) {
    cart := s.cartRepo.GetWithItems(ctx, userID)
    products := s.productRepo.FindByIDs(ctx, productIDs)  // актуальные данные
    return dto.ToCartResponse(cart, productNames)
}
```

**Плюсы:** Всегда актуальные данные
**Минусы:** Нужен JOIN или batch load

---

#### Snapshot (для Order)
```go
// OrderItem хранит ProductName на момент заказа
type OrderItem struct {
    ProductID   ProductID
    ProductName string  // snapshot!
    Quantity    int
    Price       Money   // тоже snapshot
}

// При создании заказа сохраняем название
orderItem := entity.OrderItem{
    ProductID:   cartItem.ProductID,
    ProductName: product.Name,  // копируем из Product
    Quantity:    cartItem.Quantity,
    Price:       cartItem.Price,
}
```

**Плюсы:** Исторические данные, быстрый read (нет JOIN)
**Минусы:** Денормализация, занимает место

---

### Когда использовать какой подход

| Entity | Семантика | Подход | Почему |
|--------|-----------|--------|--------|
| Cart | Черновик покупки | Enrichment | Нужны актуальные данные |
| Order | Юридический документ | Snapshot | Нужны данные на момент покупки |
| Invoice | Бухгалтерский документ | Snapshot | Юридическое требование |
| Wishlist | Список желаний | Enrichment | Нужны актуальные цены |

---

### Реализация Snapshot в Order

**1. Entity:**
```go
// domain/entity/order.go
type OrderItem struct {
    ProductID   ProductID
    ProductName string  // snapshot
    Quantity    int
    Price       Money   // snapshot (уже было)
}
```

**2. Миграция:**
```sql
ALTER TABLE order_items ADD COLUMN product_name VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE order_items ALTER COLUMN product_name DROP DEFAULT;
```

**3. Repository:**
```go
// Create — сохраняем ProductName
INSERT INTO order_items (product_id, product_name, quantity, ...)
VALUES ($1, $2, $3, ...)

// FindByID — читаем ProductName
SELECT oi.product_id, oi.product_name, oi.quantity, ...
```

**4. DTO Mapper (упрощается!):**
```go
// Было (с Enrichment):
func ToOrderResponse(order *Order, productNames map[ProductID]string) OrderResponse

// Стало (с Snapshot):
func ToOrderResponse(order *Order) OrderResponse {
    for _, item := range order.Items {
        // ProductName уже в entity — не нужен map!
        items = append(items, OrderItemResponse{
            ProductName: item.ProductName,
        })
    }
}
```

---

## Action-based API: Production подход

### Проблема: как управлять статусом заказа?

**Три подхода:**

#### Подход 1: Целевой статус (простой)
```
PATCH /orders/{id}/status
Body: { "status": "paid" }
```

**Плюсы:** Простой, универсальный
**Минусы:** Нельзя передать доп. данные (paymentID, trackingNumber)

---

#### Подход 2: Текущий статус → следующий (не работает!)
```
PATCH /orders/{id}/status
Body: { "status": "pending" }  // "я в pending, переведи дальше"
```

**Проблема:** Из `pending` можно в `paid` ИЛИ `cancelled`. Куда "дальше"?

---

#### Подход 3: Actions (production)
```
POST /orders/{id}/pay      { "payment_id": "..." }
POST /orders/{id}/ship     { "tracking_number": "..." }
POST /orders/{id}/deliver  { "signature": "..." }
POST /orders/{id}/cancel   { "reason": "..." }
```

**Плюсы:**
- Семантически понятные URL (действия, не состояния)
- Каждый action принимает свои параметры
- Легко добавлять side effects (email, интеграции)
- Проще документировать и тестировать

---

### Реализация в Service

```go
// Подход 1: универсальный UpdateStatus
func (s *OrderService) UpdateStatus(ctx, userID, orderID, newStatus string) (*dto.OrderResponse, error) {
    order, _ := s.orderRepo.FindByID(ctx, orderID)

    // Ownership check
    if order.UserID != userID {
        return nil, domain.NewNotFoundError("order")
    }

    status := entity.OrderStatus(newStatus)

    switch status {
    case entity.OrderStatusPaid:
        order.MarkAsPaid()
    case entity.OrderStatusShipped:
        order.Ship()
    // ...
    }

    s.orderRepo.Update(ctx, order)
    return dto.ToOrderResponse(order), nil
}

// Подход 3: отдельные Actions
func (s *OrderService) PayOrder(ctx, userID, orderID, paymentID string) (*dto.OrderResponse, error) {
    order, _ := s.orderRepo.FindByID(ctx, orderID)

    if order.UserID != userID {
        return nil, domain.NewNotFoundError("order")
    }

    order.MarkAsPaid()
    // order.PaymentID = paymentID  // сохраняем доп. данные

    s.orderRepo.Update(ctx, order)

    // Side effects:
    // s.notificationService.SendPaymentConfirmation(ctx, order)
    // s.auditLog.Record(ctx, "order.paid", order.ID, paymentID)

    return dto.ToOrderResponse(order), nil
}
```

---

### Сравнение подходов

| Аспект | Подход 1 (Status) | Подход 3 (Actions) |
|--------|-------------------|-------------------|
| Сложность | Простой | Сложнее |
| Доп. данные | Нет | Да |
| Side effects | Сложно добавить | Легко |
| Тестирование | Один метод | Отдельные методы |
| Документация | Один endpoint | Много endpoints |
| Когда использовать | MVP, учебный проект | Production |

---

## PlaceOrder: Транзакция оформления заказа

### Алгоритм

```
1. BEGIN TRANSACTION
2. defer tx.Rollback()  // безопасен даже после Commit
3. Создать репозитории с tx
4. Получить корзину → проверить не пуста
5. Получить Products (для stock и ProductName)
6. Для каждого item:
   - Проверить stock >= quantity
   - Создать OrderItem с ProductName (snapshot)
7. entity.NewOrder(userID, items)
8. orderRepo.Create(ctx, order)
9. DecrementStock для каждого товара
10. Очистить корзину
11. tx.Commit()
12. Вернуть OrderResponse
```

---

### Полная реализация

```go
func (s *OrderService) PlaceOrder(ctx context.Context, userID entity.UserID) (*dto.OrderResponse, error) {
    // 1. Начинаем транзакцию
    tx, err := s.pool.Begin(ctx)
    if err != nil {
        return nil, domain.NewInternalError("failed to begin transaction", err)
    }
    defer tx.Rollback(ctx)  // безопасен даже после Commit

    // 2. Создаём репозитории с tx — все операции в одной транзакции
    cartRepo := postgres.NewCartRepository(tx)
    productRepo := postgres.NewProductRepository(tx)
    orderRepo := postgres.NewOrderRepository(tx)
    cartItemRepo := postgres.NewCartItemsRepository(tx)

    // 3. Получаем корзину
    cart, err := cartRepo.FindByUserID(ctx, userID)
    if err != nil {
        return nil, domain.NewNotFoundError("cart")
    }

    cart, err = cartRepo.GetCartWithItems(ctx, cart.ID)
    if err != nil {
        return nil, domain.NewNotFoundError("cart")
    }

    // 4. Проверяем не пуста ли корзина
    if len(cart.Items) == 0 {
        return nil, domain.NewValidationError("cart is empty")
    }

    // 5. Получаем Products для проверки stock и ProductName
    productIDs := make([]entity.ProductID, 0, len(cart.Items))
    for _, item := range cart.Items {
        productIDs = append(productIDs, item.ProductID)
    }

    products, err := productRepo.FindByIDs(ctx, productIDs)
    if err != nil {
        return nil, domain.NewInternalError("failed to get products", err)
    }

    // Map для O(1) поиска
    productsMap := make(map[entity.ProductID]*entity.Product, len(products))
    for _, p := range products {
        productsMap[p.ID] = p
    }

    // 6. Проверяем stock и создаём OrderItems
    orderItems := make([]entity.OrderItem, 0, len(cart.Items))

    for _, item := range cart.Items {
        product, ok := productsMap[item.ProductID]
        if !ok {
            return nil, domain.NewNotFoundError("product")
        }

        if product.Stock < item.Quantity {
            return nil, domain.NewValidationError("insufficient stock for " + product.Name)
        }

        // Snapshot: сохраняем ProductName
        orderItems = append(orderItems, entity.OrderItem{
            ProductID:   item.ProductID,
            ProductName: product.Name,  // snapshot!
            Quantity:    item.Quantity,
            Price:       item.Price,
        })
    }

    // 7. Создаём Order
    order, err := entity.NewOrder(userID, orderItems)
    if err != nil {
        return nil, err
    }

    // 8. Сохраняем Order
    if err := orderRepo.Create(ctx, order); err != nil {
        return nil, err
    }

    // 9. Уменьшаем stock
    for _, item := range orderItems {
        if err := productRepo.DecrementStock(ctx, item.ProductID, item.Quantity); err != nil {
            return nil, err
        }
    }

    // 10. Очищаем корзину
    if err := cartItemRepo.Clear(ctx, cart.ID); err != nil {
        return nil, domain.NewInternalError("failed to clear cart", err)
    }

    // 11. Commit
    if err := tx.Commit(ctx); err != nil {
        return nil, domain.NewInternalError("failed to commit", err)
    }

    // 12. Возвращаем ответ
    response := dto.ToOrderResponse(order)
    return &response, nil
}
```

---

### Что обеспечивает транзакция

**ACID гарантии:**
- **Atomicity:** либо всё, либо ничего (rollback при любой ошибке)
- **Consistency:** stock не уйдёт в минус, корзина очищена
- **Isolation:** другие транзакции не видят промежуточное состояние
- **Durability:** после commit данные сохранены

**Порядок операций важен:**
1. Сначала Create Order → если упадёт, stock не изменится
2. Потом DecrementStock → если упадёт, заказ откатится
3. Потом Clear Cart → если упадёт, всё откатится

---

## Ownership Check: Безопасность

### Что такое Ownership Check

Проверка, что ресурс принадлежит текущему пользователю.

```go
order, _ := s.orderRepo.FindByID(ctx, orderID)

// Ownership check
if order.UserID != userID {
    return nil, domain.NewNotFoundError("order")  // НЕ Forbidden!
}
```

---

### Почему NotFound, а не Forbidden?

**Security best practice:** Не раскрываем существование чужих ресурсов.

```
❌ 403 Forbidden:
   - Злоумышленник знает, что заказ с этим ID существует
   - Может перебирать ID для сбора информации

✅ 404 Not Found:
   - Злоумышленник не знает, существует ли заказ
   - Выглядит как "такого заказа нет"
```

---

### Где применять

```go
// GetOrder
if order.UserID != userID {
    return nil, domain.NewNotFoundError("order")
}

// UpdateStatus
if order.UserID != userID {
    return nil, domain.NewNotFoundError("order")
}

// CancelOrder, PayOrder, etc.
if order.UserID != userID {
    return nil, domain.NewNotFoundError("order")
}
```

---

## 📖 Дополнительные ресурсы

- [Эффективный Go (официальная документация)](https://go.dev/doc/effective_go)
- [Domain-Driven Design, Eric Evans](https://www.domainlanguage.com/ddd/)
- [Clean Architecture, Robert Martin](https://blog.cleancoder.com/uncle-bob/2012/08/13/the-clean-architecture.html)
- [PostgreSQL Error Codes](https://www.postgresql.org/docs/current/errcodes-appendix.html)

---

**Версия:** 2.6
**Обновлено:** 2026-02-04
**Проект:** real_time_system (e-commerce с real-time возможностями)

**Changelog 2.6:**
- Добавлен раздел "Snapshot Pattern: Исторические данные"
- Snapshot vs Data Enrichment: когда использовать
- ProductName в OrderItem entity (юридические данные на момент покупки)
- Реализация: entity → миграция → repository → упрощённый DTO mapper
- Добавлен раздел "Action-based API: Production подход"
- Сравнение: Status (простой) vs Actions (production)
- Каждый action принимает свои параметры (paymentID, trackingNumber)
- Side effects: уведомления, интеграции, audit log
- Добавлен раздел "PlaceOrder: Транзакция оформления заказа"
- Полный алгоритм: BEGIN → репозитории с tx → проверки → snapshot → commit
- ACID гарантии, порядок операций
- Добавлен раздел "Ownership Check: Безопасность"
- Проверка order.UserID == userID
- NotFound вместо Forbidden (не раскрываем существование чужих ресурсов)

**Changelog 2.5:**
- Добавлен раздел "Querier Interface: Поддержка транзакций"
- Querier = абстракция для *pgxpool.Pool и pgx.Tx
- Репозитории принимают Querier → могут работать в транзакции
- Транзакции в Service Layer (оркестрация нескольких агрегатов)
- Сравнение: локальные TX vs 2PC vs SAGA
- Добавлен раздел "Атомарное обновление stock"
- UPDATE SET stock = stock - $2 WHERE stock >= $2 (без race condition)
- DecrementStock с проверкой ErrInsufficientStock

**Changelog 2.4:**
- Добавлен раздел "Service Layer: Оркестрация и Helper-методы"
- DRY: вынесение повторяющегося кода в приватные helpers
- getOrCreateCartEntity, getCartWithItems, toResponse
- Rule of Three: 3 повторения → выноси в функцию
- Структура Service: PUBLIC METHODS + PRIVATE HELPERS

**Changelog 2.3:**
- Добавлен раздел "Data Enrichment Pattern"
- Service обогащает данные из связанных entities
- Batch loading для избежания N+1 запросов
- Mapper принимает обогащённые данные (productNames map)
- Добавлен раздел "DTO для составных типов (MoneyResponse)"
- MoneyResponse с Amount, Currency, Formatted
- Вычисляемые поля в DTO (Subtotal = Quantity × Price)

**Changelog 2.2:**
- Добавлен раздел "Связанные таблицы (1:N): Cart + CartItems"
- LEFT JOIN для nullable данных (пустая корзина)
- Nullable поля при сканировании (*string, *int для LEFT JOIN)
- Parse-функции для typed IDs (ParseProductID, ParseCartID, ParseCartItemID)
- UPSERT Pattern (INSERT ON CONFLICT DO UPDATE)
- EXCLUDED таблица PostgreSQL (доступ к вставляемым значениям)
- ON CONFLICT по бизнес-ключу (cart_id, product_id) vs primary key
- ON DELETE CASCADE vs Soft Delete
- Идемпотентность операций (Clear на пустой корзине)

**Changelog 2.1:**
- Добавлен Repository Patterns (работа с PostgreSQL)
- Money в БД: два поля (price_amount + price_currency) vs JSON
- created_at иммутабельность (никогда не обновляется)
- FindByPriceRange (BETWEEN $1 AND $2, ORDER BY)
- Пустой результат vs ошибка ([] + nil vs NotFoundError)
- Pre-allocation для slice (make([]*T, 0, len(source)))
- rows.Err() проверка после итерации

**Changelog 2.0:**
- Добавлен DTO Pattern (изоляция слоёв, mapper functions)
- Добавлен SOFT DELETE (production подход к удалению данных)
- Добавлен sql.Scanner/Valuer (интеграция typed IDs с БД)
- Добавлен Partial Update (pointer-based DTO)
- Добавлен Production Logging (что логировать, что нет)
- Добавлен PostgreSQL Error Handling (конвертация в domain errors)
