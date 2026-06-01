# Claude Code Prompts — Applications Feature (PKI Service)

Промпты выполняются **последовательно**. Каждый следующий зависит от предыдущего.
После каждого промпта: `cp -r .claude/worktrees/*/. .` если файлы легли в worktree.

---

## Промпт 1 — Миграции БД

```
Ты работаешь над Go-сервисом PKI (Chi router, sqlc, PostgreSQL 16).
Посмотри существующие миграции в папке migrations/ и добавь новую миграцию.

Создай файл migrations/NNNN_add_applications.up.sql (NNNN = следующий номер).
Также создай соответствующий migrations/NNNN_add_applications.down.sql.

В up.sql создай три таблицы:

1. applications
   - id UUID PRIMARY KEY DEFAULT gen_random_uuid()
   - tenant_id UUID NOT NULL REFERENCES tenants(id)
   - external_id VARCHAR(255) NOT NULL  -- ID заявки в BPM системе клиента
   - status VARCHAR(50) NOT NULL DEFAULT 'active'
     -- допустимые значения: active, signing, round_completed, completed, cancelled
   - signing_round INT NOT NULL DEFAULT 1
   - signer_role VARCHAR(100) NOT NULL  -- роль текущего подписанта: client, manager, director
   - callback_url TEXT  -- URL для webhook уведомлений (опционально)
   - callback_secret VARCHAR(255)  -- shared secret для HMAC подписи webhook
   - cancelled_at TIMESTAMPTZ
   - cancel_reason TEXT
   - created_at TIMESTAMPTZ NOT NULL DEFAULT now()
   - updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
   - UNIQUE (tenant_id, external_id)

2. application_documents
   - id UUID PRIMARY KEY DEFAULT gen_random_uuid()
   - application_id UUID NOT NULL REFERENCES applications(id)
   - document_id UUID REFERENCES documents(id)  -- наш внутренний документ после скачивания
   - document_name VARCHAR(500) NOT NULL  -- оригинальное имя файла
   - version INT NOT NULL DEFAULT 1
   - signing_round INT NOT NULL DEFAULT 1
   - source_url TEXT NOT NULL  -- GET pre-signed URL от клиента
   - target_url TEXT  -- PUT pre-signed URL от клиента (обновляемый)
   - target_s3_key VARCHAR(1000)  -- S3 ключ для метаданных и аудита
   - status VARCHAR(50) NOT NULL DEFAULT 'pending'
     -- допустимые значения: pending, fetching, ready, signing, signed,
     --                       uploading, uploaded, upload_failed, superseded
   - superseded_by UUID REFERENCES application_documents(id)
   - upload_attempts INT NOT NULL DEFAULT 0
   - last_error TEXT  -- последняя ошибка загрузки
   - uploaded_at TIMESTAMPTZ
   - created_at TIMESTAMPTZ NOT NULL DEFAULT now()

3. application_webhooks
   - id UUID PRIMARY KEY DEFAULT gen_random_uuid()
   - application_id UUID NOT NULL REFERENCES applications(id)
   - event_type VARCHAR(100) NOT NULL
     -- допустимые значения: document_signed, round_completed, round_failed, application_cancelled
   - payload JSONB NOT NULL
   - hmac_signature VARCHAR(255)
   - status VARCHAR(50) NOT NULL DEFAULT 'pending'
     -- допустимые значения: pending, delivered, failed
   - attempts INT NOT NULL DEFAULT 0
   - last_attempt_at TIMESTAMPTZ
   - delivered_at TIMESTAMPTZ
   - created_at TIMESTAMPTZ NOT NULL DEFAULT now()

Индексы:
- applications: (tenant_id, external_id), (status), (tenant_id, status)
- application_documents: (application_id), (document_id), (application_id, status),
  (application_id, document_name, signing_round) для поиска версий
- application_webhooks: (application_id), (status, created_at) для воркера

down.sql: DROP TABLE в обратном порядке (webhooks → documents → applications).

После создания файлов выполни миграцию:
  goose -dir migrations postgres "$DATABASE_URL" up
```

---

## Промпт 2 — sqlc queries

