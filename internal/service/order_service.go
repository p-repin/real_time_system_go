package service

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"real_time_system/domain"
	"real_time_system/domain/entity"
	"real_time_system/domain/repository"
	"real_time_system/internal/events"
	"real_time_system/internal/observability"
	"real_time_system/internal/repository/postgres"
	"real_time_system/internal/service/dto"
)

type OrderService struct {
	cartRepo       repository.CartRepository
	orderRepo      repository.OrderRepository
	productRepo    repository.ProductRepository
	cartItemRepo   repository.CartItemRepository
	pool           *pgxpool.Pool
	// eventPublisher публикует доменные события после бизнес-операций.
	//
	// ┌──────────────────────────────────────────────────────────────────────┐
	// │ ПОЧЕМУ ИНТЕРФЕЙС, А НЕ *KafkaOrderPublisher?                          │
	// └──────────────────────────────────────────────────────────────────────┘
	//
	// Dependency Inversion Principle (буква D в SOLID):
	// OrderService зависит от абстракции (интерфейса), а не от реализации.
	// Это позволяет:
	// - В тестах передать MockPublisher или NoOpPublisher
	// - В production заменить Kafka на NATS без изменения OrderService
	// - Легко добавить декоратор (LoggingPublisher, MetricsPublisher)
	//
	// ❌ Если бы поле было *KafkaOrderPublisher:
	//   - Тест требует поднятой Kafka (медленно, ненадёжно)
	//   - Смена транспорта = изменение OrderService (нарушение OCP)
	eventPublisher events.OrderEventPublisher
}

// NewOrderService создаёт OrderService с публикатором событий.
//
// eventPublisher — обязательная зависимость (не опциональная).
// Если Kafka недоступна — передай events.NoOpOrderPublisher{}.
// Это явно: вызывающий код решает, нужны ли события.
func NewOrderService(
	cartRepo repository.CartRepository,
	orderRepo repository.OrderRepository,
	productRepo repository.ProductRepository,
	cartItemRepo repository.CartItemRepository,
	pool *pgxpool.Pool,
	eventPublisher events.OrderEventPublisher,
) *OrderService {
	return &OrderService{
		cartRepo:       cartRepo,
		orderRepo:      orderRepo,
		productRepo:    productRepo,
		cartItemRepo:   cartItemRepo,
		pool:           pool,
		eventPublisher: eventPublisher,
	}
}

func (s *OrderService) GetOrder(ctx context.Context, userID entity.UserID, orderID entity.OrderID) (*dto.OrderResponse, error) {
	order, err := s.orderRepo.FindByID(ctx, orderID)
	if err != nil {
		return nil, err
	}

	// Ownership check: не раскрываем существование чужих заказов
	if order.UserID != userID {
		return nil, domain.NewNotFoundError("order")
	}

	response := dto.ToOrderResponse(order)
	return &response, nil
}

func (s *OrderService) GetUserOrders(ctx context.Context, userID entity.UserID) ([]dto.OrderResponse, error) {
	orders, err := s.orderRepo.FindByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	response := make([]dto.OrderResponse, 0, len(orders))

	for _, or := range orders {
		response = append(response, dto.ToOrderResponse(or))
	}
	return response, nil
}

