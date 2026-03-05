package service

import (
	"context"
	"real_time_system/domain/entity"
	"real_time_system/domain/repository"
	"real_time_system/domain/value_objects"
	"real_time_system/internal/service/dto"
	"time"
)

type ProductService struct {
	productRepo repository.ProductRepository
}

func NewProductService(productRepo repository.ProductRepository) *ProductService {
	return &ProductService{
		productRepo: productRepo,
	}
}

// CreateProduct создаёт новый продукт.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ PRODUCTION ПАТТЕРН: ВАЛИДАЦИЯ В ENTITY, А НЕ В SERVICE                     │
// └──────────────────────────────────────────────────────────────────────────┘
//
// ПОЧЕМУ entity.NewProduct() ВЫПОЛНЯЕТ ВАЛИДАЦИЮ:
// 1. Невалидный Product не может существовать в системе (инвариант)
// 2. Если добавим ещё один способ создания Product (импорт из файла) →
//    валидация сработает автоматически
// 3. Service не дублирует бизнес-логику — она в domain
//
// ЧТО БЫЛО БЫ БЕЗ ФАБРИКИ:
// product := &entity.Product{Name: req.Name, ...}
// if product.Name == "" { return error } // дублируем валидацию в service
//
// ПРОБЛЕМА: если добавим правило "Name не должен содержать спецсимволы",
// придётся добавлять проверку везде, где создаём Product.
func (s *ProductService) CreateProduct(ctx context.Context, req *dto.CreateProductRequest) (*dto.ProductResponse, error) {
	// Фабрика entity.NewProduct проверяет:
	// - Name не пустой
	// - Price валиден (через value_objects.NewMoney)
	// - Stock >= 0 (если будет валидация)
	product, err := entity.NewProduct(req.Name, req.Description, req.Price, req.Stock)
	if err != nil {
		// Ошибка валидации (domain.ErrEmptyProductName) → возвращаем как есть
		// Service НЕ оборачивает domain-ошибки — они уже содержат HTTP-код
		return nil, err
	}

	if err := s.productRepo.Create(ctx, product); err != nil {
		// Может быть:
		// - domain.ConflictError (duplicate name)
		// - domain.InternalError (БД недоступна)
		return nil, err
	}

	response := dto.ToProductResponse(product)

	return &response, nil
}

func (s *ProductService) GetProductByID(ctx context.Context, id entity.ProductID) (*dto.ProductResponse, error) {
	product, err := s.productRepo.FindByID(ctx, id)
	if err != nil {
		// domain.NotFoundError или domain.InternalError
		return nil, err
	}

	response := dto.ToProductResponse(product)

	return &response, nil
}

// GetProductByRange возвращает товары в ценовом диапазоне.
//
// SENIOR INSIGHT: PRE-ALLOCATION ДЛЯ SLICE
// БЫЛО: make([]*dto.ProductResponse, 0)
// ✅ ЛУЧШЕ: make([]*dto.ProductResponse, 0, len(products))
//
// ПОЧЕМУ:
// Мы знаем финальный размер slice → можем выделить память заранее.
// Без len(products) append будет реаллоцировать массив при росте:
//   cap=0 → 1 → 2 → 4 → 8 → 16 ... (удвоение каждый раз)
// С len(products) сразу выделяем нужное количество → одна аллокация.
//
// КОГДА ЭТО КРИТИЧНО:
// - Большие списки (>1000 элементов) → экономия на аллокациях
// - High-load API → меньше нагрузка на GC
func (s *ProductService) GetProductByRange(ctx context.Context, min, max value_objects.Money) ([]*dto.ProductResponse, error) {
	products, err := s.productRepo.FindByPriceRange(ctx, min, max)
	if err != nil {
		return nil, err
	}

	// ✅ ИСПРАВЛЕНО: pre-allocate slice с известным размером
	// БЫЛО: make([]*dto.ProductResponse, 0)
	productsResponse := make([]*dto.ProductResponse, 0, len(products))

	for _, p := range products {
		response := dto.ToProductResponse(p)
		productsResponse = append(productsResponse, &response)
	}

	return productsResponse, nil
}

// UpdateProduct обновляет данные продукта (partial update).
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ PRODUCTION ПАТТЕРН: PARTIAL UPDATE                                         │
// └──────────────────────────────────────────────────────────────────────────┘
//
// ПОЧЕМУ POINTER-BASED DTO (*string, *Money, *int):
// Позволяет различать "не передано" от "передано нулевое значение".
//
// БЕЗ POINTER:
// type UpdateProductRequest struct { Stock int }
// req := UpdateProductRequest{} // Stock = 0
//
// ПРОБЛЕМА: не понять, это пользователь хочет обнулить Stock или не передал поле.
// Мы обновим Stock = 0, хотя пользователь просто хотел изменить Name.
//
// С POINTER:
// type UpdateProductRequest struct { Stock *int }
// req := UpdateProductRequest{} // Stock = nil → не трогаем поле в БД
// req := UpdateProductRequest{Stock: ptr(0)} // Stock = 0 → обнуляем в БД
//
// ЭТО СТАНДАРТ для PATCH-запросов в REST API.
func (s *ProductService) UpdateProduct(ctx context.Context, id entity.ProductID, req *dto.UpdateProductRequest) (*dto.ProductResponse, error) {
	// Сначала получаем текущую версию из БД
	product, err := s.productRepo.FindByID(ctx, id)
	if err != nil {
		// domain.NotFoundError → товар не существует или удалён
		return nil, err
	}

	// Применяем изменения только для переданных полей
	if req.Name != nil {
		product.Name = *req.Name
	}

	if req.Price != nil {
		product.Price = *req.Price
	}

	if req.Stock != nil {
		product.Stock = *req.Stock
	}

	if req.Description != nil {
		product.Description = *req.Description
	}

	// Обновляем timestamp
	// ✅ ВАРИАНТ 1 (текущий): вручную устанавливаем time.Now()
	product.UpdatedAt = time.Now()

	// ✅ ВАРИАНТ 2 (senior-уровень): добавить метод в entity
	// func (p *Product) MarkUpdated() { p.UpdatedAt = time.Now() }
	// product.MarkUpdated()
	//
	// ПРЕИМУЩЕСТВА ВАРИАНТА 2:
	// - Инкапсуляция: логика в entity, а не в service
	// - Если добавим audit log, достаточно изменить один метод
	// - Читаемость: product.MarkUpdated() понятнее, чем product.UpdatedAt = ...
	//
	// ДЛЯ ТЕКУЩЕГО ПРОЕКТА: вариант 1 приемлем, но вариант 2 — это senior-подход.

	// Сохраняем в БД
	if err := s.productRepo.Update(ctx, product); err != nil {
		// Может быть:
		// - domain.ConflictError (новый name уже существует)
		// - domain.NotFoundError (товар удалили между FindByID и Update)
		// - domain.InternalError (БД недоступна)
		return nil, err
	}

	response := dto.ToProductResponse(product)

	return &response, nil
}

func (s *ProductService) DeleteProduct(ctx context.Context, id entity.ProductID) error {
	// SOFT DELETE в repository установит deleted_at = NOW()
	return s.productRepo.Delete(ctx, id)
}
