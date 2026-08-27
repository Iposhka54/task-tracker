# Техническое Задание: "TaskFlow" — Распределённый Планировщик Задач

## 1. Общее описание и архитектурное видение

**TaskFlow** — это многопользовательский планировщик задач на Go. Система разбита на несколько сервисов там, где это даёт реальную пользу: разные жизненные циклы, разные данные, разный способ коммуникации.

### 1.1. Принципы разбиения

Сервисы выделяются не «по таблице в БД», а по границам ответственности:

| Сервис | Почему отдельно |
|---|---|
| **User Service** | Идентичность: регистрация, сессии, профиль. Остальные сервисы зависят от пользователя, но не владеют им. |
| **Task Service** | Ядро продукта: задачи и категории — одна предметная область, категории без задач бессмысленны. |
| **Notification Service** | Доставка сообщений (in-app, WebSocket, email). Асинхронный consumer, не request/response CRUD. |
| **Scheduler Service** | Фоновые джобы по расписанию. Другой жизненный цикл, чем у HTTP API. |
| **API Gateway** | Единая точка входа, auth middleware, rate limit, маршрутизация. |

Что **не** выносим в отдельные сервисы:

- **Auth ≠ User** — логин и профиль живут в одном bounded context. Отдельный auth-service порождает синхронизацию `users` между двумя БД без выигрыша.
- **Category ≠ Task** — категория это атрибут задачи. gRPC на каждый `ListCategories` ради CRUD из 6 полей — лишняя сложность.
- **Email ≠ Notification** — email это канал доставки, не отдельный домен. Шаблоны и SMTP — часть notification-service.
- **Audit Service** — не нужен. Действия пишутся в структурированные логи и трейсы. Отдельный сервис + ClickHouse ради истории CRUD — overkill для этого проекта.

### 1.2. Ключевые архитектурные решения

1. **API Gateway** — единая точка входа для клиентов
2. **Database per Service** — каждый сервис владеет своей PostgreSQL-схемой
3. **Синхронная коммуникация** — gRPC для внутренних вызовов
4. **Асинхронная коммуникация** — Apache Kafka для событий
5. **Service Discovery** — Consul
6. **Observability** — логи, метрики, трейсинг
7. **Circuit Breaker** — отказоустойчивость при межсервисных вызовах

## 2. Архитектура системы

### 2.1. Список сервисов

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              CLIENT LAYER                                   │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐                   │
│  │ Browser  │  │ Mobile   │  │ Desktop  │  │ 3rd Party │                   │
│  │   SPA    │  │   App    │  │   App    │  │   API    │                   │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘                   │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ HTTPS/REST
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                          API GATEWAY                                        │
│  Rate Limiting · JWT · Routing · Load Balancing · Circuit Breaking          │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
              ┌─────────────────────┼─────────────────────┐
              ▼                     ▼                     ▼
┌─────────────────────┐ ┌─────────────────────┐ ┌─────────────────────┐
│    User Service     │ │    Task Service     │ │ Notification Service│
│                     │ │                     │ │                     │
│  Auth · Profile     │ │  Tasks · Categories │ │  In-app · WS · Email│
│  Settings · Avatar  │ │  Comments · Files   │ │  Templates · SMTP   │
└─────────────────────┘ └─────────────────────┘ └─────────────────────┘
                                                          ▲
                                                          │ Kafka
                                              ┌─────────────────────┐
                                              │  Scheduler Service  │
                                              │  Cron · Reminders   │
                                              └─────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│  PostgreSQL (per svc) · Redis · Kafka · MinIO · Consul                      │
