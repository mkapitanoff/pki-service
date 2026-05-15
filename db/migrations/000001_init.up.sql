CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TYPE tenant_type AS ENUM ('individual', 'legal_entity');
CREATE TYPE doc_status AS ENUM ('draft', 'pending', 'partially_signed', 'signed', 'rejected');
CREATE TYPE ocsp_status_type AS ENUM ('good', 'revoked', 'unknown');

CREATE TABLE tenants (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    type        tenant_type NOT NULL,
    is_active   BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE api_keys (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL REFERENCES tenants(id),
    key_hash     TEXT NOT NULL UNIQUE,
    label        TEXT NOT NULL,
    is_active    BOOLEAN NOT NULL DEFAULT true,
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_api_keys_tenant ON api_keys(tenant_id);

CREATE TABLE documents (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id),
    title           TEXT,
    s3_key_original TEXT NOT NULL,
    s3_key_current  TEXT NOT NULL,
    current_version INT NOT NULL DEFAULT 0,
    status          doc_status NOT NULL DEFAULT 'draft',
    metadata        JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_documents_tenant ON documents(tenant_id);

CREATE TABLE document_versions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id UUID NOT NULL REFERENCES documents(id),
    tenant_id   UUID NOT NULL,
    version     INT NOT NULL,
    s3_key      TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(document_id, version)
);

CREATE TABLE signatures (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id     UUID NOT NULL REFERENCES documents(id),
    tenant_id       UUID NOT NULL,
    version_number  INT NOT NULL,
    sequence_num    INT NOT NULL,

    cms_b64         TEXT NOT NULL,
    role            TEXT NOT NULL,
    signer_iin      TEXT,
    signer_name     TEXT NOT NULL,
    signer_bin      TEXT,
    org_name        TEXT,
    signer_type     TEXT NOT NULL,
    basis           TEXT,

    cert_serial     TEXT NOT NULL,
    cert_not_before TIMESTAMPTZ NOT NULL,
    cert_not_after  TIMESTAMPTZ NOT NULL,
    ca_name         TEXT NOT NULL,

    ocsp_status     ocsp_status_type NOT NULL,
    ocsp_checked_at TIMESTAMPTZ NOT NULL,
    tsp_time        TIMESTAMPTZ,
    sha256_hash     TEXT NOT NULL,
    sign_format     TEXT NOT NULL DEFAULT 'CAdES (CMS, PKCS#7)',

    qr_url          TEXT NOT NULL,

    signed_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_signatures_document ON signatures(document_id);
CREATE INDEX idx_signatures_tenant ON signatures(tenant_id);

CREATE TABLE webhooks (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id),
    url         TEXT NOT NULL,
    events      TEXT[] NOT NULL,
    secret      TEXT NOT NULL,
    is_active   BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE webhook_deliveries (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    webhook_id    UUID NOT NULL REFERENCES webhooks(id),
    event         TEXT NOT NULL,
    payload       JSONB NOT NULL,
    attempt       INT NOT NULL DEFAULT 1,
    status        TEXT NOT NULL,
    response_code INT,
    error_msg     TEXT,
    scheduled_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at  TIMESTAMPTZ
);

CREATE TABLE audit_log (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL,
    action      TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id   UUID,
    actor_id    UUID,
    meta        JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_audit_tenant_time ON audit_log(tenant_id, created_at DESC);
