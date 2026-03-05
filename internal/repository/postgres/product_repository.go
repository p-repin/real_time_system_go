package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"real_time_system/domain"
	"real_time_system/domain/entity"
	"real_time_system/domain/repository"
	"real_time_system/domain/value_objects"
	"real_time_system/internal/logger"
	"real_time_system/pkg/client"
)

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ COMPILE-TIME CHECK                                                        │
// └──────────────────────────────────────────────────────────────────────────┘
//
// БЫЛО: var _ repository.ProductRepository = ProductRepositoryPg(nil) ❌
//
// ПОЧЕМУ ОШИБКА:
// ProductRepositoryPg(nil) — это type conversion, но nil не имеет типа pointer.
// Компилятор не знает, что мы хотим проверить *ProductRepositoryPg.
//
// ПРАВИЛЬНО: (*ProductRepositoryPg)(nil)
// Мы создаём nil-pointer типа *ProductRepositoryPg и проверяем, что он
// реализует интерфейс repository.ProductRepository.
//
// Если забудем реализовать хотя бы один метод → ошибка компиляции.
var _ repository.ProductRepository = (*ProductRepositoryPg)(nil)

type ProductRepositoryPg struct {
	db client.Querier
}

// NewProductRepository — конструктор для dependency injection.
//
// Принимает Querier — может быть *pgxpool.Pool (обычная работа) или pgx.Tx (транзакция).
// Это позволяет использовать репозиторий как для отдельных запросов, так и внутри транзакций.
func NewProductRepository(db client.Querier) *ProductRepositoryPg {
	return &ProductRepositoryPg{db: db}
}