// UpdateStatus изменяет статус заказа.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПОДХОД 1: ЦЕЛЕВОЙ СТАТУС (текущая реализация)                             │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Клиент отправляет целевой статус: PATCH /orders/{id}/status { "status": "paid" }
// Сервер проверяет возможность перехода и выполняет его.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПОДХОД 3: ACTIONS (production-рекомендация)                               │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Вместо одного endpoint с параметром status — отдельные endpoints-действия:
//
//	POST /orders/{id}/pay      → вызывает MarkAsPaid(), может принимать payment_id
//	POST /orders/{id}/ship     → вызывает Ship(), может принимать tracking_number
//	POST /orders/{id}/deliver  → вызывает Deliver(), может принимать signature
//	POST /orders/{id}/cancel   → вызывает Cancel(), может принимать reason
//	POST /orders/{id}/refund   → вызывает Refund(), может принимать refund_amount
//
// Преимущества actions:
//   - Семантически понятные URL (действия, не состояния)
//   - Каждый action может принимать свои параметры
//   - Легче добавлять бизнес-логику (уведомления, интеграции)
//   - Проще документировать и тестировать
//
// Для учебного проекта Подход 1 достаточен.
func (s *OrderService) UpdateStatus(ctx context.Context, userID entity.UserID, orderID entity.OrderID, newStatus string) (*dto.OrderResponse, error) {
	order, err := s.orderRepo.FindByID(ctx, orderID)
	if err != nil {
		return nil, err
	}

	// Ownership check: пользователь может менять только свои заказы.
	// Возвращаем NotFound вместо Forbidden — не раскрываем существование чужих заказов.
	if order.UserID != userID {
		return nil, domain.NewNotFoundError("order")
	}

	status := entity.OrderStatus(newStatus)

	// Проверяем допустимость перехода через state machine
	if !order.CanTransitionTo(status) {
		return nil, domain.NewValidationError("invalid status transition")
	}

	// Вызываем соответствующий метод entity для перехода
	switch status {
	case entity.OrderStatusPaid:
		if err := order.MarkAsPaid(); err != nil {
			return nil, err
		}
	case entity.OrderStatusShipped:
		if err := order.Ship(); err != nil {
			return nil, err
		}
	case entity.OrderStatusDelivered:
		if err := order.Deliver(); err != nil {
			return nil, err
		}
	case entity.OrderStatusCancelled:
		if err := order.Cancel(); err != nil {
			return nil, err
		}
	default:
		return nil, domain.NewValidationError("unknown status")
	}

	if err := s.orderRepo.Update(ctx, order); err != nil {
		return nil, err
	}

	response := dto.ToOrderResponse(order)
	return &response, nil
}

// ══════════════════════════════════════════════════════════════════════════════
// PRODUCTION APPROACH: ACTION-BASED METHODS
// ══════════════════════════════════════════════════════════════════════════════
//
// Каждый метод — отдельное бизнес-действие с собственными параметрами и side effects.
// Это позволяет:
//   - Принимать специфичные данные (payment_id, tracking_number)
//   - Выполнять side effects (уведомления, интеграции с внешними сервисами)
//   - Логировать действия для аудита
//   - Легко тестировать каждое действие отдельно

// PayOrder подтверждает оплату заказа.
//
// В production здесь была бы интеграция с платёжной системой:
//   - Проверка статуса платежа через Payment Gateway API
//   - Сохранение payment_id для reconciliation (сверки)
//   - Отправка email/push "Ваш заказ оплачен"
//   - Запись в audit log для бухгалтерии
func (s *OrderService) PayOrder(ctx context.Context, userID entity.UserID, orderID entity.OrderID, paymentID string) (*dto.OrderResponse, error) {
	order, err := s.orderRepo.FindByID(ctx, orderID)
	if err != nil {
		return nil, err
	}

	if order.UserID != userID {
		return nil, domain.NewNotFoundError("order")
	}

	// Бизнес-логика оплаты
	if err := order.MarkAsPaid(); err != nil {
		return nil, err
	}

	// TODO: В production — сохранить paymentID в order
	// order.PaymentID = paymentID

	if err := s.orderRepo.Update(ctx, order); err != nil {
		return nil, err
	}

	// Публикуем событие ПОСЛЕ успешного сохранения в БД.
	//
	// ┌──────────────────────────────────────────────────────────────────────┐
	// │ ПОЧЕМУ ПОСЛЕ Update, А НЕ ДО?                                         │
	// └──────────────────────────────────────────────────────────────────────┘
	//
	// Порядок имеет значение:
	//   1. orderRepo.Update() — сохраняем в БД
	//   2. eventPublisher.Publish() — публикуем в Kafka
	//
	// Если опубликовать ДО сохранения:
	//   - Консьюмер получает "order.paid" и начинает обработку
	//   - В этот момент Update() падает, rollback
	//   - В БД заказ остался "pending", но консьюмер думает что "paid"
	//   - Несогласованность данных!
	//
	// Если опубликовать ПОСЛЕ:
	//   - Update() завершился успешно → БД в консистентном состоянии
	//   - Публикуем событие (может упасть, но это best-effort)
	//   - Худший сценарий: событие потеряно, но БД корректна
	//
	// Ошибку публикации НЕ возвращаем клиенту — оплата уже прошла.
	// Логируем внутри publisher для мониторинга.
	_ = s.eventPublisher.PublishOrderPaid(ctx, order)

	response := dto.ToOrderResponse(order)
	return &response, nil
}

