-- 000010_idempotency_keys.up.sql
--
-- 2.2 из плана интеграции с Lovable: поддержка Idempotency-Key header.
-- При повторном вызове /sign/initiate с тем же ключом — отдаём ранее
-- сохранённый response без повторного создания сессии.
--
-- TTL: 24 часа (cleanup делается воркером session_cleanup).
-- Ключ изолирован по tenant_id — два tenant'а с одинаковым "abc" не
-- конфликтуют между собой.

CREATE TABLE idempotency_keys (
    tenant_id      UUID NOT NULL,
    idem_key       TEXT NOT NULL,
    method         TEXT NOT NULL,
    path           TEXT NOT NULL,
    status_code    INT  NOT NULL,
    response_body  JSONB NOT NULL,
    session_id     UUID,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at     TIMESTAMPTZ NOT NULL DEFAULT (now() + INTERVAL '24 hours'),
    PRIMARY KEY (tenant_id, idem_key, method, path)
);

CREATE INDEX idx_idempotency_expires_at ON idempotency_keys(expires_at);
