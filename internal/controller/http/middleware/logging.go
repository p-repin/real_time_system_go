package middleware

import (
	"net/http"
	"time"

	"real_time_system/internal/logger"
)

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ LOGGING MIDDLEWARE                                                         │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Логирует каждый HTTP-запрос: метод, путь, статус, время выполнения.
//
// ЗАЧЕМ ЭТО НУЖНО:
// 1. Мониторинг: видим все запросы к API
// 2. Отладка: понимаем, какие endpoint'ы вызываются
// 3. Метрики: время выполнения, статус-коды
// 4. Аудит: кто и когда обращался к API
//
// ФОРМАТ ЛОГА:
//   INFO  http request completed  method=POST path=/api/v1/users status=201 duration=45ms request_id=abc123

// responseWriter — обёртка для http.ResponseWriter, сохраняющая статус-код.
//
// ПРОБЛЕМА:
// Стандартный http.ResponseWriter не предоставляет метод для получения
// записанного статус-кода. После w.WriteHeader(201) мы не можем узнать "201".
//
// РЕШЕНИЕ:
// Оборачиваем ResponseWriter, перехватываем WriteHeader, сохраняем статус.
type responseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{
		ResponseWriter: w,
		status:         http.StatusOK, // default если WriteHeader не вызван
	}
}

func (rw *responseWriter) WriteHeader(code int) {
	if rw.wroteHeader {
		return // уже записали, игнорируем повторный вызов
	}
	rw.status = code
	rw.wroteHeader = true
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(b)
}

// Logging middleware логирует HTTP-запросы.
//
// ПАТТЕРН: обёртка ResponseWriter для захвата статус-кода.
//
// ПОРЯДОК MIDDLEWARE ВАЖЕН:
//
//	router.Use(middleware.RequestID)  // 1. сначала RequestID
//	router.Use(middleware.Logging)    // 2. потом Logging (использует RequestID)
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Оборачиваем writer для захвата статус-кода
		wrapped := newResponseWriter(w)

		// Выполняем запрос
		next.ServeHTTP(wrapped, r)

		// Логируем после выполнения
		duration := time.Since(start)
		l := logger.FromContext(r.Context())

		// Определяем уровень лога по статус-коду
		//   2xx, 3xx → Info
		//   4xx → Warn (клиентская ошибка, не наша проблема)
		//   5xx → Error (серверная ошибка, нужно разбираться)
		switch {
		case wrapped.status >= 500:
			l.Errorw("http request failed",
				"method", r.Method,
				"path", r.URL.Path,
				"status", wrapped.status,
				"duration_ms", duration.Milliseconds(),
				"request_id", GetRequestID(r.Context()),
			)
		case wrapped.status >= 400:
			l.Warnw("http request client error",
				"method", r.Method,
				"path", r.URL.Path,
				"status", wrapped.status,
				"duration_ms", duration.Milliseconds(),
				"request_id", GetRequestID(r.Context()),
			)
		default:
			l.Infow("http request completed",
				"method", r.Method,
				"path", r.URL.Path,
				"status", wrapped.status,
				"duration_ms", duration.Milliseconds(),
				"request_id", GetRequestID(r.Context()),
			)
		}
	})
}
