package http_test

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ТЕСТЫ HTTP HANDLER: UserHandler                                            │
// └──────────────────────────────────────────────────────────────────────────┘
//
// ИНСТРУМЕНТЫ (только стандартная библиотека):
//   - net/http/httptest  — фиктивный ResponseWriter и Request без реального сервера
//   - encoding/json      — сериализация/десериализация тел запросов и ответов
//
// СТРАТЕГИЯ ТЕСТИРОВАНИЯ HANDLER'А:
//
//  Handler — тонкий слой. Его зона ответственности:
//   ✅ Парсинг запроса (JSON body, path params)
//   ✅ Вызов правильного метода сервиса
//   ✅ Правильный HTTP статус-код при успехе и ошибке
//   ✅ Правильная структура JSON-ответа
//
//  Handler НЕ тестирует:
//   ❌ Бизнес-логику (это тесты service)
//   ❌ SQL-запросы (это тесты repository)
//
// MOCK vs REAL:
//   В unit-тестах handler'а сервис заменяется на mock.
//   Mock — простая структура с предзаданными ответами.
//   Это изолирует тест от внешних зависимостей (БД, сеть).
//
// СТРУКТУРА ТЕСТА (AAA — Arrange, Act, Assert):
//   Arrange: настраиваем mock, создаём handler, готовим запрос
//   Act:     вызываем handler через router
//   Assert:  проверяем статус-код и тело ответа

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"real_time_system/domain"
	"real_time_system/domain/entity"
	httphandler "real_time_system/internal/controller/http"
	"real_time_system/internal/service/dto"
)

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ MOCK                                                                       │
// └──────────────────────────────────────────────────────────────────────────┘
//
// mockUserService — тестовая заглушка для userService интерфейса.
//
// ПОЧЕМУ HANDWRITTEN MOCK, А НЕ GOMOCK/MOCKERY:
//   - Нет зависимости от внешних инструментов
//   - Прозрачно: сразу видно что возвращает "сервис" в каждом тесте
//   - Достаточно для простых случаев
//
// Поля `fn*` — функции, которые можно переопределить в каждом тесте.
// Nil-значение → метод не вызывался, если вызван — паника с понятным сообщением.
type mockUserService struct {
	fnCreate func(ctx context.Context, req dto.CreateUserRequest) (*dto.UserResponse, error)
	fnGet    func(ctx context.Context, id entity.UserID) (*dto.UserResponse, error)
	fnUpdate func(ctx context.Context, id entity.UserID, req dto.UpdateUserRequest) (*dto.UserResponse, error)
	fnDelete func(ctx context.Context, id entity.UserID) error
}

func (m *mockUserService) CreateUser(ctx context.Context, req dto.CreateUserRequest) (*dto.UserResponse, error) {
	if m.fnCreate == nil {
		panic("mockUserService.CreateUser: unexpected call")
	}
	return m.fnCreate(ctx, req)
}

func (m *mockUserService) GetUser(ctx context.Context, id entity.UserID) (*dto.UserResponse, error) {
	if m.fnGet == nil {
		panic("mockUserService.GetUser: unexpected call")
	}
	return m.fnGet(ctx, id)
}

func (m *mockUserService) UpdateUser(ctx context.Context, id entity.UserID, req dto.UpdateUserRequest) (*dto.UserResponse, error) {
	if m.fnUpdate == nil {
		panic("mockUserService.UpdateUser: unexpected call")
	}
	return m.fnUpdate(ctx, id, req)
}

func (m *mockUserService) DeleteUser(ctx context.Context, id entity.UserID) error {
	if m.fnDelete == nil {
		panic("mockUserService.DeleteUser: unexpected call")
	}
	return m.fnDelete(ctx, id)
}

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ХЕЛПЕРЫ                                                                    │
// └──────────────────────────────────────────────────────────────────────────┘

// newRouter создаёт chi-роутер с зарегистрированным handler'ом.
//
// Мы тестируем через роутер (а не вызывая handler напрямую), потому что:
//   - chi извлекает path-параметры ({id}) и кладёт в context запроса
//   - Без роутера chi.URLParam(r, "id") вернёт пустую строку
//   - Тест через роутер проверяет и маршрутизацию, и handler вместе
func newRouter(svc *mockUserService) http.Handler {
	r := chi.NewRouter()
	h := httphandler.NewUserHandlerWithService(svc)
	h.RegisterRoutes(r)
	return r
}

// decodeBody десериализует JSON-тело ответа в переданную структуру.
func decodeBody(t *testing.T, body *bytes.Buffer, v any) {
	t.Helper()
	if err := json.NewDecoder(body).Decode(v); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
}

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ТЕСТЫ: Create                                                              │
// └──────────────────────────────────────────────────────────────────────────┘

