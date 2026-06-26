# PKI Service ↔ Lovable Factoring integration

Документ для команды Lovable factoring. Описывает hash-flow, headers,
коды ошибок, мэппинг документов, idempotency, webhook callback, лимиты и
SLA. Покрывает Lovable жалобы #1–#13 (см. integration backlog).

Базовый URL: `https://pki-service.darch.pro` (production), `https://pki.fin4b.kz` (миграция fin4b ICDC). Cutover план — см. ADR.

OpenAPI спека: `/api/docs` (Redoc UI), сырой YAML: `/api/docs/openapi.yaml`.

---

## 1. Аутентификация

`Authorization: Bearer <API_KEY>` — длинный API-токен без точек. Тенант определяется на стороне PKI по SHA-256 хэшу ключа. JWT-вариант (для админ-UI) тоже поддерживается, но для серверной интеграции из Lovable Edge используется API-key.

Кредиты на новый кластер (`pki.fin4b.kz`) идентичны старому — мигрируются вместе с БД (`api_keys` таблица, см. PKI-16).

---

## 2. Сквозная корреляция: `X-Request-Id`

Каждый запрос → передавайте `X-Request-Id: <uuid>` (или любая строка ≤ 255 символов). PKI:

- Если заголовок есть — используется как есть.
- Если пуст — PKI генерирует UUID.
- Возвращается в response header **и** в JSON-теле ошибок (`error.request_id`).

Один и тот же ID для всей операции у клиента — сильно упрощает дебаг
случаев когда что-то «зависло»: вы grep'ите по нему в Edge-логах,
команда PKI — в своих.

```http
POST /api/v1/sign/initiate
X-Request-Id: 8b6e2f0a-3ec4-4d4a-b2c0-e2f8a44d2f10
Idempotency-Key: 4a9b1f23-...
Authorization: Bearer pkis_...
```

---

## 3. Hash-flow (рекомендуется)

Раньше PKI скачивал каждый PDF в `/sign/initiate` чтобы посчитать SHA-256. Это узкое место. Теперь — **клиент уже считает хэш при upload в S3** и присылает в payload:

```json
{
  "signer_role": "client",
  "application_id": "FF-2026-00123",
  "callback_url": "https://factoring.lovable.dev/api/pki/callback",
  "callback_secret": "<HMAC secret>",
  "documents": [
    {
      "name": "agreement.pdf",
      "client_ref": "doc-001",
      "source_url": "https://s3.eu-1.amazonaws.com/...&X-Amz-Signature=...",
      "target_url": "https://s3.eu-1.amazonaws.com/...PUT...",
      "target_s3_key": "signed/FF-2026-00123/agreement.pdf",
      "s3_bucket": "lovable-uploads",
      "s3_key": "uploads/FF-2026-00123/agreement.pdf",
      "hash": "fOm6P5G4Z6+Q1/0wM9ZqLtN5lXjE6XdYZb+VsHmCkR8=",
      "hash_algorithm": "SHA256",
      "size": 184523,
      "content_type": "application/pdf"
    }
  ]
}
```

Поведение PKI:

- `hash` есть → PKI принимает его как авторитетный, **не качает `source_url`** синхронно. `status="ready"` сразу, `hash` echo + `hash_algorithm: "SHA256"` в response.
- `hash` отсутствует → fallback на старый flow (синхронный fetch + SHA-256), TTL `source_url` ≥ 5 минут с момента вызова.

После этого:

1. NCALayer `kz.gov.pki.knca.basics.signHashes` подписывает массив hash'ей за один пин-промпт (используйте detached CMS).
2. `POST /api/v1/sign/complete { session_id, signatures: [{doc_id, cms}, ...] }`.
3. Поллите `GET /api/v1/sign/status/{session_id}` — раз в 2с, до 120с.

---

## 4. Маппинг документов request ↔ response

Lovable Edge сейчас сопоставляет `response.documents[i]` с `request.documents[i]` по индексу. С 2026-06 есть **два надёжных способа**:

| Способ | Когда применять |
|---|---|
| `client_ref` (рекомендуется) | Если у вас есть стабильный доменный ID документа |
| `client_index` | Если документы безымянные / без ID |
| Имя файла | НЕ использовать — дубли вызовут 409 |

