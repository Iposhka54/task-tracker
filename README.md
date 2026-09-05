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
8. **Hexagonal architecture** — внутри каждого бизнес-сервиса: домен в центре, порты (интерфейсы), адаптеры (gRPC, Postgres, Kafka, SMTP)

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

### 2.2. gRPC-контракты: один proto на всех

Proto **не принадлежит** сервису. Это публичный контракт между процессами: auth-service его реализует (server), gateway его вызывает (client). Если положить `.proto` только в `services/auth-service/`, gateway либо тянет весь модуль сервиса (БД, внутренности), либо копирует файлы — оба варианта плохие.

В монорепе стандарт — **proto отдельно, сгенерированный Go — отдельный модуль**:

```
task-tracker/
├── api/                              # protobuf-модуль (не Go)
│   ├── buf.yaml
│   ├── buf.gen.yaml
│   ├── auth/auth.proto
│   ├── task/task.proto
│   ├── notification/notification.proto
│   └── scheduler/scheduler.proto
├── pkg/api/                          # сгенерированный Go, отдельный модуль
│   ├── go.mod                        # github.com/Iposhka54/task-tracker/pkg/api
│   ├── auth/                         # auth.pb.go, auth_grpc.pb.go
│   ├── task/
│   ├── notification/
│   └── scheduler/
├── services/
│   ├── api-gateway/                  # require pkg/api → gRPC client
│   ├── auth-service/                 # require pkg/api → gRPC server
│   ├── task-service/
│   ├── notification-service/
│   └── scheduler-service/
└── go.work
```

`api/` нельзя сделать Go-модулем «как есть»: `.proto` не импортируется через `import`. Сервисы импортируют **сгенерированные** пакеты из `pkg/api`.

**Кто что делает:**

| Роль | Import | Что пишет |
|---|---|---|
| Auth Service | `github.com/Iposhka54/task-tracker/pkg/api/auth` | `authpb.UnimplementedAuthServiceServer` |
| API Gateway | тот же пакет | `authpb.NewAuthServiceClient(conn)` |
| Другой сервис | тот же пакет | только client, без импорта `services/auth-service` |

Связка пути proto → Go-импорт:

```
api/auth/auth.proto
  package auth
  option go_package = "github.com/Iposhka54/task-tracker/pkg/api/auth;authpb"

buf generate (из api/)  →  pkg/api/auth/auth.pb.go
                           pkg/api/auth/auth_grpc.pb.go

сервис:
  import authpb "github.com/Iposhka54/task-tracker/pkg/api/auth"
```

`;authpb` — имя Go-пакета, чтобы не пересекаться с доменным `auth`.

Версию в путь (`auth/v1`) не кладём: все клиенты в этом монорепо, контракт меняем аддитивно. Если когда-нибудь понадобится несовместимый rewrite — заведём рядом `api/auth2/` (или тогда уже `v2`), старый пакет оставим жить, пока его не выпилим. До этого момента `buf breaking` ловит случайные поломки.

`go.work` резолвит локальный модуль. В каждом сервисе всё равно нужен `require` **и** `replace`: workspace не попадает в Docker-контекст одного сервиса.

```
# go.work
go 1.25.5

use (
    ./pkg/api
    ./services/api-gateway
    ./services/auth-service
    ./services/task-service
    ./services/notification-service
    ./services/scheduler-service
)
```

```
# services/auth-service/go.mod
module github.com/Iposhka54/task-tracker/services/auth-service

go 1.25.5

require github.com/Iposhka54/task-tracker/pkg/api v0.0.0

replace github.com/Iposhka54/task-tracker/pkg/api => ../../pkg/api
```

Генерация из `api/`: `buf generate`.

```yaml
# api/buf.yaml
version: v2

# api/buf.gen.yaml
version: v2
plugins:
  - local: protoc-gen-go
    out: ../pkg/api
    opt: [paths=source_relative]
  - local: protoc-gen-go-grpc
    out: ../pkg/api
    opt: [paths=source_relative]
```

