package entity

import (
	"errors"
	"real_time_system/domain"
	"real_time_system/domain/value_objects"
	"testing"
)

func TestNewProduct(t *testing.T) {
	price := value_objects.Money{Amount: 9990, Currency: value_objects.RUB}

	tests := []struct {
		name        string
		productName string
		description string
		price       value_objects.Money
		stock       int
		wantErr     error
	}{
		{
			name:        "valid product",
			productName: "Laptop",
			description: "Gaming laptop",
			price:       price,
			stock:       10,
		},
		{
			name:        "valid without description",
			productName: "Mouse",
			description: "",
			price:       price,
			stock:       5,
		},
		{
			name:        "empty name",
			productName: "",
			description: "Some desc",
			price:       price,
			stock:       1,
			wantErr:     domain.ErrEmptyProductName,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			product, err := NewProduct(tt.productName, tt.description, tt.price, tt.stock)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("NewProduct() error = %v, want %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("NewProduct() unexpected error: %v", err)
			}

			if product.Name != tt.productName {
				t.Errorf("Name = %q, want %q", product.Name, tt.productName)
			}
			if product.ID.IsZero() {
				t.Error("ID should not be zero")
			}
			if product.Price != tt.price {
				t.Errorf("Price = %v, want %v", product.Price, tt.price)
			}
			if product.CreatedAt.IsZero() {
				t.Error("CreatedAt should be set")
			}
		})
	}
}