**`client_ref`** (опц.):
```json
{"name": "agreement.pdf", "client_ref": "doc-001", ...}
```
PKI возвращает его как есть в `/sign/initiate` response и в `/sign/status`. Уникальность валидируется в рамках сессии (дубль → `400 INVALID_REQUEST` с `details.reason = "duplicate_client_ref"`).

**`client_index`** — PKI хранит позицию в массиве запроса. `ListSessionDocuments` сортирует `ORDER BY client_index NULLS LAST, created_at`, так что `response.documents[i].doc_id` всегда соответствует `request.documents[i]`.

**Имя файла** — уникально в рамках сессии (PG unique index). Дубль → `409 DUPLICATE_DOCUMENT_NAME` с `details = {name, index}`.

---

## 5. Идемпотентность: `Idempotency-Key`

`/sign/initiate` дедуплицируется заголовком `Idempotency-Key`. Повтор с тем же ключом + теми же `(method, path, tenant)` в течение 24h:

- Возвращает закэшированный response (HTTP status + body).
- Дополнительный заголовок `X-Idempotent-Replay: true`.
- НЕ создаёт дубль сессии в БД.

Рекомендация: используйте `Idempotency-Key = uuid v4` или `sha256(request_body)`. Длина ≤ 255 символов. После 24h ключ освобождается.

Concurrent повтор того же ключа (две Edge-функции одновременно) — `ON CONFLICT DO NOTHING`, побеждает первая транзакция; второй вызов на ретрае получит её ответ.

---

## 6. Единый формат ошибок

Все 4xx/5xx ответы — JSON `application/json`:

```json
{
  "error": {
    "code": "HASH_MISMATCH",
    "message": "CMS messageDigest does not match document hash",
    "request_id": "8b6e2f0a-3ec4-4d4a-b2c0-e2f8a44d2f10",
    "details": { "doc_id": "..." }
  }
}
```

Известные коды (полный список — в OpenAPI):

**Общие**
- `INVALID_REQUEST` 400 — невалидная схема, отсутствует поле, etc.
- `UNAUTHORIZED` 401 — нет API-key / неверный.
- `FORBIDDEN` 403 — tenant неактивен.
- `PAYLOAD_TOO_LARGE` 413 — превышен лимит (`details.limit_bytes`).
- `INTERNAL` 500 — серверная ошибка, в логах PKI есть `request_id`.

**Документы**
- `DOCUMENT_NOT_FOUND` 404
- `DUPLICATE_DOCUMENT_NAME` 409 — `details: {name, index}`
- (`INVALID_REQUEST` с `details.reason: "duplicate_client_ref"`)

**Сессии**
- `SESSION_NOT_FOUND` 404
- `SESSION_EXPIRED` 410
- `SESSION_CLOSED` 409 — сессия уже completed/failed/expired

**CMS / verifyCMS**
- `INVALID_CMS_BASE64` 422 — base64 не парсится
- `INVALID_CMS_STRUCTURE` 422 — payload не CMS/PKCS#7
- `HASH_MISMATCH` 422 — messageDigest ≠ stored content_hash
- `CERT_REVOKED` 422 — OCSP отозвал серт
- `CERT_NOT_TRUSTED` 422 — нет цепочки доверия
- `CMS_INVALID` 422 — общая ошибка валидации NCANode

**Fetch**
- `FETCH_FAILED` 502 — все документы провалили fetch (legacy flow без `hash`)

При 5xx PKI всегда возвращает JSON с `request_id`. Если по какой-то причине пришла HTML-страница (`text/html`) — это апстрим nginx/Ingress, **не наш контракт** — продолжайте парсить такое как PKI_UNAVAILABLE (см. ваш fallback).

---

## 7. Статусы сессии и документа

`GET /api/v1/sign/status/{session_id}` возвращает:

```json
{
  "session_id": "...",
  "status": "signing",
  "expires_at": "2026-06-26T14:00:00Z",
  "application_id": "FF-2026-00123",
  "documents": [
    {
      "doc_id": "...",
      "name": "agreement.pdf",
      "client_ref": "doc-001",
      "status": "uploaded",
      "state": "UPLOADED",
      "s3_key": "signed/FF-2026-00123/agreement.pdf",
      "signed_at": "2026-06-26T12:01:34Z",
      "uploaded_at": "2026-06-26T12:01:36Z",
      "error_code": "",
      "error": null
    }
  ]
}
```

