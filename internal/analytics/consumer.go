package analytics

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ANALYTICS CONSUMER: KAFKA → CLICKHOUSE                                    │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Этот consumer читает события заказов из Kafka и записывает их в ClickHouse
// для аналитики. Он работает ПАРАЛЛЕЛЬНО с OrderEventConsumer
// (internal/events/order_consumer.go), каждый в своей consumer group.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ CONSUMER GROUPS: ПОЧЕМУ ДВА CONSUMER'А ЧИТАЮТ ОДИН ТОПИК?                 │
// └──────────────────────────────────────────────────────────────────────────┘
//
//   Kafka topic "orders":
//   ┌─────────────────────────────────────────────────────────────────────┐
//   │  Partition 0: [placed] [paid] [shipped] ...                         │
//   │  Partition 1: [placed] [cancelled] ...                              │
//   │  Partition 2: [placed] [paid] [delivered] ...                       │
//   └─────────────────────────────────────────────────────────────────────┘
//          │                                          │
//          ▼                                          ▼
//   Group "order-notifications"               Group "order-analytics"
//   (internal/events/order_consumer.go)       (ЭТОТ ФАЙЛ)
//   → отправляет email, SMS, push            → пишет в ClickHouse
//
// Каждая группа получает ВСЕ события НЕЗАВИСИМО.
// Если analytics consumer упал → notifications продолжают работать.
// Если notifications consumer отстал → analytics не замедляется.
//
// Это фундаментальное преимущество Event-Driven Architecture:
// добавление нового потребителя НЕ влияет на существующих.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ БУФЕРИЗАЦИЯ: ПОЧЕМУ НЕ ПИСАТЬ КАЖДОЕ СОБЫТИЕ СРАЗУ?                      │
// └──────────────────────────────────────────────────────────────────────────┘
//
// ClickHouse рекомендует: максимум 1 INSERT в секунду на таблицу.
// При 100 events/sec без буфера: 100 INSERT/sec → "Too many parts" через час.
//
// Решение: буферизация с двумя триггерами:
//   1. По размеру: накопили BatchSize событий → flush
//   2. По времени: прошло FlushInterval → flush (даже если буфер не полон)
//
// Это стандартный паттерн "time-or-size buffering", используемый в:
//   - Prometheus remote_write (batch отправка в долговременное хранилище)
//   - Logstash (batch write в Elasticsearch)
//   - Kafka producer (linger.ms + batch.size)
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ AT-LEAST-ONCE СЕМАНТИКА И ДУБЛИКАТЫ                                       │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Мы коммитим Kafka offset ТОЛЬКО после успешного flush в ClickHouse.
// Если приложение упадёт после flush, но до commit →
// при рестарте те же события прочитаются снова → дубликаты в ClickHouse.
//
// Как с этим жить:
//   Вариант 1 (наш): принять дубликаты, дедуплицировать при чтении.
//     SELECT count(DISTINCT event_id) вместо count(*)
//     SELECT ... FROM order_events FINAL (ReplacingMergeTree)
//   Плюс: простота, нет overhead при записи.
//   Минус: аналитик должен помнить про DISTINCT.
//
//   Вариант 2: ReplacingMergeTree с ORDER BY (event_id).
//     ClickHouse при merge удаляет дубликаты по event_id.
//     Плюс: автоматическая дедупликация.
//     Минус: merge непредсказуем по времени; до merge дубликаты видны.
//     И ORDER BY (event_id) ухудшит аналитические запросы.
//
//   Вариант 3: exactly-once через Kafka transactions + idempotent producer.
//     Плюс: никаких дубликатов.
//     Минус: сложность, overhead, не все клиенты поддерживают.
//
// Для аналитики at-least-once + DISTINCT — достаточно и просто.

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	domainEvents "real_time_system/domain/events"
	"go.uber.org/zap"

	"real_time_system/internal/logger"
	"real_time_system/pkg/kafka"
)

// Consumer читает события заказов из Kafka и batch-записывает в ClickHouse.
type Consumer struct {
	kafkaConsumer *kafka.Consumer
	repo          *Repository
	batchSize     int
	flushInterval time.Duration

	// mu защищает buffer и pendingMsgs от concurrent access.
	//
	// ┌──────────────────────────────────────────────────────────────────────┐
	// │ НУЖЕН ЛИ ЗДЕСЬ MUTEX?                                                 │
	// └──────────────────────────────────────────────────────────────────────┘
	//
	// В текущей реализации consumer однопоточный (один select loop).
	// Mutex — ЗАЩИТА НА БУДУЩЕЕ:
	//   - Если добавим метод Flush() для вызова из healthcheck
	//   - Если добавим Metrics() для экспорта размера буфера в Prometheus
	//   - Если перейдём на параллельное чтение из нескольких партиций
	//
	// Стоимость mutex при отсутствии contention: ~25ns (один CAS).
	// Стоимость race condition бага: дни отладки.
	mu          sync.Mutex
	buffer      []domainEvents.OrderEvent
	pendingMsgs []kafka.ConsumedMessage
}

