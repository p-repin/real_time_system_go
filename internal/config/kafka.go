package config

import "time"

// KafkaConfig — конфигурация Kafka producer и consumer.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПОЧЕМУ КОНФИГ В ОТДЕЛЬНОМ ФАЙЛЕ?                                           │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Следуем тому же паттерну, что и PostgresConfig / HTTPConfig:
// каждый внешний ресурс — отдельный struct в отдельном файле.
// Config (config.go) просто встраивает все sub-configs через embedding.
// Это позволяет добавлять новые ресурсы, не трогая config.go.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПЕРЕМЕННЫЕ ОКРУЖЕНИЯ                                                       │
// └──────────────────────────────────────────────────────────────────────────┘
//
// KAFKA_BROKERS        — список брокеров через запятую: "localhost:9094"
//                        В Docker-compose: "kafka:9092"
//                        Несколько брокеров: "broker1:9092,broker2:9092,broker3:9092"
//
// KAFKA_TOPIC_ORDERS   — название топика для событий заказов
//
// KAFKA_WRITE_TIMEOUT  — таймаут записи одного сообщения.
//                        Если брокер недоступен и таймаут истёк → ошибка.
//                        Без таймаута: publisher заблокируется навсегда!
//
// KAFKA_REQUIRED_ACKS  — сколько брокеров должны подтвердить запись:
//                          0 = fire-and-forget (fastest, no guarantee)
//                          1 = только leader (default, может потерять при failover)
//                         -1 = все in-sync replicas (slowest, максимальная надёжность)
//                        Для at-least-once delivery используем -1 (RequireAll).
type KafkaConfig struct {
	// Brokers — список адресов Kafka-брокеров через запятую.
	// ВАЖНО: при локальной разработке (go run) используй localhost:9094.
	// В Docker-compose app-контейнер использует kafka:9092.
	// Если не указать правильный адрес → "dial tcp: connection refused"
	Brokers []string `env:"KAFKA_BROKERS" envSeparator:"," envDefault:"localhost:9094"`

	// TopicOrders — топик для событий жизненного цикла заказов.
	// Все OrderEvent'ы (placed, paid, shipped, ...) идут в один топик.
	// Почему один топик? Консьюмер читает все события заказа из одного места.
	// Можно разбить на "order-lifecycle" и "order-payments" — но это усложнение.
	TopicOrders string `env:"KAFKA_TOPIC_ORDERS" envDefault:"orders"`

	// WriteTimeout — таймаут на запись одного сообщения в Kafka.
	// Если брокер не ответил за это время → WriteMessages вернёт ошибку.
	// Слишком маленький: ложные ошибки при нагрузке на брокер.
	// Слишком большой: HTTP-обработчик долго ждёт → плохой UX.
	// 5 секунд — разумный компромисс для production.
	WriteTimeout time.Duration `env:"KAFKA_WRITE_TIMEOUT" envDefault:"5s"`

	// RequiredAcks — уровень подтверждения от брокеров:
	//   0 — не ждём подтверждения (максимальная скорость, возможна потеря)
	//   1 — ждём подтверждения от leader-брокера (баланс скорости и надёжности)
	//  -1 — ждём подтверждения от всех in-sync replica (максимальная надёжность)
	//
	// Для нашего учебного проекта используем 1 (leader ack).
	// В финансовых системах — обязательно -1.
	RequiredAcks int `env:"KAFKA_REQUIRED_ACKS" envDefault:"1"`
}
