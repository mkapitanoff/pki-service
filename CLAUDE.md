# PKI Service — CLAUDE.md

> Этот файл читается Claude Code в начале КАЖДОЙ сессии. Не удалять разделы.
> Обновлять раздел «Текущий статус» после завершения каждого модуля.

---

## Что это за проект

SaaS-платформа для подписания PDF-документов юридически значимой ЭЦП Республики Казахстан.

**Что делает сервис:**
1. Принимает PDF-документ (из S3) и CMS-подпись от NCALayer
2. Верифицирует подпись через NCANode (CMS vs документ + OCSP + TSP)
3. Генерирует QR-код со ссылкой на публичную страницу верификации
4. Добавляет QR-штамп на каждую страницу PDF
5. Перегенерирует «Лист подписей» в конце PDF (со всеми накопленными подписями)
6. Сохраняет новую версию PDF в S3
7. Возвращает URL подписанного документа + данные подписи

**Модель подписания — итеративная:**
- Каждый подписант делает отдельный вызов POST /api/v1/documents/{id}/sign
- При каждом вызове создаётся новая версия PDF с обновлённым Листом подписей (все подписи)
- Документ проходит статусы: DRAFT → PENDING → PARTIALLY_SIGNED → SIGNED

**Кто использует:**
- Внешние приложения — через REST API с API-ключом
- Физ. лица и юр. лица — разные типы тенантов

**Функциональный референс:** SIGEX (sigex.kz)

---

## Два контура — ОБЯЗАТЕЛЬНО

Контур определяется переменной окружения APP_ENV=test|prod.

| Параметр         | test                              | prod                        |
|---|---|---|
| NCANode OCSP     | http://test.pki.gov.kz/ocsp/      | http://ocsp.pki.gov.kz/     |
| NCANode TSP      | http://test.pki.gov.kz/tsp/       | http://tsp.pki.gov.kz/      |
| NCANode CRL      | http://test.pki.gov.kz/crl/...    | http://crl.pki.gov.kz/...   |
| S3 bucket        | eds-test                          | eds-prod                    |
| PostgreSQL DB    | eds_test                          | eds_prod                    |
| Log level        | debug                             | info                        |
| Verify base URL  | https://test.sign.example.kz      | https://sign.example.kz     |

Конфиг загружается из configs/config.{APP_ENV}.yaml, переменные ENV перекрывают yaml.
Docker Compose файлы: docker/docker-compose.test.yml и docker/docker-compose.prod.yml.

---

## Стек (не менять без явного согласования)

| Слой              | Технология                    |
|---|---|
| Язык              | Go 1.22+                      |
| Router            | Chi v5                        |
| Конфигурация      | Viper v1                      |
| DB layer          | sqlc v1.25+ (генерация кода)  |
| Миграции          | golang-migrate v4             |
| БД                | PostgreSQL 15                 |
| Крипто-сервис     | NCANode 3.x (Docker sidecar)  |
| Брокер            | RabbitMQ 3.12 (amqp091-go)   |
| Cache             | Redis 7 (go-redis/v9)        |
| PDF               | pdfcpu                        |
| QR                | go-qr-code (skip2)            |
| Object storage    | aws-sdk-go-v2 (S3 / MinIO)   |
| Логи              | uber-go/zap v1                |
| Тесты             | testify + net/http/httptest   |
| Контейнеры        | Docker + Docker Compose       |

Нельзя добавлять: другие ORM, другие роутеры, прямые вызовы crypto/x509 для PKCS#7.
Нельзя: писать криптографический код вне пакета internal/ncanode/.

---

## Структура проекта

