# Chandra (PKI) — интеграция сервисов для подписания ЭЦП РК

Практический reference для любой команды, интегрирующей подписание документов ЭЦП РК через
Chandra. Источник истины (архитектурный контракт) — Platform Contract
`contracts/chandra-signing-integration.md`; здесь — runtime-версия того же контракта.

> **Инвариант:** Chandra не дорабатывается под конкретный сервис. Подключение = тенант + API-ключ
> (выдаёт команда Chandra) + соблюдение этого контракта. Всё специфичное реализуется у клиента.

Base URL: `https://pki.fin4b.kz`. OpenAPI: `/api/docs` (UI), `/api/docs/openapi.yaml` (raw).

---

## 1. Аутентификация и заголовки

- `Authorization: Bearer <API_KEY>` — токен без точек; тенант определяется по `sha256(key)`.
  (JWT-вариант — только для admin-UI.)
- `X-Request-Id: <uuid>` — сквозная корреляция; возвращается в ответе и в `error.request_id`.
- `Idempotency-Key: <uuid>` на `/sign/initiate` — **свежий на каждую попытку** (см. Правила).

Ключ и base URL держите в конфиге/секретах, не в коде: аутентификация мигрирует на OAuth2
client-credentials через корпоративный IAM (ADR-038).

---

## 2. Эндпоинты

| Метод | Путь | Назначение |
|---|---|---|
| POST | `/api/v1/sign/initiate` | Создать сессию, получить doc_id + хэши |
| POST | `/api/v1/sign/complete` | Передать CMS, собрать подписанные PDF |
| GET | `/api/v1/sign/status/{session_id}` | Статус сессии/документов |
| PATCH | `/api/v1/sign/refresh-urls` | Обновить истёкшие target-URL |
| GET | `/verify/{doc_id}` | Публичная страница проверки (без auth) |

### initiate — request
```json
{
  "signer_role": "client",
  "application_id": "PR-2026-000123",
  "callback_url": "https://<client>/pki/callback",
  "callback_secret": "<HMAC secret>",
  "documents": [{
    "name": "agreement.pdf",
    "client_ref": "doc-001",
    "source_url": "https://<s3>/...GET-presigned...",
    "target_url": "https://<s3>/...PUT-presigned...",
    "target_s3_key": "signed/PR-2026-000123/agreement.pdf",
    "hash": "<base64 SHA-256 залитых байт>",
    "hash_algorithm": "SHA256",
    "s3_bucket": "<бакет source-объекта>",
    "s3_key": "<ключ source-объекта>"
  }]
}
```
`signer_role`: `client | manager | director`. Поля `hash`/`s3_bucket`/`s3_key` — client-hash
fast-path (см. Правило 4). Если `hash` не прислан — Chandra сама скачает `source_url` и посчитает
SHA-256 (медленнее, `verification_status=unavailable`).

### initiate — response
```json
{ "session_id": "…", "documents": [
  { "doc_id": "…", "name": "agreement.pdf", "client_ref": "doc-001",
    "hash": "<base64>", "hash_algorithm": "SHA256", "status": "ready" } ] }
```

### complete
```json
// request
{ "session_id": "…", "signatures": [ { "doc_id": "…", "cms": "<base64 CAdES/CMS>" } ] }
// response — status:"signed" уже означает "юридически подписан", но НЕ значит
// "файл готов к скачиванию" (s3_key намеренно отсутствует — см. §3.1)
{ "succeeded": 3, "failed": 0, "documents": [
  { "doc_id": "…", "name": "…", "status": "signed" } ] }
```

### status
Per-doc `state`: `PENDING → FETCHING → READY → SIGNING → SIGNED → UPLOADING → UPLOADED` | `FAILED`
(`error_code`: `FETCH_FAILED` | `UPLOAD_FAILED` | `SIGNING_FAILED` | `POST_PROCESSING_FAILED` |
`TARGET_URL_EXPIRED`). `session.status`: `pending | signing | completed | failed | expired`.

`verify_url` — публичная страница проверки подписи, появляется с `state: SIGNED` (не ждёт
`UPLOADED` — верификация не зависит от готовности скачиваемого файла). `s3_key` — наоборот,
валиден только при `state: UPLOADED`, до этого отсутствует в ответе.

### webhooks
`callback_url` → события `session.document_signed | completed | failed`; подпись
`X-PKI-Signature: sha256=<hex>` (HMAC на `callback_secret`); retry 5×, backoff 1→2→4→8→16с.

---

## 3. Поток подписания (hash-flow)

1. Материализовать PDF; посчитать `sha256 = SHA-256(bytes)` — **один раз**.
2. PUT PDF в своё S3 с метаданными `x-amz-meta-sha256 = sha256`.
3. `POST /sign/initiate` с `hash=sha256`, `s3_bucket`, `s3_key`, `source_url`, `target_url`.
4. NCALayer `signHashes([hash])` — `digested:true`, `format:"cms"` (один PIN на пакет).
5. `POST /sign/complete` с CMS.
6. Поллить `GET /sign/status/{session_id}`.

CMS: NCALayer встраивает SHA-256 документа как eContent (attached CAdES/CMS); именно eContent
Chandra сверяет с хэшем документа.

### 3.1. Подписан vs Готов к скачиванию

