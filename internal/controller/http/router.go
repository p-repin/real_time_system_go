package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	httpSwagger "github.com/swaggo/http-swagger"

	"real_time_system/internal/controller/http/middleware"
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
func NewRouter(userService *service.UserService, cartService *service.CartService, orderService *service.OrderService) *Router {
	r := chi.NewRouter()

	// ── Global Middleware ──────────────────────────────────────────────────

	r.Use(middleware.RequestID)
	r.Use(chimiddleware.Recoverer)
	r.Use(middleware.Logging)

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
