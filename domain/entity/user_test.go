package entity

import (
	"errors"
	"real_time_system/domain"
	"testing"
)

func TestNewUser(t *testing.T) {
	tests := []struct {
		name    string
		email   string
		uname   string
		surname string
		wantErr error
	}{
		{
			name:    "valid user",
			email:   "test@example.com",
			uname:   "John",
			surname: "Doe",
		},
		{
			name:    "valid without surname",
			email:   "test@example.com",
			uname:   "John",
			surname: "",
		},
		{
			name:    "empty email",
			email:   "",
			uname:   "John",
			surname: "Doe",
			wantErr: domain.ErrEmptyEmail,
		},
		{
			name:    "empty name",
			email:   "test@example.com",
			uname:   "",
			surname: "Doe",
			wantErr: domain.ErrEmptyName,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user, err := NewUser(tt.email, tt.uname, tt.surname)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("NewUser() error = %v, want %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("NewUser() unexpected error: %v", err)
			}

			if user.Email != tt.email {
				t.Errorf("Email = %q, want %q", user.Email, tt.email)
			}
			if user.Name != tt.uname {
				t.Errorf("Name = %q, want %q", user.Name, tt.uname)
			}
			if user.ID.IsZero() {
				t.Error("ID should not be zero")
			}
			if user.CreatedAt.IsZero() {
				t.Error("CreatedAt should be set")
			}
		})
	}
}

func TestUserID_String(t *testing.T) {
	id := NewUserID()
	s := id.String()

	if len(s) != 36 { // UUID format: 8-4-4-4-12
		t.Errorf("String() length = %d, want 36", len(s))
	}
}

func TestParseUserID(t *testing.T) {
	original := NewUserID()
	s := original.String()

	parsed, err := ParseUserID(s)
	if err != nil {
		t.Fatalf("ParseUserID() error: %v", err)
	}

	if parsed != original {
		t.Errorf("parsed ID = %v, want %v", parsed, original)
	}
}

func TestParseUserID_Invalid(t *testing.T) {
	_, err := ParseUserID("not-a-uuid")
	if err == nil {
		t.Error("ParseUserID(invalid) should return error")
	}
}
