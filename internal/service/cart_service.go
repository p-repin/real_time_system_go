package service

import (
	"context"
	"errors"
	"net/http"
	"real_time_system/domain"
	"real_time_system/domain/entity"
	"real_time_system/domain/repository"
	"real_time_system/domain/value_objects"
	"real_time_system/internal/service/dto"
	"time"
)

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ CART SERVICE: ОРКЕСТРАЦИЯ КОРЗИНЫ                                         │
// └──────────────────────────────────────────────────────────────────────────┘
//
// CartService координирует работу трёх репозиториев:
// - CartRepository: управление корзиной (create, find, update)
// - CartItemRepository: управление позициями (add, update, remove, clear)
// - ProductRepository: получение информации о товарах (stock, price, name)
//
// КЛЮЧЕВЫЕ БИЗНЕС-ПРАВИЛА:
// 1. Одна корзина на пользователя (GetOrCreate pattern)
// 2. Проверка stock перед добавлением товара
// 3. Цена фиксируется в момент добавления (не меняется при изменении прайса)
// 4. Data enrichment: ответ содержит ProductName для отображения в UI
//
// ПОЧЕМУ SERVICE, А НЕ ENTITY:
// Cart entity знает только о своих данных (Items, TotalPrice).
// Service знает о других entities (Product.Stock) и репозиториях.
// Проверка "хватает ли товара" требует данных из Product — это application logic.

type CartService struct {
	cartRepo     repository.CartRepository
	cartItemRepo repository.CartItemRepository
	productRepo  repository.ProductRepository
}

