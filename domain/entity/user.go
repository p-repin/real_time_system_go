package entity

import (
	"database/sql/driver"
	"fmt"
	"github.com/google/uuid"
	"real_time_system/domain"
	"time"
)

// UserID — типизированная обёртка над uuid.UUID.
//
// ПОЧЕМУ не используем uuid.UUID напрямую:
// - Типобезопасность: нельзя случайно передать ProductID туда, где ожидается UserID.
//   Без обёртки компилятор не поймает ошибку NewOrder(productID, items) вместо NewOrder(userID, items).
// - Семантика: код читается как бизнес-логика, а не как технические детали.
// - Единая точка изменения: если решим перейти с UUID на ULID или int64 —
//   меняем только определение типа и его методы, а не весь проект.
//
// КАКИЕ ПРОБЛЕМЫ БЫЛИ БЫ БЕЗ ОБЁРТКИ:
// - Путаница аргументов: func CreateOrder(userID, productID uuid.UUID) — легко перепутать порядок
// - Невозможность добавить методы к uuid.UUID (чужой пакет)
// - При смене формата ID пришлось бы менять сигнатуры по всему проекту
type UserID uuid.UUID

// NewUserID генерирует новый уникальный UserID.
func NewUserID() UserID {
	return UserID(uuid.New())
}

// String возвращает строковое представление ID.
// Нужен для логирования, HTTP-ответов и сериализации.
func (id UserID) String() string {
	return uuid.UUID(id).String()
}

// IsZero проверяет, является ли ID пустым (не инициализированным).
// Полезно для валидации: if user.ID.IsZero() { return error }
func (id UserID) IsZero() bool {
	return uuid.UUID(id) == uuid.Nil
}

// ParseUserID парсит строку в UserID.
// Нужен для десериализации из HTTP-запросов, БД, и т.д.
//
// ПОЧЕМУ отдельная функция, а не метод:
// Метод требует уже существующий экземпляр UserID, что неудобно для парсинга.
// Функция-конструктор — идиоматичный Go-подход.
func ParseUserID(s string) (UserID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return UserID{}, err
	}
	return UserID(id), nil
}

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ sql.Scanner и driver.Valuer — ИНТЕГРАЦИЯ С БАЗОЙ ДАННЫХ                   │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Эти интерфейсы позволяют UserID работать напрямую с pgx и другими SQL драйверами.
//
// БЕЗ ЭТОГО (что было раньше):
//   var idStr string
//   err := db.QueryRow(...).Scan(&idStr, ...) // сканируем в строку
//   user.ID, err = entity.ParseUserID(idStr)  // парсим вручную
//
// С ЭТИМ:
//   var user entity.User
//   err := db.QueryRow(...).Scan(&user.ID, ...) // pgx автоматически вызовет Scan()
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ КАК ЭТО РАБОТАЕТ:                                                          │
// └──────────────────────────────────────────────────────────────────────────┘
//
// 1. Чтение из БД (QueryRow, Query):
//    БД возвращает UUID → pgx вызывает Scan(src interface{}) → записывает в UserID
//
// 2. Запись в БД (Exec, Query с параметрами):
//    Передаём UserID → pgx вызывает Value() → конвертирует в строку → отправляет в БД
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПОЧЕМУ ЭТО ЛУЧШЕ РУЧНОГО ПАРСИНГА:                                         │
// └──────────────────────────────────────────────────────────────────────────┘
//
// 1. Меньше кода: не нужно парсить вручную в каждом методе repository
// 2. Меньше ошибок: нельзя забыть проверить err от ParseUserID
// 3. Единообразие: все typed IDs работают одинаково
// 4. Производительность: pgx может оптимизировать конвертацию

// Scan реализует интерфейс sql.Scanner для чтения UserID из БД.
//
// PostgreSQL может вернуть UUID в разных форматах:
//   - string: "550e8400-e29b-41d4-a716-446655440000"
//   - []byte: байтовое представление (16 байт)
//
// ВАЖНО: обрабатываем оба формата для совместимости с разными драйверами.
func (id *UserID) Scan(src interface{}) error {
	switch v := src.(type) {
	case string:
		// Парсим UUID из строки
		parsed, err := uuid.Parse(v)
		if err != nil {
			return fmt.Errorf("invalid UUID string: %w", err)
		}
		*id = UserID(parsed)
		return nil

	case []byte:
		// Парсим UUID из байтов
		parsed, err := uuid.ParseBytes(v)
		if err != nil {
			return fmt.Errorf("invalid UUID bytes: %w", err)
		}
		*id = UserID(parsed)
		return nil

	case nil:
		// NULL из БД → zero-value
		*id = UserID(uuid.Nil)
		return nil

	default:
		// Неожиданный тип → ошибка
		return fmt.Errorf("cannot scan %T into UserID", src)
	}
}

