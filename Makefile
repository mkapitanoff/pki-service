.PHONY: help dev worker build test test-cover lint \
        docker-up docker-down docker-up-prod \
        migrate-up migrate-down migrate-create \
        sqlc install-tools clean

APP_ENV  ?= test
BIN_DIR  := bin
API_BIN  := $(BIN_DIR)/api
WORK_BIN := $(BIN_DIR)/worker

# ─────────────────────────────────────────
help: ## Показать список команд
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

# ─────────────────────────────────────────
# Локальная разработка
# ─────────────────────────────────────────

dev: ## Запустить API сервер (APP_ENV=test по умолчанию)
	APP_ENV=$(APP_ENV) go run cmd/api/main.go

worker: ## Запустить worker (RabbitMQ consumer)
	APP_ENV=$(APP_ENV) go run cmd/worker/main.go

build: ## Собрать бинарники в bin/
	@mkdir -p $(BIN_DIR)
	go build -ldflags="-s -w" -o $(API_BIN) cmd/api/main.go
	go build -ldflags="-s -w" -o $(WORK_BIN) cmd/worker/main.go
	@echo "Built: $(API_BIN) $(WORK_BIN)"

clean: ## Удалить бинарники и артефакты
	rm -rf $(BIN_DIR) coverage.out coverage.html

# ─────────────────────────────────────────
# Тесты
# ─────────────────────────────────────────

test: ## Запустить все тесты
	APP_ENV=test go test ./... -v -count=1 -race -timeout 60s

test-short: ## Запустить только unit-тесты (без интеграционных)
	APP_ENV=test go test ./... -v -count=1 -short

test-cover: ## Тесты с отчётом покрытия
	APP_ENV=test go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# ─────────────────────────────────────────
# Docker
# ─────────────────────────────────────────

docker-up: ## Поднять test-контур (PG + Redis + RabbitMQ + MinIO + NCANode)
	docker compose -f docker/docker-compose.test.yml up -d
	@echo "Test контур запущен. MinIO UI: http://localhost:9001"
	@echo "RabbitMQ UI: http://localhost:15672 (guest/guest)"

docker-down: ## Остановить test-контур
	docker compose -f docker/docker-compose.test.yml down

docker-logs: ## Логи test-контура
	docker compose -f docker/docker-compose.test.yml logs -f

docker-up-prod: ## Поднять prod-контур (ОСТОРОЖНО)
	docker compose -f docker/docker-compose.prod.yml up -d

docker-down-prod: ## Остановить prod-контур
	docker compose -f docker/docker-compose.prod.yml down

# ─────────────────────────────────────────
# Миграции (golang-migrate)
# ─────────────────────────────────────────

migrate-up: ## Применить все миграции (DATABASE_URL из env)
	@[ -n "$(DATABASE_URL)" ] || (echo "ERROR: DATABASE_URL не задан"; exit 1)
	migrate -path db/migrations -database "$(DATABASE_URL)" up
	@echo "Миграции применены"

migrate-down: ## Откатить последнюю миграцию
	@[ -n "$(DATABASE_URL)" ] || (echo "ERROR: DATABASE_URL не задан"; exit 1)
	migrate -path db/migrations -database "$(DATABASE_URL)" down 1

migrate-status: ## Статус миграций
	@[ -n "$(DATABASE_URL)" ] || (echo "ERROR: DATABASE_URL не задан"; exit 1)
	migrate -path db/migrations -database "$(DATABASE_URL)" version

migrate-create: ## Создать новую миграцию (make migrate-create name=add_webhooks)
	@[ -n "$(name)" ] || (echo "ERROR: укажи make migrate-create name=<migration_name>"; exit 1)
	migrate create -ext sql -dir db/migrations -seq $(name)
	@echo "Созданы файлы миграции: db/migrations/*_$(name).up.sql и *.down.sql"

# ─────────────────────────────────────────
# sqlc
# ─────────────────────────────────────────

sqlc: ## Сгенерировать Go-код из SQL запросов
	sqlc generate
	@echo "sqlc: код сгенерирован в internal/repository/"

sqlc-check: ## Проверить sqlc конфиг без генерации
	sqlc compile

# ─────────────────────────────────────────
# Инструменты
# ─────────────────────────────────────────

install-tools: ## Установить dev-инструменты (sqlc, migrate, golangci-lint)
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
	go install github.com/golang-migrate/migrate/v4/cmd/migrate@latest
	echo "golangci-lint installed via brew"
	@echo "Инструменты установлены. Убедись что GOPATH/bin в PATH."

lint: ## Запустить линтер
	golangci-lint run ./...

# ─────────────────────────────────────────
# Быстрый старт (для новых разработчиков)
# ─────────────────────────────────────────

setup: ## Полный setup: инструменты + docker + миграции
	@echo "1. Устанавливаем инструменты..."
	$(MAKE) install-tools
	@echo "2. Поднимаем test-контур..."
	$(MAKE) docker-up
	@echo "3. Ждём БД (10 сек)..."
	sleep 10
	@echo "4. Применяем миграции..."
	DATABASE_URL="postgres://user:pass@localhost:5432/eds_test?sslmode=disable" $(MAKE) migrate-up
	@echo "5. Генерируем sqlc..."
	$(MAKE) sqlc
	@echo "\n✓ Проект готов. Запусти: make dev"