// NewConsumer создаёт analytics consumer с буферизацией.
//
// batchSize:     сколько событий накопить перед flush (рекомендуется 100-1000)
// flushInterval: максимальное время между flush'ами (рекомендуется 1s-10s)
func NewConsumer(
	kafkaConsumer *kafka.Consumer,
	repo *Repository,
	batchSize int,
	flushInterval time.Duration,
) *Consumer {
	return &Consumer{
		kafkaConsumer: kafkaConsumer,
		repo:          repo,
		batchSize:     batchSize,
		flushInterval: flushInterval,
		buffer:        make([]domainEvents.OrderEvent, 0, batchSize),
		pendingMsgs:   make([]kafka.ConsumedMessage, 0, batchSize),
	}
}

// Run запускает цикл чтения из Kafka с буферизацией.
//
// Блокирующий метод — запускается в отдельной горутине.
// Завершается когда ctx отменён (graceful shutdown).
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ АЛГОРИТМ БУФЕРИЗИРОВАННОГО ЧТЕНИЯ                                        │
// └──────────────────────────────────────────────────────────────────────────┘
//
//   ┌─── Основной цикл (for) ─────────────────────────────────────────────┐
//   │                                                                       │
//   │  1. ReadMessage с коротким timeout (1s)                              │
//   │     ├── Получили → десериализуем → добавляем в буфер                 │
//   │     └── Timeout  → проверяем flush по таймеру                        │
//   │                                                                       │
//   │  2. Если len(buffer) >= batchSize → flush                            │
//   │                                                                       │
//   │  3. Если ticker сработал → flush (даже если буфер не полон)          │
//   │                                                                       │
//   │  4. ctx.Done() → финальный flush → выход                             │
//   │                                                                       │
//   └───────────────────────────────────────────────────────────────────────┘
//
// ПОЧЕМУ ReadMessage С TIMEOUT, А НЕ BLOCKING ReadMessage + ОТДЕЛЬНЫЙ TIMER?
//
//   Вариант 1 (blocking read в горутине + select с timer):
//     go func() { msg := Read(ctx); msgChan <- msg }()
//     select { case msg := <-msgChan: ...; case <-ticker.C: flush() }
//     Проблема: горутина на каждое чтение → goroutine leak если Read завис.
//
//   Вариант 2 (наш: read с timeout + проверка timer после каждого read):
//     readCtx с timeout 1s → если нет сообщений, возвращается через 1s.
//     После каждого возврата проверяем: не пора ли flush?
//     Простой, предсказуемый, без лишних горутин.
//
//   Вариант 2 проще и надёжнее. Latency flush'а: до readTimeout + flushInterval.
//   Для аналитики задержка в 1-6 секунд абсолютно приемлема.
func (c *Consumer) Run(ctx context.Context) {
	l := logger.FromContext(ctx)
	l.Info("analytics consumer started")

	ticker := time.NewTicker(c.flushInterval)
	defer ticker.Stop()

	// readTimeout — максимальное время ожидания нового сообщения из Kafka.
	//
	// Не путать с flushInterval:
	//   readTimeout  = как долго ждать одно сообщение (1s — быстрый возврат)
	//   flushInterval = как часто сбрасывать буфер в ClickHouse (5s по умолчанию)
	//
	// Маленький readTimeout (1s) означает:
	//   - Быстрая реакция на ctx.Done() (shutdown)
	//   - Быстрая проверка ticker (flush по таймеру)
	//   - При отсутствии сообщений: цикл "крутится" каждую секунду (минимальный overhead)
	const readTimeout = 1 * time.Second

	for {
		// Проверяем shutdown ПЕРЕД чтением — быстрый выход.
		select {
		case <-ctx.Done():
			c.finalFlush(ctx, l)
			l.Info("analytics consumer stopped")
			return
		default:
		}

		// Читаем с коротким timeout.
		// Если сообщений нет — вернётся через readTimeout с ошибкой.
		readCtx, cancel := context.WithTimeout(ctx, readTimeout)
		msg, err := c.kafkaConsumer.ReadMessage(readCtx)
		cancel()

		if err != nil {
			// Timeout или ctx cancelled — НЕ ошибка, это нормальный flow.
			if ctx.Err() != nil {
				// Основной контекст отменён → shutdown.
				c.finalFlush(ctx, l)
				l.Info("analytics consumer stopped")
				return
			}
			// readCtx timeout → нет новых сообщений, проверяем flush по таймеру.
			select {
			case <-ticker.C:
				c.timerFlush(ctx, l)
			default:
			}
			continue
		}

		// Десериализуем и добавляем в буфер.
		if err := c.addToBuffer(msg, l); err != nil {
			// Невалидное сообщение: логируем и пропускаем.
			// НЕ останавливаем consumer из-за одного "ядовитого" сообщения.
			//
			// ПРОБЛЕМА: offset этого сообщения не закоммичен →
			// при рестарте оно придёт снова → бесконечный retry.
			//
			// РЕШЕНИЕ: коммитим offset "ядовитого" сообщения (пропускаем его).
			// В production: отправить в Dead Letter Queue для ручного анализа.
			l.Errorw("failed to process message, skipping",
				"error", err,
				"partition", msg.Partition,
				"offset", msg.Offset,
			)
			_ = c.kafkaConsumer.Commit(ctx, msg)
			continue
		}

		// Проверяем: не пора ли flush по размеру буфера?
		c.mu.Lock()
		shouldFlush := len(c.buffer) >= c.batchSize
		c.mu.Unlock()

		if shouldFlush {
			c.doFlush(ctx, l)
		}

		// Проверяем flush по таймеру (не блокируемся — select с default).
		select {
		case <-ticker.C:
			c.timerFlush(ctx, l)
		default:
		}
	}
}