`paths=source_relative` кладёт `auth/auth.proto` в `pkg/api/auth/`.

Dockerfile сервиса собирается **из корня репо**, иначе `replace ../../pkg/api` не найдёт модуль:

```dockerfile
COPY pkg/api ./pkg/api
COPY services/auth-service ./services/auth-service
WORKDIR /src/services/auth-service
RUN go build -o /service ./cmd
```

**Gateway не проксирует gRPC байты как есть.** Снаружи REST/JSON, внутри gRPC. Типичный handler:

```go
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
    var req LoginJSON
    json.NewDecoder(r.Body).Decode(&req)

    resp, err := h.users.Login(r.Context(), &authpb.LoginRequest{
        Email:    req.Email,
        Password: req.Password,
    })
    // JSON-ответ клиенту
}
```

Альтернатива — аннотации `google.api.http` + `grpc-gateway`, тогда REST генерируется из proto. Имеет смысл, если HTTP — тонкая проекция RPC. Для BFF с rate limit по путям, своим JSON и JWT на gateway проще ручной маппинг.

**Правила совместимости:** поля в proto только добавлять, номера (`= 1`) не переиспользовать, breaking change ловить `buf breaking`. Отдельный `v2` не заводим, пока все потребители в этом репозитории.

**Чего не делать:**
- не класть `.proto` только внутрь сервиса-владельца
- не импортировать `services/auth-service` из gateway
- не генерировать stubs отдельно в каждом сервисе из копий proto (разъедутся)
- не светить внутренние DTO/модели БД в proto — контракт отдельно от доменной модели

### 2.3. Гексагональная архитектура (порты и адаптеры)

Межсервисная нарезка — это процессы. **Внутри** user/task/notification/scheduler — гексагон: бизнес-логика не знает ни про gRPC, ни про Postgres.

```
                    inbound adapters              outbound adapters
                 (driving, вызывают ядро)      (driven, ядро вызывает их)

   ┌────────────┐     ┌────────────┐           ┌────────────┐  ┌────────────┐
   │ gRPC server│     │Kafka consumer│         │  Postgres  │  │   Redis    │
   └─────┬──────┘     └──────┬─────┘           └─────▲──────┘  └─────▲──────┘
         │                   │                       │               │
         ▼                   ▼                       │               │
   ┌──────────┐        ┌──────────┐            ┌─────┴──────┐  ┌─────┴──────┐
   │ inbound  │        │ inbound  │            │ outbound   │  │ outbound   │
   │ port     │───────►│  app /   │───────────►│ port       │  │ port       │
   │ (use     │        │ use case │            │ (repo,     │  │ (cache,    │
   │  case)   │        │          │            │  publisher)│  │  storage)  │
   └──────────┘        └──────────┘            └────────────┘  └────────────┘
                              │
                              │                 domain: User, Task, errors
                              └───────────────► без импортов инфраструктуры
```

**Порт** — интерфейс на границе ядра. **Адаптер** — реализация, которая говорит с внешним миром.

| Вид | Кто инициирует | Примеры в TaskFlow |
|---|---|---|
| **Inbound (driving)** | внешний мир → ядро | gRPC-сервер, Kafka consumer, cron в scheduler |
| **Outbound (driven)** | ядро → внешний мир | Postgres, Redis, MinIO, Kafka producer, SMTP |

Один и тот же use case можно дернуть с gRPC и из consumer'а — ядро одно.

**Правило зависимостей:** стрелки только внутрь.

```
cmd/main.go          → собирает всё
adapter/grpc         → port + domain + pkg/api
adapter/postgres     → domain  (и при желании port для var _ port.UserRepo = ...)
service              → port + domain
port                 → domain
domain               → никого
```

Репозиторий — **не** отдельный слой и не пакет `repository/`. Это outbound-порт (`port.UserRepo`) плюс адаптер (`adapter/postgres`). «Сервис» в гексагоне — use case (`internal/service`), не gRPC-сервер.

### Пакеты: плоско, по роли, без inbound/outbound

