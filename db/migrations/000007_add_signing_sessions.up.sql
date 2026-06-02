-- 000007_add_signing_sessions.up.sql

CREATE TABLE signing_sessions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id),
    application_id  VARCHAR(255),
    signer_role     VARCHAR(100) NOT NULL,
    callback_url    TEXT,
    callback_secret VARCHAR(255),
    status          VARCHAR(50) NOT NULL DEFAULT 'pending',
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT (now() + INTERVAL '2 hours'),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_signing_sessions_tenant_status ON signing_sessions(tenant_id, status);
CREATE INDEX idx_signing_sessions_expires_at ON signing_sessions(expires_at);

CREATE TABLE signing_session_documents (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id      UUID NOT NULL REFERENCES signing_sessions(id) ON DELETE CASCADE,
    document_name   VARCHAR(500) NOT NULL,
    source_url      TEXT NOT NULL,
    target_url      TEXT,
    target_s3_key   VARCHAR(1000),
    content_hash    VARCHAR(64) NOT NULL,
    cached_s3_key   VARCHAR(1000),
    signed_s3_key   VARCHAR(1000),
    cms_s3_key      VARCHAR(1000),
    status          VARCHAR(50) NOT NULL DEFAULT 'pending',
    last_error      TEXT,
    upload_attempts INT NOT NULL DEFAULT 0,
    signed_at       TIMESTAMPTZ,
    uploaded_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_ssd_session ON signing_session_documents(session_id);
CREATE INDEX idx_ssd_session_status ON signing_session_documents(session_id, status);
CREATE UNIQUE INDEX idx_ssd_session_document_name ON signing_session_documents(session_id, document_name);
