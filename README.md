# PKI Service

Сервис подписания PDF-документов юридически значимой ЭЦП Республики Казахстан.

## Быстрый старт

```bash
# 1. Клонировать репозиторий
git clone <repo-url>
cd pki-service

# 2. Установить инструменты (sqlc, migrate, golangci-lint)
make install-tools

# 3. Поднять инфраструктуру (test-контур)
make docker-up

# 4. Применить миграции
DATABASE_URL="postgres://user:pass@localhost:5432/eds_test?sslmode=disable" make migrate-up

# 5. Сгенерировать код из SQL
make sqlc

# 6. Запустить
make dev
```

Сервер: `http://localhost:8080`
MinIO UI: `http://localhost:9001` (minioadmin / minioadmin)
RabbitMQ UI: `http://localhost:15672` (guest / guest)

## Команды

| Команда | Описание |
|---|---|
| `make dev` | Запустить API в dev-режиме |
| `make worker` | Запустить worker |
| `make test` | Все тесты |
| `make docker-up` | Поднять test-контур |
| `make docker-down` | Остановить test-контур |
| `make migrate-up` | Применить миграции |
| `make migrate-create name=xxx` | Создать новую миграцию |
| `make sqlc` | Регенерировать код из SQL |
| `make lint` | Запустить линтер |
| `make build` | Собрать бинарники |

## Два контура

| | Test | Prod |
|---|---|---|
| NCANode | test.pki.gov.kz | pki.gov.kz |
| S3 bucket | eds-test (MinIO) | eds-prod (S3) |
| Config | configs/config.test.yaml | configs/config.prod.yaml |
| Docker | docker/docker-compose.test.yml | docker/docker-compose.prod.yml |

Переключение: `APP_ENV=test make dev` или `APP_ENV=prod make dev`

## Важно для разработчиков

- Читать `CLAUDE.md` перед началом работы
- Все криптографические операции — только через NCANode (`internal/ncanode/`)
- Каждый SQL-запрос — через sqlc-generated функции (`internal/repository/`)
- После изменения `db/queries/*.sql` — обязательно `make sqlc`
- `configs/config.prod.yaml` не коммитить (в `.gitignore`)

## Стек

Go 1.22 · Chi · PostgreSQL 15 · Redis 7 · RabbitMQ 3.12 · NCANode · MinIO/S3 · pdfcpu

## Интеграция

### Загрузка документа

```javascript
const formData = new FormData();
formData.append('file', pdfBlob, 'document.pdf');
formData.append('title', 'Договор №123');
formData.append('callback_url', 'https://your-app.com/documents/123/signed');

const response = await fetch('https://pki-service.darch.pro/api/v1/upload', {
  method: 'POST',
  headers: { 'Authorization': 'Bearer YOUR_API_KEY' },
  body: formData
});
const { data } = await response.json();
// data.sign_url — ссылка на страницу подписания
// Редирект пользователя на страницу подписания:
window.location.href = `https://pki-service.darch.pro/document/${data.document_id}`;
```

После подписания пользователь автоматически перенаправляется на:
`https://your-app.com/documents/123/signed?document_id=<uuid>&status=signed`

### Webhook payload (POST на ваш URL)

```json
{
  "event": "document.signed",
  "document_id": "uuid",
  "signature_id": "uuid",
  "signer_name": "ИВАНОВ ИВАН",
  "signed_at": "2026-05-22T10:00:00Z",
  "download_url": "https://pki-service.darch.pro/api/v1/documents/{id}/file"
}
```

### Скачать подписанный PDF

```
GET https://pki-service.darch.pro/api/v1/documents/{document_id}/file
Authorization: Bearer YOUR_API_KEY
```

---

## Batch API

Batch-эндпоинты позволяют загружать и подписывать несколько документов одним запросом.

### POST /api/v1/batch/upload

Загрузка нескольких PDF-файлов за один запрос.

**Request:** `multipart/form-data`
- `files[]` — массив PDF-файлов (обязательно)
- `titles[]` — массив названий (опционально; если не передан — берётся имя файла без `.pdf`)
- `callback_url` — общий callback URL для всех документов (опционально)

