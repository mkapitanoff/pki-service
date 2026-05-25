ALTER TABLE documents ADD COLUMN IF NOT EXISTS sha256_hash_current TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_documents_sha256_current ON documents(tenant_id, sha256_hash_current);
