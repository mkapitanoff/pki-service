-- 000013_idempotency_request_hash.down.sql
ALTER TABLE idempotency_keys
    DROP COLUMN IF EXISTS request_hash;