```
Ты работаешь над Go-сервисом PKI. Посмотри существующие файлы в internal/db/queries/
чтобы понять стиль именования и структуру.

Создай файл internal/db/queries/applications.sql со следующими запросами:

-- name: CreateApplication :one
Вставка новой заявки, возврат полной записи.

-- name: GetApplicationByExternalID :one
Поиск по tenant_id и external_id.

-- name: GetApplicationByID :one
Поиск по id и tenant_id (для авторизации).

-- name: UpdateApplicationStatus :one
Обновление status, signing_round, updated_at по id.

-- name: CancelApplication :one
Установка status='cancelled', cancelled_at=now(), cancel_reason по id.

-- name: CreateApplicationDocument :one
Вставка документа заявки, возврат полной записи.

-- name: GetApplicationDocumentByID :one
Поиск по id.

-- name: ListApplicationDocuments :many
Все документы заявки по application_id, отсортированные по created_at.

-- name: ListActiveApplicationDocuments :many
Документы заявки где status != 'superseded', по application_id и signing_round.

-- name: UpdateApplicationDocumentStatus :one
Обновление status, last_error по id.

-- name: UpdateApplicationDocumentAfterFetch :one
После скачивания: установить document_id, status='ready' по id.

-- name: UpdateApplicationDocumentTargetURL :one
Обновление target_url (для refresh-urls) по id.

-- name: MarkApplicationDocumentUploaded :one
Установка status='uploaded', uploaded_at=now() по id.

-- name: IncrementUploadAttempts :one
Увеличение upload_attempts на 1, установка last_error по id.

-- name: SupersedeApplicationDocument :one
Установка status='superseded', superseded_by=<new_id> по id.

-- name: FindPreviousVersions :many
Найти все документы с тем же document_name и application_id где status != 'superseded'.

-- name: CreateApplicationWebhook :one
Вставка записи вебхука.

-- name: GetPendingWebhooks :many
Вебхуки со status='pending' или (status='failed' AND attempts < 5),
упорядоченные по created_at, LIMIT 50. Для воркера.

-- name: UpdateWebhookStatus :one
Обновление status, attempts, last_attempt_at, delivered_at по id.

После создания запросов выполни:
  sqlc generate
```

---

## Промпт 3 — S3 клиент (скачивание и загрузка)

```
Ты работаешь над Go-сервисом PKI. Посмотри internal/storage/ — там уже есть MinIO клиент.

Создай файл internal/s3client/client.go для работы с ЧУЖИМ S3 через pre-signed URLs.
Это НЕ наш MinIO, это S3 хранилище клиента (финансовой платформы).

Интерфейс:

type ExternalS3Client interface {
    // Скачать файл по pre-signed GET URL, вернуть содержимое
    DownloadFromPresignedURL(ctx context.Context, url string) ([]byte, string, error)
    // contentType определять из Content-Type заголовка ответа

    // Загрузить файл по pre-signed PUT URL с S3 метаданными
    UploadToPresignedURL(ctx context.Context, url string, data []byte,
        contentType string, metadata S3Metadata) error
}

type S3Metadata struct {
    ApplicationID  string
    DocumentID     string
    DocumentName   string
    SignerRole      string
    SignedAt        time.Time
    SigningRound    int
    DocumentVersion int
    CMSStorageKey  string  // ключ в нашем MinIO где лежит CMS
}

В UploadToPresignedURL добавляй заголовки:
  x-amz-meta-pki-signed: true
  x-amz-meta-pki-application-id: {ApplicationID}
  x-amz-meta-pki-document-id: {DocumentID}
  x-amz-meta-pki-document-name: {DocumentName} (URL-encoded)
  x-amz-meta-pki-signer-role: {SignerRole}
  x-amz-meta-pki-signed-at: {SignedAt RFC3339}
  x-amz-meta-pki-signing-round: {SigningRound}
  x-amz-meta-pki-document-version: {DocumentVersion}
  x-amz-meta-pki-cms-key: {CMSStorageKey}

Реализация:
- Использовать стандартный net/http клиент с таймаутом 60s
- DownloadFromPresignedURL: GET запрос, проверить статус 200, вернуть body + content-type
- UploadToPresignedURL: PUT запрос с заголовками выше, тело = data, проверить статус 200 или 204
- Логировать URL (без query params для безопасности), размер, статус
- Оба метода возвращают описательные ошибки с контекстом

Создай также internal/s3client/retry.go с функцией:
  UploadWithRetry(ctx context.Context, client ExternalS3Client, url string,
    data []byte, contentType string, metadata S3Metadata,
    maxAttempts int) error

Retry logic: exponential backoff 1s → 3s → 9s. Не ретраить если статус 403 (URL протух).
При 403 возвращать специальный тип ошибки ErrPresignedURLExpired.
```

