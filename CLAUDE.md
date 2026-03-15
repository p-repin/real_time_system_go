# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## General Rules

- Всегда отвечай на русском языке
- Разработка ведётся на ОС Windows 10
- Не редактируй файл `.env` напрямую — вместо этого сообщай, какие переменные нужно добавить или изменить
- Используй Context7 MCP-сервер для доступа к документации библиотек
- На Windows для MCP-серверов в `.mcp.json` нужно использовать `"command": "cmd"` с `"args": ["/c", "npx", ...]` вместо прямого вызова `npx`, иначе сервер не запустится

## MCP Servers

Проект использует следующие MCP-серверы (настроены в `.mcp.json`):

- **context7** — получение актуальной документации и примеров кода для любых библиотек
- **postgres** — выполнение read-only SQL-запросов к базе данных проекта
- **playwright** — браузерная автоматизация (снимки страниц, клики, навигация, скриншоты)

**Правила работы с MCP:**
- При добавлении нового MCP-сервера — обязательно обновляй этот раздел в CLAUDE.md
- При использовании `postgres` MCP будь аккуратен: выполняй только SELECT-запросы, не модифицируй данные; перед выполнением запроса убедись, что он безопасен и не затронет продакшен-данные
- Для получения документации библиотек всегда используй `context7` (сначала `resolve-library-id`, затем `query-docs`)

## Build & Run Commands

```bash
# ══════════════════════════════════════════════════════════════════════════════
# BUILD & RUN
# ══════════════════════════════════════════════════════════════════════════════

# Build
go build -o real_time_system ./cmd/app

# Run locally (требует PostgreSQL и .env)
go run ./cmd/app

# Run tests
go test ./...

# Run a single test
go test ./domain/entity -run TestFunctionName

# Tidy dependencies
go mod tidy

# ══════════════════════════════════════════════════════════════════════════════
# SWAGGER
# ══════════════════════════════════════════════════════════════════════════════

# Генерация документации (после изменений в handlers)
swag init -g cmd/app/main.go --parseDependency --parseInternal

# Swagger UI доступен по адресу:
# http://localhost:8080/swagger/index.html

# ══════════════════════════════════════════════════════════════════════════════
# DOCKER
# ══════════════════════════════════════════════════════════════════════════════

# Сборка образа
docker build -t real-time-system:latest .

# Запуск с docker-compose (app + PostgreSQL + Kafka + Observability stack)
docker-compose up -d

# Логи
docker-compose logs -f app

# Остановка
docker-compose down

# Очистка volumes (удалит данные PostgreSQL!)
docker-compose down -v

# ══════════════════════════════════════════════════════════════════════════════
# KUBERNETES
# ══════════════════════════════════════════════════════════════════════════════

# Применение всех манифестов
kubectl apply -f k8s/namespace.yaml
kubectl apply -f k8s/configmap.yaml -n real-time-system
kubectl apply -f k8s/secret.yaml -n real-time-system
kubectl apply -f k8s/deployment.yaml -n real-time-system
kubectl apply -f k8s/service.yaml -n real-time-system
kubectl apply -f k8s/ingress.yaml -n real-time-system

# Проверка статуса
kubectl get pods -n real-time-system
kubectl get svc -n real-time-system

# Логи
kubectl logs -f deployment/app -n real-time-system

# Port-forward для локального доступа (без Ingress)
kubectl port-forward svc/app-service 8080:80 -n real-time-system
```

## Environment Configuration

The app requires PostgreSQL, Kafka, ClickHouse. Set variables via `.env` or environment:

**PostgreSQL:**
- `POSTGRES_USERNAME`, `POSTGRES_PASSWORD`, `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_NAME`, `POSTGRES_CONNECT_TIMEOUT`

**HTTP Server:**
- `HTTP_PORT` (default: 8080)
- `HTTP_READ_TIMEOUT` (default: 15s)
- `HTTP_WRITE_TIMEOUT` (default: 15s)
- `HTTP_IDLE_TIMEOUT` (default: 60s)
- `HTTP_SHUTDOWN_TIMEOUT` (default: 30s)

**Kafka:**
- `KAFKA_BROKERS` (default: localhost:9094)
- `KAFKA_TOPIC_ORDERS` (default: orders)

