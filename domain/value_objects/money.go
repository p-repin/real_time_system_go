package value_objects

import (
	"fmt"
	"real_time_system/domain"
)

type Currency string

const (
	RUB Currency = "RUB"
	USD Currency = "USD"
)

// Money — Value Object, представляющий денежную сумму в определённой валюте.
//
// ПОЧЕМУ Amount — int64, а не float64:
// Числа с плавающей точкой (float64) имеют проблемы с точностью.
// Пример: 0.1 + 0.2 = 0.30000000000000004 в float64.
// В финансовых расчётах это недопустимо: клиент заплатит 10.00₽, а система
// запишет 9.999999999₽ — и баланс никогда не сойдётся.
//
// Мы храним суммы в минимальных единицах валюты (копейки, центы): 1500 = 15.00₽.
// Все операции — целочисленные, без потери точности.
//
// КАКИЕ ПРОБЛЕМЫ БЫЛИ БЫ С float64:
// - Накопление ошибок округления при множестве операций (корзина из 50 товаров)
// - Невозможность точного сравнения: money1 == money2 может быть false из-за epsilon
// - Аудит и бухгалтерия не сойдутся с реальными транзакциями
type Money struct {
	Amount   int64
	Currency Currency
}

// NewMoney — фабрика Money с валидацией.
//
// ПОЧЕМУ используем фабрику, а не создаём struct напрямую:
// - Гарантируем, что Money всегда валиден (нет отрицательных сумм, пустых валют)
// - Невалидный Money не может существовать в системе — это инвариант Value Object
// - Если правила валидации изменятся, меняем только одно место
func NewMoney(amount int64, currency Currency) (Money, error) {
	if amount < 0 {
		return Money{}, domain.ErrNegativeAmount
	}
	if currency == "" {
		return Money{}, domain.ErrEmptyCurrency
	}
	return Money{
		Amount:   amount,
		Currency: currency,
	}, nil
}

// Zero возвращает нулевую сумму в указанной валюте.
// Удобно для инициализации TotalPrice в Cart, чтобы не создавать Money напрямую.
func Zero(currency Currency) Money {
	return Money{Amount: 0, Currency: currency}
}

// Add складывает две суммы. Валюты должны совпадать.
//
// ПОЧЕМУ возвращаем новый Money, а не мутируем текущий:
// Value Object по определению иммутабельный. Если бы мы меняли Amount на месте,
// то любой код, хранящий ссылку на этот Money, получил бы неожиданно изменённое значение.
// Это особенно критично при конкурентном доступе к Cart: два горутины могут
// одновременно изменить один и тот же Money, что приведёт к data race.
func (m Money) Add(other Money) (Money, error) {
	if other.Currency != m.Currency {
		return Money{}, domain.ErrCurrencyMismatch
	}
	return Money{Amount: m.Amount + other.Amount, Currency: m.Currency}, nil
}

// Subtract вычитает сумму. Нужен для пересчёта TotalPrice при удалении товара из корзины.
//
// ПОЧЕМУ проверяем результат на отрицательность:
// Отрицательная сумма в корзине — это бизнес-ошибка, которая может указывать
// на баг в логике (удалили больше, чем добавили). Лучше упасть с ошибкой,
// чем молча записать -500₽ в TotalPrice.
func (m Money) Subtract(other Money) (Money, error) {
	if other.Currency != m.Currency {
		return Money{}, domain.ErrCurrencyMismatch
	}
	result := m.Amount - other.Amount
	if result < 0 {
		return Money{}, domain.ErrNegativeAmount
	}
	return Money{Amount: result, Currency: m.Currency}, nil
}

// Multiply умножает сумму на количество. Нужен для расчёта стоимости позиции:
// цена_за_единицу * количество.
//
// ПОЧЕМУ multiplier — int64, а не float64:
// Количество товара — всегда целое число. Использование float64 здесь
// снова вносит проблему точности: 19.99 * 3 в float64 может дать 59.96999...
// А нам нужно ровно 5997 копеек.
func (m Money) Multiply(quantity int64) (Money, error) {
	if quantity <= 0 {
		return Money{}, domain.ErrInvalidMultiplier
	}
	return Money{Amount: m.Amount * quantity, Currency: m.Currency}, nil
}

// IsZero проверяет, равна ли сумма нулю. Полезно для проверки пустой корзины.
func (m Money) IsZero() bool {
	return m.Amount == 0
}

// Equals сравнивает два Money (и сумму, и валюту).
//
// ПОЧЕМУ отдельный метод, а не просто ==:
// В Go оператор == для struct сравнивает все поля, что здесь корректно.
// Но явный метод Equals лучше для читаемости и даёт возможность
// в будущем добавить логику (например, нормализацию валюты).
func (m Money) Equals(other Money) bool {
	return m.Amount == other.Amount && m.Currency == other.Currency
}

func (m Money) String() string {
	return fmt.Sprintf("%.2f %s", float64(m.Amount)/100, m.Currency)
}
