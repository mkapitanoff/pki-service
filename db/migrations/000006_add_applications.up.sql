-- 000006_add_applications.up.sql

CREATE TABLE applications (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID NOT NULL REFERENCES tenants(id),
    external_id      VARCHAR(255) NOT NULL,
    status           VARCHAR(50) NOT NULL DEFAULT 'active',
    signing_round    INT NOT NULL DEFAULT 1,
    signer_role      VARCHAR(100) NOT NULL,
    callback_url     TEXT,
    callback_secret  VARCHAR(255),
    cancelled_at     TIMESTAMPTZ,
    cancel_reason    TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, external_id)
);

CREATE INDEX idx_applications_tenant_external ON applications(tenant_id, external_id);
CREATE INDEX idx_applications_status ON applications(status);
CREATE INDEX idx_applications_tenant_status ON applications(tenant_id, status);

CREATE TABLE application_documents (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id   UUID NOT NULL REFERENCES applications(id),
    document_id      UUID REFERENCES documents(id),
    document_name    VARCHAR(500) NOT NULL,
    version          INT NOT NULL DEFAULT 1,
    signing_round    INT NOT NULL DEFAULT 1,
    source_url       TEXT NOT NULL,
    target_url       TEXT,
    target_s3_key    VARCHAR(1000),
    status           VARCHAR(50) NOT NULL DEFAULT 'pending',
    superseded_by    UUID REFERENCES application_documents(id),
    upload_attempts  INT NOT NULL DEFAULT 0,
    last_error       TEXT,
    uploaded_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_app_docs_application ON application_documents(application_id);
CREATE INDEX idx_app_docs_document ON application_documents(document_id);
CREATE INDEX idx_app_docs_application_status ON application_documents(application_id, status);
CREATE INDEX idx_app_docs_name_round ON application_documents(application_id, document_name, signing_round);

CREATE TABLE application_webhooks (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id   UUID NOT NULL REFERENCES applications(id),
    event_type       VARCHAR(100) NOT NULL,
    payload          JSONB NOT NULL,
    hmac_signature   VARCHAR(255),
    status           VARCHAR(50) NOT NULL DEFAULT 'pending',
    attempts         INT NOT NULL DEFAULT 0,
    next_attempt_at  TIMESTAMPTZ,
    last_attempt_at  TIMESTAMPTZ,
    delivered_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_app_webhooks_application ON application_webhooks(application_id);
CREATE INDEX idx_app_webhooks_status_created ON application_webhooks(status, created_at);
CREATE INDEX idx_app_webhooks_next_attempt ON application_webhooks(next_attempt_at) WHERE status IN ('pending', 'failed');