└─────────────────────────────────────────────────────────────────────────────┘
```

Итого **5 процессов**: gateway + 4 бизнес-сервиса.

## 3. Детальное описание сервисов

### 3.1. API Gateway Service

**Порт:** 443 (HTTPS), 80 (HTTP redirect), 8080 в dev

**Технологии:** Go, Echo или Chi, gRPC-Gateway, Redis (rate limiting)

**Структура:**
```
api-gateway/
├── cmd/
│   └── main.go
├── internal/
│   ├── config/
│   ├── middleware/
│   │   ├── auth.go
│   │   ├── ratelimit.go
│   │   ├── circuitbreaker.go
│   │   └── cors.go
│   ├── proxy/
│   │   ├── http_proxy.go
│   │   ├── grpc_proxy.go
│   │   └── websocket.go
│   ├── discovery/
│   │   └── consul.go
│   └── ratelimit/
│       ├── token_bucket.go
│       └── sliding_window.go
├── pkg/
│   ├── logger/
│   ├── metrics/
│   └── jwt/
├── configs/
│   └── config.yaml
├── Dockerfile
└── go.mod
```

**Обязанности:**

1. **Маршрутизация:**
   - `/api/v1/auth/*` → User Service
   - `/api/v1/users/*` → User Service
   - `/api/v1/tasks/*` → Task Service
   - `/api/v1/categories/*` → Task Service
   - `/ws/notifications` → Notification Service (WebSocket)

2. **Rate Limiting:**
   - Token Bucket, состояние в Redis
   - `/api/v1/auth/*`: 10 запросов/минута
   - `/api/v1/tasks/*`: 60 запросов/минута
   - Остальные: 100 запросов/минута

3. **Circuit Breaker:**
   - `github.com/sony/gobreaker` на каждый downstream
   - MaxRequests: 5 (полуоткрытое)
   - Interval: 60 секунд
   - Timeout: 30 секунд (открытое)

4. **Service Discovery:** Consul, только здоровые инстансы

5. **Request ID:** уникальный ID на запрос, заголовок `X-Request-ID`, во все логи

6. **Response Caching:** GET в Redis, инвалидация на POST/PUT/DELETE, TTL 30 секунд

### 3.2. User Service

**Порт:** 9001 (gRPC), 9101 (HTTP/metrics)

**База данных:** PostgreSQL (схема `users`)

**Технологии:** Go, gRPC, JWT, OAuth2, bcrypt, Redis (refresh tokens), MinIO (аватары)

Один сервис владеет аккаунтом целиком: креды, сессии, профиль, настройки.

**Структура:**
```
user-service/
├── api/
│   └── proto/
│       ├── auth.proto
│       ├── user.proto
│       └── health.proto
├── cmd/
│   └── main.go
├── internal/
│   ├── config/
│   ├── model/
│   │   ├── user.go
│   │   ├── token.go
│   │   ├── session.go
│   │   ├── profile.go
│   │   └── settings.go
│   ├── repository/
│   │   ├── user_repo.go
│   │   ├── token_repo.go
│   │   ├── profile_repo.go
│   │   └── settings_repo.go
│   ├── service/
│   │   ├── auth_service.go
│   │   ├── token_service.go
│   │   ├── oauth_service.go
│   │   ├── profile_service.go
│   │   └── settings_service.go
│   ├── security/
│   │   ├── password_hasher.go
│   │   └── jwt_manager.go
│   └── storage/
│       └── minio_client.go
├── migrations/
│   ├── 000001_create_users.up.sql
│   └── 000002_create_profiles.up.sql
├── Dockerfile
└── go.mod
```

**Protobuf контракт:**
```protobuf
syntax = "proto3";

package user.v1;

service AuthService {
  rpc Register(RegisterRequest) returns (RegisterResponse);
  rpc Login(LoginRequest) returns (LoginResponse);
  rpc Logout(LogoutRequest) returns (LogoutResponse);
  rpc RefreshToken(RefreshTokenRequest) returns (RefreshTokenResponse);
  rpc ValidateToken(ValidateTokenRequest) returns (ValidateTokenResponse);
}

service UserService {
  rpc GetProfile(GetProfileRequest) returns (GetProfileResponse);
  rpc UpdateProfile(UpdateProfileRequest) returns (UpdateProfileResponse);
  rpc UploadAvatar(UploadAvatarRequest) returns (UploadAvatarResponse);
  rpc GetUserSettings(GetUserSettingsRequest) returns (GetUserSettingsResponse);
  rpc UpdateUserSettings(UpdateUserSettingsRequest) returns (UpdateUserSettingsResponse);
  rpc GetUserStats(GetUserStatsRequest) returns (GetUserStatsResponse);
}

message User {
  uint64 id = 1;
  string email = 2;
  string username = 3;
  bool is_active = 4;
  string avatar_url = 5;
  string created_at = 6;
  string updated_at = 7;
}

message Profile {
  uint64 user_id = 1;
  string username = 2;
  string full_name = 3;
  string bio = 4;
  string avatar_url = 5;
  string phone = 6;
  string timezone = 7;
  string locale = 8;
  string created_at = 9;
  string updated_at = 10;
}
```

**Особенности:**

1. **JWT с refresh tokens:**
   - Access Token: JWT, 15 минут
   - Refresh Token: UUID в Redis, 30 дней
   - Logout удаляет refresh token

2. **OAuth2:** Google и GitHub через `golang.org/x/oauth2`

3. **Безопасность:**
   - bcrypt (cost=12)
   - brute-force защита через Redis
   - блокировка после 5 неудачных попыток

4. **Профиль и настройки:**
   - ФИО, био, аватар (MinIO), телефон, timezone, locale
   - Уведомления (email / in-app), тема, язык, приватность

5. **Статистика:** количество задач, выполненные, серии — агрегаты из событий `task.events` или по запросу к Task Service (не дублировать source of truth)

6. **Схема БД:**
```sql
CREATE TABLE users (
    id BIGSERIAL PRIMARY KEY,
    email VARCHAR(255) UNIQUE NOT NULL,
    username VARCHAR(50) UNIQUE NOT NULL,
    password_hash VARCHAR(255),
    is_active BOOLEAN DEFAULT true,
    is_verified BOOLEAN DEFAULT false,
    last_login_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE oauth_accounts (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
    provider VARCHAR(50) NOT NULL,
    provider_user_id VARCHAR(255) NOT NULL,
    access_token TEXT,
    refresh_token TEXT,
    expires_at TIMESTAMPTZ,
    UNIQUE(provider, provider_user_id)
);

CREATE TABLE login_attempts (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT REFERENCES users(id),
    ip_address INET,
    success BOOLEAN,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE profiles (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    full_name VARCHAR(255),
    bio TEXT,
    avatar_url VARCHAR(500),
    phone VARCHAR(50),
    timezone VARCHAR(50) DEFAULT 'UTC',
    locale VARCHAR(10) DEFAULT 'en',
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE user_settings (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    email_notifications BOOLEAN DEFAULT true,
    inapp_notifications BOOLEAN DEFAULT true,
    theme VARCHAR(20) DEFAULT 'system',
    language VARCHAR(10) DEFAULT 'en',
    updated_at TIMESTAMPTZ DEFAULT now()
);
```

### 3.3. Task Service

**Порт:** 9002 (gRPC), 9102 (HTTP/metrics)

**База данных:** PostgreSQL (схема `tasks`)

**Технологии:** Go, gRPC, Redis (кэш списков)

Задачи и категории в одном сервисе: `category_id` — обычный FK, без сетевого hop на каждую задачу.

**Структура:**
```
task-service/
├── api/
│   └── proto/
│       ├── task.proto
│       └── category.proto
├── cmd/
│   └── main.go
├── internal/
│   ├── config/
│   ├── model/
│   │   ├── task.go
│   │   ├── category.go
│   │   ├── comment.go
│   │   └── attachment.go
│   ├── repository/
│   │   ├── task_repo.go
│   │   ├── category_repo.go
│   │   ├── comment_repo.go
│   │   └── attachment_repo.go
│   ├── service/
│   │   ├── task_service.go
│   │   ├── category_service.go
│   │   ├── comment_service.go
│   │   └── attachment_service.go
│   └── cache/
│       └── redis_cache.go
├── migrations/
│   ├── 000001_create_categories.up.sql
│   ├── 000002_create_tasks.up.sql
│   ├── 000003_create_comments.up.sql
│   └── 000004_create_attachments.up.sql
├── Dockerfile
└── go.mod
```

**Protobuf контракт:**
```protobuf
syntax = "proto3";

package task.v1;

service TaskService {
  rpc CreateTask(CreateTaskRequest) returns (CreateTaskResponse);
  rpc GetTask(GetTaskRequest) returns (GetTaskResponse);
  rpc UpdateTask(UpdateTaskRequest) returns (UpdateTaskResponse);
  rpc DeleteTask(DeleteTaskRequest) returns (DeleteTaskResponse);
  rpc ListTasks(ListTasksRequest) returns (ListTasksResponse);
  rpc UpdateTaskStatus(UpdateTaskStatusRequest) returns (UpdateTaskStatusResponse);
  rpc AssignTask(AssignTaskRequest) returns (AssignTaskResponse);
  rpc AddComment(AddCommentRequest) returns (AddCommentResponse);
  rpc ListComments(ListCommentsRequest) returns (ListCommentsResponse);
}

service CategoryService {
  rpc CreateCategory(CreateCategoryRequest) returns (CreateCategoryResponse);
  rpc GetCategory(GetCategoryRequest) returns (GetCategoryResponse);
  rpc UpdateCategory(UpdateCategoryRequest) returns (UpdateCategoryResponse);
  rpc DeleteCategory(DeleteCategoryRequest) returns (DeleteCategoryResponse);
  rpc ListCategories(ListCategoriesRequest) returns (ListCategoriesResponse);
  rpc ReorderCategories(ReorderCategoriesRequest) returns (ReorderCategoriesResponse);
}

message Task {
  uint64 id = 1;
  uint64 user_id = 2;
  uint64 category_id = 3;
  string title = 4;
  string description = 5;
  TaskStatus status = 6;
  TaskPriority priority = 7;
  string due_date = 8;
  string completed_at = 9;
  string created_at = 10;
  string updated_at = 11;
  repeated string tags = 12;
}

message Category {
  uint64 id = 1;
  uint64 user_id = 2;
  string name = 3;
  string color = 4;
  string icon = 5;
  int32 position = 6;
  bool is_default = 7;
  string created_at = 8;
  string updated_at = 9;
}

enum TaskStatus {
  TASK_STATUS_UNSPECIFIED = 0;
  TASK_STATUS_TODO = 1;
  TASK_STATUS_IN_PROGRESS = 2;
  TASK_STATUS_DONE = 3;
  TASK_STATUS_ARCHIVED = 4;
}

enum TaskPriority {
  TASK_PRIORITY_UNSPECIFIED = 0;
  TASK_PRIORITY_LOW = 1;
  TASK_PRIORITY_MEDIUM = 2;
  TASK_PRIORITY_HIGH = 3;
  TASK_PRIORITY_URGENT = 4;
}
```

**Особенности:**

1. **Модель задачи:** приоритет, теги, срок, вложения, комментарии, подзадачи
2. **Категории:** цвет, иконка, порядок, дефолтные категории при регистрации пользователя
3. **Кэш:** Redis, Cache-Aside на списки задач, инвалидация при изменениях
4. **Удаление категории:** задачи не удаляются — `category_id` ставится в NULL или в дефолтную

**Схема БД:**
```sql
CREATE TABLE categories (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    name VARCHAR(100) NOT NULL,
    color VARCHAR(7) DEFAULT '#808080',
    icon VARCHAR(50),
    position INT NOT NULL DEFAULT 0,
    is_default BOOLEAN DEFAULT false,
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE tasks (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    category_id BIGINT REFERENCES categories(id) ON DELETE SET NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    status VARCHAR(20) NOT NULL DEFAULT 'todo',
    priority VARCHAR(20) NOT NULL DEFAULT 'medium',
    due_date TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE task_tags (
    task_id BIGINT REFERENCES tasks(id) ON DELETE CASCADE,
    tag VARCHAR(50),
    PRIMARY KEY (task_id, tag)
);

CREATE TABLE comments (
    id BIGSERIAL PRIMARY KEY,
    task_id BIGINT REFERENCES tasks(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL,
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE attachments (
    id BIGSERIAL PRIMARY KEY,
    task_id BIGINT REFERENCES tasks(id) ON DELETE CASCADE,
    file_name VARCHAR(255) NOT NULL,
    file_url VARCHAR(500) NOT NULL,
    file_size BIGINT,
    mime_type VARCHAR(100),
    uploaded_by BIGINT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_tasks_user_status ON tasks(user_id, status);
CREATE INDEX idx_tasks_user_due_date ON tasks(user_id, due_date);
CREATE INDEX idx_categories_user ON categories(user_id, position);
```

### 3.4. Notification Service

**Порт:** 9003 (gRPC), 9103 (HTTP/metrics), 9104 (WebSocket)

**Технологии:** Go, WebSocket, Kafka consumer, Redis pub/sub, SMTP, HTML templates

Один сервис доставки: in-app, realtime по WebSocket и email. Канал выбирается по настройкам пользователя и типу события.

**Структура:**
```
notification-service/
├── api/
│   └── proto/
│       └── notification.proto
├── cmd/
│   └── main.go
├── internal/
│   ├── config/
│   ├── model/
│   ├── consumer/
│   │   └── kafka_consumer.go
│   ├── delivery/
│   │   ├── websocket.go
│   │   ├── inapp.go
│   │   └── email.go
│   ├── template/
│   │   ├── welcome.go
│   │   ├── reminder.go
│   │   └── report.go
│   ├── repository/
│   └── service/
├── templates/
│   ├── welcome.html
│   ├── daily_report.html
│   ├── weekly_report.html
│   └── task_reminder.html
├── Dockerfile
└── go.mod
```

**Protobuf контракт:**
```protobuf
syntax = "proto3";

package notification.v1;

service NotificationService {
  rpc Subscribe(SubscribeRequest) returns (SubscribeResponse);
  rpc Unsubscribe(UnsubscribeRequest) returns (UnsubscribeResponse);
  rpc ListNotifications(ListNotificationsRequest) returns (ListNotificationsResponse);
  rpc MarkAsRead(MarkAsReadRequest) returns (MarkAsReadResponse);
  rpc MarkAllAsRead(MarkAllAsReadRequest) returns (MarkAllAsReadResponse);
}
```

**Каналы доставки:**

| Событие | In-app / WS | Email |
|---|---|---|
| Задача назначена | да | опционально |
| Новый комментарий | да | опционально |
| Приближается срок | да | да |
| Ежедневный / недельный отчёт | нет | да |
| Приветственное письмо | нет | да |

**Email:**
- SMTP + HTML/text шаблоны
- Worker pool, graceful shutdown
- Retry с exponential backoff, dead letter queue

**Realtime:**
- WebSocket с клиентами
- Redis pub/sub между инстансами
- Опционально FCM для мобильных push

### 3.5. Scheduler Service

**Порт:** 9004 (gRPC), 9104 (HTTP/metrics)

**Технологии:** Go, gRPC, cron, Kafka producer

Не ходит в чужие БД. Ставит джобы и публикует события в Kafka: «пора напомнить / пора отчёт». Notification и Task сами решают, что с этим делать.

```protobuf
syntax = "proto3";

package scheduler.v1;

service SchedulerService {
  rpc ScheduleJob(ScheduleJobRequest) returns (ScheduleJobResponse);
  rpc CancelJob(CancelJobRequest) returns (CancelJobResponse);
  rpc ListJobs(ListJobsRequest) returns (ListJobsResponse);
}

message ScheduleJobRequest {
  string job_id = 1;
  string cron_expression = 2;
  string job_type = 3;
  string payload = 4;
}
```

**Типы джоб:**
- `daily_report` — ежедневный отчёт
- `weekly_report` — еженедельный отчёт
- `task_reminder` — напоминание о сроке
- `cleanup_old_tasks` — архивация старых задач (событие в `task.events`)

**Расписания:** `0 0 * * *`, `0 0 * * 1`, `0 * * * *`, `*/15 * * * *`

## 4. Инфраструктура и данные

### 4.1. Базы данных

**PostgreSQL:** отдельная БД/схема на сервис (`users`, `tasks`). Notification может хранить inbox уведомлений в своей схеме или обойтись Redis + Postgres — на усмотрение реализации. Scheduler — состояние джоб в своей схеме или в памяти + persist.

**Redis:** кэш, rate limiting, refresh tokens, pub/sub для WebSocket.

**MinIO:** аватары и вложения задач.

**Без ClickHouse.** Аналитика и аудит не являются продуктовой фичей на этом этапе. Логи в ELK / Loki, трейсы в Jaeger.

### 4.2. Kafka

**Топики:**
```
user.events          — регистрация, логин, смена профиля
task.events          — CRUD задач, смена статуса, комментарии
notification.events  — команды на доставку (in-app / email)
scheduler.events     — сработавшие джобы
deadletter.events    — сообщения после исчерпания retry
```

Notification Service — основной consumer `task.events`, `user.events`, `scheduler.events` и `notification.events`.

**Конфигурация (prod):** репликация 3, партиции 12, retention 7 дней, compression snappy. В dev — одноброкерный Kafka.

### 4.3. Service Discovery (Consul)

Health checks, DNS, HTTP API, KV для конфигурации.

## 5. Наблюдаемость (Observability)

### 5.1. Логирование

**Стек:** ELK (Elasticsearch, Logstash, Kibana) или Loki + Grafana — выбрать один, не оба.

**Формат логов:**
```json
{
  "timestamp": "2024-01-15T10:30:00Z",
  "level": "info",
  "service": "task-service",
  "trace_id": "a1b2c3d4e5f6",
  "request_id": "x1y2z3w4v5",
  "method": "CreateTask",
  "duration_ms": 150,
  "user_id": 123,
  "message": "Task created successfully",
  "metadata": {
    "task_id": 456,
    "category_id": 10
  }
}
```

Сбор: Fluent Bit из контейнеров.

### 5.2. Метрики

**Стек:** Prometheus + Grafana

- HTTP/gRPC: latency, request count, error rate
- Бизнес: tasks created, users registered, emails sent
- Инфраструктура: CPU, memory
- Kafka: consumer lag
- Redis: cache hit/miss

Дашборды: Service Overview, API Performance, Database, Kafka, Business KPIs.

### 5.3. Трейсинг

**Стек:** OpenTelemetry + Jaeger

Инструментация: HTTP/gRPC, Kafka produce/consume, SQL, внешние API (SMTP, OAuth, MinIO).

## 6. Docker и Kubernetes

### 6.1. Docker Compose для разработки

```yaml
version: '3.9'

