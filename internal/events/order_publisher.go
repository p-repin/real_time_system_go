// Package events содержит публикаторы доменных событий.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ СЛОИ И ЗАВИСИМОСТИ                                                         │
// └──────────────────────────────────────────────────────────────────────────┘
//
// domain/events   → определяет ЧТО публикуем (OrderEvent struct)
// internal/events → определяет КАК публикуем (через Kafka, через HTTP, через mock)
// pkg/kafka       → определяет КАК именно через Kafka (Writer, сериализация)
//
// OrderService зависит от интерфейса OrderEventPublisher (из этого пакета).
// Реализация (KafkaOrderPublisher) передаётся через DI в server.go.
// В тестах можно подменить реализацию на MockPublisher.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПОЧЕМУ ИНТЕРФЕЙС В internal/events, А НЕ В domain?                         │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Интерфейс описывает СПОСОБ ПУБЛИКАЦИИ — это инфраструктурная операция.
// Domain не должен знать, что события публикуются (он только их определяет).
// Service зависит от интерфейса публикатора → можно менять транспорт.
//
// Если бы интерфейс был в domain → domain зависел бы от инфраструктурных
// концепций (публикация событий), что нарушает Clean Architecture.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"real_time_system/domain/entity"
	"real_time_system/domain/events"
	"real_time_system/internal/config"
	"real_time_system/internal/logger"
	"real_time_system/pkg/kafka"
)

// OrderEventPublisher — интерфейс публикации событий заказа.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПОЧЕМУ ИНТЕРФЕЙС, А НЕ КОНКРЕТНАЯ СТРУКТУРА?                               │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Три причины:
//
//  1. ТЕСТИРУЕМОСТЬ:
//     OrderService в тестах получает MockPublisher, а не реальный KafkaPublisher.
//     Тест не требует запущенного Kafka → быстрые unit-тесты.
//
//  2. ЗАМЕНА ТРАНСПОРТА:
//     Сегодня Kafka, завтра NATS или RabbitMQ → меняем только реализацию,
//     OrderService не трогаем.
//
//  3. NO-OP ДЛЯ ПРОСТЫХ DEPLOYMENTS:
//     Можно передать NoOpOrderPublisher (ничего не делает) для
//     деплоя без Kafka — система работает, просто без событий.
type OrderEventPublisher interface {
	// PublishOrderPlaced публикует событие создания заказа.
	PublishOrderPlaced(ctx context.Context, order *entity.Order) error

	// PublishOrderPaid публикует событие оплаты заказа.
	PublishOrderPaid(ctx context.Context, order *entity.Order) error

	// PublishOrderShipped публикует событие отправки заказа.
	PublishOrderShipped(ctx context.Context, order *entity.Order) error

	// PublishOrderDelivered публикует событие доставки заказа.
	PublishOrderDelivered(ctx context.Context, order *entity.Order) error

	// PublishOrderCancelled публикует событие отмены заказа.
	PublishOrderCancelled(ctx context.Context, order *entity.Order) error
}

// KafkaOrderPublisher — реализация OrderEventPublisher через Kafka.
type KafkaOrderPublisher struct {
	producer *kafka.Producer
	topic    string
}

// NewKafkaOrderPublisher создаёт KafkaOrderPublisher.
//
// Принимает *kafka.Producer (низкоуровневый клиент) и конфиг.
// topic берём из конфига, а не хардкодим — чтобы можно было легко
// сменить имя топика через переменную окружения (разные окружения:
// "orders" в prod, "orders-staging" в staging).
func NewKafkaOrderPublisher(producer *kafka.Producer, cfg config.KafkaConfig) *KafkaOrderPublisher {
	return &KafkaOrderPublisher{
		producer: producer,
		topic:    cfg.TopicOrders,
	}
}

// PublishOrderPlaced публикует событие создания заказа.
func (p *KafkaOrderPublisher) PublishOrderPlaced(ctx context.Context, order *entity.Order) error {
	return p.publish(ctx, order, events.OrderPlaced)
}

// PublishOrderPaid публикует событие оплаты заказа.
func (p *KafkaOrderPublisher) PublishOrderPaid(ctx context.Context, order *entity.Order) error {
	return p.publish(ctx, order, events.OrderPaid)
}

// PublishOrderShipped публикует событие отправки заказа.
func (p *KafkaOrderPublisher) PublishOrderShipped(ctx context.Context, order *entity.Order) error {
	return p.publish(ctx, order, events.OrderShipped)
}

// PublishOrderDelivered публикует событие доставки заказа.
func (p *KafkaOrderPublisher) PublishOrderDelivered(ctx context.Context, order *entity.Order) error {
	return p.publish(ctx, order, events.OrderDelivered)
}

// PublishOrderCancelled публикует событие отмены заказа.
func (p *KafkaOrderPublisher) PublishOrderCancelled(ctx context.Context, order *entity.Order) error {
	return p.publish(ctx, order, events.OrderCancelled)
}

