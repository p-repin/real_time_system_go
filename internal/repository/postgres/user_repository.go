package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"real_time_system/domain"
	"real_time_system/domain/entity"
	"real_time_system/domain/repository"
	"real_time_system/internal/logger"
	"real_time_system/pkg/client"
)

// Compile-time check: проверяем, что UserRepositoryPg реализует интерфейс.
// Если забудем реализовать метод → ошибка компиляции.
var _ repository.UserRepository = (*UserRepositoryPg)(nil)

type UserRepositoryPg struct {
	db client.Querier
}

// NewUserRepository создаёт репозиторий пользователей.
// Принимает Querier — может быть *pgxpool.Pool (обычная работа) или pgx.Tx (транзакция).
func NewUserRepository(db client.Querier) *UserRepositoryPg {
	return &UserRepositoryPg{db: db}
}

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ PRODUCTION ПАТТЕРН: ОБРАБОТКА ОШИБОК POSTGRESQL                            │
// └──────────────────────────────────────────────────────────────────────────┘
//
// PostgreSQL возвращает специфичные коды ошибок (pgconn.PgError):
//   23505 → unique_violation (duplicate key)
//   23503 → foreign_key_violation
//   23514 → check_violation
//
// МЫ КОНВЕРТИРУЕМ ИХ В domain.DomainError:
//   23505 → 409 Conflict (email уже существует)
//   23503 → 400 Bad Request (ссылка на несуществующую сущность)
//   Остальное → 500 Internal Server Error
//
// ПОЧЕМУ ЭТО ВАЖНО:
// 1. Handler не зависит от PostgreSQL
//    - Можно заменить на MySQL → handler не меняется
// 2. Клиент API получает правильные HTTP коды
//    - 409 для duplicate → клиент знает, что email занят
//    - 500 для сбоя БД → клиент знает, что проблема на сервере
// 3. Ошибки становятся частью domain-логики
//    - errors.Is(err, domain.ErrConflict) в тестах

func (r *UserRepositoryPg) Create(ctx context.Context, user *entity.User) error {
	l := logger.FromContext(ctx)

	q := `
		INSERT INTO users (id, name, surname, email, created_at, updated_at, deleted_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`

	// ИСПОЛЬЗУЕМ sql.Valuer:
	// user.ID.Value() автоматически вызывается pgx → конвертируется в строку
	_, err := r.db.Exec(ctx, q,
		user.ID,        // pgx вызовет Value() → "550e8400-e29b-41d4-a716-446655440000"
		user.Name,
		user.Surname,
		user.Email,
		user.CreatedAt,
		user.UpdatedAt,
		user.DeletedAt, // nil для активного пользователя
	)

	if err != nil {
		// Проверяем, это ошибка PostgreSQL?
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			// 23505 = unique_violation (email уже существует)
			if pgErr.Code == "23505" {
				// НЕ логируем — это нормальный бизнес-сценарий
				// Пользователь ввёл занятый email → возвращаем 409
				return domain.NewConflictError("email already exists")
			}
		}

		// Любая другая ошибка — логируем
		l.Errorw("failed to create user", "error", err, "user_id", user.ID)
		return domain.NewInternalError("failed to create user", err)
	}

	// Успех — логируем важное бизнес-событие (уровень Info)
	l.Infow("user created", "user_id", user.ID, "email", user.Email)

	return nil
}

// FindByID возвращает пользователя по ID.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ PRODUCTION ПАТТЕРН: WHERE deleted_at IS NULL                               │
// └──────────────────────────────────────────────────────────────────────────┘
//
// ВАЖНО: все запросы к таблице с soft delete ДОЛЖНЫ включать:
//   WHERE deleted_at IS NULL
//
// БЕЗ ЭТОГО:
//   - FindByID вернёт удалённого пользователя → баг
//   - FindByEmail найдёт удалённый email → нельзя регистрироваться повторно
//
// BEST PRACTICE В PRODUCTION:
// 1. Создать VIEW для активных пользователей:
//    CREATE VIEW active_users AS SELECT * FROM users WHERE deleted_at IS NULL;
//    SELECT * FROM active_users WHERE id = $1;
//
// 2. Использовать PostgreSQL Row-Level Security (RLS):
//    CREATE POLICY active_users_only ON users
//    FOR SELECT USING (deleted_at IS NULL);
//
// Для учебного проекта используем явное WHERE — проще понять.
func (r *UserRepositoryPg) FindByID(ctx context.Context, id entity.UserID) (*entity.User, error) {
	l := logger.FromContext(ctx)

	q := `
		SELECT id, name, surname, email, created_at, updated_at, deleted_at
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
	`

	var user entity.User

	// ИСПОЛЬЗУЕМ sql.Scanner:
	// pgx автоматически вызовет Scan() для user.ID
	err := r.db.QueryRow(ctx, q, id).Scan(
		&user.ID,        // pgx вызовет Scan(src) → запишет UUID в UserID
		&user.Name,
		&user.Surname,
		&user.Email,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.DeletedAt, // может быть NULL → запишется nil
	)

	if err != nil {
		// pgx v5 использует pgx.ErrNoRows вместо sql.ErrNoRows
		if errors.Is(err, pgx.ErrNoRows) {
			// НЕ логируем — "not found" это нормальный сценарий
			return nil, domain.NewNotFoundError("user")
		}

		// Логируем реальные ошибки БД
		l.Errorw("failed to find user by id", "error", err, "user_id", id)
		return nil, domain.NewInternalError("failed to find user", err)
	}

	return &user, nil
}