func TestUserHandler_Create_Success(t *testing.T) {
	// Arrange: сервис вернёт готовый UserResponse
	want := &dto.UserResponse{
		ID:    "550e8400-e29b-41d4-a716-446655440000",
		Name:  "Ivan",
		Email: "ivan@example.com",
	}
	svc := &mockUserService{
		fnCreate: func(_ context.Context, req dto.CreateUserRequest) (*dto.UserResponse, error) {
			// Проверяем, что handler передал правильные данные в сервис
			if req.Name != "Ivan" || req.Email != "ivan@example.com" {
				t.Errorf("unexpected req: %+v", req)
			}
			return want, nil
		},
	}

	// Act
	body := `{"name":"Ivan","email":"ivan@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	newRouter(svc).ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", rec.Code)
	}

	var got dto.UserResponse
	decodeBody(t, rec.Body, &got)

	if got.ID != want.ID {
		t.Errorf("expected ID %s, got %s", want.ID, got.ID)
	}
	if got.Name != want.Name {
		t.Errorf("expected Name %s, got %s", want.Name, got.Name)
	}
}

func TestUserHandler_Create_InvalidJSON(t *testing.T) {
	// Arrange: сервис не должен вызываться при невалидном JSON
	svc := &mockUserService{} // fnCreate = nil → паника если вызовется

	// Act: отправляем сломанный JSON
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewBufferString(`{bad json`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	newRouter(svc).ServeHTTP(rec, req)

	// Assert: handler должен вернуть 400, не вызывая сервис
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}

	var errResp httphandler.ErrorResponse
	decodeBody(t, rec.Body, &errResp)

	if errResp.Code != http.StatusBadRequest {
		t.Errorf("expected error code 400, got %d", errResp.Code)
	}
}

func TestUserHandler_Create_Conflict(t *testing.T) {
	// Arrange: сервис возвращает ошибку "email already exists"
	svc := &mockUserService{
		fnCreate: func(_ context.Context, _ dto.CreateUserRequest) (*dto.UserResponse, error) {
			return nil, domain.NewConflictError("email already exists")
		},
	}

	// Act
	body := `{"name":"Ivan","email":"duplicate@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	newRouter(svc).ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", rec.Code)
	}

	var errResp httphandler.ErrorResponse
	decodeBody(t, rec.Body, &errResp)

	if errResp.Code != http.StatusConflict {
		t.Errorf("expected error code 409, got %d", errResp.Code)
	}
}

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ТЕСТЫ: GetByID                                                             │
// └──────────────────────────────────────────────────────────────────────────┘

func TestUserHandler_GetByID_Success(t *testing.T) {
	userID := "550e8400-e29b-41d4-a716-446655440000"
	want := &dto.UserResponse{
		ID:    userID,
		Name:  "Ivan",
		Email: "ivan@example.com",
	}

	svc := &mockUserService{
		fnGet: func(_ context.Context, id entity.UserID) (*dto.UserResponse, error) {
			if id.String() != userID {
				t.Errorf("expected ID %s, got %s", userID, id.String())
			}
			return want, nil
		},
	}

	// Act
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+userID, nil)
	rec := httptest.NewRecorder()

	newRouter(svc).ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var got dto.UserResponse
	decodeBody(t, rec.Body, &got)

	if got.ID != userID {
		t.Errorf("expected ID %s, got %s", userID, got.ID)
	}
}

func TestUserHandler_GetByID_NotFound(t *testing.T) {
	userID := "550e8400-e29b-41d4-a716-446655440000"

	svc := &mockUserService{
		fnGet: func(_ context.Context, _ entity.UserID) (*dto.UserResponse, error) {
			return nil, domain.NewNotFoundError("user")
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+userID, nil)
	rec := httptest.NewRecorder()

	newRouter(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestUserHandler_GetByID_InvalidUUID(t *testing.T) {
	// Arrange: невалидный UUID — сервис не вызывается
	svc := &mockUserService{} // fnGet = nil

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/not-a-uuid", nil)
	rec := httptest.NewRecorder()

	newRouter(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ТЕСТЫ: Delete                                                              │
// └──────────────────────────────────────────────────────────────────────────┘

func TestUserHandler_Delete_Success(t *testing.T) {
	userID := "550e8400-e29b-41d4-a716-446655440000"

	svc := &mockUserService{
		fnDelete: func(_ context.Context, id entity.UserID) error {
			if id.String() != userID {
				t.Errorf("expected ID %s, got %s", userID, id.String())
			}
			return nil
		},
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/"+userID, nil)
	rec := httptest.NewRecorder()

	newRouter(svc).ServeHTTP(rec, req)

	// DELETE → 204 No Content, тело пустое
	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("expected empty body, got: %s", rec.Body.String())
	}
}