---

## Промпт 4 — Worker: скачивание документов

```
Ты работаешь над Go-сервисом PKI. Посмотри как устроены существующие воркеры или
background goroutines в cmd/ или internal/.

Создай файл internal/worker/document_fetcher.go.

Воркер DocumentFetcher запускается как background goroutine при старте сервиса.
Задача: скачивать документы заявок у которых статус application_documents.status = 'pending'.

Логика:
1. Каждые 10 секунд делать запрос GetPendingFetchDocuments (добавить в SQL:
   SELECT * FROM application_documents WHERE status='pending' ORDER BY created_at LIMIT 20)
2. Для каждого документа:
   a. Обновить статус на 'fetching'
   b. Скачать файл через ExternalS3Client.DownloadFromPresignedURL(source_url)
   c. Сохранить файл в наш MinIO (использовать существующий storage клиент)
   d. Создать запись в таблице documents (как при обычной загрузке файла)
   e. Вызвать UpdateApplicationDocumentAfterFetch с новым document_id
   f. Если ошибка — UpdateApplicationDocumentStatus(status='pending', last_error=err),
      увеличить счётчик попыток, пропустить до следующего цикла
3. Graceful shutdown через context cancellation

Параметры воркера получать из конфига:
  FetchInterval  time.Duration (default 10s)
  FetchBatchSize int           (default 20)
  MaxFetchRetries int          (default 3, после этого status='fetch_failed')

Добавь 'fetch_failed' в допустимые статусы application_documents.status.

Инициализацию воркера добавь в main.go или server.go там где стартуют другие сервисы.
Воркер принимает ctx, db, minioClient, s3client, logger.
```

---

## Промпт 5 — Worker: webhook dispatcher

```
Ты работаешь над Go-сервисом PKI. 

Создай файл internal/worker/webhook_dispatcher.go.

Воркер WebhookDispatcher отправляет webhook уведомления клиентам.

Структура payload (JSON):
{
  "event": "document_signed",  // или round_completed, round_failed, application_cancelled
  "application_id": "uuid",
  "external_id": "BPM-12345",
  "signing_round": 1,
  "timestamp": "2026-06-01T10:00:00Z",
  "data": {
    // для document_signed:
    "document_id": "uuid",
    "document_name": "Договор.pdf",
    "document_version": 1,
    "signer_role": "client",
    "signed_at": "2026-06-01T10:00:00Z",
    "s3_key": "applications/12345/signed/Договор.pdf"
    // для round_completed:
    "documents_signed": 3,
    "next_action": "submit_next_round"
    // для round_failed:
    "failed_documents": ["uuid1", "uuid2"],
    "error": "S3 upload failed after 3 attempts"
  }
}

HMAC подпись:
- Вычислять HMAC-SHA256 от JSON payload используя application.callback_secret
- Добавлять заголовок X-PKI-Signature: sha256={hex}
- Добавлять X-PKI-Event: {event_type}
- Добавлять X-PKI-Timestamp: {unix timestamp}

Логика воркера:
1. Каждые 5 секунд GetPendingWebhooks (уже есть в SQL)
2. Для каждого вебхука:
   a. Отправить POST на application.callback_url с payload и заголовками
   b. Таймаут запроса: 10 секунд
   c. Успех (2xx): UpdateWebhookStatus(delivered)
   d. Неуспех: IncrementAttempts, если attempts >= 5 — status='failed'
3. Exponential backoff между попытками: 30s → 5m → 30m → 2h → 24h
   (хранить next_attempt_at в таблице, добавить поле)

Вспомогательная функция CreateAndDispatchWebhook(ctx, db, applicationID, eventType, data):
- Создаёт запись в application_webhooks
- Если callback_url не задан — просто логирует и возвращает nil
- Воркер подхватит на следующем цикле

Инициализацию воркера добавь туда же где DocumentFetcher.
```