// Create создаёт новый продукт в БД.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ PRODUCTION ПАТТЕРН: РАБОТА С MONEY В БД                                    │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Money в domain — это struct:
//   type Money struct { Amount int64; Currency Currency }
//
// В БД храним как ДВА поля:
//   price_amount INTEGER   — значение в минимальных единицах (копейки)
//   price_currency VARCHAR — код валюты ('RUB', 'USD')
//
// ПОЧЕМУ НЕ ОДИН JSON-FIELD:
// 1. Поиск по цене: WHERE price_amount BETWEEN 1000 AND 5000
//    С JSON пришлось бы делать: WHERE (price::json->>'amount')::int BETWEEN ...
//    → медленнее, нельзя использовать индекс
//
// 2. Агрегация: SUM(price_amount) для расчёта выручки
//    С JSON нужны сложные JSON-функции
//
// 3. Индексы: CREATE INDEX ON products(price_amount)
//    С JSON индекс на вложенное поле сложнее
//
// АЛЬТЕРНАТИВА (когда JSON лучше):
// Если Money содержит >3 полей (Amount, Currency, ExchangeRate, Tax) →
// JSON может быть удобнее для хранения.
func (r *ProductRepositoryPg) Create(ctx context.Context, product *entity.Product) error {
	l := logger.FromContext(ctx)

	// БЫЛО: VALUES ($1, &2, $3, ..., 9) ❌
	// ОШИБКИ:
	// - &2 вместо $2 → SQL syntax error
	// - 9 вместо $9 → вставится литерал "9", а не значение deleted_at
	//
	// PostgreSQL использует $1, $2, ... для placeholders (не ?, как в MySQL)
	q := `
		INSERT INTO products (id, name, description, price_amount, price_currency, stock, created_at, updated_at, deleted_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	_, err := r.db.Exec(ctx, q,
		product.ID,             // sql.Valuer → автоматически вызовется ProductID.Value()
		product.Name,
		product.Description,
		product.Price.Amount,   // int64 → INTEGER
		product.Price.Currency, // Currency (string) → VARCHAR
		product.Stock,
		product.CreatedAt,
		product.UpdatedAt,
		product.DeletedAt, // nil для активного продукта → NULL в БД
	)

	if err != nil {
		var pgErr *pgconn.PgError

		if errors.As(err, &pgErr) {
			// 23505 = unique_violation (name уже существует)
			if pgErr.Code == "23505" {
				// НЕ логируем — это нормальный бизнес-сценарий
				// Кто-то пытается создать товар с существующим именем
				return domain.NewConflictError("product with this name already exists")
			}
		}

		// Любая другая ошибка — логируем с контекстом
		l.Errorw("failed to create product", "error", err, "product_id", product.ID)
		return domain.NewInternalError("failed to create product", err)
	}

	// Успех — логируем важное бизнес-событие
	l.Infow("product created", "product_id", product.ID, "name", product.Name)

	return nil
}

// FindByID возвращает продукт по ID.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ КРИТИЧЕСКАЯ ОШИБКА: ПРОПУЩЕН ПАРАМЕТР В SCAN                               │
// └──────────────────────────────────────────────────────────────────────────┘
//
// БЫЛО:
// SELECT возвращает 9 колонок (id, name, ..., stock, created_at, ...)
// Scan принимает 8 параметров (пропущен &product.Stock)
//
// ПОСЛЕДСТВИЕ:
// panic: sql: expected 8 destination arguments in Scan, not 9
//
// КАК ИЗБЕЖАТЬ:
// 1. Считай колонки в SELECT и параметры в Scan — должно совпадать
// 2. Используй code generation (sqlc, sqlboiler) — генерируют код автоматически
// 3. Пиши integration тесты — они поймают это сразу
func (r *ProductRepositoryPg) FindByID(ctx context.Context, id entity.ProductID) (*entity.Product, error) {
	l := logger.FromContext(ctx)

	q := `
		SELECT id, name, description, price_amount, price_currency, stock, created_at, updated_at, deleted_at
		FROM products
		WHERE id = $1 AND deleted_at IS NULL
	`

	var product entity.Product

	// ВАЖНО: порядок Scan должен соответствовать порядку SELECT
	// БЫЛО: пропущен &product.Stock между price_currency и created_at
	err := r.db.QueryRow(ctx, q, id).Scan(
		&product.ID,             // sql.Scanner → вызовется ProductID.Scan()
		&product.Name,
		&product.Description,
		&product.Price.Amount,   // int64 (копейки)
		&product.Price.Currency, // string ('RUB')
		&product.Stock,          // ✅ ИСПРАВЛЕНО: добавлен пропущенный параметр
		&product.CreatedAt,
		&product.UpdatedAt,
		&product.DeletedAt, // может быть NULL → nil
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// НЕ логируем — "not found" это нормальный сценарий
			return nil, domain.NewNotFoundError("product")
		}

		l.Errorw("failed to find product by id", "error", err, "product_id", id)
		return nil, domain.NewInternalError("failed to find product", err)
	}

	return &product, nil
}

// Update обновляет данные продукта.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ КРИТИЧЕСКАЯ ОШИБКА: ОБНОВЛЕНИЕ created_at                                  │
// └──────────────────────────────────────────────────────────────────────────┘
//
// БЫЛО: UPDATE products SET ..., created_at = $6, ... ❌
//
// ПОЧЕМУ ЭТО НЕПРАВИЛЬНО:
// created_at — это аудитная метка, которая устанавливается ОДИН РАЗ при создании
// и НИКОГДА не меняется. Это timestamp "когда запись появилась в системе".
//
// ЕСЛИ ОБНОВЛЯТЬ created_at:
// 1. Теряем информацию о реальном времени создания
// 2. Нарушаем compliance (GDPR требует знать, когда данные появились)
// 3. Ломаем аудит (бухгалтерия не сойдётся с реальными датами)
// 4. Невозможно построить отчёты "товары, добавленные в январе"
//
// ПРАВИЛО: обновляем только бизнес-поля + updated_at.
func (r *ProductRepositoryPg) Update(ctx context.Context, product *entity.Product) error {
	l := logger.FromContext(ctx)

	// ✅ ИСПРАВЛЕНО:
	// - Убрали created_at и deleted_at из UPDATE
	// - Убрали лишний параметр product.ID из конца
	q := `
		UPDATE products
		SET name = $1, description = $2, price_amount = $3, price_currency = $4, stock = $5, updated_at = $6
		WHERE id = $7 AND deleted_at IS NULL
	`

	// БЫЛО: передавали 8 параметров, а SQL ожидал 9 (пропущен product.ID)
	// ✅ ИСПРАВЛЕНО: 6 полей для SET + 1 для WHERE = 7 параметров
	result, err := r.db.Exec(ctx, q,
		product.Name,
		product.Description,
		product.Price.Amount,
		product.Price.Currency,
		product.Stock,
		product.UpdatedAt, // обновляется через time.Now() в сервисе
		product.ID,        // для WHERE id = $7
	)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			// Проверяем unique violation для name
			if pgErr.Code == "23505" {
				return domain.NewConflictError("product with this name already exists")
			}
		}

		l.Errorw("failed to update product", "error", err, "product_id", product.ID)
		return domain.NewInternalError("failed to update product", err)
	}

	// Проверяем, был ли обновлён хотя бы один ряд
	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		// Продукт с таким ID не найден ИЛИ уже удалён
		return domain.NewNotFoundError("product")
	}

	l.Infow("product updated", "product_id", product.ID)

	return nil
}

// Delete помечает продукт как удалённый (SOFT DELETE).
func (r *ProductRepositoryPg) Delete(ctx context.Context, id entity.ProductID) error {
	l := logger.FromContext(ctx)

	// SOFT DELETE: обновляем deleted_at вместо физического удаления
	q := `
		UPDATE products
		SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`

	result, err := r.db.Exec(ctx, q, id)

	if err != nil {
		l.Errorw("failed to delete product", "error", err, "product_id", id)
		return domain.NewInternalError("failed to delete product", err)
	}

	// Проверяем, был ли удалён хотя бы один ряд
	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		// Продукт с таким ID не найден ИЛИ уже удалён
		return domain.NewNotFoundError("product")
	}

	l.Infow("product soft deleted", "product_id", id)

	return nil
}

// FindByPriceRange возвращает продукты в заданном ценовом диапазоне.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ КРИТИЧЕСКАЯ ОШИБКА: НЕПРАВИЛЬНЫЙ SQL-ЗАПРОС                                │
// └──────────────────────────────────────────────────────────────────────────┘
//
// БЫЛО:
// WHERE id = $1 AND deleted_at IS NULL   ❌ ищем по ID, а не по цене!
// ORDER BY id OFFSET $1 LIMIT $2         ❌ $1 и $2 уже заняты!
//
// ЧТО НЕ ТАК:
// 1. Ищем по ID вместо price_amount → вернём 0-1 товар вместо диапазона
// 2. OFFSET/LIMIT используют $1/$2, которые уже заняты для min/max
// 3. Пропущен &product.UpdatedAt в Scan → runtime panic
// 4. return []*entity.Product{}, ... при ошибке → неправильная семантика
//
// ПРАВИЛЬНЫЙ ПОДХОД:
// - WHERE price_amount BETWEEN $1 AND $2 — ищем по диапазону цен
// - BETWEEN включает границы: min <= price <= max
// - ORDER BY price_amount — логичная сортировка для ценового фильтра
// - return nil при ошибке, а не пустой slice
func (r *ProductRepositoryPg) FindByPriceRange(ctx context.Context, min, max value_objects.Money) ([]*entity.Product, error) {
	l := logger.FromContext(ctx)

	// ✅ ИСПРАВЛЕНО:
	// - WHERE price_amount BETWEEN $1 AND $2 (вместо id = $1)
	// - ORDER BY price_amount (от дешёвых к дорогим)
	// - Убрали OFFSET/LIMIT (если нужна пагинация — добавим отдельные параметры)
	q := `
		SELECT id, name, description, price_amount, price_currency, stock, created_at, updated_at, deleted_at
		FROM products
		WHERE price_amount BETWEEN $1 AND $2 AND deleted_at IS NULL
		ORDER BY price_amount ASC
	`

	rows, err := r.db.Query(ctx, q, min.Amount, max.Amount)
	if err != nil {
		// БЫЛО: return []*entity.Product{}, domain.NewNotFoundError(...)
		//
		// ПОЧЕМУ НЕПРАВИЛЬНО:
		// При ошибке БД (connection lost, syntax error) возвращаем пустой slice
		// + NotFoundError → caller подумает, что просто нет товаров в диапазоне.
		//
		// ПРАВИЛЬНО: return nil + InternalError
		// Caller увидит ошибку и сможет её обработать (retry, fallback, alert)
		l.Errorw("failed to find products by price range", "error", err, "min", min, "max", max)
		return nil, domain.NewInternalError("failed to find products by price range", err)
	}

	defer rows.Close()

	// Создаём slice с начальной ёмкостью 0 (будет расти по мере добавления)
	// АЛЬТЕРНАТИВА: make([]*entity.Product, 0, 10) — если знаем примерное кол-во
	products := make([]*entity.Product, 0)

	for rows.Next() {
		var product entity.Product

		// ✅ ИСПРАВЛЕНО: добавлен &product.UpdatedAt (БЫЛО: пропущен → panic)
		if err := rows.Scan(
			&product.ID,
			&product.Name,
			&product.Description,
			&product.Price.Amount,
			&product.Price.Currency,
			&product.Stock,
			&product.CreatedAt,
			&product.UpdatedAt, // ✅ БЫЛО: пропущено!
			&product.DeletedAt,
		); err != nil {
			// Ошибка Scan → проблема с типами или структурой
			l.Errorw("failed to scan product", "error", err)
			return nil, domain.NewInternalError("failed to scan product", err)
		}

		products = append(products, &product)
	}

	// Проверяем ошибку rows.Err() после завершения итерации
	// Это может быть connection error во время чтения
	if err := rows.Err(); err != nil {
		l.Errorw("error during rows iteration", "error", err)
		return nil, domain.NewInternalError("error during rows iteration", err)
	}

	// ЧТО ЕСЛИ НЕТ ТОВАРОВ В ДИАПАЗОНЕ:
	// Возвращаем пустой slice + nil error.
	// Это НЕ ошибка — просто нет товаров в этом ценовом диапазоне.
	// Caller может проверить len(products) == 0.
	//
	// АЛЬТЕРНАТИВА (если хотим различать "нет товаров" и "успех"):
	// if len(products) == 0 {
	//     return nil, domain.NewNotFoundError("no products in price range")
	// }
	//
	// Но обычно пустой результат != ошибка (как в REST API: 200 OK + [])
	return products, nil
}

// FindByIDs возвращает продукты по списку ID.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ PRODUCTION ПАТТЕРН: BATCH QUERY ДЛЯ DATA ENRICHMENT                        │
// └──────────────────────────────────────────────────────────────────────────┘
//
// ЗАЧЕМ НУЖЕН BATCH:
// Корзина содержит N товаров. Чтобы показать их названия, нужно получить
// N продуктов из БД. Два подхода:
//
// 1. N запросов FindByID (N+1 problem):
//    for _, item := range cart.Items {
//        product := repo.FindByID(item.ProductID)  // SQL запрос
//    }
//    → 10 товаров = 10 SQL запросов = медленно + нагрузка на БД
//
// 2. Один batch-запрос (правильно):
//    products := repo.FindByIDs(productIDs)  // 1 SQL запрос
//    → 10 товаров = 1 SQL запрос = быстро
//
// ПОЧЕМУ WHERE id = ANY($1):
// PostgreSQL поддерживает массивы как параметры. ANY($1) — это оператор,
// который проверяет, входит ли id в массив $1.
// Эквивалент: WHERE id IN ('uuid1', 'uuid2', 'uuid3')
// Но ANY($1) безопаснее (нет SQL injection) и удобнее (один параметр).
func (r *ProductRepositoryPg) FindByIDs(ctx context.Context, ids []entity.ProductID) ([]*entity.Product, error) {
	l := logger.FromContext(ctx)

	// Пустой список — пустой результат (не ошибка)
	if len(ids) == 0 {
		return []*entity.Product{}, nil
	}

	// Конвертируем []ProductID в []string для pgx
	// pgx не умеет автоматически конвертировать custom types в массив
	stringIDs := make([]string, len(ids))
	for i, id := range ids {
		stringIDs[i] = id.String()
	}

	q := `
		SELECT id, name, description, price_amount, price_currency, stock, created_at, updated_at, deleted_at
		FROM products
		WHERE id = ANY($1) AND deleted_at IS NULL
	`

	rows, err := r.db.Query(ctx, q, stringIDs)
	if err != nil {
		l.Errorw("failed to find products by IDs", "error", err, "count", len(ids))
		return nil, domain.NewInternalError("failed to find products by IDs", err)
	}
	defer rows.Close()

	// Pre-allocate с известной capacity
	products := make([]*entity.Product, 0, len(ids))

	for rows.Next() {
		var product entity.Product

		if err := rows.Scan(
			&product.ID,
			&product.Name,
			&product.Description,
			&product.Price.Amount,
			&product.Price.Currency,
			&product.Stock,
			&product.CreatedAt,
			&product.UpdatedAt,
			&product.DeletedAt,
		); err != nil {
			l.Errorw("failed to scan product", "error", err)
			return nil, domain.NewInternalError("failed to scan product", err)
		}

		products = append(products, &product)
	}

	if err := rows.Err(); err != nil {
		l.Errorw("error during rows iteration", "error", err)
		return nil, domain.NewInternalError("error during rows iteration", err)
	}

	// Если часть ID не найдена — это НЕ ошибка.
	// Caller получит те продукты, которые существуют.
	// Это важно для graceful degradation: если товар удалён,
	// корзина всё равно отобразится (с "Unknown Product").

	return products, nil
}

// DecrementStock уменьшает stock продукта на указанное количество.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ АТОМАРНОЕ ОБНОВЛЕНИЕ STOCK                                                │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Используем UPDATE с условием stock >= quantity в WHERE.
// Это атомарная операция — даже при параллельных запросах не уйдём в минус.
//
// ПОЧЕМУ НЕ SELECT + UPDATE:
//
//	stock = SELECT stock FROM products WHERE id = $1  -- читаем 100
//	-- другой запрос тоже читает 100
//	UPDATE products SET stock = 100 - 10              -- оба пишут 90
//	-- потеряли одно списание!
//
// ПРАВИЛЬНО: атомарный UPDATE с проверкой
//
//	UPDATE products SET stock = stock - $2 WHERE id = $1 AND stock >= $2
//
// Если stock < quantity — UPDATE затронет 0 рядов → возвращаем ErrInsufficientStock.
func (r *ProductRepositoryPg) DecrementStock(ctx context.Context, productID entity.ProductID, quantity int) error {
	l := logger.FromContext(ctx)

	// Атомарное уменьшение stock с проверкой достаточности
	// stock >= $2 гарантирует, что не уйдём в минус
	q := `
		UPDATE products
		SET stock = stock - $2, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL AND stock >= $2
	`

	result, err := r.db.Exec(ctx, q, productID, quantity)
	if err != nil {
		l.Errorw("failed to decrement stock", "error", err, "product_id", productID, "quantity", quantity)
		return domain.NewInternalError("failed to decrement stock", err)
	}

	if result.RowsAffected() == 0 {
		// Либо продукт не найден, либо недостаточно stock
		// Проверяем, существует ли продукт
		var exists bool
		checkQuery := `SELECT EXISTS(SELECT 1 FROM products WHERE id = $1 AND deleted_at IS NULL)`
		if err := r.db.QueryRow(ctx, checkQuery, productID).Scan(&exists); err != nil {
			return domain.NewInternalError("failed to check product existence", err)
		}

		if !exists {
			return domain.NewNotFoundError("product")
		}

		// Продукт существует, но stock недостаточен
		return domain.ErrInsufficientStock
	}

	l.Infow("stock decremented", "product_id", productID, "quantity", quantity)
	return nil
}