// addToBuffer десериализует событие и добавляет в буфер.
func (c *Consumer) addToBuffer(msg kafka.ConsumedMessage, l *zap.SugaredLogger) error {
	var event domainEvents.OrderEvent
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		return fmt.Errorf("unmarshal order event: %w", err)
	}

	c.mu.Lock()
	c.buffer = append(c.buffer, event)
	c.pendingMsgs = append(c.pendingMsgs, msg)
	c.mu.Unlock()

	l.Debugw("buffered analytics event",
		"event_type", event.EventType,
		"order_id", event.OrderID,
		"buffer_size", len(c.buffer),
	)

	return nil
}

// timerFlush — flush по таймеру (FlushInterval).
func (c *Consumer) timerFlush(ctx context.Context, l *zap.SugaredLogger) {
	c.mu.Lock()
	hasData := len(c.buffer) > 0
	c.mu.Unlock()

	if hasData {
		l.Debugw("timer flush triggered")
		c.doFlush(ctx, l)
	}
}

// finalFlush — последний flush при shutdown.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПОЧЕМУ ФИНАЛЬНЫЙ FLUSH КРИТИЧЕСКИ ВАЖЕН                                   │
// └──────────────────────────────────────────────────────────────────────────┘
//
// При graceful shutdown (SIGTERM):
//   1. ctx отменяется → consumer выходит из цикла
//   2. В буфере могут быть незаписанные события
//   3. Без finalFlush: эти события ПОТЕРЯНЫ (offset не закоммичен)
//   4. С finalFlush: записываем в ClickHouse → коммитим offset → данные сохранены
//
// Используем context.Background() (не ctx, который уже отменён!),
// но с таймаутом, чтобы не блокировать shutdown бесконечно.
func (c *Consumer) finalFlush(ctx context.Context, l *zap.SugaredLogger) {
	c.mu.Lock()
	hasData := len(c.buffer) > 0
	c.mu.Unlock()

	if !hasData {
		return
	}

	l.Infow("performing final flush before shutdown",
		"buffer_size", len(c.buffer),
	)

	// Используем фоновый контекст с таймаутом: основной ctx уже cancelled.
	// 10 секунд — достаточно для batch insert в ClickHouse (обычно ~50ms).
	flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c.doFlush(flushCtx, l)
}