services:
  postgres:
    image: postgres:16-alpine
    ports: ["5432:5432"]
    volumes: ["postgres_data:/var/lib/postgresql/data"]

  redis:
    image: redis:7-alpine
    ports: ["6379:6379"]

  kafka:
    image: confluentinc/cp-kafka:latest
    ports: ["9092:9092"]

  zookeeper:
    image: confluentinc/cp-zookeeper:latest
    ports: ["2181:2181"]

  consul:
    image: consul:latest
    ports: ["8500:8500"]

  minio:
    image: minio/minio:latest
    ports: ["9000:9000", "9001:9001"]

  prometheus:
    image: prom/prometheus
    ports: ["9090:9090"]
    volumes: ["./observability/prometheus.yml:/etc/prometheus/prometheus.yml"]

  grafana:
    image: grafana/grafana
    ports: ["3000:3000"]

  jaeger:
    image: jaegertracing/all-in-one:latest
    ports: ["16686:16686", "14268:14268"]

  api-gateway:
    build: ./api-gateway
    ports: ["8080:8080"]
    depends_on: [consul, redis]

  user-service:
    build: ./user-service
    ports: ["9001:9001", "9101:9101"]
    depends_on: [postgres, redis, consul, minio]

  task-service:
    build: ./task-service
    ports: ["9002:9002", "9102:9102"]
    depends_on: [postgres, redis, consul]

  notification-service:
    build: ./notification-service
    ports: ["9003:9003", "9103:9103", "9104:9104"]
    depends_on: [consul, kafka, redis]

  scheduler-service:
    build: ./scheduler-service
    ports: ["9004:9004", "9105:9105"]
    depends_on: [consul, kafka]
