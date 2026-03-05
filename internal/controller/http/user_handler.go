package http

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"real_time_system/domain/entity"
	"real_time_system/internal/service"
	"real_time_system/internal/service/dto"
)

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ USER HTTP HANDLER                                                          │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Handler — это "тонкий" слой между HTTP и Service.
//
// ОТВЕТСТВЕННОСТЬ HANDLER'А:
// 1. Парсинг HTTP-запроса (JSON body, path params, query params)
// 2. Вызов Service с правильными параметрами
// 3. Формирование HTTP-ответа (статус-код, JSON body)
//
// HANDLER НЕ ДОЛЖЕН:
// ❌ Содержать бизнес-логику (это Service)
// ❌ Работать с БД напрямую (это Repository)
// ❌ Знать про SQL, pgx, транзакции
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ SWAGGO КОММЕНТАРИИ                                                         │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Формат: // @тег значение
// После изменений: swag init -g cmd/app/main.go
//
// Основные теги:
// - @Summary: краткое описание (одна строка)
// - @Description: подробное описание
// - @Tags: группировка в UI
// - @Accept: Content-Type запроса
// - @Produce: Content-Type ответа
// - @Param: параметры (path, query, body)
// - @Success: успешный ответ
// - @Failure: ошибка
// - @Router: путь и метод

// userService — интерфейс для работы с пользователями.
//
// ПОЧЕМУ ИНТЕРФЕЙС, А НЕ КОНКРЕТНЫЙ ТИП:
// - Тестируемость: в тестах подменяем на mock без поднятия БД
// - Loose coupling: handler не знает про конкретную реализацию
// - Принцип D из SOLID: зависимость от абстракции, не от реализации
//
// Go-идиома: интерфейс определяет ПОТРЕБИТЕЛЬ (handler), не поставщик (service).
// Так интерфейс содержит ровно те методы, которые нужны этому handler'у.
type userService interface {
	CreateUser(ctx context.Context, req dto.CreateUserRequest) (*dto.UserResponse, error)
	GetUser(ctx context.Context, id entity.UserID) (*dto.UserResponse, error)
	UpdateUser(ctx context.Context, id entity.UserID, req dto.UpdateUserRequest) (*dto.UserResponse, error)
	DeleteUser(ctx context.Context, id entity.UserID) error
}

// UserHandler обрабатывает HTTP-запросы для User entity.
type UserHandler struct {
	userService userService
}

// NewUserHandler создаёт новый handler.
func NewUserHandler(userService *service.UserService) *UserHandler {
	return &UserHandler{
		userService: userService,
	}
}

// NewUserHandlerWithService создаёт handler с произвольной реализацией userService.
//
// Используется в тестах: позволяет подставить mock вместо реального сервиса.
// В production-коде используйте NewUserHandler.
func NewUserHandlerWithService(svc userService) *UserHandler {
	return &UserHandler{userService: svc}
}

// RegisterRoutes регистрирует маршруты для User API.
func (h *UserHandler) RegisterRoutes(r chi.Router) {
	r.Route("/api/v1/users", func(r chi.Router) {
		r.Post("/", h.Create)
		r.Get("/{id}", h.GetByID)
		r.Patch("/{id}", h.Update)
		r.Delete("/{id}", h.Delete)
	})
}

// Create создаёт нового пользователя.
//
// @Summary      Create a new user
// @Description  Создаёт нового пользователя. Email должен быть уникальным.
// @Tags         Users
// @Accept       json
// @Produce      json
// @Param        request  body      dto.CreateUserRequest  true  "User data"
// @Success      201      {object}  dto.UserResponse       "User created"
// @Failure      400      {object}  ErrorResponse          "Invalid request"
// @Failure      409      {object}  ErrorResponse          "Email already exists"
// @Failure      500      {object}  ErrorResponse          "Internal server error"
// @Router       /api/v1/users [post]
func (h *UserHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user, err := h.userService.CreateUser(r.Context(), req)
	if err != nil {
		HandleError(w, err)
		return
	}

	Created(w, user)
}

// GetByID возвращает пользователя по ID.
//
// @Summary      Get user by ID
// @Description  Возвращает пользователя по UUID.
// @Tags         Users
// @Produce      json
// @Param        id   path      string  true  "User ID (UUID)"  format(uuid)
// @Success      200  {object}  dto.UserResponse  "User found"
// @Failure      400  {object}  ErrorResponse     "Invalid UUID format"
// @Failure      404  {object}  ErrorResponse     "User not found"
// @Failure      500  {object}  ErrorResponse     "Internal server error"
// @Router       /api/v1/users/{id} [get]
func (h *UserHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	idParam := chi.URLParam(r, "id")

	userID, err := entity.ParseUserID(idParam)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid user ID format")
		return
	}

	user, err := h.userService.GetUser(r.Context(), userID)
	if err != nil {
		HandleError(w, err)
		return
	}

	JSON(w, http.StatusOK, user)
}

// Update обновляет данные пользователя (partial update).
//
// @Summary      Update user (partial)
// @Description  Частичное обновление пользователя. Передайте только поля для изменения.
// @Tags         Users
// @Accept       json
// @Produce      json
// @Param        id       path      string                 true  "User ID (UUID)"  format(uuid)
// @Param        request  body      dto.UpdateUserRequest  true  "Fields to update"
// @Success      200      {object}  dto.UserResponse       "User updated"
// @Failure      400      {object}  ErrorResponse          "Invalid request"
// @Failure      404      {object}  ErrorResponse          "User not found"
// @Failure      409      {object}  ErrorResponse          "Email already exists"
// @Failure      500      {object}  ErrorResponse          "Internal server error"
// @Router       /api/v1/users/{id} [patch]
func (h *UserHandler) Update(w http.ResponseWriter, r *http.Request) {
	idParam := chi.URLParam(r, "id")
	userID, err := entity.ParseUserID(idParam)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid user ID format")
		return
	}

	var req dto.UpdateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user, err := h.userService.UpdateUser(r.Context(), userID, req)
	if err != nil {
		HandleError(w, err)
		return
	}

	JSON(w, http.StatusOK, user)
}

// Delete удаляет пользователя (soft delete).
//
// @Summary      Delete user
// @Description  Soft delete — помечает пользователя как удалённого. Данные сохраняются.
// @Tags         Users
// @Param        id   path  string  true  "User ID (UUID)"  format(uuid)
// @Success      204  "User deleted"
// @Failure      400  {object}  ErrorResponse  "Invalid UUID format"
// @Failure      404  {object}  ErrorResponse  "User not found"
// @Failure      500  {object}  ErrorResponse  "Internal server error"
// @Router       /api/v1/users/{id} [delete]
func (h *UserHandler) Delete(w http.ResponseWriter, r *http.Request) {
	idParam := chi.URLParam(r, "id")
	userID, err := entity.ParseUserID(idParam)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid user ID format")
		return
	}

	if err := h.userService.DeleteUser(r.Context(), userID); err != nil {
		HandleError(w, err)
		return
	}

	NoContent(w)
}
