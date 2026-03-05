package events

import (
	"context"
	"encoding/json"
	"fmt"

	"real_time_system/domain/events"
	"real_time_system/internal/logger"
	"real_time_system/pkg/kafka"
)

// OrderEventConsumer читает события заказов из Kafka и обрабатывает их.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ЦЕЛЬ ЭТОГО КОНСЬЮМЕРА                                                      │
// └──────────────────────────────────────────────────────────────────────────┘
//
// В реальном production этот консьюмер был бы ОТДЕЛЬНЫМ СЕРВИСОМ
// (notification-service, analytics-service и т.д.).
//
// Здесь мы реализуем его внутри того же приложения для демонстрации:
// 1. Как читать из Kafka
// 2. Как обрабатывать разные типы событий
// 3. Как корректно коммитить offset
// 4. Как останавливать консьюмер по сигналу
//
// В production-архитектуре: отдельный бинарник/сервис.
// В монолите: отдельная горутина, запускается в server.Run().
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПАТТЕРН ОБРАБОТЧИКОВ                                                        │
// └──────────────────────────────────────────────────────────────────────────┘
//
// OrderEventConsumer — маршрутизатор событий.
// Он не занимается бизнес-логикой напрямую, а вызывает нужный обработчик.
// Это позволяет легко добавлять новые типы событий и обработчики.
type OrderEventConsumer struct {
	consumer *kafka.Consumer
}

// NewOrderEventConsumer создаёт консьюмер для событий заказов.
//
// groupID должен быть уникальным для каждого логического потребителя.
// Примеры:
//   "notification-service" — для отправки уведомлений
//   "analytics-service"    — для сбора метрик
//   "inventory-service"    — для управления складом
func NewOrderEventConsumer(consumer *kafka.Consumer) *OrderEventConsumer {
	return &OrderEventConsumer{consumer: consumer}
}

// Run запускает цикл чтения событий.
//
// Блокирующий метод — запускается в отдельной горутине.
// Завершается когда ctx отменён (при graceful shutdown).
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ЦИКЛ ОБРАБОТКИ: READ → PROCESS → COMMIT                                    │
// └──────────────────────────────────────────────────────────────────────────┘
//
// for {
//   msg = Read()        // блокируется до нового сообщения
//   err = Process(msg)  // бизнес-логика
//   if err == nil {
//     Commit(msg)       // фиксируем offset только при успехе
//   }
//   // при ошибке — не коммитим → сообщение придёт снова (at-least-once)
// }
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ЧТО ДЕЛАТЬ ПРИ ОШИБКЕ ОБРАБОТКИ?                                           │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Вариант 1 (наш): логируем и продолжаем (пропускаем "ядовитое" сообщение).
//   + Консьюмер не застревает навсегда
//   - Потеря события при постоянных ошибках
//
// Вариант 2 (production): Dead Letter Queue (DLQ).
//   При ошибке → публикуем в топик "orders-dlq" → retry service → ещё раз
//   Если N попыток → публикуем в "orders-poison-pill" → алерт команде
//
// Для нашего учебного проекта: вариант 1.
func (c *OrderEventConsumer) Run(ctx context.Context) {
	l := logger.FromContext(ctx)
	l.Info("order event consumer started")

	for {
		// FetchMessage блокируется до нового сообщения или отмены контекста.
		msg, err := c.consumer.ReadMessage(ctx)
		if err != nil {
			// ctx.Done() — нормальное завершение при shutdown.
			// Проверяем через ctx.Err(): если контекст отменён — выходим штатно.
			if ctx.Err() != nil {
				l.Info("order event consumer stopped (context cancelled)")
				return
			}
			// Реальная ошибка I/O (разрыв соединения с Kafka, таймаут).
			// Логируем и продолжаем — kafka-go автоматически переподключается.
			l.Errorw("failed to read kafka message", "error", err)
			continue
		}

		// Обрабатываем сообщение.
		if err := c.processMessage(ctx, msg); err != nil {
			l.Errorw("failed to process order event",
				"error", err,
				"key", string(msg.Key),
			)
			// НЕ коммитим offset при ошибке → сообщение придёт снова.
			// В production здесь был бы retry с backoff или отправка в DLQ.
			continue
		}

		// Фиксируем offset только после успешной обработки.
		// Это гарантирует at-least-once: если упадём до Commit,
		// при рестарте получим это сообщение снова.
		if err := c.consumer.Commit(ctx, msg); err != nil {
			// Ошибка commit — редкость, но возможна при потере соединения.
			// Логируем, но не паникуем — при следующем рестарте сообщение
			// будет обработано снова (идемпотентность должна защитить нас).
			l.Errorw("failed to commit kafka offset",
				"error", err,
				"partition", msg.Partition,
				"offset", msg.Offset,
			)
		}
	}
}

