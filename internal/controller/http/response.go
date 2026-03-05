package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"real_time_system/domain"
)

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ HTTP RESPONSE HELPERS                                                      │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Централизованные функции для формирования HTTP-ответов.
// Единообразие ответов важно для:
// 1. Клиентов API — всегда знают формат ответа
// 2. Мониторинга — единый формат ошибок легче парсить
// 3. DRY — не дублируем json.NewEncoder в каждом handler

// ErrorResponse — стандартный формат ошибки для API.
//
// ПОЧЕМУ отдельная структура:
// - Клиент всегда знает, что ошибка придёт в формате {"error": "...", "code": 400}
// - Можем добавить поля: request_id, details, timestamp
// - Унификация: все endpoint'ы отвечают одинаково
type ErrorResponse struct {
	Error string `json:"error"`
	Code  int    `json:"code"`
}

// JSON отправляет успешный JSON-ответ.
//
// ПАТТЕРН: вместо ручного:
//
//	w.Header().Set("Content-Type", "application/json")
//	w.WriteHeader(status)
//	json.NewEncoder(w).Encode(data)
//
// Используем:
//
//	http.JSON(w, http.StatusOK, data)
func JSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if data != nil {
		if err := json.NewEncoder(w).Encode(data); err != nil {
			// Логировать ошибку сериализации (маловероятно, но возможно)
			// В production: logger.Error("failed to encode response", "error", err)
			return
		}
	}
}

// Error отправляет ошибку в стандартном формате.
//
// ИСПОЛЬЗОВАНИЕ:
//
//	httputil.Error(w, http.StatusNotFound, "user not found")
//	httputil.Error(w, http.StatusBadRequest, "invalid email format")
func Error(w http.ResponseWriter, status int, message string) {
	JSON(w, status, ErrorResponse{
		Error: message,
		Code:  status,
	})
}

// HandleError обрабатывает domain ошибки и отправляет правильный HTTP-статус.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПАТТЕРН: КОНВЕРТАЦИЯ DOMAIN ERRORS → HTTP STATUS                          │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Service возвращает domain.DomainError с StatusCode внутри.
// Handler не должен знать про бизнес-логику ошибок — только конвертирует.
//
// ПРИМЕРЫ:
//   - domain.NewNotFoundError("user")     → 404 + {"error": "user not found"}
//   - domain.NewConflictError("email...") → 409 + {"error": "email already exists"}
//   - domain.NewValidationError("...")    → 400 + {"error": "..."}
//   - любая другая ошибка                 → 500 + {"error": "internal server error"}
//
// ПОЧЕМУ НЕ ЭКСПОЗИМ ВНУТРЕННИЕ ОШИБКИ:
// При 500 мы НЕ отправляем err.Error() клиенту:
//   - Безопасность: клиент не должен видеть stack trace, SQL-запросы
//   - Пользователь: "pq: connection refused" ничего не скажет пользователю
//
// Внутренняя ошибка логируется, а клиент получает generic "internal server error".
func HandleError(w http.ResponseWriter, err error) {
	var domainErr *domain.DomainError
	if errors.As(err, &domainErr) {
		// DomainError содержит StatusCode — используем его
		Error(w, domainErr.StatusCode, domainErr.Message)
		return
	}

	// Проверяем sentinel errors (без HTTP-кода)
	// Конвертируем в 400 Bad Request для ошибок валидации
	switch {
	case errors.Is(err, domain.ErrEmptyEmail),
		errors.Is(err, domain.ErrEmptyName),
		errors.Is(err, domain.ErrEmptyProductName),
		errors.Is(err, domain.ErrInvalidQuantity),
		errors.Is(err, domain.ErrNegativeAmount),
		errors.Is(err, domain.ErrEmptyCurrency),
		errors.Is(err, domain.ErrCurrencyMismatch):
		Error(w, http.StatusBadRequest, err.Error())
		return
	}

	// Неизвестная ошибка → 500
	// В production: логируем полную ошибку, клиенту — generic message
	Error(w, http.StatusInternalServerError, "internal server error")
}

// NoContent отправляет 204 No Content (для DELETE).
func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// Created отправляет 201 Created с данными.
func Created(w http.ResponseWriter, data interface{}) {
	JSON(w, http.StatusCreated, data)
}
