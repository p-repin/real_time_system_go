package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	httpSwagger "github.com/swaggo/http-swagger"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"real_time_system/internal/controller/http/middleware"
	"real_time_system/internal/observability"
	"real_time_system/internal/service"

	// ВАЖНО: импорт docs для регистрации swagger спецификации
	// Этот import выглядит неиспользуемым, но он нужен!
	// При импорте выполняется init(), который регистрирует swagger.json
	_ "real_time_system/docs"
)

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ HTTP ROUTER                                                                │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Централизованная настройка HTTP-маршрутизации.
//
// SWAGGER UI:
// После запуска сервера открой http://localhost:8080/swagger/index.html
//
// ГЕНЕРАЦИЯ ДОКУМЕНТАЦИИ:
// swag init -g cmd/app/main.go
// Создаёт папку docs/ с swagger.json и docs.go

// Router содержит настроенный HTTP-роутер с middleware.
type Router struct {
	mux *chi.Mux
}

// NewRouter создаёт и настраивает HTTP-роутер.
//
// m — Prometheus метрики, передаются снаружи (создаются в server.go).
// Это dependency injection: router не создаёт метрики сам, получает их извне.
func NewRouter(
	userService *service.UserService,
	cartService *service.CartService,
	orderService *service.OrderService,
	m *observability.Metrics,
) *Router {
	r := chi.NewRouter()

	// ── Global Middleware ──────────────────────────────────────────────────
	//
	// ПОРЯДОК MIDDLEWARE ИМЕЕТ ЗНАЧЕНИЕ:
	//
	// 1. RequestID   — генерируем/читаем X-Request-ID (нужен логгеру и трейсингу)
	// 2. Recoverer   — ловим panic ПЕРЕД логированием (иначе 500 не залогируется)
	// 3. OTel HTTP   — создаём trace span для всего запроса (span охватывает весь pipeline)
	// 4. Metrics     — Prometheus счётчики (после OTel, чтобы видеть трейс в метриках)
	// 5. Logging     — логируем запрос последним (видим финальный статус после всех MW)

	r.Use(middleware.RequestID)
	r.Use(chimiddleware.Recoverer)

	// ── OpenTelemetry HTTP Middleware ──────────────────────────────────────
	//
	// otelhttp.NewMiddleware создаёт span для каждого HTTP-запроса:
	//   span name: "GET /api/v1/orders/{id}"
	//   span attributes: http.method, http.url, http.status_code, http.target
	//
	// Span автоматически связывается с входящим traceparent заголовком
	// (если запрос пришёл от другого сервиса).
	//
	// WithRouteTag — добавляет chi route pattern в span как атрибут.
	// Без него span name = URL с реальным UUID → высокая кардинальность.
	r.Use(func(next http.Handler) http.Handler {
		// otelhttp.NewMiddleware оборачивает весь handler, включая роутинг.
		// operation name "http.request" будет уточнён chi route pattern'ом.
		return otelhttp.NewMiddleware("http.request")(next)
	})

	// ── Prometheus Metrics Middleware ──────────────────────────────────────
	r.Use(m.Middleware)

	r.Use(middleware.Logging)

	// ── Prometheus Metrics Endpoint ────────────────────────────────────────
	//
	// GET /metrics — Prometheus scrape endpoint.
	//
	// ПОЧЕМУ НЕ ВЫНОСИТЬ НА ОТДЕЛЬНЫЙ ПОРТ?
	// В production /metrics часто доступен только внутри кластера (не в Ingress).
	// Для нашего случая (локальная разработка) один порт удобнее.
	// В kubernetes можно закрыть через NetworkPolicy.
	r.Get("/metrics", observability.Handler().ServeHTTP)

	// ── Health Check ───────────────────────────────────────────────────────

	// @Summary      Health check
	// @Description  Проверка работоспособности сервиса
	// @Tags         Health
	// @Produce      json
	// @Success      200  {object}  map[string]string  "status: ok"
	// @Router       /health [get]
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// ── Swagger UI ─────────────────────────────────────────────────────────
	//
	// GET /swagger/* → Swagger UI
	// GET /swagger/doc.json → OpenAPI спецификация
	//
	// ПОЧЕМУ httpSwagger.WrapHandler:
	// - Отдаёт статические файлы Swagger UI
	// - Загружает doc.json из зарегистрированной спецификации
	// - Не требует отдельного файлового сервера
	r.Get("/swagger/*", httpSwagger.WrapHandler)

	// ── API Routes ─────────────────────────────────────────────────────────

	userHandler := NewUserHandler(userService)
	userHandler.RegisterRoutes(r)

	cartHandler := NewCartHandler(cartService)
	cartHandler.RegisterRoutes(r)

	orderHandler := NewOrderHandler(orderService)
	orderHandler.RegisterRoutes(r)

	return &Router{mux: r}
}

// ServeHTTP реализует http.Handler interface.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt.mux.ServeHTTP(w, r)
}

// Handler возвращает http.Handler для использования с http.Server.
func (rt *Router) Handler() http.Handler {
	return rt.mux
}
