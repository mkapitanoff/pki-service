-- 000008_session_hash_verification.up.sql
--
-- Серверный SHA-256 + асинхронная сверка целостности.
-- См. план: /Users/user/.claude/plans/synthetic-launching-blanket.md

-- 1. signing_session_documents: client-mode hash + verification fields
ALTER TABLE signing_session_documents
  ALTER COLUMN content_hash DROP NOT NULL,
  ADD COLUMN hash_source             VARCHAR(20) NOT NULL DEFAULT 'computed',
  ADD COLUMN source_s3_bucket        VARCHAR(255),
  ADD COLUMN source_s3_key           VARCHAR(1024),
  ADD COLUMN source_content_type     VARCHAR(100),
  ADD COLUMN source_size_bytes       BIGINT,
  ADD COLUMN source_meta_hash        VARCHAR(64),
  ADD COLUMN verification_status     VARCHAR(30),
  ADD COLUMN verification_checked_at TIMESTAMPTZ,
  ADD COLUMN verification_error      TEXT,
  ADD COLUMN verification_attempts   INT NOT NULL DEFAULT 0,
  ADD COLUMN verification_next_at    TIMESTAMPTZ,
  ADD CONSTRAINT ck_ssd_client_hash_present
    CHECK (hash_source <> 'client' OR content_hash IS NOT NULL);

CREATE INDEX idx_ssd_verification_pending
  ON signing_session_documents(verification_next_at)
  WHERE verification_status IN ('pending', 'retrying');

-- 2. signing_sessions: внутренний агрегат worst-of
ALTER TABLE signing_sessions
  ADD COLUMN verification_status VARCHAR(30);
