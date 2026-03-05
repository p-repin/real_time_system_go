package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ REQUEST ID MIDDLEWARE                                                      │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Каждый HTTP-запрос получает уникальный ID для трейсинга.
//
// ЗАЧЕМ ЭТО НУЖНО:
// 1. Логирование: все логи одного запроса связаны одним ID
//    [req-id=abc123] started processing
//    [req-id=abc123] database query took 50ms
//    [req-id=abc123] completed with 200
//
// 2. Отладка: пользователь сообщает "ошибка при создании заказа",
//    мы просим X-Request-ID из ответа и находим все логи этого запроса
//
// 3. Distributed tracing: ID пробрасывается в другие сервисы
//    Service A (req-id=abc) → Service B (parent-id=abc) → Service C
//
// 4. Мониторинг: группировка метрик по request_id

// requestIDKey — ключ для хранения request ID в context.
// Приватный тип предотвращает коллизии с другими пакетами.
type requestIDKey struct{}

// RequestIDHeader — заголовок для передачи request ID.
const RequestIDHeader = "X-Request-ID"

// RequestID middleware добавляет уникальный ID к каждому запросу.
//
// ЛОГИКА:
// 1. Проверяем входящий заголовок X-Request-ID (от клиента или прокси)
// 2. Если нет — генерируем новый UUID
// 3. Сохраняем в context для использования в handler'ах
// 4. Добавляем в response headers для клиента
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Берём ID из заголовка или генерируем новый
		requestID := r.Header.Get(RequestIDHeader)
		if requestID == "" {
			requestID = uuid.New().String()
		}

		// Добавляем в context — доступен в handler'ах через GetRequestID(ctx)
		ctx := context.WithValue(r.Context(), requestIDKey{}, requestID)

		// Добавляем в response headers — клиент может использовать для отладки
		w.Header().Set(RequestIDHeader, requestID)

		// Передаём управление следующему middleware/handler
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetRequestID извлекает request ID из context.
//
// ИСПОЛЬЗОВАНИЕ В HANDLER'АХ:
//
//	func (h *UserHandler) Create(w http.ResponseWriter, r *http.Request) {
//	    requestID := middleware.GetRequestID(r.Context())
//	    logger.Info("creating user", "request_id", requestID)
//	}
func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		return id
	}
	return ""
}