В Java часто делают `port/inbound` и `adapter/outbound/postgres`. В Go это даёт пакеты `inbound` / `outbound` / `grpc` (конфликт со stdlib) и импорты вроде `inbound.AuthService`. Не нужно.

Четыре пакета ядра + адаптер на каждую технологию:

```
auth-service/
├── cmd/main.go                      # composition root, единственное место wiring
└── internal/
    ├── domain/                      # package domain
    │   ├── user.go
    │   ├── session.go
    │   └── errors.go
    ├── port/                        # package port — все интерфейсы
    │   ├── auth.go                  # inbound: что вызывают адаптеры входа
    │   ├── user_repo.go             # outbound
    │   ├── token_store.go
    │   ├── hasher.go
    │   └── events.go
    ├── service/                     # package service — use cases
    │   ├── auth.go                  # реализует port.Auth
    │   └── profile.go
    └── adapter/
        ├── grpc/                    # package grpcadapter  (не package grpc)
        │   ├── auth.go
        │   └── map.go               # proto ↔ domain
        ├── postgres/                # package postgres — реализует *Repo
        │   └── user_repo.go
        ├── redis/                   # package redis
        ├── bcrypt/                  # package bcryptadapter
        └── kafka/                   # package kafka
```

| Вещь | Пакет | Имя типов | Сюда не класть |
|---|---|---|---|
| Сущность, VO, доменная ошибка | `domain` | `User`, `ErrNotFound` | proto, `sql.Null*`, pgx |
| Интерфейс use case | `port` | `Auth`, `Profile` | реализации |
| Интерфейс хранилища/клиента | `port` | `UserRepo`, `TokenStore`, `Hasher` | pgx, SMTP |
| Сценарий Login/Register | `service` | `Auth` struct | `*grpc.Server`, `pgx.Pool` |
| gRPC handler | `adapter/grpc` | `AuthServer` | бизнес-правила |
| SQL | `adapter/postgres` | `UserRepo` | use case |

Файлы в `port/` можно группировать как угодно — это **один** пакет `port`. Inbound и outbound отличаются именем: `Auth` vs `UserRepo`, а не пакетом.

```go
// internal/port/auth.go
package port

type Auth interface {
    Login(ctx context.Context, cmd Login) (Tokens, error)
    Register(ctx context.Context, cmd Register) (Tokens, error)
}

type Login struct {
    Email, Password, DeviceInfo string
}

// internal/port/user_repo.go
package port

type UserRepo interface {
    Save(ctx context.Context, u domain.User) error
    FindByEmail(ctx context.Context, email string) (domain.User, error)
}
```

```go
// internal/service/auth.go
package service

type Auth struct {
    users  port.UserRepo
    hasher port.Hasher
    tokens port.TokenStore
}

func NewAuth(users port.UserRepo, hasher port.Hasher, tokens port.TokenStore) *Auth {
    return &Auth{users: users, hasher: hasher, tokens: tokens}
}

func (a *Auth) Login(ctx context.Context, cmd port.Login) (port.Tokens, error) { /* ... */ }
```

gRPC-адаптер зовёт `port.Auth` (или сразу `*service.Auth`, если вход один). Пакет **не** называть `grpc`:

```go
// internal/adapter/grpc/auth.go
package grpcadapter

type AuthServer struct {
    authpb.UnimplementedAuthServiceServer
    auth port.Auth
}

func (s *AuthServer) Login(ctx context.Context, req *authpb.LoginRequest) (*authpb.LoginResponse, error) {
    tokens, err := s.auth.Login(ctx, port.Login{Email: req.Email, Password: req.Password})
    // domain/port error → codes.NotFound / Unauthenticated
    return &authpb.LoginResponse{AccessToken: tokens.Access}, err
}
```

Postgres-адаптер — это и есть «репозиторий»:

```go
// internal/adapter/postgres/user_repo.go
package postgres

type UserRepo struct{ db *pgxpool.Pool }

func (r *UserRepo) FindByEmail(ctx context.Context, email string) (domain.User, error) { /* scan → domain.User */ }

var _ port.UserRepo = (*UserRepo)(nil)
```