```
pki-service/
├── CLAUDE.md
├── cmd/
│   ├── api/
│   │   └── main.go            HTTP сервер
│   └── worker/
│       └── main.go            RabbitMQ consumers (webhook delivery)
├── internal/
│   ├── config/
│   │   └── config.go          Viper, структура Config, два контура
│   ├── handler/
│   │   ├── sign.go            POST /api/v1/documents/:id/sign
│   │   ├── document.go        POST /api/v1/documents, GET /api/v1/documents/:id
│   │   ├── verify.go          GET /verify/:signature_id (публичный, HTML)
│   │   └── middleware.go      API-key auth, tenant ctx, rate limit
│   ├── service/
│   │   ├── sign.go            оркестрация: NCANode + PDF + S3 + DB
│   │   └── document.go        CRUD документов
│   ├── ncanode/
│   │   └── client.go          HTTP-клиент к NCANode (весь крипто)
│   ├── pdf/
│   │   ├── stamp.go           QR-штамп на каждой странице
│   │   └── signpage.go        Лист подписей (финальная страница)
│   ├── qr/
│   │   └── generator.go       QR-код (go-qr-code)
│   ├── storage/
│   │   └── s3.go              upload/download из S3
│   ├── queue/
│   │   ├── producer.go        публикация событий в RabbitMQ
│   │   └── consumer.go        обработка webhook-задач
│   ├── webhook/
│   │   └── delivery.go        HTTP доставка webhook с HMAC + retry
│   ├── errors/
│   │   └── errors.go          типизированные AppError
│   └── repository/            sqlc-generated код + обёртки
│       ├── db.go
│       └── tx.go              транзакционные хелперы
├── db/
│   ├── migrations/            golang-migrate SQL файлы (001_init.up.sql и т.д.)
│   └── queries/               .sql файлы для sqlc
├── sqlc.yaml
├── configs/
│   ├── config.test.yaml
│   └── config.prod.yaml
├── docker/
│   ├── docker-compose.test.yml
│   └── docker-compose.prod.yml
├── go.mod
└── go.sum
```

---

## Схема БД (PostgreSQL DDL — источник истины)

