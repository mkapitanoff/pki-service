DROP INDEX IF EXISTS idx_documents_sha256_current;
ALTER TABLE documents DROP COLUMN IF EXISTS sha256_hash_current;
