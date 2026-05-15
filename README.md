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