Файлы миграций живут в db/migrations/.
sqlc читает запросы из db/queries/*.sql.
НИКОГДА не менять схему вручную в коде — только через новый файл миграции.

```sql
-- 001_init.up.sql

CREATE TYPE tenant_type AS ENUM ('individual', 'legal_entity');
CREATE TYPE doc_status AS ENUM ('draft', 'pending', 'partially_signed', 'signed', 'rejected');
CREATE TYPE ocsp_status_type AS ENUM ('good', 'revoked', 'unknown');

CREATE TABLE tenants (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    type        tenant_type NOT NULL,
    is_active   BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE api_keys (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL REFERENCES tenants(id),
    key_hash     TEXT NOT NULL UNIQUE,
    label        TEXT NOT NULL,
    is_active    BOOLEAN NOT NULL DEFAULT true,
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_api_keys_tenant ON api_keys(tenant_id);

CREATE TABLE documents (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id),
    title           TEXT,
    s3_key_original TEXT NOT NULL,
    s3_key_current  TEXT NOT NULL,
    current_version INT NOT NULL DEFAULT 0,
    status          doc_status NOT NULL DEFAULT 'draft',
    metadata        JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_documents_tenant ON documents(tenant_id);

CREATE TABLE document_versions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id UUID NOT NULL REFERENCES documents(id),
    tenant_id   UUID NOT NULL,
    version     INT NOT NULL,
    s3_key      TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(document_id, version)
);

CREATE TABLE signatures (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id     UUID NOT NULL REFERENCES documents(id),
    tenant_id       UUID NOT NULL,
    version_number  INT NOT NULL,
    sequence_num    INT NOT NULL,

    cms_b64         TEXT NOT NULL,
    role            TEXT NOT NULL,
    signer_iin      TEXT,
    signer_name     TEXT NOT NULL,
    signer_bin      TEXT,
    org_name        TEXT,
    signer_type     TEXT NOT NULL,
    basis           TEXT,

    cert_serial     TEXT NOT NULL,
    cert_not_before TIMESTAMPTZ NOT NULL,
    cert_not_after  TIMESTAMPTZ NOT NULL,
    ca_name         TEXT NOT NULL,

    ocsp_status     ocsp_status_type NOT NULL,
    ocsp_checked_at TIMESTAMPTZ NOT NULL,
    tsp_time        TIMESTAMPTZ,
    sha256_hash     TEXT NOT NULL,
    sign_format     TEXT NOT NULL DEFAULT 'CAdES (CMS, PKCS#7)',

    qr_url          TEXT NOT NULL,

    signed_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_signatures_document ON signatures(document_id);
CREATE INDEX idx_signatures_tenant ON signatures(tenant_id);

CREATE TABLE webhooks (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id),
    url         TEXT NOT NULL,
    events      TEXT[] NOT NULL,
    secret      TEXT NOT NULL,
    is_active   BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE webhook_deliveries (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    webhook_id    UUID NOT NULL REFERENCES webhooks(id),
    event         TEXT NOT NULL,
    payload       JSONB NOT NULL,
    attempt       INT NOT NULL DEFAULT 1,
    status        TEXT NOT NULL,
    response_code INT,
    error_msg     TEXT,
    scheduled_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at  TIMESTAMPTZ
);

CREATE TABLE audit_log (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL,
    action      TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id   UUID,
    actor_id    UUID,
    meta        JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_audit_tenant_time ON audit_log(tenant_id, created_at DESC);
```

---

## API контракт

### Аутентификация

**Email + пароль (JWT):**
- `POST /auth/register { email, password, name }` → 201 `{ user: {...}, token }`
- `POST /auth/login { email, password }` → 200 `{ user: {...}, token }` / 401
- `GET  /auth/me` (Bearer JWT) → 200 `{ id, email, name, role, tenant_id }`
- `POST /auth/logout` (Bearer JWT) → 200 `{ ok: true }`

JWT: HS256, payload `{ sub: user_id, email, role, tenant_id, exp: +24h }`.
Секрет: `app.jwt_secret` в конфиге (обязательно задать в .env.prod или yaml).

**API-ключ (машинный доступ):**
Все `/api/v1/*` принимают оба варианта `Authorization: Bearer <token>`:
- Если токен содержит 2 точки → JWT-аутентификация
- Иначе → API-ключ (sha256 ключа проверяется по таблице api_keys)

`/verify/:signature_id` — публичный, без auth, rate limit 60 req/min per IP.

### Роуты

```
POST   /api/v1/documents
       Body: { "s3_key": "string", "title": "string", "metadata": {} }
       → 201 { "data": { "id", "status", "created_at" } }

GET    /api/v1/documents/:id
       → 200 { "data": { "id", "title", "status", "current_version",
                         "s3_key_current", "signatures": [...] } }

POST   /api/v1/documents/:id/sign
       Body: { "cms": "base64...", "role": "client|factor|..." }
       → 200 { "data": { "signature_id", "signed_document_url", "signature": {...} } }
       → 422 если CMS невалиден, сертификат отозван, хэш не совпадает

GET    /verify/:signature_id
       → 200 HTML страница верификации (публичная)

POST   /api/v1/webhooks
DELETE /api/v1/webhooks/:id
POST   /api/v1/webhooks/:id/test

GET    /api/v1/tenants/me
POST   /api/v1/api-keys
DELETE /api/v1/api-keys/:id

GET    /health
```

### Поток POST /api/v1/documents/:id/sign

```
1.  Auth: API-ключ → tenant_id
2.  SELECT document WHERE id=:id AND tenant_id=$1
3.  Скачать document.s3_key_current из S3
4.  Вычислить SHA-256 хэш PDF
5.  NCANode.VerifyCMS(cms, sha256) → если invalid → 422
6.  Извлечь данные сертификата из ответа NCANode
7.  Если ocsp_status=revoked → 422
8.  NCANode.GetTSP(sha256) → tsp_time
9.  sequence_num = COUNT(signatures WHERE document_id=:id) + 1
10. new_signature_id = uuid.New()
11. qr_url = config.VerifyBaseURL + "/verify/" + new_signature_id
12. Сгенерировать QR-изображение для qr_url
13. Загрузить ВСЕ подписи документа из DB (1..N-1) + новую
14. Скачать document.s3_key_current (или original если v0)
15. Перегенерировать PDF:
    a. На каждой странице: QR-штампы всех подписантов
    b. Заменить/добавить Лист подписей (последняя страница)
16. Новый s3_key: {tenant_id}/{doc_id}/v{version+1}.pdf
17. Загрузить новый PDF в S3
18. BEGIN TRANSACTION:
    a. INSERT INTO signatures
    b. INSERT INTO document_versions
    c. UPDATE documents SET s3_key_current, current_version, status, updated_at
    d. INSERT INTO audit_log
    COMMIT
19. Опубликовать событие "signature.added" в RabbitMQ
20. Вернуть ответ
```

---

## NCANode интеграция

URL из конфига: ncanode.url
Весь крипто-код — ТОЛЬКО в internal/ncanode/client.go. Нигде больше.

```go
// internal/ncanode/client.go

type VerifyResult struct {
    Valid          bool
    SignerIIN      string
    SignerName     string
    SignerBIN      string
    OrgName        string
    SignerType     string    // "individual" | "legal_entity_rep"
    Basis          string    // "Устав" | "Доверенность" | ""
    CertSerial     string
    CertNotBefore  time.Time
    CertNotAfter   time.Time
    CAName         string
    OCSPStatus     string    // "good" | "revoked" | "unknown"
    OCSPCheckedAt  time.Time
    TSPTime        time.Time
    SignFormat     string
}

type NCANodeClient interface {
    VerifyCMS(ctx context.Context, cmsBase64 string, docSHA256 string) (*VerifyResult, error)
    GetTSP(ctx context.Context, dataSHA256 string) (time.Time, error)
}
```

NCANode в test-контуре:
```
NCANODE_DEBUG=true
NCANODE_OCSP_URL=http://test.pki.gov.kz/ocsp/
NCANODE_TSP_URL=http://test.pki.gov.kz/tsp/
NCANODE_CRL_URL=http://test.pki.gov.kz/crl/nca_rsa_test.crl http://test.pki.gov.kz/crl/nca_gost_test.crl
NCANODE_CA_URL=http://test.pki.gov.kz/cert/root_gost_test.cer http://test.pki.gov.kz/cert/root_rsa_test.cer
```

---

## Лист подписей — формат (из референсных скриншотов)

Каждая запись:
```
✓ ДОКУМЕНТ ПОДПИСАН ЭЦП
Дата подписания:  14.05.2026, 14:10:04
Организация:      ТОО "МеталлОптТорг KZ"
БИН:              230240030302
Подписант:        БАХЫТЖАНОВА ТОЖАН БАХЫТЖАНОВНА
ИИН:              8904****1782      ← маскировать: первые 4 + **** + последние 4
Тип:              Представитель юридического лица
Основание:        Устав / Доверенность

СЕРТИФИКАТ
УЦ:               ҰЛТТЫҚ КУӘЛАНДЫРУШЫ ОРТАЛЫҚ
№ сертификата:    2F:5...3:91       ← первые 4 символа + "..." + последние 3
Действителен:     с 08.01.2026 по 08.01.2027

ПОДПИСЬ
Формат:           CAdES (CMS, PKCS#7)
Хэш SHA-256:      125939f4...71ece070  ← первые 8 + "..." + последние 8 символов
Статус:           Подпись действительна ✓

[QR-код 80x80px]  Сканируйте для проверки
```

---

## S3 структура ключей

```
{tenant_id}/
└── {document_id}/
    ├── original.pdf
    ├── v1.pdf
    ├── v2.pdf
    └── ...
```

S3 bucket из конфига: storage.bucket

---

## Конфигурация

```yaml
# configs/config.test.yaml
app:
  env: test
  port: 8080
  verify_base_url: "https://test.sign.example.kz"

database:
  dsn: "postgres://user:pass@localhost:5432/eds_test?sslmode=disable"

ncanode:
  url: "http://ncanode:14579"
  timeout_sec: 30

storage:
  endpoint: "http://minio:9000"
  bucket: "eds-test"
  access_key: "minioadmin"
  secret_key: "minioadmin"
  use_path_style: true

redis:
  addr: "redis:6379"
  db: 0

rabbitmq:
  url: "amqp://guest:guest@rabbitmq:5672/"
  webhook_queue: "webhook.delivery"
  event_exchange: "eds.events"

log:
  level: "debug"
```

---

## Правила для Claude Code

### Обязательно

1. Каждый SQL-запрос — только через sqlc-generated функции в internal/repository/
2. Каждый запрос к БД включает tenant_id фильтр. Нет исключений.
3. Шаг 18 в потоке /sign — в одной транзакции через tx.go
4. Ошибки типизированы через AppError в internal/errors/errors.go
5. Логирование — только через zap. Никаких fmt.Println, log.Printf.
6. Конфиг — только через config.Config struct, не os.Getenv напрямую в бизнес-логике.

### Запрещено

- Писать криптографический код вне internal/ncanode/
- SQL без tenant_id в WHERE
- Хранить сырой API-ключ (только sha256(key))
- Логировать CMS, API-ключи, пароли в plaintext
- Использовать interface{} без крайней необходимости
- Игнорировать ошибки S3/NCANode

---

## Соглашения по коду

```go
// Сервис — чистые функции, нет HTTP-зависимостей
func (s *SignService) SignDocument(
    ctx context.Context,
    tenantID uuid.UUID,
    documentID uuid.UUID,
    input SignInput,
) (*SignResult, error)

// Handler — только decode → service → respond
func (h *SignHandler) HandleSign(w http.ResponseWriter, r *http.Request) {
    input, err := decodeSignRequest(r)
    if err != nil { respondError(w, ErrInvalidRequest); return }
    result, err := h.signSvc.SignDocument(r.Context(), tenantFromCtx(r), ...)
    if err != nil { respondError(w, err); return }
    respondJSON(w, http.StatusOK, result)
}

// Ошибки
var ErrCMSInvalid = &AppError{Code: "CMS_INVALID", Status: 422, Message: "CMS signature is invalid"}
var ErrCertRevoked = &AppError{Code: "CERT_REVOKED", Status: 422, Message: "Certificate is revoked"}
var ErrDocumentNotFound = &AppError{Code: "DOCUMENT_NOT_FOUND", Status: 404, Message: "Document not found"}
```

---

## Dev-окружение

```bash
# Test контур
docker compose -f docker/docker-compose.test.yml up -d

# Миграции
migrate -path db/migrations -database "$DATABASE_URL" up

# Генерация sqlc (после изменения db/queries/*.sql)
sqlc generate

# Запуск
APP_ENV=test go run cmd/api/main.go

# Тесты
go test ./... -v -count=1
```

---

## Текущий статус (обновлять вручную)

- [x] Модуль 0: Scaffold + Docker Compose (test + prod) + Config
- [x] Модуль 1: DB миграции + sqlc генерация + repository layer
- [x] Модуль 2: NCANode client + mock для тестов
- [x] Модуль 3: S3 storage layer
- [x] Модуль 4: PDF — QR-штамп + Лист подписей (pdfcpu)
- [x] Модуль 5: Sign service (оркестрация) + Handler
- [x] Модуль 6: API-key auth middleware + tenant context
- [x] Модуль 7: Публичный /verify/:signature_id (HTML)
- [x] Модуль 10: Email+пароль auth (JWT) + admin seed
- [ ] Модуль 8: RabbitMQ + Webhook delivery
- [ ] Модуль 9: Тесты (unit + integration)

---

## Правила работы с файлами и git (Claude Code)

### Рабочая директория
Все файлы писать ТОЛЬКО в текущую рабочую директорию проекта.
НИКОГДА не использовать git worktrees, временные директории, или поддиректории .claude/.

Перед созданием любого файла проверить что pwd = корень проекта:
```bash
pwd  # должно быть /Users/user/pki-service или аналог
```

### Git после каждого промпта
После успешного выполнения задачи (все тесты зелёные, go build чистый):

```bash
git add .
git commit -m "feat: <описание модуля>"
git push origin main
```

Формат commit message:
- feat: новая функциональность
- fix: исправление бага
- refactor: рефакторинг без изменения поведения

### Обязательная последовательность завершения сессии
1. go build ./...  → должно быть чисто
2. go test ./internal/... -count=1 → все OK или SKIP
3. git add .
4. git commit -m "feat: ..."
5. git push origin main
6. Отчитаться: список созданных файлов + результаты тестов

---

## Правила работы с файлами и git (Claude Code)

CRITICAL: Always work in the main project directory, never in worktrees.
Before writing any file, verify: pwd must be the project root (e.g. /Users/user/pki-service).
If pwd contains ".claude/worktrees" — STOP and cd to the project root first.

After all tests pass:
1. git add .
2. git commit -m "feat: <description>"
3. git push origin main
