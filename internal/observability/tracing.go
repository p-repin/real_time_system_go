package observability

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ OPENTELEMETRY ТРЕЙСИНГ → JAEGER                                            │
// └──────────────────────────────────────────────────────────────────────────┘
//
// ЗАЧЕМ ТРЕЙСИНГ?
//
//   Логи отвечают на вопрос "что произошло".
//   Метрики отвечают на вопрос "сколько раз и насколько медленно".
//   Трейсинг отвечает на вопрос "ПОЧЕМУ этот конкретный запрос был медленным".
//
//   Пример: p99 latency = 2s. Из метрик непонятно — это БД? Kafka? Валидация?
//   Трейс покажет: HTTP → PlaceOrder → BEGIN TX (10ms) → DecrementStock (1.9s) → COMMIT (10ms)
//   Вывод: DecrementStock медленный → нет индекса на products.id при обновлении.
//
// АРХИТЕКТУРА OpenTelemetry:
//
//   Код → OTel SDK → Exporter → Collector / Jaeger
//
//   1. Мы создаём span'ы в коде: tracer.Start(ctx, "PlaceOrder")
//   2. SDK батчит span'ы и отправляет через Exporter
//   3. Exporter: OTLP gRPC → отправляет на Jaeger (порт 4317)
//   4. Jaeger хранит и визуализирует трейсы
//
// ПОЧЕМУ OTLP, А НЕ JAEGER NATIVE EXPORTER?
//
//   Jaeger Native (Thrift/gRPC) — проприетарный протокол Jaeger.
//   OTLP — стандарт OpenTelemetry, принимается Jaeger, Zipkin, Datadog, Grafana Tempo.
//   Переключиться с Jaeger на Grafana Tempo = изменить один адрес экспортера.
//   OTLP — правильный выбор для нового кода.

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TracerProvider оборачивает sdktrace.TracerProvider и добавляет метод Shutdown.
//
// Мы не возвращаем *sdktrace.TracerProvider напрямую, чтобы:
// 1. Скрыть детали реализации (OTel SDK)
// 2. Упростить вызов в server.go: tp.Shutdown(ctx)
type TracerProvider struct {
	provider *sdktrace.TracerProvider
}

// InitTracing инициализирует OpenTelemetry TracerProvider с Jaeger экспортером.
//
// Параметры:
//   - ctx: контекст для инициализации gRPC соединения
//   - serviceName: имя сервиса в Jaeger (отображается в UI)
//   - jaegerEndpoint: gRPC адрес Jaeger OTLP приёмника (например "localhost:4317")
//
// Эта функция:
//  1. Создаёт gRPC соединение с Jaeger
//  2. Создаёт OTLP exporter (батчит и отправляет span'ы)
//  3. Настраивает Resource (имя сервиса, версия)
//  4. Создаёт TracerProvider с BatchSpanProcessor
//  5. Регистрирует provider глобально (otel.SetTracerProvider)
//  6. Устанавливает W3C TraceContext propagator (передача trace-id через HTTP заголовки)
func InitTracing(ctx context.Context, serviceName, jaegerEndpoint string) (*TracerProvider, error) {
	// ── 1. gRPC соединение с Jaeger ────────────────────────────────────────
	//
	// WithBlock() — ждём установки соединения при старте (fail-fast).
	// В production можно убрать WithBlock и использовать lazy connect.
	// WithTransportCredentials(insecure.NewCredentials()) — без TLS (локальная разработка).
	conn, err := grpc.NewClient(
		jaegerEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("create grpc connection to jaeger: %w", err)
	}

	// ── 2. OTLP Exporter ───────────────────────────────────────────────────
	//
	// otlptracegrpc.New() создаёт экспортер, который:
	// - Принимает span'ы от BatchSpanProcessor
	// - Сериализует в protobuf
	// - Отправляет по gRPC на Jaeger
	//
	// Таймаут 5s: если Jaeger недоступен, не блокируем приложение надолго.
	exporter, err := otlptracegrpc.New(
		ctx,
		otlptracegrpc.WithGRPCConn(conn),
		otlptracegrpc.WithTimeout(5*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("create otlp exporter: %w", err)
	}

	// ── 3. Resource — метаданные сервиса ───────────────────────────────────
	//
	// Resource описывает "кто" генерирует трейсы.
	// В Jaeger UI это отображается как имя сервиса в выпадающем списке.
	//
	// semconv.ServiceNameKey — стандартный атрибут OTel.
	// Использование стандартных атрибутов важно для совместимости
	// (Grafana Tempo, Datadog, New Relic понимают одинаково).
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion("1.0.0"),
			// deployment.environment помогает фильтровать dev/staging/prod трейсы
			semconv.DeploymentEnvironment("development"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create otel resource: %w", err)
	}

	// ── 4. TracerProvider с BatchSpanProcessor ─────────────────────────────
	//
	// BatchSpanProcessor — ключевой компонент:
	// - Буферизирует span'ы в памяти (не блокирует основной поток)
	// - Экспортирует пачками каждые N секунд или при заполнении буфера
	// - При Shutdown() — флашит все оставшиеся span'ы (graceful shutdown)
	//
	// Альтернатива SimpleSpanProcessor — синхронный, только для тестов.
	// В production всегда BatchSpanProcessor.
	bsp := sdktrace.NewBatchSpanProcessor(exporter)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		// Sampler: AlwaysSample — трейсим 100% запросов.
		// В production при высокой нагрузке: TraceIDRatioBased(0.1) — 10%.
		// AlwaysSample удобен для разработки — видим каждый запрос.
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(bsp),
	)

	// ── 5. Глобальная регистрация ──────────────────────────────────────────
	//
	// otel.SetTracerProvider(tp) — все вызовы otel.Tracer("...") в любом месте кода
	// будут использовать наш provider. Это удобно: не нужно передавать provider
	// через весь стек вызовов.
	//
	// Минус: глобальное состояние (плохо для unit-тестов).
	// Для тестов используют otel.SetTracerProvider(otel.NewNoopTracerProvider()).
	otel.SetTracerProvider(tp)

	// ── 6. W3C TraceContext Propagator ─────────────────────────────────────
	//
	// Propagator отвечает за передачу trace-id между сервисами через HTTP заголовки:
	//   traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01
	//
	// W3C TraceContext — стандарт W3C, поддерживается всеми современными системами.
	// Baggage — дополнительные данные (user-id, tenant-id) передаваемые с трейсом.
	//
	// БЕЗ PROPAGATOR: каждый сервис создаёт новый трейс → невозможно связать
	// цепочку вызовов между микросервисами.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, // traceparent header
		propagation.Baggage{},     // baggage header
	))

	return &TracerProvider{provider: tp}, nil
}

// Shutdown корректно завершает TracerProvider.
//
// Критически важно вызвать Shutdown при остановке приложения:
// - BatchSpanProcessor флашит буфер → все незакрытые span'ы отправляются в Jaeger
// - gRPC соединение закрывается
//
// БЕЗ SHUTDOWN: последние N секунд трейсов теряются (ещё в буфере, не успели отправить).
func (tp *TracerProvider) Shutdown(ctx context.Context) error {
	return tp.provider.Shutdown(ctx)
}

// Tracer возвращает именованный трейсер для создания span'ов.
//
// Имя трейсера обычно = имя пакета ("real_time_system/internal/service").
// Это помогает в Jaeger UI понять, в каком компоненте создан span.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}
