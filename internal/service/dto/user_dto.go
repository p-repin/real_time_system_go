package dto

import "real_time_system/domain/entity"

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ЧТО ТАКОЕ DTO (Data Transfer Object)?                                     │
// └──────────────────────────────────────────────────────────────────────────┘
//
// DTO — объект для передачи данных между слоями приложения.
// Отличается от Entity тем, что:
//   - НЕ содержит бизнес-логики (нет методов, только данные)
//   - Представляет данные в формате, удобном для конкретного слоя (HTTP, gRPC)
//   - Изолирует внешний API от внутренней domain модели
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПОЧЕМУ НЕ ИСПОЛЬЗОВАТЬ ENTITY НАПРЯМУЮ В API?                              │
// └──────────────────────────────────────────────────────────────────────────┘
//
// ❌ БЕЗ DTO (анти-паттерн):
//   func CreateUser(ctx, user *entity.User) error {
//       // Проблемы:
//       // 1. Handler создаёт entity → знает про domain
//       // 2. API завязан на структуру entity → нельзя изменить поля без breaking changes
//       // 3. Экспозим внутренние поля (ID, CreatedAt) в запросе
//   }
//
// ✅ С DTO:
//   func CreateUser(ctx, req CreateUserRequest) (*UserResponse, error) {
//       // Преимущества:
//       // 1. API независим от domain структуры
//       // 2. Можем добавить поля в entity без изменения API
//       // 3. Контролируем, что принимаем и возвращаем (не экспозим CreatedAt в request)
//   }
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ КОГДА ИСПОЛЬЗОВАТЬ DTO:                                                    │
// └──────────────────────────────────────────────────────────────────────────┘
//
// ✅ Всегда для HTTP API (REST, GraphQL)
// ✅ Для gRPC (protobuf → DTO → entity)
// ✅ Для внешних интеграций (сторонние API)
// ✅ Для сложных форм (entity.Order + entity.User + доп. данные)
//
// ❌ НЕ нужно для внутренних вызовов (service → repository)

// CreateUserRequest — DTO для создания пользователя.
//
// ПОЧЕМУ отдельная структура от entity.User:
// - API принимает только имя, фамилию, email
// - ID, CreatedAt, UpdatedAt генерируются автоматически, не должны приходить в request
// - Если добавим в entity поле "Password", оно не попадёт в этот DTO автоматически
type CreateUserRequest struct {
	Name    string `json:"name" validate:"required,min=1,max=255"`
	Surname string `json:"surname" validate:"max=255"`
	Email   string `json:"email" validate:"required,email"`
}

// UpdateUserRequest — DTO для обновления данных пользователя.
//
// ПОЧЕМУ отдельная структура от CreateUserRequest:
// - При обновлении может быть partial update (не все поля обязательны)
// - Может потребоваться обновлять только email, не трогая name/surname
// - В будущем можем добавить поля только для update (например, "active" bool)
type UpdateUserRequest struct {
	Name    *string `json:"name,omitempty" validate:"omitempty,min=1,max=255"`
	Surname *string `json:"surname,omitempty" validate:"omitempty,max=255"`
	Email   *string `json:"email,omitempty" validate:"omitempty,email"`
}

// UserResponse — DTO для возврата данных пользователя.
//
// ПОЧЕМУ отдельная структура от entity.User:
// - Контролируем формат данных в API (ID как строка, даты в ISO8601)
// - Можем скрыть чувствительные поля (Password, внутренние флаги)
// - Можем добавить поля, которых нет в entity (computed fields, links)
//
// ПРИМЕР computed field:
//   FullName string `json:"full_name"` // не хранится в БД, вычисляется
type UserResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Surname   string `json:"surname"`
	Email     string `json:"email"`
	CreatedAt string `json:"created_at"` // ISO8601 формат
	UpdatedAt string `json:"updated_at"`
}

// ToUserResponse конвертирует entity.User в UserResponse.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПАТТЕРН: MAPPER ФУНКЦИИ                                                    │
// └──────────────────────────────────────────────────────────────────────────┘
//
// В production часто выносят в отдельный пакет mapper/, но для простых случаев
// можно держать рядом с DTO.
//
// АЛЬТЕРНАТИВЫ:
// 1. Метод на entity: func (u *User) ToResponse() UserResponse
//    - Плюсы: удобно вызывать user.ToResponse()
//    - Минусы: entity знает про DTO (нарушение зависимостей)
//
// 2. Функция в DTO (текущий подход): func ToUserResponse(user *entity.User)
//    - Плюсы: зависимость однонаправленная (DTO → entity)
//    - Минусы: нужно импортировать dto пакет
//
// 3. Отдельный mapper пакет: mapper.UserToResponse(user)
//    - Плюсы: чистое разделение, легко тестировать
//    - Минусы: +1 пакет, overkill для простых случаев
func ToUserResponse(user *entity.User) UserResponse {
	return UserResponse{
		ID:        user.ID.String(),
		Name:      user.Name,
		Surname:   user.Surname,
		Email:     user.Email,
		CreatedAt: user.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), // ISO8601
		UpdatedAt: user.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// ToUserResponseList конвертирует слайс entities в слайс DTO.
//
// ПОЧЕМУ отдельная функция, а не цикл в handler:
// - DRY: используется в нескольких местах (GetUsers, SearchUsers)
// - Тестируемость: можем протестировать маппинг отдельно
// - Оптимизация: можем предаллоцировать слайс нужной длины
func ToUserResponseList(users []*entity.User) []UserResponse {
	// Преаллокация: резервируем память сразу, избегаем reallocations
	result := make([]UserResponse, 0, len(users))
	for _, user := range users {
		result = append(result, ToUserResponse(user))
	}
	return result
}