**Response 201:**
```json
{
  "documents": [
    {
      "document_id": "3c7f38a7-b6eb-40ce-acfd-f2d4bb644160",
      "title": "Договор поставки",
      "sha256_hash": "a3f1...",
      "status": "draft",
      "deduplicated": false
    }
  ],
  "total": 2
}
```

### POST /api/v1/batch/sign

Подписание нескольких документов одним синхронным запросом. Таймаут — 10 минут.
Ошибка одного документа не прерывает обработку остальных.

**Request:** `application/json`
```json
{
  "documents": [
    {
      "document_id": "3c7f38a7-b6eb-40ce-acfd-f2d4bb644160",
      "cms": "<base64 CMS от NCALayer>",
      "role": "client"
    }
  ]
}
```

**Response 200:**
```json
{
  "results": [
    {
      "document_id": "3c7f38a7-b6eb-40ce-acfd-f2d4bb644160",
      "status": "signed",
      "signature_id": "a1b2c3d4-...",
      "download_url": "https://pki-service.darch.pro/api/v1/documents/.../file"
    },
    {
      "document_id": "fd8bb651-...",
      "status": "error",
      "error": "CMS signature is invalid"
    }
  ],
  "total": 2,
  "succeeded": 1,
  "failed": 1
}
```

### Пример на Python

```python
import requests

API_KEY = "YOUR_API_KEY"
BASE_URL = "https://pki-service.darch.pro"
HEADERS = {"Authorization": f"Bearer {API_KEY}"}

# 1. Загрузить несколько документов
files = [open("doc1.pdf", "rb"), open("doc2.pdf", "rb")]
response = requests.post(
    f"{BASE_URL}/api/v1/batch/upload",
    headers=HEADERS,
    files=[("files[]", f) for f in files],
    data={"callback_url": "https://your-app.com/signed"},
)
documents = response.json()["documents"]

# 2. Пользователь подписывает через NCALayer — получаем массив CMS
# cms_signatures = [cms1, cms2]  ← от NCALayer

# 3. Отправить все подписи одним запросом
sign_response = requests.post(
    f"{BASE_URL}/api/v1/batch/sign",
    headers={**HEADERS, "Content-Type": "application/json"},
    json={
        "documents": [
            {"document_id": doc["document_id"], "cms": cms, "role": "client"}
            for doc, cms in zip(documents, cms_signatures)
        ]
    },
)
results = sign_response.json()

# 4. Скачать подписанные документы
for result in results["results"]:
    if result["status"] == "signed":
        pdf = requests.get(result["download_url"], headers=HEADERS)
        with open(f"{result['document_id']}_signed.pdf", "wb") as f:
            f.write(pdf.content)

print(f"Успешно: {results['succeeded']}, Ошибок: {results['failed']}")
```

### Пример на JavaScript

```javascript
const API_KEY = "YOUR_API_KEY";
const BASE_URL = "https://pki-service.darch.pro";
const headers = { Authorization: `Bearer ${API_KEY}` };

// 1. Загрузить несколько документов
async function batchUpload(files) {
  const form = new FormData();
  for (const file of files) form.append("files[]", file);

  const res = await fetch(`${BASE_URL}/api/v1/batch/upload`, {
    method: "POST",
    headers,
    body: form,
  });
  return (await res.json()).documents;
}

// 2. Подписать через NCALayer и отправить все CMS
async function batchSign(documents, cmsSignatures) {
  const res = await fetch(`${BASE_URL}/api/v1/batch/sign`, {
    method: "POST",
    headers: { ...headers, "Content-Type": "application/json" },
    body: JSON.stringify({
      documents: documents.map((doc, i) => ({
        document_id: doc.document_id,
        cms: cmsSignatures[i],
        role: "client",
      })),
    }),
  });
  return res.json();
}

// 3. Скачать подписанные PDF
async function downloadSigned(results) {
  for (const result of results.results) {
    if (result.status !== "signed") continue;
    const pdf = await fetch(result.download_url, { headers });
    const blob = await pdf.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${result.document_id}_signed.pdf`;
    a.click();
    URL.revokeObjectURL(url);
  }
}

// Полный флоу
const files = [/* File objects из input */];
const documents = await batchUpload(files);
// ... пользователь подписывает через NCALayer ...
const results = await batchSign(documents, cmsSignatures);
console.log(`Успешно: ${results.succeeded}, Ошибок: ${results.failed}`);
await downloadSigned(results);
```
