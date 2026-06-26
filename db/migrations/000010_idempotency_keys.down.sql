-- 000010_idempotency_keys.down.sql

DROP INDEX IF EXISTS idx_idempotency_expires_at;
DROP TABLE IF EXISTS idempotency_keys;
