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

# Pre-install DejaVuSans into pdfcpu font cache so the app starts without delay.
RUN mkdir -p /root/.config/pdfcpu/fonts && \
    cp /usr/share/fonts/truetype/dejavu/DejaVuSans.ttf /root/.config/pdfcpu/fonts/ 2>/dev/null || \
    find /usr/share/fonts -name "DejaVuSans.ttf" -exec cp {} /root/.config/pdfcpu/fonts/ \; 2>/dev/null || true

WORKDIR /app

# Бинарники
COPY --from=builder /out/pki-api .
COPY --from=builder /out/pki-seed .

# Конфиги
COPY configs/ ./configs/

EXPOSE 8080

CMD ["/app/pki-api"]