Wiring только в `cmd/main.go`:

```go
pool := postgres.NewPool(cfg.DSN)
users := postgres.NewUserRepo(pool)
hasher := bcryptadapter.New()
tokens := redis.NewTokenStore(rdb)
auth := service.NewAuth(users, hasher, tokens)
srv := grpcadapter.NewAuthServer(auth)
```

**Inbound-порт** (`port.Auth`) нужен, когда входов несколько: gRPC и Kafka consumer зовут один use case (notification). Если вход один — gRPC может зависеть от `*service.Auth` напрямую, интерфейс не обязателен.

**Outbound-порт** нужен почти всегда: иначе `service` тащит `pgx`, юнит-тесты без Docker невозможны.

Имена адаптеров — по технологии (`postgres`, `redis`, `smtp`), не `outbound`. Inbound-адаптеры: `grpc`, `kafka` (consumer), `cron`.

**Чего не делать:**
- пакет `repository/` рядом с `service/` — это трёхслойка, не гексагон
- пакеты `inbound` / `outbound` — шум, роль видна из имени типа
- `package grpc` — конфликт с `google.golang.org/grpc`
- один `Repository` на все таблицы сервиса
- proto-структуры в `domain` / `service`
- `pgx.Pool` в `service`
- интерфейс «на будущее, вдруг Mongo»
- гексагон в API Gateway

**Тесты:** `service` + фейки на `port.UserRepo` в памяти; `adapter/postgres` — Testcontainers.

## 3. Детальное описание сервисов

### 3.1. API Gateway Service

**Порт:** 443 (HTTPS), 80 (HTTP redirect), 8080 в dev

**Технологии:** Go, Echo или Chi, gRPC-клиенты из `pkg/api`, Redis (rate limiting)

Gateway — BFF, не гексагон: HTTP-handler зовёт gRPC-клиент. Своего домена почти нет.

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
│   ├── handler/           # REST → вызов gRPC-клиента
│   │   ├── auth.go
│   │   ├── user.go
│   │   └── task.go
│   ├── client/            # коннекты к сервисам
│   │   └── grpc.go
│   ├── proxy/
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

**Структура** (гексагон, см. 2.3):
```
auth-service/
├── cmd/
│   └── main.go
├── internal/
│   ├── domain/
│   ├── port/                        # Auth, UserRepo, TokenStore, Hasher, Events
│   ├── service/
│   └── adapter/
│       ├── grpc/                    # package grpcadapter
│       ├── postgres/
│       ├── redis/
│       ├── minio/
│       ├── bcrypt/
│       └── kafka/
├── migrations/
│   ├── 000001_create_users.up.sql
│   └── 000002_create_profiles.up.sql
├── Dockerfile
└── go.mod
```

**Protobuf контракт:**
```protobuf
syntax = "proto3";

package auth;

option go_package = "github.com/Iposhka54/task-tracker/pkg/api/auth;authpb";

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
  string id = 1; // UUID
  string email = 2;
  string username = 3;
  bool is_active = 4;
  string avatar_url = 5;
  string created_at = 6;
  string updated_at = 7;
}

message Profile {
  string user_id = 1; // UUID
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
    id UUID PRIMARY KEY,
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
    id UUID PRIMARY KEY,
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    provider VARCHAR(50) NOT NULL,
    provider_user_id VARCHAR(255) NOT NULL,
    access_token TEXT,
    refresh_token TEXT,
    expires_at TIMESTAMPTZ,
    UNIQUE(provider, provider_user_id)
);

CREATE TABLE login_attempts (
    id UUID PRIMARY KEY,
    user_id UUID REFERENCES users(id),
    ip_address INET,
    success BOOLEAN,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE profiles (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
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

**Структура** (гексагон, см. 2.3):
```
task-service/
├── cmd/
│   └── main.go
├── internal/
│   ├── domain/
│   ├── port/                        # Task, Category, TaskRepo, Cache, Events
│   ├── service/
│   └── adapter/
│       ├── grpc/
│       ├── postgres/
│       ├── redis/
│       └── kafka/
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