**Observability:**
- `JAEGER_ENDPOINT` (default: localhost:4317)
- `SERVICE_NAME` (default: real-time-system)
- `TRACING_ENABLED` (default: true)

**ClickHouse (аналитика):**
- `CLICKHOUSE_HOST` (default: localhost)
- `CLICKHOUSE_PORT` (default: 9000, Native TCP)
- `CLICKHOUSE_DATABASE` (default: analytics)
- `CLICKHOUSE_USERNAME` (default: default)
- `CLICKHOUSE_PASSWORD` (default: "")
- `CLICKHOUSE_CONNECT_TIMEOUT` (default: 10s)
- `CLICKHOUSE_BATCH_SIZE` (default: 100)
- `CLICKHOUSE_FLUSH_INTERVAL` (default: 5s)

## Architecture

This is a Go 1.25 project (`real_time_system` module) following Clean Architecture with DDD principles.

**Layers:**
- `cmd/app/` — Entry point, Swagger annotations
- `domain/errors.go` — Sentinel errors + DomainError с HTTP-кодами
- `domain/entity/` — Core business entities (User, Product, Order, Cart)
- `domain/value_objects/` — Immutable value objects (Money)
- `domain/repository/` — Repository interfaces
- `internal/config/` — Environment-based config (PostgreSQL, HTTP, Observability)
- `internal/logger/` — Structured logging via Zap
- `internal/server/` — Application lifecycle, dependency injection, graceful shutdown
- `internal/controller/http/` — HTTP handlers, middleware, router (chi)
- `internal/controller/http/middleware/` — Request ID, Logging middleware (с trace_id корреляцией)
- `internal/repository/postgres/` — PostgreSQL repository implementations
- `internal/service/` — Business logic, orchestration
- `internal/service/dto/` — Request/Response DTOs, mappers
- `internal/worker/` — Concurrency patterns (Worker Pool, Fan-Out/Fan-In, Pipeline)
- `internal/resilience/` — Resilience patterns (Semaphore)
- `internal/observability/` — Prometheus metrics, OpenTelemetry tracing
- `internal/events/` — Kafka event publishers/consumers
- `internal/analytics/` — ClickHouse analytics (consumer + repository, batch insert)
- `pkg/client/` — Reusable clients (PostgreSQL pool)
- `pkg/kafka/` — Kafka producer/consumer wrappers
- `docs/` — Auto-generated Swagger documentation
- `k8s/` — Kubernetes manifests
- `observability/` — Configs for Prometheus, Grafana, Jaeger, Loki, Promtail

**Key patterns:**
- Factory methods for entity creation with validation
- Thread-safe Cart operations using `sync.RWMutex`
- Order status state machine (O(1) map-based transitions)
- Sentinel errors + DomainError for programmatic error handling
- Typed IDs (UserID, ProductID, etc.) with sql.Scanner/driver.Valuer
- SOFT DELETE with `deleted_at` + partial unique indexes
- DTO pattern for API isolation + partial update (pointer-based)
- Data Enrichment pattern (ProductName в CartItemResponse)
- Snapshot Pattern (ProductName в OrderItem — данные на момент покупки)
- Graceful shutdown with configurable timeout
- Event-Driven Architecture (Kafka — order events)
- Semaphore pattern (channel-based concurrent limiter)

