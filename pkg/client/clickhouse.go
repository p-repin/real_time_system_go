package client

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	"real_time_system/internal/config"
	"real_time_system/internal/logger"
)

// ClickHouse — обёртка над clickhouse.Conn с управлением жизненным циклом.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПАТТЕРН: ОБЁРТКА КЛИЕНТА (тот же что pkg/client/postgres.go)              │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Зачем обёртка, если можно использовать clickhouse.Conn напрямую?
//
// 1. Инкапсуляция конфигурации: NewClickHouse(ctx, cfg) — один вызов.
//    Без обёртки: копировать 20 строк конфига в каждом месте создания.
//
// 2. Управление lifecycle: Conn хранится в Server struct →
//    Close() вызывается при graceful shutdown.
//
// 3. InitSchema(): создание таблиц при старте — удобно для dev-окружения.
//    В production: миграции через отдельный инструмент (goose, migrate).
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ CLICKHOUSE vs POSTGRESQL: CONNECTION POOLING                              │
// └──────────────────────────────────────────────────────────────────────────┘
//
// PostgreSQL (pgxpool.Pool):
//   - Каждый запрос берёт connection из pool → выполняет → возвращает
//   - Pool нужен, потому что PG connection = процесс на сервере (дорого)
//
// ClickHouse (clickhouse.Conn):
//   - clickhouse-go/v2 сам управляет пулом (MaxOpenConns, MaxIdleConns)
//   - clickhouse.Open() возвращает Conn с встроенным pool'ом
//   - Один Conn на всё приложение — нормально (как pgxpool.Pool)
type ClickHouse struct {
	Conn clickhouse.Conn
	cfg  config.ClickHouseConfig
}

// NewClickHouse создаёт подключение к ClickHouse с native TCP протоколом.
//
// Возвращает ошибку если:
// - Не удалось установить TCP-соединение (ClickHouse недоступен)
// - Ping не прошёл (соединение есть, но сервер не отвечает)
// - Не удалось создать базу данных
func NewClickHouse(ctx context.Context, cfg *config.Config) (*ClickHouse, error) {
	l := logger.FromContext(ctx)

	addr := fmt.Sprintf("%s:%s", cfg.ClickHouseHost, cfg.ClickHousePort)

	l.Infow("connecting to ClickHouse",
		"addr", addr,
		"database", cfg.ClickHouseDatabase,
	)

	conn, err := clickhouse.Open(&clickhouse.Options{
		// Addr — список адресов серверов ClickHouse.
		// При нескольких серверах clickhouse-go балансирует нагрузку
		// и автоматически переключается при сбое (failover).
		// Для локальной разработки — один сервер.
		Addr: []string{addr},

		Auth: clickhouse.Auth{
			// Database подключения.
			// ВАЖНО: сначала подключаемся к "default", потом создаём "analytics".
			// Если указать несуществующую БД — connection fail.
			Database: "default",
			Username: cfg.ClickHouseUsername,
			Password: cfg.ClickHousePassword,
		},

		// DialTimeout — таймаут TCP handshake.
		// Отличается от query timeout: это только про установку соединения.
		DialTimeout: cfg.ClickHouseConnectTimeout,

		// ┌──────────────────────────────────────────────────────────────────┐
		// │ COMPRESSION: LZ4                                                   │
		// └──────────────────────────────────────────────────────────────────┘
		//
		// LZ4 — стандарт де-факто для ClickHouse:
		//   - Очень быстрая компрессия (3-5 GB/s)
		//   - Умеренная степень сжатия (2-3x)
		//   - Минимальный CPU overhead
		//
		// Альтернатива ZSTD: лучше сжимает (3-5x), но медленнее.
		// Для batch insert LZ4 — оптимальный выбор (throughput важнее ratio).
		//
		// Сжатие работает на уровне протокола: client сжимает → сервер распаковывает.
		// Уменьшает сетевой трафик, что критично при большом объёме данных.
		Compression: &clickhouse.Compression{
			Method: clickhouse.CompressionLZ4,
		},

		// ┌──────────────────────────────────────────────────────────────────┐
		// │ CONNECTION POOL                                                     │
		// └──────────────────────────────────────────────────────────────────┘
		//
		// MaxOpenConns — максимум одновременных соединений с ClickHouse.
		// Для analytics consumer достаточно 3:
		//   1 — для batch insert (основная работа)
		//   1 — для Ping / healthcheck
		//   1 — запас для параллельных запросов (Grafana SELECT)
		//
		// В отличие от PostgreSQL, ClickHouse легко держит сотни соединений
		// (каждое соединение — лёгкий поток, не процесс).
		MaxOpenConns: 3,
		MaxIdleConns: 3,

		// ConnMaxLifetime — время жизни одного соединения.
		// После этого времени соединение закрывается и создаётся новое.
		// Помогает при:
		// - Балансировке: новые соединения попадают на другой сервер (round-robin DNS)
		// - Утечках памяти в серверных обработчиках (редко, но бывает)
		ConnMaxLifetime: time.Hour,
	})
	if err != nil {
		return nil, fmt.Errorf("open clickhouse connection: %w", err)
	}

	// Ping проверяет, что сервер доступен и отвечает.
	// Без Ping: ошибка обнаружится только при первом запросе (позднее, менее понятно).
	ctx, cancel := context.WithTimeout(ctx, cfg.ClickHouseConnectTimeout)
	defer cancel()

	if err := conn.Ping(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("ping clickhouse: %w", err)
	}

	l.Infow("connected to ClickHouse",
		"addr", addr,
	)

	return &ClickHouse{
		Conn: conn,
		cfg:  cfg.ClickHouseConfig,
	}, nil
}

