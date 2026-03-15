package config

import "time"

// ClickHouseConfig — конфигурация подключения к ClickHouse.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ЗАЧЕМ CLICKHOUSE В E-COM ПРОЕКТЕ?                                         │
// └──────────────────────────────────────────────────────────────────────────┘
//
// PostgreSQL (OLTP) оптимизирован для транзакций:
//   INSERT/UPDATE одной строки за ~1ms, ACID, row-level locks.
//
// ClickHouse (OLAP) оптимизирован для аналитики:
//   SELECT count(*), avg(total) FROM orders WHERE date BETWEEN ... GROUP BY status
//   На 100М строк: PostgreSQL ~30s, ClickHouse ~0.1s.
//
// Причина: ClickHouse хранит данные по колонкам (columnar storage).
// Запрос "средний чек" читает ТОЛЬКО колонку total_amount (1 колонка из 10),
// а PostgreSQL читает все 10 колонок целой строки (row-based storage).
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ NATIVE PROTOCOL (TCP :9000) vs HTTP (:8123)                               │
// └──────────────────────────────────────────────────────────────────────────┘
//
// ClickHouse поддерживает два протокола:
//
//   HTTP (8123):
//   + Проще для отладки (curl, браузер)
//   + Работает через прокси/балансировщики (HTTP-совместимые)
//   - Текстовый формат → overhead на сериализацию/десериализацию
//   - Нет стриминга результатов (всё в одном HTTP response)
//
//   Native TCP (9000):
//   + Бинарный формат → меньше overhead для batch insert
//   + Стриминг данных (не нужно держать всё в памяти)
//   + Compression встроена в протокол (LZ4)
//   - Сложнее дебажить (не откроешь в браузере)
//
// Для Go-клиента с batch insert: Native TCP — правильный выбор.
// Для ad-hoc запросов через UI: HTTP (встроенный play UI на :8123).
type ClickHouseConfig struct {
	// Host — адрес ClickHouse сервера.
	// Локально: localhost. В Docker: clickhouse (hostname контейнера).
	ClickHouseHost string `env:"CLICKHOUSE_HOST" envDefault:"localhost"`

	// Port — порт Native TCP протокола (не HTTP!).
	// 9000 — стандартный порт для native protocol.
	// 8123 — HTTP (не используем для batch insert).
	ClickHousePort string `env:"CLICKHOUSE_PORT" envDefault:"9000"`

	// Database — имя базы данных для аналитики.
	// Отдельная БД "analytics" — логическое разделение от OLTP-данных.
	// В ClickHouse базы данных — это namespace'ы (как schema в PostgreSQL).
	ClickHouseDatabase string `env:"CLICKHOUSE_DATABASE" envDefault:"analytics"`

	// Username / Password — аутентификация.
	// "default" — встроенный суперпользователь ClickHouse (как "postgres" в PG).
	// В production: создать отдельного пользователя с ограниченными правами
	// (только INSERT в analytics.*, только SELECT для Grafana).
	ClickHouseUsername string `env:"CLICKHOUSE_USERNAME" envDefault:"default"`
	ClickHousePassword string `env:"CLICKHOUSE_PASSWORD" envDefault:""`

	// ConnectTimeout — таймаут установки TCP-соединения.
	// Если ClickHouse не ответил за 10s — скорее всего, он недоступен.
	// Без таймаута: горутина зависнет навсегда при сетевых проблемах.
	ClickHouseConnectTimeout time.Duration `env:"CLICKHOUSE_CONNECT_TIMEOUT" envDefault:"10s"`

	// BatchSize — количество событий, накапливаемых перед отправкой в ClickHouse.
	//
	// ┌──────────────────────────────────────────────────────────────────────┐
	// │ ПОЧЕМУ BATCH, А НЕ ПО ОДНОМУ?                                         │
	// └──────────────────────────────────────────────────────────────────────┘
	//
	// ClickHouse оптимизирован для БОЛЬШИХ вставок:
	//   1 INSERT с 1000 строк  → ~5ms  (один round-trip, один merge)
	//   1000 INSERT по 1 строке → ~5s   (1000 round-trip'ов, 1000 merge'ей)
	//
	// Каждый INSERT создаёт новый "part" на диске.
	// Слишком много мелких parts → "Too many parts" ошибка.
	// ClickHouse рекомендует: не более 1 INSERT в секунду на таблицу.
	//
	// 100 — разумный default для нашего трафика.
	// Highload (>10k events/sec): увеличить до 1000-10000.
	ClickHouseBatchSize int `env:"CLICKHOUSE_BATCH_SIZE" envDefault:"100"`

	// FlushInterval — максимальное время ожидания перед принудительным flush.
	//
	// Проблема: при низком трафике (1 event/min) буфер никогда не наберёт
	// BatchSize=100. Без таймера данные застрянут в буфере на часы.
	//
	// Решение: flush по таймеру, даже если буфер не полон.
	// 5s — данные появятся в ClickHouse максимум через 5 секунд.
	// Для real-time дашбордов: уменьшить до 1s.
	// Для batch аналитики (отчёты за день): увеличить до 30s-60s.
	ClickHouseFlushInterval time.Duration `env:"CLICKHOUSE_FLUSH_INTERVAL" envDefault:"5s"`
}