// ShipOrder отмечает заказ как отправленный.
//
// В production здесь была бы интеграция с логистикой:
//   - Сохранение tracking number для отслеживания
//   - Интеграция с курьерской службой (СДЭК, DHL, etc.)
//   - Отправка email/push "Ваш заказ отправлен" с tracking link
//   - Обновление estimated delivery date
func (s *OrderService) ShipOrder(ctx context.Context, userID entity.UserID, orderID entity.OrderID, trackingNumber string) (*dto.OrderResponse, error) {
	order, err := s.orderRepo.FindByID(ctx, orderID)
	if err != nil {
		return nil, err
	}

	if order.UserID != userID {
		return nil, domain.NewNotFoundError("order")
	}

	if err := order.Ship(); err != nil {
		return nil, err
	}

	// TODO: В production — сохранить trackingNumber
	// order.TrackingNumber = trackingNumber
	// order.EstimatedDelivery = calculateDeliveryDate()

	if err := s.orderRepo.Update(ctx, order); err != nil {
		return nil, err
	}

	// Публикуем событие отправки — консьюмеры могут отправить уведомление
	// с трек-номером, обновить статус в личном кабинете.
	_ = s.eventPublisher.PublishOrderShipped(ctx, order)

	response := dto.ToOrderResponse(order)
	return &response, nil
}

// DeliverOrder подтверждает доставку заказа.
//
// В production здесь была бы:
//   - Фиксация подписи получателя (signature)
//   - Фото подтверждения доставки
//   - Начисление бонусных баллов клиенту
//   - Отправка запроса на отзыв о товаре
//   - Закрытие заказа в системе логистики
func (s *OrderService) DeliverOrder(ctx context.Context, userID entity.UserID, orderID entity.OrderID, signature string) (*dto.OrderResponse, error) {
	order, err := s.orderRepo.FindByID(ctx, orderID)
	if err != nil {
		return nil, err
	}

	if order.UserID != userID {
		return nil, domain.NewNotFoundError("order")
	}

	if err := order.Deliver(); err != nil {
		return nil, err
	}

	// TODO: В production — сохранить подтверждение доставки
	// order.DeliverySignature = signature
	// order.DeliveredAt = time.Now()

	if err := s.orderRepo.Update(ctx, order); err != nil {
		return nil, err
	}

	// Публикуем событие доставки — консьюмеры могут начислить бонусы,
	// запросить отзыв, закрыть задачу в CRM.
	_ = s.eventPublisher.PublishOrderDelivered(ctx, order)

	response := dto.ToOrderResponse(order)
	return &response, nil
}

// CancelOrder отменяет заказ.
//
// В production здесь была бы сложная логика:
//   - Проверка возможности отмены (не отправлен ли уже?)
//   - Возврат денег через Payment Gateway
//   - Возврат товаров на склад (увеличение stock)
//   - Отправка email "Ваш заказ отменён"
//   - Сохранение причины отмены для аналитики
func (s *OrderService) CancelOrder(ctx context.Context, userID entity.UserID, orderID entity.OrderID, reason string) (*dto.OrderResponse, error) {
	order, err := s.orderRepo.FindByID(ctx, orderID)
	if err != nil {
		return nil, err
	}

	if order.UserID != userID {
		return nil, domain.NewNotFoundError("order")
	}

	// Проверяем, был ли заказ оплачен — нужен ли refund?
	wasPaid := order.Status == entity.OrderStatusPaid

	if err := order.Cancel(); err != nil {
		return nil, err
	}

	// TODO: В production — сохранить причину отмены
	// order.CancellationReason = reason
	// order.CancelledAt = time.Now()

	if err := s.orderRepo.Update(ctx, order); err != nil {
		return nil, err
	}

	// TODO: В production — side effects:
	// if wasPaid {
	//     s.paymentService.Refund(ctx, order.PaymentID, order.TotalAmount)
	// }
	// s.inventoryService.RestoreStock(ctx, order.Items)  // вернуть товары на склад
	// s.notificationService.SendCancellationNotification(ctx, order, reason)
	// s.analyticsService.TrackCancellation(ctx, order, reason)

	_ = wasPaid // пока не используется, подавляем warning

	// Публикуем событие отмены — консьюмеры могут инициировать возврат денег,
	// восстановить stock на складе, уведомить пользователя.
	_ = s.eventPublisher.PublishOrderCancelled(ctx, order)

	response := dto.ToOrderResponse(order)
	return &response, nil
}

