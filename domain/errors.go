package domain

import (
	"errors"
	"net/http"
)

type DomainError struct {
	Message    string
	StatusCode int
	Err        error
}

func (e *DomainError) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *DomainError) Unwrap() error {
	return e.Err
}

func NewNotFoundError(resource string) *DomainError {
	return &DomainError{
		Message:    resource + " not found",
		StatusCode: http.StatusNotFound, // 404
	}
}

func NewValidationError(message string) *DomainError {
	return &DomainError{
		Message:    message,
		StatusCode: http.StatusBadRequest, // 400
	}
}

func NewInternalError(message string, err error) *DomainError {
	return &DomainError{
		Message:    message,
		StatusCode: http.StatusInternalServerError, // 500
		Err:        err, // передаём оригинальную ошибку для wrapping
	}
}

// NewConflictError создаёт ошибку для случаев конфликта (duplicate key, constraint violation).
// HTTP код 409 (Conflict) сигнализирует клиенту, что ресурс уже существует.
func NewConflictError(message string) *DomainError {
	return &DomainError{
		Message:    message,
		StatusCode: http.StatusConflict, // 409
	}
}

// Sentinel errors — именованные ошибки уровня домена.
//
// ПОЧЕМУ ТАК: В Go ошибки — это значения. Когда мы используем fmt.Errorf("some text")
// в каждом месте, у нас нет возможности программно отличить одну ошибку от другой.
// Service-слой или handler не сможет понять: это ошибка валидации (400) или что-то другое?
//
// Sentinel errors решают эту проблему: мы объявляем ошибку один раз как переменную,
// а в вызывающем коде проверяем через errors.Is(err, domain.ErrInvalidQuantity).
//
// КАКИЕ ПРОБЛЕМЫ БЫЛИ БЫ БЕЗ ЭТОГО:
// - Handler не смог бы вернуть правильный HTTP-код (400 vs 500)
// - Невозможно писать тесты вида assert.ErrorIs(t, err, domain.ErrEmptyEmail)
// - Логирование теряет контекст: строки "email is required" могут меняться,
//   а код, завязанный на строковое сравнение, сломается
// - При локализации ошибок пришлось бы менять все проверки

// --- Entity: User ---

var (
	ErrEmptyEmail = errors.New("email is required")
	ErrEmptyName  = errors.New("name is required")
)

// --- Entity: Product ---

var (
	ErrEmptyProductName = errors.New("product name is required")
)

// --- Entity: Cart ---

var (
	// ErrInvalidQuantity — количество товара должно быть > 0.
	// Ноль тоже невалиден: добавление 0 единиц — бессмысленная операция,
	// которая не должна проходить молча.
	ErrInvalidQuantity = errors.New("quantity must be greater than zero")

	ErrItemNotFound = errors.New("item not found in cart")
	ErrEmptyCart    = errors.New("cart is empty")

	// ErrInsufficientStock — недостаточно товара на складе.
	// Используется при AddToCart, когда запрошенное количество > Product.Stock.
	ErrInsufficientStock = errors.New("insufficient stock")
)

// --- Entity: Order ---

var (
	ErrEmptyOrderItems = errors.New("order must have at least one item")

	// ErrInvalidStatusTransition — попытка перехода в недопустимый статус.
	// Например: cancelled → paid, delivered → shipped.
	// State machine в Order гарантирует, что переходы идут только вперёд по бизнес-процессу.
	ErrInvalidStatusTransition = errors.New("invalid order status transition")
)

// --- Value Object: Money ---

var (
	ErrNegativeAmount    = errors.New("money amount cannot be negative")
	ErrEmptyCurrency     = errors.New("currency cannot be empty")
	ErrCurrencyMismatch  = errors.New("cannot operate on different currencies")
	ErrInvalidMultiplier = errors.New("multiplier must be positive")
)