```

ELK в compose не поднимаем по умолчанию — тяжёлый стек. Для локальной разработки достаточно stdout JSON + Jaeger + Prometheus. ELK — опция для staging/prod.

### 6.2. Kubernetes манифесты

```
k8s/
├── namespaces/
│   ├── production.yaml
│   ├── staging.yaml
│   └── development.yaml
├── ingress/
│   └── taskflow-ingress.yaml
├── api-gateway/
├── user-service/
├── task-service/
├── notification-service/
├── scheduler-service/
├── infrastructure/
│   ├── postgres/
│   ├── redis/
│   └── kafka/
└── observability/
    ├── prometheus/
    ├── grafana/
    └── jaeger/
```

**Пример Deployment:**
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: task-service
  namespace: taskflow
spec:
  replicas: 3
  selector:
    matchLabels:
      app: task-service
  template:
    metadata:
      labels:
        app: task-service
    spec:
      containers:
      - name: task-service
        image: taskflow/task-service:latest
        ports:
        - containerPort: 9002
          name: grpc
        - containerPort: 9102
          name: metrics
        env:
        - name: DATABASE_URL
          valueFrom:
            secretKeyRef:
              name: postgres-secret
              key: url
        - name: REDIS_URL
          value: "redis:6379"
        - name: CONSUL_ADDR
          value: "consul:8500"
        readinessProbe:
          grpc:
            port: 9002
          initialDelaySeconds: 10
          periodSeconds: 5
        livenessProbe:
          httpGet:
            path: /health
            port: 9102
          initialDelaySeconds: 30
          periodSeconds: 10
        resources:
          requests:
            memory: "256Mi"
            cpu: "250m"
          limits:
            memory: "512Mi"
            cpu: "500m"
---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: task-service-hpa
  namespace: taskflow
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: task-service
  minReplicas: 2
  maxReplicas: 10
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 70
```

