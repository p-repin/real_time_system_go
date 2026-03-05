package value_objects

import (
	"errors"
	"real_time_system/domain"
	"testing"
)

// Table-driven tests — стандартный паттерн в Go.
//
// ПОЧЕМУ именно такой формат:
// - Каждый кейс — отдельная строка в таблице, легко добавить новый
// - name позволяет быстро найти упавший тест: "TestNewMoney/negative_amount"
// - Один цикл, одна структура — нет дублирования вызовов t.Run()
// - При добавлении нового правила валидации — добавляешь строку, а не копируешь функцию
//
// КАКИЕ ПРОБЛЕМЫ БЫЛИ БЫ БЕЗ TABLE-DRIVEN:
// - 10 отдельных функций TestNewMoneyNegative, TestNewMoneyEmpty... — хаос в выводе
// - Дублирование assert-логики в каждой функции
// - Сложно увидеть полную картину: какие кейсы покрыты, какие — нет

func TestNewMoney(t *testing.T) {
	tests := []struct {
		name     string
		amount   int64
		currency Currency
		wantErr  error
	}{
		{
			name:     "valid RUB",
			amount:   1500,
			currency: RUB,
			wantErr:  nil,
		},
		{
			name:     "valid USD",
			amount:   0,
			currency: USD,
			wantErr:  nil,
		},
		{
			name:     "negative amount",
			amount:   -100,
			currency: RUB,
			wantErr:  domain.ErrNegativeAmount,
		},
		{
			name:     "empty currency",
			amount:   100,
			currency: "",
			wantErr:  domain.ErrEmptyCurrency,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			money, err := NewMoney(tt.amount, tt.currency)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("NewMoney(%d, %q) error = %v, want %v", tt.amount, tt.currency, err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("NewMoney(%d, %q) unexpected error: %v", tt.amount, tt.currency, err)
			}
			if money.Amount != tt.amount {
				t.Errorf("Amount = %d, want %d", money.Amount, tt.amount)
			}
			if money.Currency != tt.currency {
				t.Errorf("Currency = %q, want %q", money.Currency, tt.currency)
			}
		})
	}
}

func TestMoney_Add(t *testing.T) {
	tests := []struct {
		name    string
		a       Money
		b       Money
		want    int64
		wantErr error
	}{
		{
			name:    "same currency",
			a:       Money{Amount: 1000, Currency: RUB},
			b:       Money{Amount: 500, Currency: RUB},
			want:    1500,
			wantErr: nil,
		},
		{
			name:    "different currencies",
			a:       Money{Amount: 1000, Currency: RUB},
			b:       Money{Amount: 500, Currency: USD},
			wantErr: domain.ErrCurrencyMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.a.Add(tt.b)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("Add() error = %v, want %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("Add() unexpected error: %v", err)
			}
			if result.Amount != tt.want {
				t.Errorf("Add() Amount = %d, want %d", result.Amount, tt.want)
			}
		})
	}
}

func TestMoney_Subtract(t *testing.T) {
	tests := []struct {
		name    string
		a       Money
		b       Money
		want    int64
		wantErr error
	}{
		{
			name: "valid subtraction",
			a:    Money{Amount: 1000, Currency: RUB},
			b:    Money{Amount: 300, Currency: RUB},
			want: 700,
		},
		{
			name: "subtract to zero",
			a:    Money{Amount: 500, Currency: RUB},
			b:    Money{Amount: 500, Currency: RUB},
			want: 0,
		},
		{
			name:    "result would be negative",
			a:       Money{Amount: 100, Currency: RUB},
			b:       Money{Amount: 500, Currency: RUB},
			wantErr: domain.ErrNegativeAmount,
		},
		{
			name:    "different currencies",
			a:       Money{Amount: 1000, Currency: RUB},
			b:       Money{Amount: 100, Currency: USD},
			wantErr: domain.ErrCurrencyMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.a.Subtract(tt.b)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("Subtract() error = %v, want %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("Subtract() unexpected error: %v", err)
			}
			if result.Amount != tt.want {
				t.Errorf("Subtract() Amount = %d, want %d", result.Amount, tt.want)
			}
		})
	}
}

func TestMoney_Multiply(t *testing.T) {
	tests := []struct {
		name     string
		money    Money
		quantity int64
		want     int64
		wantErr  error
	}{
		{
			name:     "multiply by 3",
			money:    Money{Amount: 500, Currency: RUB},
			quantity: 3,
			want:     1500,
		},
		{
			name:     "multiply by 1",
			money:    Money{Amount: 999, Currency: USD},
			quantity: 1,
			want:     999,
		},
		{
			name:     "zero quantity",
			money:    Money{Amount: 500, Currency: RUB},
			quantity: 0,
			wantErr:  domain.ErrInvalidMultiplier,
		},
		{
			name:     "negative quantity",
			money:    Money{Amount: 500, Currency: RUB},
			quantity: -2,
			wantErr:  domain.ErrInvalidMultiplier,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.money.Multiply(tt.quantity)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("Multiply() error = %v, want %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("Multiply() unexpected error: %v", err)
			}
			if result.Amount != tt.want {
				t.Errorf("Multiply() Amount = %d, want %d", result.Amount, tt.want)
			}
		})
	}
}

func TestMoney_String(t *testing.T) {
	tests := []struct {
		money Money
		want  string
	}{
		{Money{Amount: 1500, Currency: RUB}, "15.00 RUB"},
		{Money{Amount: 99, Currency: USD}, "0.99 USD"},
		{Money{Amount: 0, Currency: RUB}, "0.00 RUB"},
		{Money{Amount: 100050, Currency: RUB}, "1000.50 RUB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.money.String()
			if got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMoney_Equals(t *testing.T) {
	a := Money{Amount: 1000, Currency: RUB}
	b := Money{Amount: 1000, Currency: RUB}
	c := Money{Amount: 1000, Currency: USD}
	d := Money{Amount: 999, Currency: RUB}

	if !a.Equals(b) {
		t.Error("same amount and currency should be equal")
	}
	if a.Equals(c) {
		t.Error("different currency should not be equal")
	}
	if a.Equals(d) {
		t.Error("different amount should not be equal")
	}
}

func TestZero(t *testing.T) {
	z := Zero(RUB)
	if z.Amount != 0 {
		t.Errorf("Zero() Amount = %d, want 0", z.Amount)
	}
	if z.Currency != RUB {
		t.Errorf("Zero() Currency = %q, want %q", z.Currency, RUB)
	}
	if !z.IsZero() {
		t.Error("Zero() should be zero")
	}
}