---

## Промпт 6 — Хендлеры Applications

```
Ты работаешь над Go-сервисом PKI (Chi router). Посмотри существующие хендлеры
в internal/handler/ чтобы понять структуру, middleware авторизации и паттерны ошибок.

Создай файл internal/handler/applications.go с хендлерами:

=== POST /api/v1/applications/{external_id}/submit ===

Request body:
{
  "documents": [
    {
      "name": "Договор.pdf",
      "source_url": "https://s3.../presigned-get",
      "target_url": "https://s3.../presigned-put",
      "target_s3_key": "applications/12345/signed/Договор.pdf"
    }
  ],
  "signer_role": "client",
  "callback_url": "https://finplatform.kz/webhook/pki",  // опционально
  "callback_secret": "hmac-secret"  // опционально, обязателен если задан callback_url
}

Логика:
1. Извлечь tenant_id из middleware авторизации
2. GetApplicationByExternalID(tenant_id, external_id)
   - Если не найдена: CreateApplication (status=active, signing_round=1)
   - Если найдена и status=cancelled: вернуть 409 "application is cancelled"
   - Если найдена: это новый раунд или редактирование — incrementing signing_round
3. Для каждого документа из запроса:
   a. FindPreviousVersions(application_id, document_name)
   b. Если есть активные — SupersedeApplicationDocument для каждой
   c. CreateApplicationDocument (version = max(previous.version)+1 или 1)
4. Обновить application.status = 'signing', signer_role, callback_url, callback_secret
   (callback_url обновлять только если передан в запросе)
5. Вернуть:
{
  "application_id": "uuid",
  "external_id": "BPM-12345",
  "signing_round": 1,
  "signer_role": "client",
  "documents": [
    {
      "id": "uuid",
      "name": "Договор.pdf",
      "version": 1,
      "status": "pending"
    }
  ]
}

=== POST /api/v1/applications/{external_id}/sign ===

Request body:
{
  "signatures": [
    {
      "document_id": "uuid",
      "cms": "base64-encoded-CMS"
    }
  ]
}

Логика:
1. GetApplicationByExternalID, проверить status='signing'
2. Для каждой подписи:
   a. GetApplicationDocumentByID, проверить что document_id принадлежит этой заявке
   b. Проверить status='ready' (скачан и готов)
   c. Сохранить CMS через существующий механизм (как в /documents/{id}/sign)
   d. Обновить application_documents.status = 'signed'
   e. Запустить горутину: загрузить подписанный PDF в S3 через ExternalS3Client
      - При успехе: status='uploaded', CreateAndDispatchWebhook(document_signed)
      - При 403 (URL протух): status='upload_failed', last_error='presigned_url_expired'
      - При других ошибках: retry 3x, потом upload_failed
3. Проверить — все ли документы текущего раунда в статусе signed/uploaded/upload_failed
   Если да: CreateAndDispatchWebhook(round_completed или round_failed)
   Обновить application.status = 'round_completed'
4. Вернуть:
{
  "succeeded": 2,
  "failed": 0,
  "documents": [
    {
      "document_id": "uuid",
      "name": "Договор.pdf",
      "version": 1,
      "status": "signed",
      "s3_key": "applications/12345/signed/Договор.pdf"
    }
  ]
}

=== GET /api/v1/applications/{external_id}/status ===

Вернуть полный статус заявки:
{
  "application_id": "uuid",
  "external_id": "BPM-12345",
  "status": "round_completed",
  "signing_round": 1,
  "signer_role": "client",
  "created_at": "...",
  "updated_at": "...",
  "documents": [
    {
      "id": "uuid",
      "name": "Договор.pdf",
      "version": 2,
      "signing_round": 1,
      "status": "uploaded",
      "s3_key": "...",
      "uploaded_at": "...",
      "superseded": false
    }
  ]
}

Включать ВСЕ версии документов (включая superseded) для аудита.
Поле "superseded": true/false для удобства клиента.

=== POST /api/v1/applications/{external_id}/cancel ===

Request body: { "reason": "Клиент отозвал заявку" }

Логика: CancelApplication(id, reason). Вернуть 200 с обновлённым статусом.
Создать и отправить вебхук application_cancelled.
Запрещено отменять уже cancelled заявки (409).

=== PATCH /api/v1/applications/{external_id}/refresh-urls ===

Request body:
{
  "documents": [
    { "document_id": "uuid", "target_url": "https://s3.../new-presigned-put" }
  ]
}

Логика: для каждого document_id проверить status='upload_failed' и
last_error='presigned_url_expired', обновить target_url, сбросить статус на 'signed',
обнулить upload_attempts. Запустить повторную загрузку.

=== POST /api/v1/applications/{external_id}/retry-upload ===

Request body: { "document_ids": ["uuid1", "uuid2"] }  // если пустой — все upload_failed

Логика: найти документы со status='upload_failed', сбросить upload_attempts=0,
статус='signed', поставить в очередь на загрузку. Вернуть список затронутых документов.
```