// PlaceOrder создаёт заказ из корзины пользователя в рамках ACID-транзакции.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ТРЕЙСИНГ: ПОЧЕМУ ИМЕННО PlaceOrder?                                        │
// └──────────────────────────────────────────────────────────────────────────┘
//
// PlaceOrder — самый критичный путь в системе:
//   1. Читает корзину (SELECT)
//   2. Загружает продукты batch (SELECT IN)
//   3. Создаёт заказ (INSERT)
//   4. Уменьшает stock для каждого товара (UPDATE × N)
//   5. Очищает корзину (DELETE)
//   6. Коммитит транзакцию
//   7. Публикует событие в Kafka
//
// Если PlaceOrder медленный — клиент ждёт. Метрики покажут latency,
// но не скажут ПОЧЕМУ. Трейс покажет, какой именно шаг замедлился.
//
// Пример реального сценария:
//   span "PlaceOrder" = 800ms
//   ├── span "TX: FindByUserID" = 5ms   (индекс есть, быстро)
//   ├── span "TX: FindByIDs"    = 10ms  (batch, эффективно)
//   ├── span "TX: Create order" = 15ms
//   ├── span "TX: DecrementStock × 5" = 750ms  ← БУТЫЛОЧНОЕ ГОРЛЫШКО!
//   └── span "TX: Commit"       = 5ms
//
// Вывод: нет индекса на products.id при UPDATE DecrementStock.
// Без трейсинга пришлось бы гадать или добавлять отдельные метрики.
func (s *OrderService) PlaceOrder(ctx context.Context, userID entity.UserID) (*dto.OrderResponse, error) {
	// ── Создаём корневой span для всей операции ────────────────────────────
	//
	// observability.Tracer("real_time_system/service") — именованный трейсер.
	// Имя трейсера отображается в Jaeger как "instrumentation library".
	//
	// tracer.Start() возвращает новый контекст с активным span'ом.
	// Все дочерние span'ы (в подфункциях) будут автоматически прикреплены
	// к этому span'у через ctx.
	tracer := observability.Tracer("real_time_system/service")
	ctx, span := tracer.Start(ctx, "OrderService.PlaceOrder")
	defer span.End() // span закрывается при выходе из функции

	// Добавляем атрибуты span'а: они отображаются в Jaeger как теги.
	// userID поможет найти трейс при расследовании конкретного инцидента.
	span.SetAttributes(
		attribute.String("user.id", userID.String()),
	)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		// recordError — хелпер: устанавливает статус span'а в Error + добавляет событие.
		// Без этого span будет завершён без статуса → в Jaeger не видно что была ошибка.
		recordError(span, err)
		return nil, domain.NewInternalError("dont have transactions", err)
	}
	defer tx.Rollback(ctx)

	cartRepo := postgres.NewCartRepository(tx)
	productRepo := postgres.NewProductRepository(tx)
	orderRepo := postgres.NewOrderRepository(tx)
	cartItemRepo := postgres.NewCartItemsRepository(tx)

	cart, err := cartRepo.FindByUserID(ctx, userID)
	if err != nil {
		recordError(span, err)
		return nil, domain.NewNotFoundError("cart")
	}

	cart, err = cartRepo.GetCartWithItems(ctx, cart.ID)
	if err != nil {
		recordError(span, err)
		return nil, domain.NewNotFoundError("cart")
	}

	if len(cart.Items) == 0 {
		return nil, domain.NewValidationError("cart is empty")
	}

	// Добавляем количество товаров как атрибут: поможет коррелировать
	// latency с размером заказа (больше товаров → больше DecrementStock)
	span.SetAttributes(attribute.Int("order.items_count", len(cart.Items)))

	productIDs := make([]entity.ProductID, 0, len(cart.Items))
	for _, item := range cart.Items {
		productIDs = append(productIDs, item.ProductID)
	}

	products, err := productRepo.FindByIDs(ctx, productIDs)
	if err != nil {
		recordError(span, err)
		return nil, domain.NewInternalError("dont get products", err)
	}

	productsMap := make(map[entity.ProductID]*entity.Product, len(products))

	for _, p := range products {
		productsMap[p.ID] = p
	}

	orderItems := make([]entity.OrderItem, 0, len(cart.Items))

	for _, item := range cart.Items {
		product, ok := productsMap[item.ProductID]
		if !ok {
			return nil, domain.NewNotFoundError("product")
		}
		if product.Stock < item.Quantity {
			return nil, domain.NewValidationError("insufficient stock for " + product.Name)
		}

		orderItems = append(orderItems, entity.OrderItem{
			ProductID:   item.ProductID,
			ProductName: product.Name,
			Quantity:    item.Quantity,
			Price:       item.Price,
		})
	}

	order, err := entity.NewOrder(userID, orderItems)
	if err != nil {
		recordError(span, err)
		return nil, err
	}

	if err := orderRepo.Create(ctx, order); err != nil {
		recordError(span, err)
		return nil, err
	}

	// Дочерний span для цикла DecrementStock.
	//
	// ┌──────────────────────────────────────────────────────────────────────┐
	// │ ПОЧЕМУ SPAN ДЛЯ ЦИКЛА, А НЕ ДЛЯ КАЖДОГО ВЫЗОВА?                      │
	// └──────────────────────────────────────────────────────────────────────┘
	//
	// Каждый вызов DecrementStock — отдельная SQL операция.
	// При 100 товарах в заказе → 100 span'ов → шум в Jaeger.
	//
	// Решение: один span для всего цикла + атрибут "items.count".
	// Если цикл медленный — уже видно в родительском span'е.
	// Если нужна детализация по конкретному товару — добавить span внутрь.
	_, decrementSpan := tracer.Start(ctx, "TX.DecrementStock")
	decrementSpan.SetAttributes(attribute.Int("items.count", len(orderItems)))
	for _, item := range orderItems {
		if err := productRepo.DecrementStock(ctx, item.ProductID, item.Quantity); err != nil {
			decrementSpan.End()
			recordError(span, err)
			return nil, err
		}
	}
	decrementSpan.End()

	if err := cartItemRepo.Clear(ctx, cart.ID); err != nil {
		recordError(span, err)
		return nil, domain.NewInternalError("failed to clear cart", err)
	}

	if err := tx.Commit(ctx); err != nil {
		recordError(span, err)
		return nil, domain.NewInternalError("failed to commit", err)
	}

	// После успешного коммита — добавляем ID созданного заказа в span.
	// Теперь в Jaeger можно найти трейс по order.id.
	span.SetAttributes(attribute.String("order.id", order.ID.String()))

	// ┌──────────────────────────────────────────────────────────────────────┐
	// │ ПУБЛИКАЦИЯ СОБЫТИЯ ПОСЛЕ COMMIT ТРАНЗАКЦИИ                             │
	// └──────────────────────────────────────────────────────────────────────┘
	//
	// КРИТИЧЕСКИ ВАЖНО: публикуем ТОЛЬКО после успешного tx.Commit().
	//
	// Почему не внутри транзакции?
	//   Kafka и PostgreSQL — разные системы. Нет распределённой транзакции,
	//   которая захватила бы оба. Если Kafka падает → можно откатить Postgres,
	//   но заказ реально создан в голове пользователя...
	//   Если Postgres падает → можно откатить Kafka, но событие уже в брокере...
	//
	// Наш выбор: "Local Transaction + Best-Effort Publish"
	//   1. Коммитим транзакцию (заказ ТОЧНО создан в БД)
	//   2. Публикуем событие (best-effort, может упасть)
	//
	// Что потеряем при сбое Kafka после commit?
	//   - Событие order.placed потеряно
	//   - Уведомление не отправлено
	//   - Но заказ создан! Это главное.
	//
	// Как решить потерю события? → Outbox Pattern (следующая итерация):
	//   - В той же транзакции пишем в таблицу outbox
	//   - Отдельный воркер читает outbox и публикует в Kafka
	//   - Гарантия at-least-once для событий
	_ = s.eventPublisher.PublishOrderPlaced(ctx, order)

	response := dto.ToOrderResponse(order)
	return &response, nil
}

// recordError помечает span как ошибочный и записывает детали.
//
// span.RecordError(err) — добавляет событие "exception" в span с stack trace.
// span.SetStatus(codes.Error, ...) — устанавливает статус span'а.
//
// ПОЧЕМУ НУЖНО И ТО, И ДРУГОЕ?
//   RecordError — добавляет событие (видно в timeline span'а)
//   SetStatus   — устанавливает итоговый статус (влияет на цвет в Jaeger UI)
//   Без SetStatus span будет зелёным даже при ошибке!
func recordError(span trace.Span, err error) {
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}