**Per-document `state` enum** (рекомендуется для UI/логики):
- `PENDING` — создан, ждёт fetch.
- `FETCHING` — PKI качает PDF.
- `READY` — готов к подписанию (hash вычислен).
- `SIGNING` — `/sign/complete` принят, верификация CMS.
- `SIGNED` — CMS прошёл NCANode validate, PDF собран.
- `UPLOADING` — PUT в `target_url` клиента.
- `UPLOADED` — терминальный успешный.
- `FAILED` — терминальный неудачный, см. `error_code`.

**`error_code`** (при `state=FAILED`):
- `FETCH_FAILED` — PDF не скачался по `source_url` (TTL истёк, 4xx/5xx).
- `UPLOAD_FAILED` — PUT в `target_url` не прошёл. `/sign/refresh-urls` обновляет URL и резетит счётчик попыток.
- `SIGNING_FAILED` — CMS не прошёл валидацию.

Поле `status` (raw) сохранено для back-compat — игнорируйте, используйте `state`.

**Агрегированный `session.status`** derive из документов:
- `pending` — все ready, ничего не подписывается.
- `signing` — есть документы в процессе.
- `completed` — все uploaded.
- `failed` — есть upload_failed и нет активных.
- `expired` — session.expires_at прошёл.

---

## 8. Webhook callback

`callback_url` в `/sign/initiate` → PKI вызывает его при событиях:

- `session.document_signed` — один документ дошёл до signed.
- `session.completed` — все документы uploaded.
- `session.failed` — есть terminal-failure и сессия больше не делает прогресс.

Подпись HMAC: header `X-PKI-Signature: sha256=<hex>` от raw body, ключ — `callback_secret` из `/sign/initiate`. Параллельно есть `X-PKI-Event`, `X-PKI-Timestamp`.

Retry policy: 5 попыток с экспоненциальным backoff (1s → 2s → 4s → 8s → 16s); 2xx-response = считается доставленным. PKI логирует код ответа и первые 512 байт body для диагностики:

```
webhook_dispatcher.deliver hook=<uuid> app=<uuid> event=session.completed url=<url> status=200 body="{\"ok\":true}"
```

После cutover на `pki.fin4b.kz` HMAC-секрет тот же — Lovable получает callbacks параллельно с обоих кластеров в канарее (см. cutover план), но подпись валидна, тело идентично.

---

## 9. Лимиты

| Endpoint | Лимит body | Лимит documents[] | Примечание |
|---|---|---|---|
| `/api/v1/sign/initiate` | 1 MiB | 1..20 | На превышение — 413 `PAYLOAD_TOO_LARGE`, на лишний документ — 400 |
| `/api/v1/sign/complete` | 1 MiB | — | CMS-строки в base64 |
| `/api/v1/sign/refresh-urls` | 1 MiB | — | |
| `/api/v1/upload`, `/api/v1/batch/upload` | 50 MiB | — | multipart PDF |

Rate-limit на API-key — 300 req/min (по умолчанию, конфигурируется в `cfg.RateLimit.APIPerMinute`). Превышение → 429 `RATE_LIMITED`. На `/verify/{signature_id}` — 60 req/min per-IP.

TTL presigned URL: рекомендуем 2 часа на `source_url` и 2 часа на `target_url`. Минимум — 10 минут с момента вызова `/sign/initiate` (иначе вероятен `presigned_url_expired` до окончания подписания).

---

## 10. Roadmap / cutover

- **2026-06**: новый кластер `pki.fin4b.kz` готов. Lovable Edge добавляет per-tenant flag `PKI_SERVICE_BASE_URL` для канарейки.
- **2026-07**: 10% → 50% → 100% по тенантам, шаг 1 неделя.
- **2026-08**: DNS-flip `pki-service.darch.pro` → новый кластер.
- **T+30**: декомиссия старого VPS.

Прерывания контракта в этом окне НЕ планируются. Если нужно — оповещение за ≥ 2 недели через ADR + changelog.

---

## 11. Корреспонденция

- Issues / новый contract: gitlab.fin4b.kz/core/pki (или github.com/mkapitanoff/pki-service до cutover).
- Plane эпик: «PKI / Lovable integration».
- Slack/Telegram канал: см. внутренний contact list.
- Когда что-то ломается: пришлите `X-Request-Id` + время (в UTC) + endpoint — PKI поднимет логи за секунды.