// InitSchema создаёт базу данных и таблицы, если они не существуют.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПОЧЕМУ DDL ПРИ СТАРТЕ, А НЕ ЧЕРЕЗ МИГРАЦИИ?                               │
// └──────────────────────────────────────────────────────────────────────────┘
//
// PostgreSQL:
//   Схема меняется часто (ALTER TABLE, новые колонки, индексы).
//   Миграции (goose, migrate) — стандарт, потому что нужен версионный контроль.
//
// ClickHouse (аналитика):
//   Таблицы создаются один раз и редко меняются.
//   Если нужно изменить — проще DROP + CREATE (данные можно перезалить из Kafka).
//   DDL при старте — идемпотентный (IF NOT EXISTS), простой, без зависимостей.
//
// В production с большим объёмом данных: миграции через Atlas или golang-migrate.
// Для нашего случая: InitSchema — достаточно.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ENGINE = MergeTree()                                                       │
// └──────────────────────────────────────────────────────────────────────────┘
//
// MergeTree — основной движок ClickHouse для аналитики:
//   - Данные хранятся отсортированными по ORDER BY ключу
//   - Автоматический merge фоновых "parts" (отсюда название)
//   - Поддержка партиционирования (PARTITION BY)
//   - Поддержка TTL (автоудаление старых данных)
//
// Альтернативы:
//   ReplacingMergeTree — дедупликация по ключу (для upsert-сценариев)
//   SummingMergeTree   — автосуммирование при merge (для счётчиков)
//   AggregatingMergeTree — pre-aggregation при merge
//
// Для append-only event stream: MergeTree — правильный выбор.
// Дубликаты обрабатываем на уровне запросов (COUNT(DISTINCT event_id)).
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ PARTITION BY toYYYYMM(event_date)                                         │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Партиционирование — это физическое разделение данных на "директории":
//   /var/lib/clickhouse/data/analytics/order_events/202601/
//   /var/lib/clickhouse/data/analytics/order_events/202602/
//   ...
//
// Зачем:
//   1. Retention: DROP PARTITION '202601' — мгновенное удаление за январь
//      (вместо DELETE WHERE date < '2026-02-01' — медленная пометка)
//   2. Pruning: WHERE event_date = '2026-03-15' → ClickHouse читает
//      только партицию 202603, пропуская все остальные
//
// ВАЖНО: Не перепартиционировать! 1 партиция/день при хранении 3 лет
// = 1095 партиций → каждый INSERT проверяет все → overhead.
// Месячное партиционирование — золотая середина.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ORDER BY: ВЫБОР КЛЮЧА СОРТИРОВКИ                                          │
// └──────────────────────────────────────────────────────────────────────────┘
//
// ORDER BY определяет, как данные физически расположены на диске.
// ClickHouse может пропускать гранулы (блоки по 8192 строк),
// если WHERE-условие совпадает с ORDER BY.
//
// order_events: ORDER BY (event_type, event_date, user_id)
//   Оптимизировано для:
//   ✅ WHERE event_type = 'order.placed' AND event_date BETWEEN ...
//   ✅ WHERE event_type = 'order.placed' AND event_date = '2026-03-15' AND user_id = ...
//   ❌ WHERE user_id = ... (не prefix → full scan)
//
// Правило: колонки в ORDER BY — от наименее кардинальных (event_type: ~5 значений)
// к наиболее кардинальным (user_id: миллионы). Это максимизирует сжатие
// и эффективность гранулярного пропуска.
func (c *ClickHouse) InitSchema(ctx context.Context) error {
	// Создаём отдельную БД для аналитики.
	// Отделяем от default, чтобы не смешивать с системными таблицами.
	createDB := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s", c.cfg.ClickHouseDatabase)
	if err := c.Conn.Exec(ctx, createDB); err != nil {
		return fmt.Errorf("create database %s: %w", c.cfg.ClickHouseDatabase, err)
	}

	// ── Таблица order_events: воронка заказов ──────────────────────────────
	//
	// Каждая строка — одно событие жизненного цикла заказа.
	// Один заказ = несколько строк (placed → paid → shipped → delivered).
	//
	// LowCardinality(String) — оптимизация для колонок с малым числом уникальных значений.
	// ClickHouse строит словарь: "order.placed" → 0, "order.paid" → 1, ...
	// Вместо хранения строки хранится число → экономия памяти и CPU.
	// Эффективно при < 10000 уникальных значений. У нас ~5 event_type → идеально.
	//
	// DateTime64(3, 'UTC'):
	//   - 3 = миллисекунды (достаточно для event sourcing)
	//   - 'UTC' — все timestamps в UTC (избегаем проблем с часовыми поясами)
	//   - DateTime (без 64) = секунды, DateTime64(6) = микросекунды
	//
	// total_amount Int64:
	//   Деньги в минимальных единицах (копейки). Не Decimal!
	//   ClickHouse Decimal — fixed-point, медленнее Int64 для агрегаций.
	//   Конвертация в рубли: total_amount / 100 (в Grafana или SQL-запросе).
	createOrderEvents := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.order_events (
			event_id     String,
			event_type   LowCardinality(String),
			event_date   Date,
			occurred_at  DateTime64(3, 'UTC'),
			order_id     String,
			user_id      String,
			status       LowCardinality(String),
			total_amount Int64,
			currency     LowCardinality(String),
			items_count  UInt16,
			inserted_at  DateTime64(3, 'UTC') DEFAULT now64(3)
		) ENGINE = MergeTree()
		PARTITION BY toYYYYMM(event_date)
		ORDER BY (event_type, event_date, user_id)
		TTL event_date + INTERVAL 12 MONTH
		SETTINGS index_granularity = 8192
	`, c.cfg.ClickHouseDatabase)
	// TTL event_date + INTERVAL 12 MONTH:
	//   Автоматическое удаление данных старше 12 месяцев.
	//   ClickHouse проверяет TTL при merge и удаляет просроченные строки.
	//   Это лучше чем cron + DELETE: TTL работает на уровне parts (быстро и эффективно).
	//
	// index_granularity = 8192:
	//   Количество строк в одной "грануле" (минимальная единица чтения).
	//   8192 — default ClickHouse, хорошо для большинства случаев.
	//   Меньше → точнее гранулярный пропуск, но больше overhead на индекс.
	//   Больше → меньше overhead, но читает больше лишних строк.

	if err := c.Conn.Exec(ctx, createOrderEvents); err != nil {
		return fmt.Errorf("create order_events table: %w", err)
	}

	// ── Таблица order_item_events: аналитика по товарам ────────────────────
	//
	// Денормализованная таблица: каждая строка = один товар в заказе.
	// Позволяет отвечать на вопросы:
	//   - Топ-10 продаваемых товаров за период
	//   - Revenue по товару/категории
	//   - Средний чек по количеству товаров
	//
	// ПОЧЕМУ ОТДЕЛЬНАЯ ТАБЛИЦА, А НЕ Array(Tuple(...)) В order_events?
	//
	//   Array(Tuple(product_id, quantity, price)):
	//   + Все данные в одной таблице
	//   - GROUP BY по элементам массива медленнее (arrayJoin)
	//   - Неудобно для Grafana (сложные запросы)
	//
	//   Отдельная таблица:
	//   + Простые SQL-запросы: SELECT product_name, SUM(quantity) GROUP BY product_name
	//   + Нативная индексация по product_id (ORDER BY)
	//   + Grafana подключается напрямую
	//   - Дублирование order_id, user_id (но storage в ClickHouse дешёвый)
	//
	// В аналитике денормализация — норма. ClickHouse оптимизирован для wide tables.
	createOrderItemEvents := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.order_item_events (
			event_id      String,
			event_date    Date,
			occurred_at   DateTime64(3, 'UTC'),
			order_id      String,
			user_id       String,
			product_id    String,
			product_name  String,
			quantity      UInt32,
			price_amount  Int64,
			currency      LowCardinality(String),
			inserted_at   DateTime64(3, 'UTC') DEFAULT now64(3)
		) ENGINE = MergeTree()
		PARTITION BY toYYYYMM(event_date)
		ORDER BY (product_id, event_date)
		TTL event_date + INTERVAL 12 MONTH
		SETTINGS index_granularity = 8192
	`, c.cfg.ClickHouseDatabase)
	// ORDER BY (product_id, event_date):
	//   Оптимизировано для "статистика по товару за период":
	//   WHERE product_id = '...' AND event_date BETWEEN '2026-01-01' AND '2026-03-31'
	//   ClickHouse быстро найдёт нужные гранулы по product_id, затем отфильтрует по дате.

	if err := c.Conn.Exec(ctx, createOrderItemEvents); err != nil {
		return fmt.Errorf("create order_item_events table: %w", err)
	}

	return nil
}

// Close закрывает соединение с ClickHouse.
//
// Вызывать при graceful shutdown ПОСЛЕ закрытия analytics consumer.
// Порядок важен: consumer.Close() (flush буфера) → clickhouse.Close() (закрыть conn).
// Если закрыть conn раньше → consumer.flush() получит "connection closed".
func (c *ClickHouse) Close() error {
	return c.Conn.Close()
}
