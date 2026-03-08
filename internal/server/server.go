package server

import (
	"context"
	"errors"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"real_time_system/internal/config"
	httpctrl "real_time_system/internal/controller/http"
	"real_time_system/internal/events"
	"real_time_system/internal/logger"
	"real_time_system/internal/observability"
	"real_time_system/internal/repository/postgres"
	"real_time_system/internal/service"
	"real_time_system/pkg/client"
	"real_time_system/pkg/kafka"
)

// Server — корневая структура приложения, владеющая всеми зависимостями.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ DEPENDENCY INJECTION: COMPOSITION ROOT                                    │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Server — это "composition root", место где создаются и связываются
// все зависимости приложения:
//
//	Config → Postgres+Kafka → Repository → Service → Handler → Router → HTTP Server
//	                       → TracerProvider (OTel → Jaeger)
//	                       → Metrics (Prometheus)
//
// ПОЧЕМУ ЗДЕСЬ, А НЕ В MAIN:
// - main() должен быть тривиальным: создать → запустить → обработать ошибку
// - Server можно тестировать с mock-зависимостями
// - При добавлении новых компонентов main не разрастается
type Server struct {
	cfg            *config.Config
	pg             *client.Postgres
	kafkaProducer  *kafka.Producer            // нужен для Close() при shutdown
	orderConsumer  *events.OrderEventConsumer // нужен для Close() при shutdown
	tracerProvider *observability.TracerProvider // нужен для Shutdown() — flush span'ов
	httpServer     *http.Server
}

