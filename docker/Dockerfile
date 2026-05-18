# ── Stage 1: Build ────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/pki-api ./cmd/api/main.go

# ── Stage 2: Runtime ──────────────────────────────────────────────────────────
FROM alpine:latest

RUN apk add --no-cache ca-certificates curl tzdata fontconfig

WORKDIR /app

# Бинарник
COPY --from=builder /out/pki-api .

# Конфиги
COPY configs/ ./configs/

# Arial Unicode MS для Кириллицы на листе подписей.
# Файл должен лежать в репозитории по пути docker/fonts/Arial Unicode.ttf
# (или иметь другое имя без пробелов: ArialUnicode.ttf).
# На сервере шрифт монтируется/копируется в образ здесь.
COPY docker/fonts/ /usr/share/fonts/truetype/arial/
RUN fc-cache -f -v

EXPOSE 8080

CMD ["/app/pki-api"]