## 7. CI/CD Pipeline

### 7.1. GitHub Actions Workflow

```yaml
name: TaskFlow CI/CD Pipeline

on:
  push:
    branches: [main, develop]
  pull_request:
    branches: [main]

env:
  REGISTRY: ghcr.io
  IMAGE_PREFIX: taskflow

jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: golangci/golangci-lint-action@v3
        with:
          version: latest

  unit-tests:
    runs-on: ubuntu-latest
    needs: lint
    strategy:
      matrix:
        service:
          - api-gateway
          - user-service
          - task-service
          - notification-service
          - scheduler-service
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v4
        with:
          go-version: '1.22'
      - name: Run tests
        working-directory: ./${{ matrix.service }}
        run: |
          go test ./... -race -coverprofile=coverage.out

  integration-tests:
    runs-on: ubuntu-latest
    needs: unit-tests
    services:
      postgres:
        image: postgres:16
        env:
          POSTGRES_PASSWORD: test
        ports:
          - 5432:5432
      redis:
        image: redis:7
        ports:
          - 6379:6379
    steps:
      - uses: actions/checkout@v4
      - name: Run integration tests
        run: docker compose -f docker-compose.test.yml up --abort-on-container-exit

  build:
    runs-on: ubuntu-latest
    needs: integration-tests
    if: github.ref == 'refs/heads/main' || github.ref == 'refs/heads/develop'
    strategy:
      matrix:
        service:
          - api-gateway
          - user-service
          - task-service
          - notification-service
          - scheduler-service
    steps:
      - uses: actions/checkout@v4
      - uses: docker/setup-buildx-action@v3
      - uses: docker/login-action@v3
        with:
          registry: ${{ env.REGISTRY }}
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - uses: docker/build-push-action@v5
        with:
          context: ./${{ matrix.service }}
          push: true
          tags: |
            ${{ env.REGISTRY }}/${{ github.repository }}/${{ matrix.service }}:latest
            ${{ env.REGISTRY }}/${{ github.repository }}/${{ matrix.service }}:${{ github.sha }}
          cache-from: type=gha
          cache-to: type=gha,mode=max

  deploy-staging:
    runs-on: ubuntu-latest
    needs: build
    if: github.ref == 'refs/heads/develop'
    environment:
      name: staging
      url: https://staging.taskflow.example.com
    steps:
      - uses: actions/checkout@v4
      - uses: azure/setup-kubectl@v3
      - run: |
          mkdir -p ~/.kube
          echo "${{ secrets.KUBE_CONFIG_STAGING }}" > ~/.kube/config
          kubectl apply -f k8s/
          kubectl rollout status deployment -n taskflow-staging --timeout=10m

  deploy-production:
    runs-on: ubuntu-latest
    needs: build
    if: github.ref == 'refs/heads/main'
    environment:
      name: production
      url: https://taskflow.example.com
    steps:
      - uses: actions/checkout@v4
      - uses: azure/setup-kubectl@v3
      - run: |
          mkdir -p ~/.kube
          echo "${{ secrets.KUBE_CONFIG_PROD }}" > ~/.kube/config
          kubectl apply -f k8s/
          kubectl rollout status deployment -n taskflow --timeout=15m
```