// Value реализует интерфейс driver.Valuer для записи UserID в БД.
//
// Возвращаем строковое представление UUID, потому что:
//   - Универсально работает со всеми PostgreSQL драйверами
//   - Читаемо в логах и при отладке
//   - Не требует бинарного кодирования
//
// АЛЬТЕРНАТИВА: можно вернуть []byte для экономии места, но для UUID
// разница незначительна (36 байт строка vs 16 байт binary).
func (id UserID) Value() (driver.Value, error) {
	// Возвращаем строковое представление
	return id.String(), nil
}

// User — сущность пользователя системы.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ SOFT DELETE: DeletedAt *time.Time                                         │
// └──────────────────────────────────────────────────────────────────────────┘
//
// ПОЧЕМУ УКАЗАТЕЛЬ (*time.Time), А НЕ time.Time:
// - nil означает "не удалён" (активный пользователь)
// - !nil означает "удалён" (время удаления)
//
// АЛЬТЕРНАТИВЫ:
// 1. bool IsDeleted + time.Time DeletedAt
//    - Минусы: избыточность, можно забыть синхронизировать флаг и дату
// 2. time.Time DeletedAt с zero-value проверкой
//    - Минусы: DeletedAt.IsZero() vs DeletedAt == nil — менее явно
//
// В production стандарт: *time.Time для nullable timestamps.
type User struct {
	ID        UserID
	Name      string
	Surname   string
	Email     string
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time // nil = активный пользователь, !nil = удалён
}

// NewUser — фабричный метод для создания User с валидацией.
//
// ПОЧЕМУ валидация в фабрике, а не в отдельном Validate() методе:
// - Невалидный User не может существовать — это инвариант entity.
//   Если бы мы разрешали создавать User без валидации, то любой код мог бы
//   создать User{Email: ""} и передать его дальше. Service-слой не знает,
//   прошёл ли этот User валидацию или нет.
// - Фабрика — единственная точка входа для создания entity.
//   Это делает код предсказуемым: если User существует, он гарантированно валиден.
//
// КАКИЕ ПРОБЛЕМЫ БЫЛИ БЫ БЕЗ ЭТОГО:
// - "Зомби-объекты": User с пустым email попадает в БД, ломает отправку писем
// - Defensive programming на каждом уровне: service проверяет email,
//   repository проверяет email, handler проверяет email — дублирование
func NewUser(email, name, surname string) (*User, error) {
	if email == "" {
		return nil, domain.ErrEmptyEmail
	}
	if name == "" {
		return nil, domain.ErrEmptyName
	}

	now := time.Now()

	return &User{
		ID:        NewUserID(),
		Name:      name,
		Surname:   surname,
		Email:     email,
		CreatedAt: now,
		UpdatedAt: now,
		DeletedAt: nil, // новый пользователь всегда активен
	}, nil
}

// IsDeleted проверяет, удалён ли пользователь (soft delete).
//
// ИСПОЛЬЗОВАНИЕ:
//   if user.IsDeleted() {
//       return errors.New("cannot update deleted user")
//   }
func (u *User) IsDeleted() bool {
	return u.DeletedAt != nil
}

// Delete помечает пользователя как удалённого (soft delete).
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПОЧЕМУ МЕТОД НА ENTITY, А НЕ ПРОСТО user.DeletedAt = &time.Now()?         │
// └──────────────────────────────────────────────────────────────────────────┘
//
// 1. ИНКАПСУЛЯЦИЯ: entity контролирует своё состояние
//    - Можем добавить валидацию (нельзя удалить уже удалённого)
//    - Можем добавить логику (обнулить sensitive данные)
//
// 2. ЕДИНАЯ ТОЧКА ИЗМЕНЕНИЯ:
//    - Если добавим поле "deleted_by UserID" → меняем только этот метод
//    - Все вызовы user.Delete() автоматически получат новую логику
//
// 3. ТЕСТИРУЕМОСТЬ:
//    - Легко написать unit-тест для метода Delete()
//    - Нельзя случайно установить DeletedAt в прошлом
//
// ПРИМЕР БУДУЩЕГО РАСШИРЕНИЯ:
//   func (u *User) Delete(deletedBy UserID) error {
//       u.DeletedAt = &time.Now()
//       u.DeletedBy = deletedBy  // кто удалил (для аудита)
//       u.Email = ""              // GDPR compliance: обнуляем PII
//   }
func (u *User) Delete() error {
	// Проверка: нельзя удалить уже удалённого
	if u.IsDeleted() {
		return domain.NewValidationError("user already deleted")
	}

	now := time.Now()
	u.DeletedAt = &now
	u.UpdatedAt = now

	return nil
}