---

## Промпт 7 — Регистрация роутов и конфигурация

```
Ты работаешь над Go-сервисом PKI. Посмотри как регистрируются роуты в internal/server/
или main.go.

1. Зарегистрируй новые роуты Applications в существующем роутере под тем же middleware
   авторизации что используют /api/v1/documents:

   POST   /api/v1/applications/{external_id}/submit
   GET    /api/v1/applications/{external_id}/status
   POST   /api/v1/applications/{external_id}/sign
   POST   /api/v1/applications/{external_id}/cancel
   PATCH  /api/v1/applications/{external_id}/refresh-urls
   POST   /api/v1/applications/{external_id}/retry-upload

   Отдельная группа под admin middleware:
   DELETE /api/v1/admin/applications/{external_id}
   (hard-delete: удалить только если status=cancelled, иначе 409)

2. Добавь в конфигурацию (config.go или config.yaml) новые параметры:
   applications:
     fetch_interval: 10s
     fetch_batch_size: 20
     max_fetch_retries: 3
     webhook_interval: 5s
     webhook_max_attempts: 5
     s3_download_timeout: 60s
     s3_upload_timeout: 60s

3. Инициализируй и запусти воркеры DocumentFetcher и WebhookDispatcher в main.go
   после инициализации всех зависимостей. Убедись что они останавливаются по ctx.Done().

4. Добавь в ответы health check (/health или /api/v1/health) статус воркеров:
   "workers": {
     "document_fetcher": "running",
     "webhook_dispatcher": "running"
   }
```

---

## Промпт 8 — Интеграционные тесты

```
Ты работаешь над Go-сервисом PKI. Посмотри существующие тесты в _test.go файлах.

Создай файл internal/handler/applications_test.go с интеграционными тестами.
Используй тестовую БД (testcontainers или существующий тестовый хелпер).
Замокай ExternalS3Client.

Тест 1: TestApplicationSubmitNewApplication
- POST /submit с 2 документами
- Проверить: application создана, 2 application_documents в статусе 'pending'

Тест 2: TestApplicationSubmitDocumentVersioning
- POST /submit с документом "Договор.pdf"
- Второй POST /submit с документом "Договор.pdf" (то же имя)
- Проверить: первый документ status='superseded', второй version=2

Тест 3: TestApplicationSignDocuments
- Submit 1 документа → мок ExternalS3Client.DownloadFromPresignedURL возвращает PDF
- Имитировать DocumentFetcher (напрямую вызвать логику fetch)
- POST /sign с валидным CMS (использовать тестовый ключ из testdata/)
- Проверить: status='signed' или 'uploaded'

Тест 4: TestApplicationCancelAndResubmit
- Submit → cancel → повторный submit
- Проверить: новая заявка с тем же external_id, старая cancelled

Тест 5: TestApplicationRefreshURLs
- Submit → имитировать upload_failed с last_error='presigned_url_expired'
- PATCH /refresh-urls с новым target_url
- Проверить: статус сброшен на 'signed', upload_attempts=0

Тест 6: TestWebhookHMACSignature
- Создать application с callback_secret
- Имитировать document_signed событие
- Проверить что созданный webhook содержит корректный HMAC в payload/hmac_signature
```