`POST /sign/complete`, вернув `status: "signed"`, подтверждает только то, что CMS
криптографически провалидирован NCANode — это финальный юридический факт, дальше он не
изменится. QR-штамп на страницах, пересборка Листа подписей и выгрузка готового PDF в
`target_url` в этот момент продолжаются **в фоне** (обычно секунды, зависит от числа страниц).

Это правило одинаково для всех интеграторов — не доработка под конкретный сервис:

- Показывайте пользователю "Подписан" сразу по ответу `/sign/complete`.
- Кнопку **"Скачать"** держите неактивной, пока по `GET /sign/status/{session_id}` документ не
  дойдёт до `state: "UPLOADED"` — только тогда `s3_key` валиден и файл лежит по `target_url`.
- Если документ уходит в `state: "FAILED"` с `error_code: "POST_PROCESSING_FAILED"` — фоновая
  сборка/выгрузка исчерпала попытки, это терминально; смотрите `error` за подробностями.
- Если `error_code: "TARGET_URL_EXPIRED"` — presigned URL истёк до того, как выгрузка успела
  завершиться; пришлите свежий `target_url` через `PATCH /sign/refresh-urls`, выгрузка повторится
  автоматически без пересборки PDF.

---

## 4. Обязательные правила (частые ошибки)

1. **`Idempotency-Key` — свежий UUID на попытку.** НЕ выводить из `application_id`/имён/тела —
   иначе повтор вернёт устаревший ответ прошлой попытки (stale replay) и клиентский cross-check
   хэшей упадёт («хэш не совпал»). Тот же ключ — только при сетевом ретрае той же попытки.
2. **`hash` = SHA-256 ТОЧНЫХ байт, залитых в S3;** то же значение в `x-amz-meta-sha256` и в `hash`.
   Хэшировать один раз: PDF не байт-детерминирован (`/ID`, `/ModDate`).
3. **Presigned PUT должен подписывать заголовок `x-amz-meta-sha256`** — иначе метаданные не лягут.
4. **Для `verification_status=verified`** — слать `hash`+`s3_bucket`+`s3_key`+метаданные. Без них
   → `unavailable`.
5. **TTL presigned `source_url`/`target_url` ≥ времени до `complete`** (реком. ≥ 2 ч). Истёк target
   — обновить через `/refresh-urls`.
6. **Маппинг по `client_ref`,** не по имени (дубль имени → `409`).
7. **SHOULD:** `application_id` (связь с заявкой в реестре), `X-Request-Id` (дебаг).
8. **Кнопку скачивания включать по `state=UPLOADED`, не по ответу `/sign/complete`.** `status:
   "signed"` — это готовность подписи, не готовность файла; см. §3.1.

---

## 5. Коды ошибок

JSON `{ "error": { "code", "message", "request_id", "details"? } }`.

`INVALID_REQUEST` 400 · `UNAUTHORIZED` 401 · `FORBIDDEN` 403 · `DOCUMENT_NOT_FOUND`/`SESSION_NOT_FOUND` 404 ·
`DUPLICATE_DOCUMENT_NAME` / `IDEMPOTENCY_KEY_REUSED` / `SESSION_CLOSED` 409 · `SESSION_EXPIRED` 410 ·
`PAYLOAD_TOO_LARGE` 413 · `INVALID_CMS_BASE64` / `INVALID_CMS_STRUCTURE` / `HASH_MISMATCH` /
`CERT_REVOKED` / `CERT_NOT_TRUSTED` / `CMS_INVALID` 422 · `RATE_LIMITED` 429 · `INTERNAL` 500 ·
`FETCH_FAILED` 502. Асинхронный постпроцессинг (см. §3.1, отдаётся только через `/sign/status`,
не через HTTP-код): `POST_PROCESSING_FAILED` (state=FAILED — исчерпаны попытки фоновой сборки/
выгрузки), `TARGET_URL_EXPIRED` (state=FAILED — presigned URL истёк, восстановимо через
`/refresh-urls`).

`text/html` в ответе = апстрим (ingress), не контракт — трактовать как временную недоступность.

---

## 6. Лимиты

| Ресурс | Лимит |
|---|---|
| `/sign/initiate` тело / documents[] | ≤ 1 MiB / 1..20 |
| `/sign/complete`, `/refresh-urls` | ≤ 1 MiB |
| rate-limit per API-key | ~300/мин → 429 |
| `/verify/{doc_id}` | 60/мин на IP |
| TTL presigned | ≥ 2 ч |
| Idempotency-Key | 24 ч |

---

## 7. Онбординг нового сервиса

1. Получить у команды Chandra тенант + API-ключ; положить в конфиг/секреты.
2. Настроить S3: presigned GET + PUT, PUT подписывает `x-amz-meta-sha256`.
3. Реализовать hash-flow (§3) со всеми правилами (§4).
4. NCALayer: `signHashes`, `digested:true`, `format:"cms"`.
5. Тест-подпись → `complete` succeeded>0, `/verify/{doc_id}` открывается, реестр = `verified`.

Ни один шаг не требует правок кода Chandra.

---

## Связь / дебаг

Что-то «зависло» — пришлите `X-Request-Id` + время (UTC) + endpoint: команда Chandra поднимет
логи. Репозиторий: `gitlab.fin4b.kz/core/pki`.