// New создаёт Server, инициализируя все зависимости.
//
// ПОРЯДОК ИНИЦИАЛИЗАЦИИ ВАЖЕН:
// 1. Config — загружаем конфигурацию
// 2. Postgres — подключаемся к БД
// 3. Repositories — создаём слой доступа к данным
// 4. Services — создаём бизнес-логику
// 5. Handlers → Router — создаём HTTP слой
// 6. HTTP Server — готов к запуску
func New(ctx context.Context) (*Server, error) {
	// 1. Load config
	cfg, err := config.LoadConfig()
	if err != nil {
		return nil, err
	}

	// 2. Connect to PostgreSQL
	pg, err := client.NewPostgres(ctx, cfg)
	if err != nil {
		return nil, err
	}

	// 3. Create Kafka producer
	//
	// ┌──────────────────────────────────────────────────────────────────────┐
	// │ ПОЧЕМУ PRODUCER СОЗДАЁТСЯ ЗДЕСЬ, А НЕ ВНУТРИ OrderService?             │
	// └──────────────────────────────────────────────────────────────────────┘
	//
	// Composition Root (server.go) владеет всеми ресурсами и отвечает за их
	// жизненный цикл: создание при старте, закрытие при shutdown.
	// OrderService — бизнес-логика, не должен знать о деталях создания клиентов.
	//
	// Один Producer на всё приложение (singleton):
	// - kafka.Writer поддерживает connection pool к брокерам
	// - Создавать новый Writer на каждый запрос = overhead + утечка соединений
	// - Один Writer = одно TCP-соединение с брокером (+ переподключение при failover)
	kafkaProducer := kafka.NewProducer(cfg.KafkaConfig)

	// Создаём OrderEventPublisher — связываем Producer с доменными событиями
	orderEventPublisher := events.NewKafkaOrderPublisher(kafkaProducer, cfg.KafkaConfig)

	// Создаём Consumer для чтения событий заказов.
	//
	// В нашем монолите консьюмер работает в той же горутине что и продюсер.
	// В production это был бы отдельный сервис.
	//
	// GroupID "order-notifications" — логическая группа для обработки уведомлений.
	// Если добавим analytics-consumer — он получит свой GroupID и будет читать
	// те же события независимо.
	kafkaConsumer := kafka.NewConsumer(cfg.KafkaConfig.Brokers, cfg.KafkaConfig.TopicOrders, "order-notifications")
	orderConsumer := events.NewOrderEventConsumer(kafkaConsumer)

	// 4. Create repositories
	userRepo := postgres.NewUserRepository(pg)

	// 5. Create services
	userService := service.NewUserService(userRepo)

	cartRepo := postgres.NewCartRepository(pg)
	cartItemRepo := postgres.NewCartItemsRepository(pg)
	productRepo := postgres.NewProductRepository(pg)

	cartService := service.NewCartService(cartRepo, cartItemRepo, productRepo)

	orderRepo := postgres.NewOrderRepository(pg)
	orderService := service.NewOrderService(
		cartRepo,
		orderRepo,
		productRepo,
		cartItemRepo,
		pg.Pool,
		orderEventPublisher, // передаём publisher через DI
	)

	// ── Observability ──────────────────────────────────────────────────────

	// Prometheus метрики: создаём один экземпляр на всё приложение.
	// promauto регистрирует метрики в DefaultRegisterer при создании NewMetrics().
	metrics := observability.NewMetrics()

	// OpenTelemetry TracerProvider → Jaeger.
	//
	// ┌──────────────────────────────────────────────────────────────────────┐
	// │ ПОЧЕМУ ТРЕЙСИНГ ИНИЦИАЛИЗИРУЕТСЯ ЗДЕСЬ?                                │
	// └──────────────────────────────────────────────────────────────────────┘
	//
	// InitTracing устанавливает глобальный TracerProvider через otel.SetTracerProvider().
	// Это должно произойти ДО создания любых middleware или handlers,
	// которые вызывают otel.Tracer("...").
	//
	// Если TRACING_ENABLED=false — TracerProvider не создаётся,
	// otel использует NoopTracerProvider (нет overhead, нет трейсов).
	var tracerProvider *observability.TracerProvider
	if cfg.ObservabilityConfig.TracingEnabled {
		tracerProvider, err = observability.InitTracing(
			ctx,
			cfg.ObservabilityConfig.ServiceName,
			cfg.ObservabilityConfig.JaegerEndpoint,
		)
		if err != nil {
			// Не фатально: приложение работает без трейсинга.
			// В production можно сделать фатальным (return nil, err).
			logger.FromContext(ctx).Warnw("failed to init tracing, running without it",
				"error", err,
				"jaeger_endpoint", cfg.ObservabilityConfig.JaegerEndpoint,
			)
		}
	}

	// 6. Create HTTP router with handlers
	router := httpctrl.NewRouter(userService, cartService, orderService, metrics)

	// 6. Create HTTP server
	//
	// ┌──────────────────────────────────────────────────────────────────────────┐
	// │ HTTP SERVER TIMEOUTS: ЗАЩИТА ОТ АТАК                                      │
	// └──────────────────────────────────────────────────────────────────────────┘
	//
	// ReadTimeout:  защита от slowloris (медленная отправка запроса)
	// WriteTimeout: защита от зависших клиентов (не читают ответ)
	// IdleTimeout:  освобождение keep-alive соединений
	//
	// БЕЗ ТАЙМАУТОВ:
	// - Атакующий открывает 10000 соединений, медленно отправляет данные
	// - Все горутины заняты, легитимные пользователи не могут подключиться
	// - Server падает от OOM (каждая горутина ~2KB stack)
	httpServer := &http.Server{
		Addr:         cfg.HTTPConfig.Addr(),
		Handler:      router.Handler(),
		ReadTimeout:  cfg.HTTPConfig.ReadTimeout,
		WriteTimeout: cfg.HTTPConfig.WriteTimeout,
		IdleTimeout:  cfg.HTTPConfig.IdleTimeout,
	}

	return &Server{
		cfg:            cfg,
		pg:             pg,
		kafkaProducer:  kafkaProducer,
		orderConsumer:  orderConsumer,
		tracerProvider: tracerProvider,
		httpServer:     httpServer,
	}, nil
}

