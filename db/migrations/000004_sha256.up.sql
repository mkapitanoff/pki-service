ALTER TABLE documents ADD COLUMN IF NOT EXISTS sha256_hash TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_documents_sha256 ON documents(tenant_id, sha256_hash);