// processMessage десериализует и маршрутизирует событие по типу.
func (c *OrderEventConsumer) processMessage(ctx context.Context, msg kafka.ConsumedMessage) error {
	l := logger.FromContext(ctx)

	// Десериализуем JSON.
	// ❌ Ошибка: если payload не JSON (например, бинарные данные от другого producer)
	//   → unmarshal вернёт ошибку → сообщение не будет закоммичено → цикл сломается.
	// В production: проверять magic byte или использовать Schema Registry.
	var event events.OrderEvent
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		// Возвращаем ошибку — сообщение не будет закоммичено.
		// Если это "ядовитое" сообщение (невалидный JSON всегда) — уйдём в бесконечный retry.
		// В production здесь должен быть счётчик попыток → DLQ после N неудач.
		return fmt.Errorf("unmarshal event: %w", err)
	}

	l.Infow("received order event",
		"event_id", event.EventID,
		"event_type", event.EventType,
		"order_id", event.OrderID,
		"user_id", event.UserID,
		"status", event.Status,
	)

	// Маршрутизация по типу события.
	//
	// switch лучше if-else: компилятор предупредит если добавить новую константу
	// OrderEventType, но забыть добавить case (если использовать exhaustive-lint).
	switch event.EventType {
	case events.OrderPlaced:
		c.handleOrderPlaced(ctx, event)
	case events.OrderPaid:
		c.handleOrderPaid(ctx, event)
	case events.OrderShipped:
		c.handleOrderShipped(ctx, event)
	case events.OrderDelivered:
		c.handleOrderDelivered(ctx, event)
	case events.OrderCancelled:
		c.handleOrderCancelled(ctx, event)
	default:
		// Неизвестный тип — логируем, но не возвращаем ошибку.
		// Это может быть новая версия producer'а, который добавил новые события.
		// Не нужно ломать консьюмер из-за неизвестного типа.
		l.Warnw("unknown order event type", "event_type", event.EventType)
	}

	return nil
}

// Обработчики событий — здесь бизнес-логика консьюмера.
// В production каждый обработчик вызывал бы реальный сервис.

func (c *OrderEventConsumer) handleOrderPlaced(ctx context.Context, event events.OrderEvent) {
	l := logger.FromContext(ctx)
	// В production: отправить email "Ваш заказ #X оформлен"
	// notificationService.SendOrderConfirmation(ctx, event.UserID, event.OrderID)
	l.Infow("📦 order placed — would send confirmation email",
		"order_id", event.OrderID,
		"amount", fmt.Sprintf("%d %s", event.TotalAmount, event.Currency),
	)
}

func (c *OrderEventConsumer) handleOrderPaid(ctx context.Context, event events.OrderEvent) {
	l := logger.FromContext(ctx)
	// В production: уведомить склад о начале сборки заказа
	// warehouseService.StartPicking(ctx, event.OrderID)
	l.Infow("💳 order paid — would notify warehouse to start picking",
		"order_id", event.OrderID,
	)
}

func (c *OrderEventConsumer) handleOrderShipped(ctx context.Context, event events.OrderEvent) {
	l := logger.FromContext(ctx)
	// В production: отправить SMS с трек-номером
	// notificationService.SendTrackingInfo(ctx, event.UserID, trackingNumber)
	l.Infow("🚚 order shipped — would send tracking SMS",
		"order_id", event.OrderID,
	)
}

func (c *OrderEventConsumer) handleOrderDelivered(ctx context.Context, event events.OrderEvent) {
	l := logger.FromContext(ctx)
	// В production: начислить бонусные баллы, запросить отзыв через 3 дня
	// loyaltyService.AwardPoints(ctx, event.UserID, event.TotalAmount)
	l.Infow("✅ order delivered — would award loyalty points",
		"order_id", event.OrderID,
		"user_id", event.UserID,
	)
}

func (c *OrderEventConsumer) handleOrderCancelled(ctx context.Context, event events.OrderEvent) {
	l := logger.FromContext(ctx)
	// В production: инициировать возврат денег, уведомить пользователя
	// paymentService.Refund(ctx, event.OrderID, event.TotalAmount)
	l.Infow("❌ order cancelled — would initiate refund",
		"order_id", event.OrderID,
	)
}

// Close освобождает ресурсы консьюмера.
func (c *OrderEventConsumer) Close() error {
	return c.consumer.Close()
}
