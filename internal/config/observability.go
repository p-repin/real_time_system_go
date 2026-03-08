package config

// ObservabilityConfig содержит настройки observability стека.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ OBSERVABILITY CONFIG                                                       │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Jaeger получает трейсы через OTLP gRPC (порт 4317).
// Prometheus scrape происходит по /metrics (встроен в наш HTTP-сервер).
// Loki получает логи через Promtail, который читает stdout контейнера.
type ObservabilityConfig struct {
	// JaegerEndpoint — адрес Jaeger OTLP gRPC эндпоинта.
	// Default: localhost:4317 (стандартный OTLP gRPC порт)
	// В Docker: jaeger:4317 (hostname = имя сервиса в compose)
	JaegerEndpoint string `env:"JAEGER_ENDPOINT" envDefault:"localhost:4317"`

	// ServiceName — имя сервиса в Jaeger и Prometheus.
	// Отображается в Jaeger UI при поиске трейсов.
	ServiceName string `env:"SERVICE_NAME" envDefault:"real-time-system"`

	// TracingEnabled — включить/выключить трейсинг без перекомпиляции.
	// false → NoopTracerProvider (нет overhead, нет трейсов)
	// true  → Jaeger TracerProvider
	TracingEnabled bool `env:"TRACING_ENABLED" envDefault:"true"`
}
