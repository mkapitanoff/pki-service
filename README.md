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