func (r *UserRepositoryPg) FindByEmail(ctx context.Context, email string) (*entity.User, error) {
	l := logger.FromContext(ctx)

	q := `
		SELECT id, name, surname, email, created_at, updated_at, deleted_at
		FROM users
		WHERE email = $1 AND deleted_at IS NULL
	`

	var user entity.User

	err := r.db.QueryRow(ctx, q, email).Scan(
		&user.ID,
		&user.Name,
		&user.Surname,
		&user.Email,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.DeletedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// НЕ логируем NotFound
			return nil, domain.NewNotFoundError("user")
		}

		l.Errorw("failed to find user by email", "error", err, "email", email)
		return nil, domain.NewInternalError("failed to find user", err)
	}

	return &user, nil
}

// Update обновляет данные пользователя.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ PRODUCTION ПАТТЕРН: УПРАВЛЕНИЕ updated_at                                  │
// └──────────────────────────────────────────────────────────────────────────┘
//
// ДВА ПОДХОДА:
//
// 1. Entity владеет timestamp (текущий подход):
//    UPDATE users SET ..., updated_at = $4
//    ✅ Entity контролирует время обновления
//    ✅ Можно установить updated_at в прошлом (для миграций)
//    ⚠️ Нужно не забывать обновлять в entity перед сохранением
//
// 2. БД владеет timestamp (через триггер):
//    UPDATE users SET ... -- updated_at обновляется автоматически
//    ✅ Нельзя забыть обновить
//    ⚠️ Entity не знает финальное значение
//    ⚠️ Нужен триггер в БД
//
// В PRODUCTION часто используют подход 2 (триггер), НО:
// - Для аудита нужно знать точное время в entity
// - Для событий (event sourcing) нужен контроль времени
// Поэтому мы используем подход 1.
func (r *UserRepositoryPg) Update(ctx context.Context, user *entity.User) error {
	l := logger.FromContext(ctx)

	q := `
		UPDATE users
		SET name = $1, surname = $2, email = $3, updated_at = $4
		WHERE id = $5 AND deleted_at IS NULL
	`

	// ВАЖНО: WHERE deleted_at IS NULL → нельзя обновить удалённого пользователя
	result, err := r.db.Exec(ctx, q,
		user.Name,
		user.Surname,
		user.Email,
		user.UpdatedAt, // используем значение из entity
		user.ID,
	)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			// Проверяем unique violation для email
			if pgErr.Code == "23505" {
				return domain.NewConflictError("email already exists")
			}
		}

		l.Errorw("failed to update user", "error", err, "user_id", user.ID)
		return domain.NewInternalError("failed to update user", err)
	}

	// Проверяем, был ли обновлён хотя бы один ряд
	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		// Пользователь с таким ID не найден ИЛИ уже удалён
		return domain.NewNotFoundError("user")
	}

	l.Infow("user updated", "user_id", user.ID)

	return nil
}

// Delete помечает пользователя как удалённого (SOFT DELETE).
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ PRODUCTION ПАТТЕРН: SOFT DELETE                                            │
// └──────────────────────────────────────────────────────────────────────────┘
//
// SOFT DELETE = UPDATE users SET deleted_at = NOW()
//
// ПРЕИМУЩЕСТВА (почему так делают в production):
// 1. ВОССТАНОВЛЕНИЕ:
//    Пользователь удалил аккаунт по ошибке → можно восстановить
//    UPDATE users SET deleted_at = NULL WHERE id = $1
//
// 2. АУДИТ / GDPR COMPLIANCE:
//    Можем доказать, когда пользователь удалился
//    Можем хранить данные для юридических целей
//
// 3. FOREIGN KEYS:
//    Order → User связь остаётся валидной
//    Можем посмотреть историю заказов удалённого пользователя
//
// 4. АНАЛИТИКА:
//    SELECT COUNT(*) FROM users WHERE deleted_at IS NOT NULL
//    → сколько пользователей ушло за месяц?
//
// НЕДОСТАТКИ (и как с ними живут):
// 1. Занимает место в БД
//    → Архивируем старые deleted записи (> 1 года) в отдельную таблицу
//
// 2. Нужно WHERE deleted_at IS NULL везде
//    → Используем VIEW или Row-Level Security
//
// 3. Уникальность email
//    → Constraint с WHERE: UNIQUE (email) WHERE deleted_at IS NULL
//    → Удалённый user@mail.com не блокирует регистрацию нового
//
// АЛЬТЕРНАТИВА (когда использовать HARD DELETE):
// - Временные данные (session tokens, rate limit counters)
// - Тестовые данные
// - Данные без бизнес-ценности
func (r *UserRepositoryPg) Delete(ctx context.Context, id entity.UserID) error {
	l := logger.FromContext(ctx)

	// SOFT DELETE: обновляем deleted_at вместо физического удаления
	q := `
		UPDATE users
		SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`

	result, err := r.db.Exec(ctx, q, id)

	if err != nil {
		l.Errorw("failed to delete user", "error", err, "user_id", id)
		return domain.NewInternalError("failed to delete user", err)
	}

	// Проверяем, был ли удалён хотя бы один ряд
	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		// Пользователь с таким ID не найден ИЛИ уже удалён
		return domain.NewNotFoundError("user")
	}

	l.Infow("user soft deleted", "user_id", id)

	return nil
}
