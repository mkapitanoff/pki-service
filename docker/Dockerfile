# ── Stage 1: Build ────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/pki-api ./cmd/api/main.go
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/pki-seed ./cmd/seed/main.go

# ── Stage 2: Runtime ──────────────────────────────────────────────────────────
FROM alpine:latest

RUN apk add --no-cache ca-certificates curl tzdata ttf-dejavu fontconfig && fc-cache -fv

WORKDIR /app

# Бинарники
COPY --from=builder /out/pki-api .
COPY --from=builder /out/pki-seed .

# Конфиги
COPY configs/ ./configs/

EXPOSE 8080

CMD ["/app/pki-api"]
