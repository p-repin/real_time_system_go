package service

import (
	"context"
	"errors"
	"net/http"
	"real_time_system/domain"
	"real_time_system/domain/entity"
	"real_time_system/domain/repository"
	"real_time_system/internal/service/dto"
	"time"
)

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ PRODUCTION ПАТТЕРН: SERVICE СЛОЙ                                           │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Service-слой отвечает за:
// 1. ОРКЕСТРАЦИЮ между entities и repositories
// 2. БИЗНЕС-ПРАВИЛА уровня application (проверка duplicate email)
// 3. ТРАНЗАКЦИИ (в будущем)
// 4. СОБЫТИЯ (в будущем: отправка email после регистрации)
//
// Service НЕ должен:
// ❌ Знать про HTTP (status codes, headers) → это handler
// ❌ Знать про БД (SQL, драйверы) → это repository
// ❌ Дублировать валидацию entity → это domain
//
// DEPENDENCY DIRECTION (Clean Architecture):
//   Handler → Service → Repository → Entity
//           ↓
//          DTO

type UserService struct {
	userRepo repository.UserRepository
	// В будущем:
	// emailService EmailService
	// eventBus     EventBus
}

func NewUserService(userRepo repository.UserRepository) *UserService {
	return &UserService{
		userRepo: userRepo,
	}
}

// CreateUser создаёт нового пользователя.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ PRODUCTION ПАТТЕРН: REQUEST/RESPONSE DTO                                   │
// └──────────────────────────────────────────────────────────────────────────┘
//
// ПОЧЕМУ ПРИНИМАЕМ DTO, А НЕ ПРИМИТИВЫ:
//
// БЫЛО (примитивы):
//   func CreateUser(ctx, name, surname, email string) (*entity.User, error)
//   - При 10 полях → func CreateUser(ctx, p1, p2, p3, p4, p5, p6, p7, p8, p9, p10)
//   - Легко перепутать порядок аргументов
//
// СТАЛО (DTO):
//   func CreateUser(ctx, req CreateUserRequest) (*UserResponse, error)
//   - Количество полей не влияет на сигнатуру
//   - Невозможно перепутать порядок (именованные поля)
//   - Легко добавить новое поле (обратная совместимость)
//
// ПОЧЕМУ ВОЗВРАЩАЕМ DTO, А НЕ ENTITY:
//   - Handler не знает про entity (изоляция слоёв)
//   - Можем контролировать, что попадёт в API (например, не экспозим Password)
//   - Можем добавить computed fields (FullName, которого нет в entity)
func (s *UserService) CreateUser(ctx context.Context, req dto.CreateUserRequest) (*dto.UserResponse, error) {
	// 1. БИЗНЕС-ПРАВИЛО: проверка на дубликат email
	//    Это application-уровень logic (нужен вызов БД).
	//    Entity не может проверить email uniqueness (не знает про БД).
	existing, err := s.userRepo.FindByEmail(ctx, req.Email)
	if err != nil {
		// Проверяем, это NotFound или реальная ошибка
		var domainErr *domain.DomainError
		if errors.As(err, &domainErr) && domainErr.StatusCode == http.StatusNotFound {
			// NotFound — это ОК, email свободен, продолжаем
		} else {
			// Реальная ошибка БД — возвращаем её
			return nil, err
		}
	} else if existing != nil {
		// Нашли пользователя с таким email → конфликт
		return nil, domain.NewConflictError("email already exists")
	}

	// 2. СОЗДАНИЕ ENTITY (domain logic)
	//    Entity.NewUser() валидирует данные и защищает инварианты.
	user, err := entity.NewUser(req.Email, req.Name, req.Surname)
	if err != nil {
		// Ошибка валидации (ErrEmptyEmail, ErrEmptyName) → 400
		// Эти ошибки уже sentinel errors из domain пакета
		return nil, err
	}

	// 3. СОХРАНЕНИЕ В БД
	if err := s.userRepo.Create(ctx, user); err != nil {
		return nil, err
	}

	// 4. КОНВЕРТАЦИЯ В DTO ДЛЯ ОТВЕТА
	//    Изолируем handler от entity структуры.
	response := dto.ToUserResponse(user)

	// В будущем здесь можем добавить:
	// - s.emailService.SendWelcomeEmail(user.Email)
	// - s.eventBus.Publish("user.created", user.ID)
	// - s.analytics.Track("signup", user.ID)

	return &response, nil
}

// GetUser возвращает пользователя по ID.
func (s *UserService) GetUser(ctx context.Context, id entity.UserID) (*dto.UserResponse, error) {
	user, err := s.userRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}

	response := dto.ToUserResponse(user)
	return &response, nil
}

// UpdateUser обновляет данные пользователя.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ PRODUCTION ПАТТЕРН: PARTIAL UPDATE через DTO с pointer полями               │
// └──────────────────────────────────────────────────────────────────────────┘
//
// UpdateUserRequest использует *string вместо string:
//   Name    *string  // nil = не обновлять, !nil = обновить на новое значение
//   Email   *string
//
// ПОЧЕМУ ТАК:
// 1. Клиент может обновить только email, не трогая name/surname
//    PATCH /users/{id} {"email": "new@mail.com"}
//
// 2. Можем отличить "не передано" от "передана пустая строка"
//    nil → не обновляем
//    "" → обновляем на пустую строку (валидация entity отклонит)
//
// АЛЬТЕРНАТИВЫ:
// 1. Два разных DTO: UpdateUserEmailRequest, UpdateUserNameRequest
//    - Минусы: дублирование, нельзя обновить несколько полей атомарно
//
// 2. Использовать map[string]interface{} для динамических полей
//    - Минусы: нет типобезопасности, сложнее валидация
//
// Pointer-based partial update — стандарт в production API.
func (s *UserService) UpdateUser(ctx context.Context, id entity.UserID, req dto.UpdateUserRequest) (*dto.UserResponse, error) {
	// 1. Получаем существующего пользователя из БД
	user, err := s.userRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// 2. Обновляем только переданные поля (partial update)
	if req.Name != nil {
		user.Name = *req.Name
	}
	if req.Surname != nil {
		user.Surname = *req.Surname
	}
	if req.Email != nil {
		// Проверка на дубликат email (если email меняется)
		if *req.Email != user.Email {
			existing, err := s.userRepo.FindByEmail(ctx, *req.Email)
			if err != nil {
				var domainErr *domain.DomainError
				if !(errors.As(err, &domainErr) && domainErr.StatusCode == http.StatusNotFound) {
					return nil, err
				}
			} else if existing != nil && existing.ID != user.ID {
				// Email занят другим пользователем
				return nil, domain.NewConflictError("email already exists")
			}
		}
		user.Email = *req.Email
	}

	// 3. Обновляем updated_at (entity контролирует свои timestamps)
	user.UpdatedAt = time.Now()

	// 4. Сохраняем в БД
	if err := s.userRepo.Update(ctx, user); err != nil {
		return nil, err
	}

	response := dto.ToUserResponse(user)
	return &response, nil
}

// DeleteUser помечает пользователя как удалённого (soft delete).
func (s *UserService) DeleteUser(ctx context.Context, id entity.UserID) error {
	// В production здесь можем добавить:
	// 1. Проверка прав доступа (может ли текущий пользователь удалить этого?)
	// 2. Каскадное удаление зависимых данных (soft delete orders, carts)
	// 3. Отправка email "Ваш аккаунт удалён"
	// 4. Публикация события "user.deleted" в event bus

	// Пока просто вызываем repository
	return s.userRepo.Delete(ctx, id)
}
