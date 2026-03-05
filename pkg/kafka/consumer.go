package kafka

import (
	"context"
	"fmt"

	"github.com/segmentio/kafka-go"
)

// ConsumedMessage — сообщение, полученное из Kafka.
//
// Обёртка над kafka.Message, скрывающая детали библиотеки.
// Аналогично Message для Producer — изолируем бизнес-код от kafka-go.
type ConsumedMessage struct {
	// Key — ключ сообщения (для нас это OrderID в байтах).
	Key []byte

	// Value — полезная нагрузка (JSON с OrderEvent).
	Value []byte

	// Partition и Offset — позиция сообщения в Kafka.
	// Нужны для at-least-once семантики: после успешной обработки
	// консьюмер должен сделать Commit с этим offset'ом.
	// Если не сделать Commit → после перезапуска сообщение придёт снова.
	Partition int
	Offset    int64
}

// Consumer — читает сообщения из одного топика/группы.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ CONSUMER GROUP: КАК ЭТО РАБОТАЕТ?                                          │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Consumer Group — группа консьюмеров, совместно читающих топик.
// Kafka распределяет партиции между членами группы:
//
//   Топик "orders" с 3 партициями, группа "notification-service":
//   ┌──────────────────────────────────────────────────┐
//   │  Partition 0 → Consumer A                         │
//   │  Partition 1 → Consumer B                         │
//   │  Partition 2 → Consumer C                         │
//   └──────────────────────────────────────────────────┘
//
//   Если Consumer B упал → Kafka rebalance → B's partition → C или A.
//   Если добавить Consumer D → rebalance → перераспределение партиций.
//
// Каждое сообщение обрабатывается РОВНО ОДНИМ консьюмером в группе.
// Разные группы ("notification-service", "analytics-service") читают НЕЗАВИСИМО.
// Т.е. одно событие order.placed могут обработать оба сервиса — каждый в своей группе.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ GROUP ID: ПОЧЕМУ ВАЖНО ВЫБРАТЬ ПРАВИЛЬНЫЙ?                                 │
// └──────────────────────────────────────────────────────────────────────────┘
//
// GroupID определяет логическую группу потребителей.
// Kafka хранит offset (позицию чтения) для каждой группы отдельно.
//
// ❌ Один GroupID для разных сервисов:
//   notification-service и analytics-service с одним GroupID "my-group"
//   → они "конкурируют" за партиции → каждое событие обработает ТОЛЬКО ОДИН из них
//   → notification или analytics получат событие, не оба
//
// ✅ Разные GroupID:
//   "notification-service" и "analytics-service" → каждый читает все события
type Consumer struct {
	reader *kafka.Reader
}

// NewConsumer создаёт Consumer для чтения из топика.
//
// topic   — откуда читать
// groupID — имя группы консьюмеров (уникальное для каждого сервиса-потребителя)
// brokers — список адресов Kafka-брокеров
func NewConsumer(brokers []string, topic, groupID string) *Consumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		// Brokers — bootstrap-серверы для discovery кластера.
		Brokers: brokers,

		// Topic — топик для чтения.
		Topic: topic,

		// GroupID — имя consumer group.
		// Kafka использует это для хранения committed offsets.
		// Если запустить два экземпляра с одним GroupID → Kafka rebalance →
		// разные партиции читают разные экземпляры.
		GroupID: groupID,

		// MinBytes / MaxBytes — батчинг при чтении.
		// Reader ждёт пока накопится MinBytes данных ИЛИ не истечёт MaxWait.
		// MaxBytes — максимальный размер одного fetch запроса.
		//
		// MinBytes: 1 — не ждём батча, читаем сразу как есть (минимальная latency).
		// Для real-time уведомлений latency важнее throughput → 1.
		// Для batch аналитики → 1024*1024 (меньше сетевых запросов, выше throughput).
		MinBytes: 1,
		MaxBytes: 10e6, // 10MB максимум за один fetch

		// CommitInterval: 0 — явный manual commit (AutoCommit отключён).
		//
		// ┌──────────────────────────────────────────────────────────────────────┐
		// │ MANUAL vs AUTO COMMIT: ПОЧЕМУ MANUAL?                                 │
		// └──────────────────────────────────────────────────────────────────────┘
		//
		// AutoCommit (CommitInterval > 0): offset фиксируется автоматически каждые N секунд.
		//   Сценарий проблемы:
		//   1. Получили сообщение offset=100
		//   2. AutoCommit зафиксировал offset=100 (через 1 секунду)
		//   3. Обработчик упал при обработке
		//   4. При рестарте читаем с offset=101 — сообщение 100 ПОТЕРЯНО
		//
		// Manual Commit: сначала обрабатываем, потом фиксируем.
		//   1. Получили сообщение offset=100
		//   2. Успешно обработали (отправили уведомление)
		//   3. CommitMessages(ctx, msg) — фиксируем offset=100
		//   4. При падении до шага 3 → рестарт → снова читаем offset=100
		//   Это at-least-once: сообщение обработается минимум один раз.
		//
		// Единственный риск manual commit: при падении ПОСЛЕ обработки, НО ДО commit
		// → сообщение обработается дважды. Решение: идемпотентный обработчик.
		CommitInterval: 0,
	})

	return &Consumer{reader: reader}
}

// ReadMessage читает следующее сообщение из Kafka.
//
// Блокирующий вызов: ждёт пока придёт новое сообщение.
// Возвращает ошибку если контекст отменён (ctx.Done()) или произошла ошибка I/O.
//
// ВАЖНО: после успешной обработки нужно вызвать Commit(ctx, msg)
// для фиксации offset (at-least-once семантика).
func (c *Consumer) ReadMessage(ctx context.Context) (ConsumedMessage, error) {
	msg, err := c.reader.FetchMessage(ctx)
	if err != nil {
		return ConsumedMessage{}, fmt.Errorf("fetch message: %w", err)
	}

	return ConsumedMessage{
		Key:       msg.Key,
		Value:     msg.Value,
		Partition: msg.Partition,
		Offset:    msg.Offset,
	}, nil
}

// Commit фиксирует обработку сообщения.
//
// Вызывать ТОЛЬКО после успешной обработки сообщения.
// При ошибке обработки — НЕ вызывать Commit, тогда при рестарте
// это сообщение придёт снова.
func (c *Consumer) Commit(ctx context.Context, msg ConsumedMessage) error {
	return c.reader.CommitMessages(ctx, kafka.Message{
		Partition: msg.Partition,
		Offset:    msg.Offset,
	})
}

// Close завершает работу Consumer'а.
//
// Освобождает соединения с брокером.
// Незафиксированные offset'ы будут перечитаны другим консьюмером при rebalance.
func (c *Consumer) Close() error {
	return c.reader.Close()
}