**HTTP Layer:**
- chi router with middleware chain
- Request ID (X-Request-ID) for tracing
- Structured request/response logging с trace_id/span_id корреляцией
- Centralized error handling (DomainError → HTTP status)
- Swagger UI at `/swagger/index.html`
- OpenTelemetry HTTP middleware (otelhttp — автоматические span'ы)
- Prometheus metrics middleware (RED method: Rate, Errors, Duration)

**Observability Stack:**
- Prometheus — метрики (RED method: `rts_http_requests_total`, `rts_http_request_duration_seconds`, `rts_http_requests_in_flight`)
- Jaeger — distributed tracing через OpenTelemetry (OTLP gRPC)
- Loki + Promtail — агрегация логов из Docker контейнеров
- Grafana — единый UI для метрик, трейсов и логов (провижининг datasources + dashboards)
- Корреляция: Logs → Traces (trace_id в логах → ссылка на Jaeger в Grafana)

## Architecture Guide

**📚 См. [ARCHITECTURE_GUIDE.md](./ARCHITECTURE_GUIDE.md)** — подробный справочник архитектурных решений проекта.

Этот документ содержит:
- **Domain vs Service:** разделение ответственности, инварианты
- **State Machine:** O(1) проверка переходов через вложенную мапу
- **Конкурентность:** sync.RWMutex, защита от race conditions
- **Value Objects:** иммутабельность, thread-safety
- **Typed IDs:** безопасность типов, sql.Scanner/Valuer
- **Error Handling:** sentinel errors, DomainError с HTTP-кодами
- **Repository Patterns:** Money в БД, UPSERT, LEFT JOIN
- **DTO Patterns:** Data Enrichment, MoneyResponse
- **Идемпотентность:** Clear на пустой корзине

**Правило:** при добавлении нового архитектурного решения — обязательно обновляй ARCHITECTURE_GUIDE.md.

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check (K8s probes) |
| GET | `/metrics` | Prometheus scrape endpoint |
| GET | `/swagger/*` | Swagger UI |
| POST | `/api/v1/users` | Create user |
| GET | `/api/v1/users/{id}` | Get user by ID |
| PATCH | `/api/v1/users/{id}` | Update user (partial) |
| DELETE | `/api/v1/users/{id}` | Delete user (soft) |
| POST | `/api/v1/cart/items` | Add item to cart |

**Observability UI:**

| Service | URL | Description |
|---------|-----|-------------|
| Grafana | http://localhost:3000 | Dashboards (admin/admin) |
| Jaeger | http://localhost:16686 | Distributed tracing |
| Prometheus | http://localhost:9090 | Metrics & queries |

## Mentoring Plan — Путь к Senior

Цель проекта: e-com платформа с real-time возможностями (WebSocket, конкурентность).
Роль Claude: ментор, развёрнутые комментарии в коде с объяснением "почему так".

### Прогресс итераций

- [x] **Итерация 1** — Domain-слой: entities, value objects, state machine, sentinel errors
- [x] **Итерация 2+3** — Repository + Service + DTO для User, Product, Cart
- [x] **Итерация 4 (частично)** — HTTP API + DevOps:
  - [x] HTTP Handlers для User (chi router)
  - [x] Middleware (Request ID, Logging, Error handling)
  - [x] Swagger (swaggo/swag, code-first)
  - [x] Dockerfile (multi-stage, alpine, non-root)
  - [x] docker-compose (app + PostgreSQL)
  - [x] Kubernetes manifests (Deployment, Service, ConfigMap, Secret, Ingress)
  - [ ] Тестирование end-to-end

- [x] **Итерация 5** — CartService + CartHandler:
  - [x] CartService (оркестрация Cart + CartItem + Product)
  - [x] FindByIDs в ProductRepository (batch query для data enrichment)
  - [x] CartHandler со всеми endpoints
  - [x] Data Enrichment (ProductName в ответе)
  - [x] Все Cart endpoints (GetCart, UpdateQuantity, RemoveFromCart, ClearCart)

- [x] **Итерация 5.5** — Подготовка к транзакциям:
  - [x] Querier интерфейс (абстракция для Pool и Tx)
  - [x] Рефакторинг всех репозиториев на Querier
  - [x] Миграция orders + order_items
  - [x] OrderRepository с Create, FindByID, FindByUserID, Update, Delete
  - [x] DecrementStock в ProductRepository (атомарное обновление stock)

- [x] **Итерация 6** — OrderService + OrderHandler:
  - [x] Order DTO (OrderResponse, OrderItemResponse, UpdateStatusRequest)
  - [x] Snapshot Pattern (ProductName в OrderItem entity)
  - [x] Миграция для product_name в order_items
  - [x] OrderRepository обновлён для Snapshot
  - [x] OrderService с PlaceOrder (полная транзакция)
  - [x] Action-based методы (PayOrder, ShipOrder, DeliverOrder, CancelOrder)
  - [x] UpdateStatus (универсальный метод)
  - [x] OrderHandler (POST, GET, PATCH status) + Swagger
  - [x] Миграции применены (orders, order_items, product_name)
  - [x] Баги исправлены (nil map, currency mismatch, timestamps)

- [x] **Итерация 7** — Concurrency & Resilience Patterns:
  - [x] Generic Worker Pool (`internal/worker/pool.go`)
  - [x] TrySubmit (context-aware submit)
  - [x] Тесты: BasicFlow, ContextCancel
  - [x] Fan-Out/Fan-In (`internal/worker/fanout.go`)
  - [x] Pipeline / Stage (`internal/worker/pipeline.go`)
  - [x] Semaphore (`internal/resilience/semaphore.go`) — Acquire, Release, TryAcquire

- [x] **Итерация 7.5** — Observability Stack:
  - [x] Prometheus метрики (RED method): requests_total, request_duration, requests_in_flight
  - [x] Prometheus metrics middleware + `/metrics` endpoint
  - [x] OpenTelemetry tracing → Jaeger (OTLP gRPC)
  - [x] Трейсинг в PlaceOrder (span'ы для транзакции, DecrementStock)
  - [x] Logging middleware: корреляция с trace_id/span_id
  - [x] ObservabilityConfig (JAEGER_ENDPOINT, SERVICE_NAME, TRACING_ENABLED)
  - [x] docker-compose: Prometheus, Jaeger, Loki, Promtail, Grafana
  - [x] Grafana: провижининг datasources (Prometheus, Jaeger, Loki) + dashboard
  - [x] Prometheus config (scrape app + self-monitoring)
  - [x] Loki + Promtail для агрегации логов
  - [x] Graceful shutdown TracerProvider (flush span'ов)

- [x] **Итерация 8** — ClickHouse Analytics:
  - [x] ClickHouseConfig (env-based, batch size + flush interval)
  - [x] pkg/client/clickhouse.go (Native TCP, LZ4 compression, connection pool)
  - [x] InitSchema (order_events, order_item_events — MergeTree, TTL, partitioning)
  - [x] OrderEvent расширен: ItemsCount + Items (только для order.placed)
  - [x] analytics/repository.go (batch insert: PrepareBatch → Append → Send)
  - [x] analytics/consumer.go (Kafka → buffer → ClickHouse, time-or-size flush)
  - [x] Интеграция в server.go (non-fatal init, graceful shutdown)
  - [x] ClickHouse в docker-compose (clickhouse-server:24.8)
  - [x] Grafana datasource + analytics dashboard (воронка, GMV, топ товаров)

- [ ] **Итерация 9** — WebSocket для real-time notifications

### 🎯 Текущий статус

**Дата:** 2026-03-15

**Что сделано:**

1. **ClickHouse Analytics (Итерация 8):**
   - ✅ `internal/config/clickhouse.go` — ClickHouseConfig с batch параметрами
   - ✅ `pkg/client/clickhouse.go` — клиент (Native TCP, LZ4, connection pool)
   - ✅ `InitSchema` — создание таблиц при старте (MergeTree, partitioning, TTL 12 мес.)
   - ✅ `domain/events/order_event.go` — расширен ItemsCount + Items[]
   - ✅ `internal/events/order_publisher.go` — Items включаются для order.placed
   - ✅ `internal/analytics/repository.go` — batch insert (PrepareBatch → Append → Send)
   - ✅ `internal/analytics/consumer.go` — буферизированный consumer (time-or-size flush)
   - ✅ `internal/server/server.go` — интеграция (non-fatal, graceful shutdown)
   - ✅ `docker-compose.yaml` — ClickHouse сервис + Grafana ClickHouse плагин
   - ✅ Grafana: ClickHouse datasource + analytics dashboard (воронка, GMV, топ товаров)

**Файлы:**
```
internal/worker/
├── pool.go           # ✅ Generic Worker Pool
├── pool_test.go      # ✅ Тесты
├── fanout.go         # ✅ Fan-Out/Fan-In
├── fanout_test.go    # ✅ Тесты
├── pipeline.go       # ✅ Pipeline / Stage
└── pipeline_test.go  # ✅ Тесты

internal/resilience/
├── semaphore.go      # ✅ Channel-based semaphore
└── semaphore_test.go # ✅ Тесты

internal/observability/
├── metrics.go        # ✅ Prometheus RED metrics + middleware
└── tracing.go        # ✅ OpenTelemetry → Jaeger

internal/analytics/
├── consumer.go       # ✅ Kafka → ClickHouse (buffered, time-or-size flush)
└── repository.go     # ✅ Batch insert (PrepareBatch + Append + Send)

pkg/client/
├── postgres.go       # ✅ PostgreSQL pool
└── clickhouse.go     # ✅ ClickHouse (Native TCP, LZ4, InitSchema)

observability/
├── prometheus/prometheus.yml     # ✅ Scrape config
├── grafana/datasources/          # ✅ Prometheus, Jaeger, Loki, ClickHouse
├── grafana/dashboards/           # ✅ RTS Overview + Analytics dashboard
├── loki/loki.yml                 # ✅ Log storage config
└── promtail/promtail.yml         # ✅ Docker log collection
```

---

### 📅 План на следующую сессию

**Цель:** Circuit Breaker, Retry, WebSocket.

#### 🎯 Паттерны для реализации

**1. ✅ Worker Pool — ГОТОВ**
**2. ✅ Fan-Out/Fan-In — ГОТОВ**
**3. ✅ Pipeline / Stage — ГОТОВ**
**4. ✅ Semaphore — ГОТОВ**
**5. ✅ Observability Stack — ГОТОВ**
**6. ✅ ClickHouse Analytics — ГОТОВ**

**7. Circuit Breaker — устойчивость к сбоям**
**8. Retry с Exponential Backoff**
**9. WebSocket для real-time notifications**

---

### 🎓 Что изучили

**HTTP Layer:**
- chi router (lightweight, idiomatic Go)
- Middleware pattern (chain of responsibility)
- Request ID для distributed tracing
- Centralized error handling
- Helper-функции с multiple return values: `headerToUserID() (UserID, bool)`

**Swagger:**
- Code-first approach (swaggo/swag)
- Annotations в комментариях Go
- Swagger UI интеграция
- Важно: нет пустых строк между аннотациями и функцией!

**Service Layer:**
- Оркестрация нескольких репозиториев
- Get-Or-Create pattern (lazy creation)
- Data Enrichment (batch loading для N+1)
- DRY: helper-методы для дублирующегося кода
- Валидация в service (stock check требует данных из Product)
- Идемпотентность операций (Clear на пустой корзине)

**Транзакции и Querier:**
- `Querier` interface — абстракция для `*pgxpool.Pool` и `pgx.Tx`
- Почему нужен интерфейс: Pool и Tx — разные типы, но одинаковые методы
- Репозитории принимают `Querier` → могут работать в транзакции
- Транзакции в Service Layer (оркестрация нескольких агрегатов)
- Локальные транзакции vs 2PC vs SAGA (распределённые системы)

**Атомарные операции в БД:**
- `UPDATE ... WHERE stock >= $1` — атомарное уменьшение без race condition
- Проверка существования после UPDATE с 0 affected rows

**Snapshot Pattern vs Data Enrichment:**
- **Data Enrichment (Cart):** ProductName загружается из Product при запросе
  - Плюс: всегда актуальные данные
  - Минус: нужен JOIN или batch load
- **Snapshot (Order):** ProductName сохраняется в OrderItem при создании
  - Плюс: исторические данные, быстрый read
  - Минус: денормализация
- Когда что использовать:
  - Cart = временное состояние → Enrichment (актуальные данные)
  - Order = юридический документ → Snapshot (данные на момент покупки)

**API Design: Status vs Actions:**
- **Подход 1 (Status):** `PATCH /orders/{id}/status { "status": "paid" }`
  - Простой, универсальный
  - Нельзя передать доп. данные (paymentID)
- **Подход 3 (Actions):** `POST /orders/{id}/pay { "payment_id": "..." }`
  - Семантически понятные URL
  - Каждый action принимает свои параметры
  - Легко добавлять side effects (email, интеграции)
- Production: Actions лучше, но сложнее

**Ownership Check:**
- Проверка `order.UserID == userID` перед операцией
- Возвращать `NotFound` вместо `Forbidden` (не раскрываем существование чужих ресурсов)

**Repository Pitfalls (важные баги!):**
- **Nil map panic:** `var entity Entity` создаёт struct с `map = nil`
  - Решение: всегда инициализировать `entity.Items = make(map[K]V)`
- **Zero-value Currency:** Money с пустой Currency ("") несовместима с RUB
  - Решение: `entity.TotalPrice = value_objects.Zero(RUB)`
- **Timestamp types:** pgx сканирует timestamp в `time.Time`, не в `string`
  - Nullable timestamp → `*time.Time`
- **Правило:** После Scan из БД всегда проверять инициализацию composite types!

**Go Generics:**
- `Pool[T any, R any]` — параметры типов в квадратных скобках
- `any` = constraint "любой тип" (замена `interface{}`)
- Идеальны для утилитарных структур (пулы, очереди, кэши)
- Компилятор выводит типы при использовании

**Concurrency Patterns:**
- **Worker Pool:** N горутин читают из общего канала задач
  - `sync.WaitGroup` для ожидания завершения воркеров
  - Закрытие `results` после `wg.Wait()` в отдельной горутине
  - Порядок: Start → Submit → Close → range Results
- **Fan-Out/Fan-In:** одна горутина на задачу, результаты в один канал
  - Горутины ПИШУТ в канал, основная функция ЧИТАЕТ (разделение ответственности)
  - Буфер = len(tasks) → отправка не блокируется → select при записи избыточен
  - Порядок НЕ гарантирован → проверять сумму/количество
- **Pipeline / Stage:** цепочка этапов, каждый — горутина + канал
  - Канал без буфера → backpressure (медленный потребитель замедляет всю цепочку)
  - Select при записи НУЖЕН (unbuffered chan может заблокироваться)
  - Порядок ГАРАНТИРОВАН → можно проверять поэлементно
  - Отправитель тоже должен слушать ctx.Done() (unbuffered chan!)
- **select с каналами:**
  - Два case в одном select — проверяются одновременно (атомарно)
  - `default` — выполняется мгновенно, потом select завершён, защиты нет
  - Два ready case → Go выбирает случайно → flaky тесты!
  - Правило: конкурирующие операции — в одном select!
- **Goroutine leak:** горутина блокируется навсегда (никто не читает/пишет в канал)
  - `Submit` (простой) — блокируется если буфер полон и воркеры мертвы
  - `TrySubmit` (context-aware) — select с `ctx.Done()`, не блокируется
- **Закрытый канал:** отдаёт все оставшиеся значения, потом zero-value с ok=false
- **Тестирование concurrent кода:**
  - Проверять сумму/количество, не порядок (порядок не гарантирован)
  - `-race` flag для race detector (на Windows нужен TDM-GCC)
  - bufferSize > количества задач для предсказуемости тестов
  - Handler должен слушать ctx для корректного теста отмены

**ClickHouse (OLAP аналитика):**
- **Columnar storage:** читает только нужные колонки → на порядки быстрее для агрегаций
- **MergeTree engine:** append-only, автоматический merge фоновых parts
- **"Too many parts" проблема:** каждый INSERT = новый part → max 1 INSERT/sec на таблицу
  - Решение: batch insert (PrepareBatch → Append × N → Send = один part)
- **LowCardinality(String):** словарная оптимизация для колонок с <10K уникальных значений
- **PARTITION BY toYYYYMM:** физическое разделение данных для быстрого pruning и retention
  - DROP PARTITION мгновенно удаляет месяц данных (vs DELETE = пометка строк)
- **ORDER BY:** от менее кардинальных к более → лучше сжатие и гранулярный пропуск
- **TTL:** автоудаление старых данных при merge (без cron)
- **DateTime64(3, 'UTC'):** миллисекунды, UTC для консистентности
- **Деньги как Int64 (копейки):** быстрее Decimal для агрегаций
- **Native TCP (9000) vs HTTP (8123):** native для batch insert, HTTP для Grafana
- **At-least-once дубликаты:** count(DISTINCT event_id) вместо count(*)
- **Time-or-size buffering:** flush по размеру (BatchSize) или по таймеру (FlushInterval)
- **Graceful shutdown:** finalFlush буфера → INSERT в ClickHouse → Commit Kafka → Close
- **Non-fatal инициализация:** ClickHouse недоступен → приложение работает без аналитики

**Resilience Patterns:**
- **Semaphore:** channel-based, Acquire (context-aware), TryAcquire (non-blocking)
  - `chan struct{}` с буфером = maxConcurrent
  - select с ctx.Done() для отмены

**Observability:**
- **RED Method** (Rate, Errors, Duration) — минимальный набор метрик для сервиса
- **Prometheus:** pull-based, promauto для регистрации, histogram vs summary
  - Histogram для агрегации между инстансами (histogram_quantile)
  - Кастомные buckets для web API latency
  - High cardinality problem: нормализация URL в labels
- **OpenTelemetry + Jaeger:** OTLP gRPC, BatchSpanProcessor, W3C TraceContext propagator
  - Span'ы в критических путях (PlaceOrder → TX.DecrementStock)
  - span.RecordError() + span.SetStatus() — оба нужны!
  - Атрибуты span'а для поиска в Jaeger (user.id, order.id, items.count)
- **Logs ↔ Traces корреляция:** trace_id в логах → ссылка на Jaeger в Grafana
- **Grafana provisioning:** datasources + dashboards через файлы конфигурации
- **Loki + Promtail:** индексация labels (не содержимого), Docker socket для сбора логов
- **Порядок shutdown:** TracerProvider (flush span'ов) → Kafka → PostgreSQL

**Docker:**
- Multi-stage builds (builder + runtime)
- Layer caching (go.mod → go mod download → COPY . .)
- Security: non-root user, read-only filesystem
- HEALTHCHECK directive

**Kubernetes:**
- Declarative configuration (YAML manifests)
- ConfigMap vs Secret
- Liveness vs Readiness probes
- Resource requests/limits
- Rolling update strategy
- Ingress для HTTP routing

---

### 💪 Готовность к production

**Готово:**
- ✅ HTTP API для User (CRUD)
- ✅ HTTP API для Cart (все endpoints)
- ✅ **HTTP API для Order (все endpoints):**
  - ✅ PlaceOrder, GetOrder, GetUserOrders, UpdateStatus
  - ✅ Swagger аннотации
- ✅ CartService с полной бизнес-логикой
- ✅ **OrderService — полная реализация:**
  - ✅ PlaceOrder (ACID-транзакция)
  - ✅ GetOrder, GetUserOrders
  - ✅ UpdateStatus (state machine)
  - ✅ Production Actions (Pay, Ship, Deliver, Cancel)
- ✅ Data Enrichment (ProductName в CartResponse)
- ✅ Snapshot Pattern (ProductName в OrderItem)
- ✅ Querier интерфейс (поддержка транзакций)
- ✅ DecrementStock (атомарное обновление)
- ✅ Swagger документация
- ✅ Docker image (~20MB)
- ✅ Kubernetes manifests
- ✅ Health checks
- ✅ Graceful shutdown
- ✅ Structured logging

- ✅ **Generic Worker Pool** с TrySubmit и тестами
- ✅ **Fan-Out/Fan-In** — параллельная обработка задач
- ✅ **Pipeline / Stage** — цепочка этапов с backpressure
- ✅ **Semaphore** — channel-based concurrent limiter
- ✅ **Observability Stack:**
  - ✅ Prometheus метрики (RED method) + middleware
  - ✅ OpenTelemetry tracing → Jaeger (OTLP gRPC)
  - ✅ Loki + Promtail (агрегация логов)
  - ✅ Grafana (unified UI, provisioned datasources + dashboards)
  - ✅ Корреляция logs ↔ traces (trace_id)

- ✅ **ClickHouse Analytics:**
  - ✅ Kafka → ClickHouse pipeline (buffered consumer)
  - ✅ order_events + order_item_events таблицы (MergeTree, TTL, partitioning)
  - ✅ Batch insert (PrepareBatch → Append → Send)
  - ✅ Grafana analytics dashboard (воронка, GMV, топ товаров, % отмен)

**Следующие шаги:**
- ⏳ Circuit Breaker + Retry (Exponential Backoff)
- ⏳ WebSocket для real-time notifications
- ⏳ Integration tests
- ⏳ CI/CD pipeline
