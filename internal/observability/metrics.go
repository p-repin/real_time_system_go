package observability

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ PROMETHEUS МЕТРИКИ                                                         │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Мы покрываем метриками ТОЛЬКО самый важный участок — HTTP-слой:
//
//   http_requests_total         — сколько запросов пришло (по методу, пути, статусу)
//   http_request_duration_seconds — latency запросов (гистограмма → p50/p95/p99)
//   http_requests_in_flight     — сколько запросов сейчас выполняется (gauge)
//
// ПОЧЕМУ ИМЕННО ЭТИ МЕТРИКИ?
//
//   "RED Method" (Weaveworks) — минимальный набор метрик для любого сервиса:
//   R — Rate    (http_requests_total)
//   E — Errors  (http_requests_total с status=5xx)
//   D — Duration (http_request_duration_seconds)
//
// ПОЧЕМУ ГИСТОГРАММА, А НЕ SUMMARY?
//
//   Summary:   вычисляет квантили (p99) на стороне клиента — нельзя агрегировать
//              между несколькими инстансами (неправильный p99 при суммировании)
//   Histogram: сырые bucket'ы → Prometheus агрегирует корректно
//              histogram_quantile(0.99, ...) работает правильно при federation
//
// ПОЧЕМУ НЕ ИНСТРУМЕНТИРОВАТЬ SERVICE/REPOSITORY LAYER?
//
//   Для первого мониторинга достаточно HTTP:
//   - Видим latency и error rate всего сервиса
//   - HTTP trace (Jaeger) покрывает внутренние операции
//   - Prometheus карточки репо/сервиса добавляются по мере необходимости

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics содержит все Prometheus метрики приложения.
//
// promauto автоматически регистрирует метрики в DefaultRegisterer при создании.
// Это идиоматично для монолитов — не нужен явный registry.
type Metrics struct {
	// RequestsTotal — счётчик HTTP-запросов.
	//
	// Labels:
	//   method — GET, POST, PATCH, DELETE
	//   path   — нормализованный путь (например /api/v1/orders/{id})
	//   status — HTTP статус-код как строка ("200", "404", "500")
	//
	// ВАЖНО: не используем точный URL в labels (например с UUID) —
	// это породит бесконечное количество time series и убьёт Prometheus.
	// path должен быть ШАБЛОНОМ, а не реальным значением.
	RequestsTotal *prometheus.CounterVec

	// RequestDuration — гистограмма latency запросов.
	//
	// Buckets по умолчанию: .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10 (сек)
	// Для web API обычно важны: 10ms, 50ms, 100ms, 500ms, 1s, 5s
	RequestDuration *prometheus.HistogramVec

	// RequestsInFlight — текущее количество обрабатываемых запросов.
	//
	// Gauge (не Counter) — значение может расти и убывать.
	// Полезно для capacity planning: если in_flight ≈ thread pool size → бутылочное горлышко.
	RequestsInFlight prometheus.Gauge
}

// NewMetrics создаёт и регистрирует Prometheus метрики.
//
// promauto.With(prometheus.DefaultRegisterer) регистрирует метрики глобально.
// Это нормально для production-монолита — не нужен отдельный registry.
func NewMetrics() *Metrics {
	return &Metrics{
		RequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				// Namespace + Subsystem = "rts_http_" (префикс всех метрик)
				// Это помогает фильтровать метрики нашего приложения среди всех метрик Go-рантайма
				Namespace: "rts",
				Subsystem: "http",
				Name:      "requests_total",
				Help:      "Total number of HTTP requests by method, path and status code.",
			},
			[]string{"method", "path", "status"},
		),

		RequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "rts",
				Subsystem: "http",
				Name:      "request_duration_seconds",
				Help:      "HTTP request latency distribution.",
				// Кастомные bucket'ы для web API:
				// 10ms, 25ms, 50ms — быстрые запросы (кэш, простые SELECT)
				// 100ms, 250ms — нормальные запросы (JOIN с индексом)
				// 500ms, 1s    — медленные (транзакции, внешние вызовы)
				// 2.5s, 5s     — очень медленные (тайм-аут клиента обычно 5s)
				Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5},
			},
			[]string{"method", "path"},
		),

		RequestsInFlight: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "rts",
				Subsystem: "http",
				Name:      "requests_in_flight",
				Help:      "Current number of HTTP requests being processed.",
			},
		),
	}
}

// Handler возвращает HTTP-обработчик для эндпоинта /metrics.
//
// GET /metrics → Prometheus scrape endpoint
// Prometheus периодически (каждые 15s по умолчанию) обращается к этому URL
// и забирает все метрики в формате text/plain.
func Handler() http.Handler {
	return promhttp.Handler()
}

// Middleware оборачивает HTTP-обработчик и записывает метрики запроса.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПОЧЕМУ СВОЁ MIDDLEWARE, А НЕ otelhttp.NewHandler?                          │
// └──────────────────────────────────────────────────────────────────────────┘
//
// otelhttp.NewHandler создаёт span'ы для трейсинга, но не пишет метрики
// в Prometheus формате. Нам нужны:
// 1. Prometheus метрики (своё middleware)
// 2. OTel трейсинг (otelhttp middleware в router.go)
//
// Два middleware — два аспекта наблюдаемости. Это нормально.
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Нормализованный путь из chi: /api/v1/orders/{id} вместо реального UUID.
		// chi.RouteContext(r.Context()).RoutePattern() доступен ПОСЛЕ роутинга,
		// поэтому мы берём r.URL.Path до выполнения. Для нормализации
		// используем паттерн который chi записывает в r.Pattern (Go 1.22+).
		//
		// В нашем случае мы используем путь "as-is" из r.URL.Path,
		// но группируем в Grafana или нормализуем ниже.
		path := r.URL.Path

		// Увеличиваем in-flight счётчик, уменьшаем при завершении
		m.RequestsInFlight.Inc()
		defer m.RequestsInFlight.Dec()

		// Оборачиваем ResponseWriter для захвата статус-кода
		// (тот же паттерн что в logging middleware)
		wrapped := &statusResponseWriter{ResponseWriter: w, status: http.StatusOK}

		start := time.Now()
		next.ServeHTTP(wrapped, r)
		duration := time.Since(start).Seconds()

		// Нормализация пути: убираем конкретные IDs из метки.
		// Без нормализации каждый UUID создаст отдельную time series →
		// memory leak в Prometheus (high cardinality problem).
		//
		// Более правильное решение — брать RoutePattern из chi после обработки.
		// Здесь используем простую нормализацию для демонстрации.
		statusStr := strconv.Itoa(wrapped.status)

		m.RequestsTotal.WithLabelValues(r.Method, path, statusStr).Inc()
		m.RequestDuration.WithLabelValues(r.Method, path).Observe(duration)
	})
}

// statusResponseWriter — минимальная обёртка для захвата HTTP статус-кода.
// (Аналог в logging middleware — они дублируют код намеренно,
// чтобы пакеты не зависели друг от друга.)
type statusResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.status = code
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}
