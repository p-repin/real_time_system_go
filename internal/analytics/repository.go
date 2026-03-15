package analytics

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ANALYTICS REPOSITORY: BATCH INSERT В CLICKHOUSE                           │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Этот репозиторий отвечает ТОЛЬКО за запись аналитических данных в ClickHouse.
// Он НЕ читает данные — чтение происходит через Grafana (SQL → ClickHouse datasource).
//
// Почему "repository", а не "writer" или "sink"?
// Следуем паттерну проекта: internal/repository/postgres/ содержит CRUD-репозитории.
// Analytics repository — тот же паттерн, но write-only и для другого хранилища.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ BATCH INSERT: КРИТИЧЕСКИ ВАЖНО ДЛЯ CLICKHOUSE                            │
// └──────────────────────────────────────────────────────────────────────────┘
//
// ClickHouse хранит данные в "parts" — отсортированных блоках на диске.
// Каждый INSERT создаёт новый part. Фоновый процесс merge объединяет parts.
//
// Проблема "Too many parts":
//   Если делать 100 INSERT/сек по 1 строке → 100 parts/сек.
//   Merge не успевает → parts копятся → ClickHouse отказывается принимать INSERT.
//   Ошибка: "TOO_MANY_PARTS: Merges are processing significantly slower than inserts"
//
// Решение: batch insert (один INSERT с сотнями строк = один part).
//   Рекомендация ClickHouse: не более 1 INSERT в секунду на таблицу.
//   При FlushInterval=5s и BatchSize=100 мы делаем ≤1 INSERT / 5s — безопасно.

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	"real_time_system/domain/events"
	"real_time_system/pkg/client"
)

// Repository — write-only репозиторий для аналитических данных в ClickHouse.
type Repository struct {
	conn     clickhouse.Conn
	database string
}

// NewRepository создаёт analytics repository.
//
// Принимает *client.ClickHouse (обёртку), а не голый clickhouse.Conn,
// чтобы гарантировать что Conn инициализирован через NewClickHouse
// (с Ping, compression, connection pool).
func NewRepository(ch *client.ClickHouse, database string) *Repository {
	return &Repository{
		conn:     ch.Conn,
		database: database,
	}
}

// InsertOrderEvents вставляет пакет событий заказов в ClickHouse.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ КАК РАБОТАЕТ PrepareBatch + Append + Send                                 │
// └──────────────────────────────────────────────────────────────────────────┘
//
// 1. PrepareBatch(ctx, "INSERT INTO ...") — готовит пустой batch.
//    Открывает соединение, отправляет на сервер заголовок таблицы.
//    Данные ещё НЕ отправлены.
//
// 2. batch.Append(col1, col2, ...) — добавляет строку в буфер клиента.
//    Данные копятся в памяти Go-процесса. Нет сетевых вызовов.
//    Порядок аргументов ДОЛЖЕН совпадать с порядком колонок в CREATE TABLE.
//
// 3. batch.Send() — отправляет весь буфер одним TCP-пакетом.
//    Сервер получает все строки разом и создаёт один part.
//    Один round-trip вместо N → на порядки быстрее.
//
// АНАЛОГИЯ:
//   PostgreSQL: COPY ... FROM STDIN (bulk insert)
//   Kafka: producer batch (несколько сообщений в одном request)
//   HTTP: PUT с массивом вместо N отдельных POST
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ЧТО ЕСЛИ batch.Send() УПАДЁТ?                                             │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Весь batch отклоняется (атомарность: все или ничего).
// Данные не записаны → Kafka offset не коммитится →
// при следующем чтении consumer получит те же события →
// batch формируется заново и отправляется снова.
// Это at-least-once: возможны дубликаты, но данные не теряются.
func (r *Repository) InsertOrderEvents(ctx context.Context, orderEvents []events.OrderEvent) error {
	if len(orderEvents) == 0 {
		return nil
	}

	query := fmt.Sprintf("INSERT INTO %s.order_events", r.database)
	batch, err := r.conn.PrepareBatch(ctx, query)
	if err != nil {
		return fmt.Errorf("prepare batch for order_events: %w", err)
	}

	for _, e := range orderEvents {
		// event_date — дата без времени для партиционирования.
		// Truncate до начала дня: 2026-03-15 14:30:00 → 2026-03-15.
		// ClickHouse Date — это Date (без времени), Go time.Time → Date конвертация
		// происходит автоматически, но для ясности берём UTC.
		eventDate := e.OccurredAt.UTC().Truncate(24 * time.Hour)

		// Порядок аргументов СТРОГО соответствует порядку колонок в CREATE TABLE:
		// event_id, event_type, event_date, occurred_at, order_id, user_id,
		// status, total_amount, currency, items_count
		//
		// inserted_at — DEFAULT now64(3), не передаём (ClickHouse заполнит сам).
		err := batch.Append(
			e.EventID,
			string(e.EventType),
			eventDate,
			e.OccurredAt.UTC(),
			e.OrderID,
			e.UserID,
			e.Status,
			e.TotalAmount,
			e.Currency,
			e.ItemsCount,
		)
		if err != nil {
			// Abort batch если одна строка невалидна.
			// Можно было бы пропустить и продолжить, но:
			// 1. Append-ошибка = баг в нашем коде (неверный тип данных)
			// 2. Лучше узнать сразу, чем потерять строки молча
			_ = batch.Abort()
			return fmt.Errorf("append order event %s to batch: %w", e.EventID, err)
		}
	}

	if err := batch.Send(); err != nil {
		return fmt.Errorf("send order_events batch (%d events): %w", len(orderEvents), err)
	}

	return nil
}

// InsertOrderItemEvents вставляет пакет детализации по товарам.
//
// Вызывается ВМЕСТЕ с InsertOrderEvents для события order.placed.
// Для других событий (paid, shipped, ...) товарная детализация не нужна —
// заказ уже зафиксирован, меняется только его статус.
func (r *Repository) InsertOrderItemEvents(ctx context.Context, items []OrderItemEvent) error {
	if len(items) == 0 {
		return nil
	}

	query := fmt.Sprintf("INSERT INTO %s.order_item_events", r.database)
	batch, err := r.conn.PrepareBatch(ctx, query)
	if err != nil {
		return fmt.Errorf("prepare batch for order_item_events: %w", err)
	}

	for _, item := range items {
		eventDate := item.OccurredAt.UTC().Truncate(24 * time.Hour)

		err := batch.Append(
			item.EventID,
			eventDate,
			item.OccurredAt.UTC(),
			item.OrderID,
			item.UserID,
			item.ProductID,
			item.ProductName,
			item.Quantity,
			item.PriceAmount,
			item.Currency,
		)
		if err != nil {
			_ = batch.Abort()
			return fmt.Errorf("append order item event to batch: %w", err)
		}
	}

	if err := batch.Send(); err != nil {
		return fmt.Errorf("send order_item_events batch (%d items): %w", len(items), err)
	}

	return nil
}

// OrderItemEvent — аналитическое событие по одному товару в заказе.
//
// Отдельная структура (не domain/events.OrderEvent) потому что:
// 1. OrderEvent — доменное событие (контракт Kafka), не должно содержать аналитические поля
// 2. OrderItemEvent — аналитическая проекция (денормализация для ClickHouse)
// 3. Разные жизненные циклы: OrderEvent расширяется редко, OrderItemEvent — по потребностям аналитики
type OrderItemEvent struct {
	EventID     string
	OccurredAt  time.Time
	OrderID     string
	UserID      string
	ProductID   string
	ProductName string
	Quantity    uint32
	PriceAmount int64
	Currency    string
}