// doFlush записывает буфер в ClickHouse и коммитит Kafka offsets.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПОРЯДОК ОПЕРАЦИЙ: INSERT → COMMIT (критически важно!)                     │
// └──────────────────────────────────────────────────────────────────────────┘
//
//   1. InsertOrderEvents()    — пишем в ClickHouse
//   2. InsertOrderItemEvents() — пишем товарную детализацию
//   3. Commit()                — коммитим Kafka offset
//
// Если INSERT упал → НЕ коммитим → при рестарте прочитаем те же события.
// Если Commit упал → данные в ClickHouse есть, но offset не сдвинулся
//   → при рестарте прочитаем те же события → дубликаты (at-least-once).
//
// Почему не наоборот (Commit → INSERT)?
//   Commit успешен → падение → INSERT не выполнен.
//   При рестарте offset уже сдвинут → события ПОТЕРЯНЫ навсегда.
//   Потеря данных хуже дубликатов (дубликаты = count(DISTINCT), потеря = необратимо).
func (c *Consumer) doFlush(ctx context.Context, l *zap.SugaredLogger) {
	c.mu.Lock()
	if len(c.buffer) == 0 {
		c.mu.Unlock()
		return
	}

	// Забираем буфер и сбрасываем для нового накопления.
	// Swap trick: переиспользуем слайс, не аллоцируя новый.
	eventsToFlush := c.buffer
	msgsToCommit := c.pendingMsgs
	c.buffer = make([]domainEvents.OrderEvent, 0, c.batchSize)
	c.pendingMsgs = make([]kafka.ConsumedMessage, 0, c.batchSize)
	c.mu.Unlock()

	l.Infow("flushing analytics batch to ClickHouse",
		"events_count", len(eventsToFlush),
	)

	// ── 1. INSERT order_events ──────────────────────────────────────────────
	if err := c.repo.InsertOrderEvents(ctx, eventsToFlush); err != nil {
		l.Errorw("failed to insert order events to ClickHouse",
			"error", err,
			"events_count", len(eventsToFlush),
		)
		// Возвращаем события обратно в буфер для повторной попытки.
		// Это retry "из коробки": при следующем flush попробуем снова.
		//
		// ВНИМАНИЕ: если ClickHouse недоступен постоянно, буфер будет расти
		// и в конце концов вызовет OOM. В production: ограничить размер буфера
		// и после N неудач коммитить offset (потеря данных лучше OOM).
		c.mu.Lock()
		c.buffer = append(eventsToFlush, c.buffer...)
		c.pendingMsgs = append(msgsToCommit, c.pendingMsgs...)
		c.mu.Unlock()
		return
	}

	// ── 2. INSERT order_item_events (только для order.placed) ───────────────
	//
	// Собираем товарные позиции из событий типа order.placed.
	// Другие типы (paid, shipped, ...) не содержат Items.
	var itemEvents []OrderItemEvent
	for _, e := range eventsToFlush {
		if e.EventType == domainEvents.OrderPlaced && len(e.Items) > 0 {
			for _, item := range e.Items {
				itemEvents = append(itemEvents, OrderItemEvent{
					EventID:     e.EventID,
					OccurredAt:  e.OccurredAt,
					OrderID:     e.OrderID,
					UserID:      e.UserID,
					ProductID:   item.ProductID,
					ProductName: item.ProductName,
					Quantity:    uint32(item.Quantity),
					PriceAmount: item.PriceAmount,
					Currency:    item.Currency,
				})
			}
		}
	}

	if len(itemEvents) > 0 {
		if err := c.repo.InsertOrderItemEvents(ctx, itemEvents); err != nil {
			// Не критично: order_events уже записаны.
			// Товарная детализация — дополнительные данные, не основные.
			// Логируем и продолжаем.
			l.Errorw("failed to insert order item events to ClickHouse",
				"error", err,
				"items_count", len(itemEvents),
			)
		}
	}

	// ── 3. Commit Kafka offsets ────────────────────────────────────────────
	//
	// Коммитим ТОЛЬКО последнее сообщение из batch.
	// Kafka offset — "я обработал всё до этого offset включительно".
	// Не нужно коммитить каждое сообщение отдельно.
	if len(msgsToCommit) > 0 {
		lastMsg := msgsToCommit[len(msgsToCommit)-1]
		if err := c.kafkaConsumer.Commit(ctx, lastMsg); err != nil {
			l.Errorw("failed to commit kafka offset",
				"error", err,
				"partition", lastMsg.Partition,
				"offset", lastMsg.Offset,
			)
			// Не фатально: при рестарте прочитаем те же события → дубликаты.
			// Лучше дубликаты, чем потеря данных.
		}
	}

	l.Infow("analytics batch flushed successfully",
		"order_events", len(eventsToFlush),
		"item_events", len(itemEvents),
	)
}

// Close освобождает ресурсы consumer'а.
//
// Вызывается при graceful shutdown ПОСЛЕ завершения Run().
// Run() уже выполнил finalFlush → буфер пуст → Close() просто закрывает Kafka reader.
func (c *Consumer) Close() error {
	return c.kafkaConsumer.Close()
}
