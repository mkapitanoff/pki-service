-- 000014_signing_post_processing.down.sql
DROP INDEX IF EXISTS idx_ssd_postprocess_pending;
ALTER TABLE signing_session_documents
  DROP COLUMN IF EXISTS postprocess_status,
  DROP COLUMN IF EXISTS postprocess_error,
  DROP COLUMN IF EXISTS postprocess_error_code,
  DROP COLUMN IF EXISTS postprocess_attempts,
  DROP COLUMN IF EXISTS postprocess_next_at,
  DROP COLUMN IF EXISTS postprocess_started_at;