### 7.2. Dockerfile для каждого сервиса

```dockerfile
FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-w -s -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /service ./cmd/main.go

FROM alpine:3.19
RUN addgroup -S app && adduser -S app -G app
USER app
WORKDIR /app
COPY --from=builder /service .
COPY --from=builder /app/migrations ./migrations

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

EXPOSE 8080
CMD ["./service"]
```

## 8. Безопасность

### 8.1. Аутентификация и авторизация

1. **JWT:** access 15 минут, refresh 30 дней, ротация refresh tokens
2. **OAuth2:** Google, GitHub
3. **RBAC:** роли admin / user; проверка на gateway и в сервисах

### 8.2. Безопасность данных

- TLS на всех внешних соединениях
- Encryption at rest для БД
- Секреты в Kubernetes Secrets
- Валидация входа, параметризованные SQL-запросы

## 9. Roadmap реализации

### Фаза 1: Инфраструктура (2-3 дня)
- [ ] Docker Compose: Postgres, Redis, Kafka, Consul, MinIO, Prometheus, Grafana, Jaeger
- [ ] Базовые Dockerfile
- [ ] Каркас CI (lint + unit tests)

### Фаза 2: User Service (3-5 дней)
- [ ] Регистрация, логин, JWT, refresh tokens
- [ ] Профиль, настройки, аватар (MinIO)
- [ ] OAuth Google/GitHub
- [ ] Юнит и интеграционные тесты