package task;

option go_package = "github.com/Iposhka54/task-tracker/pkg/api/task;taskpb";

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
  string id = 1;
  string user_id = 2;
  string category_id = 3;
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
  string id = 1;
  string user_id = 2;
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
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    name VARCHAR(100) NOT NULL,
    color VARCHAR(7) DEFAULT '#808080',
    icon VARCHAR(50),
    position INT NOT NULL DEFAULT 0,
    is_default BOOLEAN DEFAULT false,
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE tasks (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    category_id UUID REFERENCES categories(id) ON DELETE SET NULL,
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
    task_id UUID REFERENCES tasks(id) ON DELETE CASCADE,
    tag VARCHAR(50),
    PRIMARY KEY (task_id, tag)
);

CREATE TABLE comments (
    id UUID PRIMARY KEY,
    task_id UUID REFERENCES tasks(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE attachments (
    id UUID PRIMARY KEY,
    task_id UUID REFERENCES tasks(id) ON DELETE CASCADE,
    file_name VARCHAR(255) NOT NULL,
    file_url VARCHAR(500) NOT NULL,
    file_size BIGINT,
    mime_type VARCHAR(100),
    uploaded_by UUID NOT NULL,
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

**Структура** (гексагон, см. 2.3):
```
notification-service/
├── cmd/
│   └── main.go
├── internal/
│   ├── domain/
│   ├── port/                        # Notify, InboxRepo, MailSender, Realtime
│   ├── service/
│   └── adapter/
│       ├── grpc/
│       ├── kafka/                   # consumer → port.Notify
│       ├── postgres/
│       ├── smtp/
│       └── redis/
├── templates/
│   ├── welcome.html
│   ├── daily_report.html
│   ├── weekly_report.html
│   └── task_reminder.html
├── Dockerfile
└── go.mod
```

Канал доставки — outbound-адаптер. Use case «уведомить о дедлайне» не знает, SMTP это или WebSocket: он зовёт `MailSender` / `RealtimePublisher` по настройкам пользователя.

**Protobuf контракт:**
```protobuf
syntax = "proto3";

package notification;

option go_package = "github.com/Iposhka54/task-tracker/pkg/api/notification;notificationpb";

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

Inbound-адаптеры: gRPC (управление джобами) и cron-ticker (срабатывание). Outbound: `JobStore`, `EventPublisher`. Тикер не содержит бизнес-логики — только зовёт inbound-порт `RunDueJobs`.

```protobuf
syntax = "proto3";

package scheduler;

option go_package = "github.com/Iposhka54/task-tracker/pkg/api/scheduler;schedulerpb";

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
          name: metric
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
- [ ] Каркас гексагона: domain / port / service / adapter, composition root
- [ ] Регистрация, логин, JWT, refresh tokens
- [ ] Профиль, настройки, аватар (MinIO)
- [ ] OAuth Google/GitHub
- [ ] Юнит-тесты service на фейковых outbound-портах; интеграция postgres-адаптера

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

1. **Юнит тесты ядра:** `internal/service` + фейки на `port.UserRepo` в памяти, без Docker. Покрытие use case'ов > 80%.
2. **Адаптеры:** Testcontainers для Postgres/Redis/Kafka; отдельно маппинг gRPC (proto ↔ domain, коды ошибок).
3. **E2E:** API-сценарии через gateway (Newman/k6), UI — если появится SPA.
4. **Нагрузка:** k6 на gateway + task-service.

## Заключение

Прагматичная схема без дробления домена на сервисы ради сервисов:

- **5 процессов:** API Gateway, User, Task, Notification, Scheduler
- **Гексагон внутри сервиса:** domain → ports → adapters (gRPC, Postgres, Kafka, SMTP)
- **gRPC** внутри, **REST** снаружи, контракты в общем `pkg/api`
- **Kafka** только там, где нужен async (уведомления, джобы)
- **Consul**, Redis, MinIO, полный observability-стек
- Без отдельного audit/email/category/auth-сервиса и без ClickHouse
