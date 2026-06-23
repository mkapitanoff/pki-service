-- 000008_session_hash_verification.down.sql

ALTER TABLE signing_sessions
  DROP COLUMN IF EXISTS verification_status;

DROP INDEX IF EXISTS idx_ssd_verification_pending;

ALTER TABLE signing_session_documents
  DROP CONSTRAINT IF EXISTS ck_ssd_client_hash_present,
  DROP COLUMN IF EXISTS verification_next_at,
  DROP COLUMN IF EXISTS verification_attempts,
  DROP COLUMN IF EXISTS verification_error,
  DROP COLUMN IF EXISTS verification_checked_at,
  DROP COLUMN IF EXISTS verification_status,
  DROP COLUMN IF EXISTS source_meta_hash,
  DROP COLUMN IF EXISTS source_size_bytes,
  DROP COLUMN IF EXISTS source_content_type,
  DROP COLUMN IF EXISTS source_s3_key,
  DROP COLUMN IF EXISTS source_s3_bucket,
  DROP COLUMN IF EXISTS hash_source,
  ALTER COLUMN content_hash SET NOT NULL;