### Фаза 3: Task Service (4-6 дней)
- [ ] CRUD задач, статусы, приоритеты, теги
- [ ] Категории в том же сервисе
- [ ] Комментарии и вложения
- [ ] Кэш списков в Redis
- [ ] Публикация `task.events`

### Фаза 4: API Gateway (3-4 дня)
- [ ] Маршрутизация на user/task/notification
- [ ] Rate limiting, circuit breaker
- [ ] JWT middleware, Request ID, Consul

### Фаза 5: Notification + Scheduler (4-5 дней)
- [ ] Kafka consumers
- [ ] WebSocket + in-app inbox
- [ ] Email (SMTP, шаблоны, retry)
- [ ] Scheduler: напоминания и отчёты через события

### Фаза 6: Observability и k8s (по необходимости)
- [ ] Метрики, дашборды, трейсинг
- [ ] Манифесты, ingress, HPA
- [ ] Нагрузочные тесты, бэкапы, документация API

## 10. Тестирование

1. **Юнит тесты:** моки зависимостей, покрытие > 80%
2. **Интеграционные:** Testcontainers для Postgres/Redis, Kafka harness, gRPC
3. **E2E:** API-сценарии (Newman/k6), UI — если появится SPA
4. **Нагрузка:** k6 на gateway + task-service

## Заключение

Прагматичная схема без дробления домена на сервисы ради сервисов:

- **5 процессов:** API Gateway, User, Task, Notification, Scheduler
- **gRPC** внутри, **REST** снаружи
- **Kafka** только там, где нужен async (уведомления, джобы)
- **Consul**, Redis, MinIO, полный observability-стек
- Без отдельного audit/email/category/auth-сервиса и без ClickHouse