func NewCartService(
	cartRepo repository.CartRepository,
	cartItemRepo repository.CartItemRepository,
	productRepo repository.ProductRepository,
) *CartService {
	return &CartService{
		cartRepo:     cartRepo,
		cartItemRepo: cartItemRepo,
		productRepo:  productRepo,
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// PUBLIC METHODS
// ──────────────────────────────────────────────────────────────────────────────

// GetOrCreateCart возвращает корзину пользователя или создаёт новую.
//
// ПАТТЕРН GET-OR-CREATE:
// Пользователь не должен явно "создавать корзину". Она появляется автоматически.
// Это lazy creation — корзина создаётся только когда нужна.
func (s *CartService) GetOrCreateCart(ctx context.Context, userID entity.UserID) (*dto.CartResponse, error) {
	cart, err := s.getOrCreateCartEntity(ctx, userID)
	if err != nil {
		return nil, err
	}

	return s.toResponse(ctx, cart)
}

// GetCartWithItems возвращает корзину со всеми товарами.
func (s *CartService) GetCartWithItems(ctx context.Context, userID entity.UserID) (*dto.CartResponse, error) {
	cart, err := s.getCartWithItems(ctx, userID)
	if err != nil {
		return nil, err
	}

	return s.toResponse(ctx, cart)
}

// AddToCart добавляет товар в корзину.
//
// БИЗНЕС-ПРАВИЛА:
// 1. Проверяем stock ПЕРЕД добавлением (не резервируем, только проверяем)
// 2. Цена берётся из Product.Price, НЕ из запроса (безопасность!)
// 3. Если товар уже в корзине — количество увеличивается (UPSERT в БД)
//
// RACE CONDITION:
// Между проверкой stock и добавлением товар может закончиться.
// Это ОК — корзина это "wishlist", финальная проверка будет в PlaceOrder.
func (s *CartService) AddToCart(ctx context.Context, userID entity.UserID, req dto.AddToCartRequest) (*dto.CartResponse, error) {
	// 1. Валидация входных данных
	productID, err := entity.ParseProductID(req.ProductID)
	if err != nil {
		return nil, domain.NewValidationError("invalid product_id format")
	}

	if req.Quantity <= 0 {
		return nil, domain.NewValidationError("quantity must be greater than zero")
	}

	// 2. Получаем продукт и проверяем stock
	product, err := s.productRepo.FindByID(ctx, productID)
	if err != nil {
		return nil, err
	}

	if product.Stock < req.Quantity {
		return nil, domain.NewValidationError("insufficient stock")
	}

	// 3. Получаем или создаём корзину
	cart, err := s.getOrCreateCartEntity(ctx, userID)
	if err != nil {
		return nil, err
	}

	// 4. Создаём CartItem
	//    ВАЖНО: Price берём из product, не из запроса!
	//    Иначе злоумышленник мог бы передать price: 1 копейка.
	cartItem := entity.CartItem{
		ID:        entity.NewCartItemID(),
		CartID:    cart.ID,
		ProductID: productID,
		Quantity:  req.Quantity,
		Price:     product.Price,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// 5. Сохраняем в БД (UPSERT — если товар есть, увеличит quantity)
	if err := s.cartItemRepo.AddItem(ctx, &cartItem); err != nil {
		return nil, err
	}

	// 6. Обновляем in-memory cart (пересчёт TotalPrice)
	if err := cart.AddItem(productID, cartItem.ID, req.Quantity, product.Price); err != nil {
		return nil, err
	}

	// 7. Сохраняем корзину (updated_at)
	if err := s.cartRepo.Update(ctx, cart); err != nil {
		return nil, err
	}

	return s.toResponse(ctx, cart)
}

// RemoveFromCart удаляет товар из корзины.
func (s *CartService) RemoveFromCart(ctx context.Context, userID entity.UserID, productIDStr string) (*dto.CartResponse, error) {
	productID, err := entity.ParseProductID(productIDStr)
	if err != nil {
		return nil, domain.NewValidationError("invalid product_id format")
	}

	// Получаем корзину с items
	cart, err := s.getCartWithItems(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Проверяем, что товар есть в корзине
	item, ok := cart.Items[productID]
	if !ok {
		return nil, domain.NewNotFoundError("item not in cart")
	}

	// Удаляем из БД
	if err := s.cartItemRepo.RemoveItem(ctx, item.ID); err != nil {
		return nil, err
	}

	// Удаляем из памяти (пересчёт TotalPrice)
	if err := cart.RemoveItem(productID); err != nil {
		return nil, err
	}

	// Сохраняем корзину
	if err := s.cartRepo.Update(ctx, cart); err != nil {
		return nil, err
	}

	return s.toResponse(ctx, cart)
}

// UpdateQuantity изменяет количество товара в корзине.
//
// УДОБСТВО ДЛЯ UI:
// quantity = 0 означает "удалить товар".
// Клиенту не нужен отдельный endpoint — просто "уменьшает до нуля".
func (s *CartService) UpdateQuantity(ctx context.Context, userID entity.UserID, req dto.UpdateCartItemRequest) (*dto.CartResponse, error) {
	// quantity = 0 → удаление
	if req.Quantity == 0 {
		return s.RemoveFromCart(ctx, userID, req.ProductID)
	}

	if req.Quantity < 0 {
		return nil, domain.NewValidationError("quantity cannot be negative")
	}

	productID, err := entity.ParseProductID(req.ProductID)
	if err != nil {
		return nil, domain.NewValidationError("invalid product_id format")
	}

	cart, err := s.getCartWithItems(ctx, userID)
	if err != nil {
		return nil, err
	}

	item, ok := cart.Items[productID]
	if !ok {
		return nil, domain.NewNotFoundError("item not in cart")
	}

	// Проверяем stock только при УВЕЛИЧЕНИИ количества
	if req.Quantity > item.Quantity {
		product, err := s.productRepo.FindByID(ctx, productID)
		if err != nil {
			return nil, err
		}
		if product.Stock < req.Quantity {
			return nil, domain.NewValidationError("insufficient stock")
		}
	}

	// Обновляем в БД
	if err := s.cartItemRepo.UpdateQuantity(ctx, item.ID, req.Quantity); err != nil {
		return nil, err
	}

	// Получаем обновлённую корзину
	//
	// ПОЧЕМУ НЕ ОБНОВЛЯЕМ В ПАМЯТИ:
	// В entity Cart нет метода UpdateItemQuantity (только Add/Remove).
	// Проще получить свежие данные из БД.
	// В production можно добавить такой метод для оптимизации.
	cart, err = s.cartRepo.GetCartWithItems(ctx, cart.ID)
	if err != nil {
		return nil, err
	}

	return s.toResponse(ctx, cart)
}

// ClearCart очищает корзину полностью.
//
// ИДЕМПОТЕНТНОСТЬ:
// Очистка пустой корзины — не ошибка.
// Клиент может вызвать Clear несколько раз (retry после network error).
func (s *CartService) ClearCart(ctx context.Context, userID entity.UserID) (*dto.CartResponse, error) {
	cart, err := s.getOrCreateCartEntity(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Очищаем items в БД
	if err := s.cartItemRepo.Clear(ctx, cart.ID); err != nil {
		return nil, err
	}

	// Очищаем в памяти
	cart.Clear()

	// Сохраняем корзину
	if err := s.cartRepo.Update(ctx, cart); err != nil {
		return nil, err
	}

	return s.toResponse(ctx, cart)
}

// ──────────────────────────────────────────────────────────────────────────────
// PRIVATE HELPERS
// ──────────────────────────────────────────────────────────────────────────────
//
// ПОЧЕМУ ВЫНОСИМ В HELPERS:
// Код "получить корзину + загрузить items" повторяется в нескольких методах.
// DRY (Don't Repeat Yourself) — выносим в приватные методы.
// Это упрощает чтение и уменьшает вероятность ошибок.

// getOrCreateCartEntity — получает или создаёт корзину (возвращает entity).
func (s *CartService) getOrCreateCartEntity(ctx context.Context, userID entity.UserID) (*entity.Cart, error) {
	cart, err := s.cartRepo.FindByUserID(ctx, userID)

	if err != nil {
		var domainErr *domain.DomainError
		if errors.As(err, &domainErr) && domainErr.StatusCode == http.StatusNotFound {
			// Корзина не найдена — создаём новую
			cart = entity.NewCart(userID, value_objects.RUB)
			if err = s.cartRepo.Create(ctx, cart); err != nil {
				return nil, err
			}
		} else {
			// Реальная ошибка БД
			return nil, err
		}
	}

	return cart, nil
}

// getCartWithItems — получает корзину с загруженными items.
//
// ЗАЧЕМ ОТДЕЛЬНЫЙ МЕТОД:
// Этот паттерн (getOrCreate + GetCartWithItems) повторяется часто.
// Вынесли в helper, чтобы не дублировать код.
func (s *CartService) getCartWithItems(ctx context.Context, userID entity.UserID) (*entity.Cart, error) {
	cart, err := s.getOrCreateCartEntity(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Загружаем items через LEFT JOIN
	return s.cartRepo.GetCartWithItems(ctx, cart.ID)
}

// toResponse — конвертирует entity в DTO.
//
// ЗАЧЕМ ОТДЕЛЬНЫЙ МЕТОД:
// 1. Сейчас передаём nil вместо productNames (без обогащения)
// 2. Позже добавим Data Enrichment здесь — в одном месте
// 3. Все публичные методы используют этот helper
func (s *CartService) toResponse(ctx context.Context, cart *entity.Cart) (*dto.CartResponse, error) {
	// TODO: добавить Data Enrichment (productNames) когда реализуем FindByIDs
	if len(cart.Items) == 0 {
		response := dto.ToCartResponse(cart, nil)
		return &response, nil
	}

	productIDs := make([]entity.ProductID, 0, len(cart.Items))

	for productID := range cart.Items {
		productIDs = append(productIDs, productID)
	}

	products, err := s.productRepo.FindByIDs(ctx, productIDs)
	if err != nil {
		return nil, err
	}

	productNames := make(map[entity.ProductID]string, len(productIDs))

	for _, product := range products {
		productNames[product.ID] = product.Name
	}

	response := dto.ToCartResponse(cart, productNames)
	return &response, nil

}
