package client

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier — общий интерфейс для *pgxpool.Pool и pgx.Tx.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ЗАЧЕМ НУЖЕН ЭТОТ ИНТЕРФЕЙС?                                               │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Репозитории работают с БД через методы Exec, Query, QueryRow.
// Эти методы есть и у *pgxpool.Pool (обычные запросы), и у pgx.Tx (транзакции).
//
// Без интерфейса:
//
//	type OrderRepo struct { db *pgxpool.Pool }  // жёстко привязан к Pool
//	// Нельзя передать pgx.Tx для работы внутри транзакции
//
// С интерфейсом:
//
//	type OrderRepo struct { db Querier }  // принимает и Pool, и Tx
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПРИМЕР ИСПОЛЬЗОВАНИЯ В ТРАНЗАКЦИИ                                         │
// └──────────────────────────────────────────────────────────────────────────┘
//
//	func (s *OrderService) PlaceOrder(ctx context.Context, userID entity.UserID) error {
//	    tx, err := s.db.Begin(ctx)
//	    if err != nil {
//	        return err
//	    }
//	    defer tx.Rollback(ctx)
//
//	    // Создаём репозитории с tx — все операции в одной транзакции
//	    orderRepo := postgres.NewOrderRepository(tx)
//	    productRepo := postgres.NewProductRepository(tx)
//
//	    orderRepo.Create(ctx, order)         // использует tx.Exec()
//	    productRepo.DecrementStock(ctx, ...) // использует ту же tx
//
//	    return tx.Commit(ctx)
//	}
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ КАКИЕ ТИПЫ РЕАЛИЗУЮТ QUERIER                                              │
// └──────────────────────────────────────────────────────────────────────────┘
//
//   - *pgxpool.Pool — пул соединений (обычная работа)
//   - pgx.Tx — транзакция (атомарные операции)
//   - *Postgres — наша обёртка над Pool (тоже реализует, т.к. встраивает Pool)
//
// Go автоматически считает тип реализующим интерфейс, если у него есть все методы.
// Явная декларация "implements" не нужна.
type Querier interface {
	// Exec выполняет SQL без возврата строк (INSERT, UPDATE, DELETE).
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)

	// Query выполняет SQL и возвращает множество строк.
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)

	// QueryRow выполняет SQL и возвращает одну строку.
	// Ошибки откладываются до вызова Scan().
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Compile-time check: проверяем, что *Postgres реализует Querier.
// Если методы изменятся — получим ошибку компиляции здесь, а не в runtime.
var _ Querier = (*Postgres)(nil)