// publish — приватный метод, общая логика публикации для всех типов событий.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ПОЧЕМУ ОДИН ПРИВАТНЫЙ МЕТОД, А НЕ КОД В КАЖДОМ ПУБЛИЧНОМ?                  │
// └──────────────────────────────────────────────────────────────────────────┘
//
// DRY: сериализация, генерация EventID, формирование ключа — одинаковы для всех событий.
// Если дублировать → при изменении формата (добавить поле) нужно менять 5 мест.
// Публичные методы — это семантические обёртки (читается как документация):
//
//	publisher.PublishOrderPaid(ctx, order) — понятно что происходит.
func (p *KafkaOrderPublisher) publish(ctx context.Context, order *entity.Order, eventType events.OrderEventType) error {
	// Генерируем уникальный ID для ЭТОГО КОНКРЕТНОГО СОБЫТИЯ (не для заказа).
	// Два вызова PublishOrderPaid с одним заказом → два разных EventID.
	// Это важно для идемпотентности у консьюмеров: они могут дедуплицировать
	// по EventID, зная что обработали именно это конкретное событие.
	eventID := uuid.New().String()

	event := events.OrderEvent{
		EventID:     eventID,
		EventType:   eventType,
		OccurredAt:  time.Now().UTC(), // UTC! Всегда UTC в событиях для консистентности
		OrderID:     order.ID.String(),
		UserID:      order.UserID.String(),
		Status:      string(order.Status),
		TotalAmount: order.TotalAmount.Amount,
		Currency:    string(order.TotalAmount.Currency),
	}

	// Сериализуем событие в JSON.
	// json.Marshal возвращает ошибку только если структура содержит
	// несериализуемые типы (функции, каналы, циклические ссылки).
	// OrderEvent содержит только примитивы и string → ошибка теоретически невозможна,
	// но обрабатываем для корректности.
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal order event: %w", err)
	}

	// Ключ партиционирования = OrderID (в байтах).
	//
	// ┌────────────────────────────────────────────────────────────────────────┐
	// │ ПОЧЕМУ OrderID КАК КЛЮЧ?                                                │
	// └────────────────────────────────────────────────────────────────────────┘
	//
	// Kafka гарантирует порядок ВНУТРИ одной партиции.
	// Если все события одного заказа попадают в одну партицию (через hash ключа),
	// то консьюмер видит их в правильном порядке:
	//   placed → paid → shipped → delivered
	//
	// Без ключа (nil key): round-robin по партициям.
	// placed может попасть в партицию 0, paid — в партицию 2.
	// Консьюмер с партиции 2 увидит "paid" раньше чем "placed".
	// Для event sourcing и уведомлений это критично.
	key := []byte(order.ID.String())

	msg := kafka.Message{
		Key:   key,
		Value: payload,
	}

	if err := p.producer.Publish(ctx, p.topic, msg); err != nil {
		// Логируем ошибку, но НЕ прерываем бизнес-операцию.
		//
		// ┌──────────────────────────────────────────────────────────────────────┐
		// │ ПОЧЕМУ НЕ ВОЗВРАЩАЕМ ОШИБКУ НАРУЖУ?                                   │
		// └──────────────────────────────────────────────────────────────────────┘
		//
		// Заказ уже создан и сохранён в БД. Транзакция закоммичена.
		// Если мы вернём ошибку → OrderService вернёт ошибку → HTTP вернёт 500.
		// Пользователь увидит ошибку, хотя его заказ СОЗДАН УСПЕШНО.
		// Это хуже, чем потеря события.
		//
		// Правило: Kafka-событие = best-effort side effect, не часть бизнес-транзакции.
		// Бизнес-операция не должна падать из-за недоступности Kafka.
		//
		// В production: для критичных событий используют Outbox Pattern (следующая итерация).
		// Сейчас: логируем ошибку и продолжаем.
		l := logger.FromContext(ctx)
		l.Errorw("failed to publish order event",
			"event_type", eventType,
			"order_id", order.ID.String(),
			"error", err,
		)
		// Возвращаем ошибку наружу, OrderService сам решит что делать
		return fmt.Errorf("publish %s event: %w", eventType, err)
	}

	return nil
}

// ══════════════════════════════════════════════════════════════════════════════
// NO-OP IMPLEMENTATION: для деплоев без Kafka
// ══════════════════════════════════════════════════════════════════════════════

// NoOpOrderPublisher — заглушка, ничего не публикует.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ КОГДА ИСПОЛЬЗОВАТЬ?                                                         │
// └──────────────────────────────────────────────────────────────────────────┘
//
// 1. Локальная разработка без Kafka (нет docker-compose up kafka)
// 2. Тесты, где Kafka не нужна
// 3. Деплой в окружение, где Kafka ещё не настроена
//
// Передаётся в OrderService вместо KafkaOrderPublisher:
//
//	var publisher events.OrderEventPublisher = &events.NoOpOrderPublisher{}
//	orderService := service.NewOrderService(..., publisher)
//
// Система работает, просто без событий.
type NoOpOrderPublisher struct{}

func (n *NoOpOrderPublisher) PublishOrderPlaced(_ context.Context, _ *entity.Order) error {
	return nil
}
func (n *NoOpOrderPublisher) PublishOrderPaid(_ context.Context, _ *entity.Order) error {
	return nil
}
func (n *NoOpOrderPublisher) PublishOrderShipped(_ context.Context, _ *entity.Order) error {
	return nil
}
func (n *NoOpOrderPublisher) PublishOrderDelivered(_ context.Context, _ *entity.Order) error {
	return nil
}
func (n *NoOpOrderPublisher) PublishOrderCancelled(_ context.Context, _ *entity.Order) error {
	return nil
}
