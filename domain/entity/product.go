package entity

import (
	"database/sql/driver"
	"fmt"
	"github.com/google/uuid"
	"real_time_system/domain"
	"real_time_system/domain/value_objects"
	"time"
)

// ProductID — типизированная обёртка для идентификатора продукта.
// Логика та же, что у UserID: типобезопасность и семантика.
type ProductID uuid.UUID

func NewProductID() ProductID {
	return ProductID(uuid.New())
}

// ParseProductID парсит строку в ProductID.
// Используется при маппинге из БД (nullable поля в LEFT JOIN).
func ParseProductID(s string) (ProductID, error) {
	parsed, err := uuid.Parse(s)
	if err != nil {
		return ProductID{}, fmt.Errorf("invalid ProductID: %w", err)
	}
	return ProductID(parsed), nil
}

func (id ProductID) String() string {
	return uuid.UUID(id).String()
}

func (id ProductID) IsZero() bool {
	return uuid.UUID(id) == uuid.Nil
}

// Product — доменная сущность товара.
//
// ПОЧЕМУ Price хранится здесь, а не только в CartItem/OrderItem:
// Price в Product — это текущая актуальная цена товара (прайс-лист).
// В CartItem/OrderItem хранится цена на момент добавления/покупки,
// которая может отличаться от текущей (скидки, изменение прайса).
// Это стандартный паттерн в e-com: "цена товара" vs "цена в заказе" — разные вещи.
type Product struct {
	ID          ProductID
	Name        string
	Description string
	Price       value_objects.Money
	Stock       int
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   *time.Time
}

// NewProduct — фабрика Product с валидацией.
//
// ПОЧЕМУ Name обязателен, а Description — нет:
// Name — это идентифицирующий атрибут для пользователя (отображается в каталоге, корзине, заказе).
// Товар без имени — это баг, а не фича. Description — дополнительная информация,
// которая может быть заполнена позже (например, при импорте из внешней системы).
func NewProduct(name, description string, price value_objects.Money, stock int) (*Product, error) {
	if name == "" {
		return nil, domain.ErrEmptyProductName
	}

	now := time.Now()

	return &Product{
		ID:          NewProductID(),
		Name:        name,
		Description: description,
		Price:       price,
		Stock:       stock,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func (id *ProductID) Scan(src interface{}) error {
	switch v := src.(type) {
	case string:
		// Парсим UUID из строки
		parsed, err := uuid.Parse(v)
		if err != nil {
			return fmt.Errorf("invalid UUID string: %w", err)
		}
		*id = ProductID(parsed)
		return nil

	case []byte:
		// Парсим UUID из байтов
		parsed, err := uuid.ParseBytes(v)
		if err != nil {
			return fmt.Errorf("invalid UUID bytes: %w", err)
		}
		*id = ProductID(parsed)
		return nil

	case nil:
		// NULL из БД → zero-value
		*id = ProductID(uuid.Nil)
		return nil

	default:
		// Неожиданный тип → ошибка
		return fmt.Errorf("cannot scan %T into UserID", src)
	}
}

// Value реализует интерфейс driver.Valuer для записи UserID в БД.
//
// Возвращаем строковое представление UUID, потому что:
//   - Универсально работает со всеми PostgreSQL драйверами
//   - Читаемо в логах и при отладке
//   - Не требует бинарного кодирования
//
// АЛЬТЕРНАТИВА: можно вернуть []byte для экономии места, но для UUID
// разница незначительна (36 байт строка vs 16 байт binary).
func (id ProductID) Value() (driver.Value, error) {
	// Возвращаем строковое представление
	return id.String(), nil
}