// Run запускает приложение и блокируется до получения сигнала завершения.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ GRACEFUL SHUTDOWN: ПОЧЕМУ ЭТО ВАЖНО                                        │
// └──────────────────────────────────────────────────────────────────────────┘
//
// При получении SIGINT/SIGTERM:
// 1. Прекращаем принимать новые соединения
// 2. Ждём завершения активных запросов (до ShutdownTimeout)
// 3. Закрываем БД
//
// БЕЗ GRACEFUL SHUTDOWN:
// - Kubernetes посылает SIGTERM → процесс мгновенно умирает
// - Активные запросы обрываются → клиенты получают connection reset
// - Транзакции откатываются на середине → несогласованные данные
func (s *Server) Run() error {
	// signal.NotifyContext — основа graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	l := logger.FromContext(ctx)

	// Канал для ошибок HTTP-сервера
	httpErrChan := make(chan error, 1)

	// Запускаем Kafka Consumer в отдельной горутине.
	//
	// ┌──────────────────────────────────────────────────────────────────────┐
	// │ ПОЧЕМУ В ОТДЕЛЬНОЙ ГОРУТИНЕ?                                           │
	// └──────────────────────────────────────────────────────────────────────┘
	//
	// Consumer.Run() блокирующий: он читает Kafka в бесконечном цикле.
	// Если запустить в основной горутине — HTTP-сервер никогда не запустится.
	//
	// При получении ctx.Done() (SIGINT/SIGTERM):
	// - ctx отменяется
	// - Consumer.ReadMessage(ctx) возвращает ошибку ctx.Err()
	// - Consumer.Run() возвращает управление (горутина завершается)
	go s.orderConsumer.Run(ctx)

	// Запускаем HTTP-сервер в отдельной горутине
	go func() {
		l.Infow("starting HTTP server",
			"addr", s.httpServer.Addr,
		)

		// ListenAndServe блокируется до ошибки или Shutdown
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			httpErrChan <- err
		}
		close(httpErrChan)
	}()

	l.Infow("application started",
		"http_addr", s.httpServer.Addr,
	)

	// Ждём: либо сигнал завершения, либо ошибку сервера
	select {
	case <-ctx.Done():
		l.Info("shutdown signal received")

	case err := <-httpErrChan:
		if err != nil {
			l.Errorw("HTTP server error", "error", err)
			return err
		}
	}

	// ── Graceful Shutdown ──────────────────────────────────────────────────

	l.Info("shutting down gracefully...")

	// Создаём контекст с таймаутом для shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(),
		s.cfg.HTTPConfig.ShutdownTimeout,
	)
	defer shutdownCancel()

	// Останавливаем HTTP-сервер (ждёт завершения активных запросов)
	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		l.Errorw("HTTP server shutdown error", "error", err)
	}

	// Закрываем остальные ресурсы
	s.shutdown()

	l.Info("shutdown complete")
	return nil
}

// shutdown выполняет корректное освобождение ресурсов.
//
// ПОРЯДОК ЗАКРЫТИЯ ВАЖЕН:
// 1. HTTP-сервер уже остановлен (Shutdown вызван в Run)
// 2. TracerProvider — flush незаконченных span'ов в Jaeger
// 3. Kafka Producer — flush буферизированных сообщений
// 4. PostgreSQL — закрыть пул соединений последним
//
// Почему TracerProvider первым?
// После HTTP Shutdown новых span'ов нет, но BatchSpanProcessor может иметь
// span'ы в буфере. TracerProvider.Shutdown() флашит их в Jaeger.
// Если закрыть раньше — потеряем трейсы последних запросов.
func (s *Server) shutdown() {
	// Закрываем TracerProvider: flush всех буферизированных span'ов.
	// БЕЗ ЭТОГО: последние ~5 секунд трейсов будут потеряны.
	if s.tracerProvider != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.tracerProvider.Shutdown(shutdownCtx)
	}

	// Закрываем Kafka Consumer: дожидаемся завершения текущего сообщения.
	// Consumer.Run() уже завершился (ctx отменён), Close() освобождает соединение.
	if s.orderConsumer != nil {
		_ = s.orderConsumer.Close()
	}

	// Закрываем Kafka Producer: flush буферизированных сообщений
	if s.kafkaProducer != nil {
		if err := s.kafkaProducer.Close(); err != nil {
			_ = err
		}
	}

	// Закрываем пул соединений с БД ПОСЛЕДНИМ
	// (после того как HTTP-сервер остановлен и нет активных запросов)
	if s.pg != nil {
		s.pg.Close()
	}
}

// Close — публичный метод для принудительного освобождения ресурсов (для тестов).
func (s *Server) Close() {
	s.shutdown()
}
