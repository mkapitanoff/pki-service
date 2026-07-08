-- Signer/cert поля для signing_session_documents, зеркалят signatures.
-- ncanode.VerifyResult вычисляется в /sign/complete, но раньше нигде не
-- персистился — терялся сразу после рендера Листа подписей. Без этих полей
-- нет данных для публичной verify-страницы session-документов, и QR в PDF
-- указывал на нерабочий плейсхолдер data:cms:<id>.
ALTER TABLE signing_session_documents
    ADD COLUMN signer_iin      TEXT,
    ADD COLUMN signer_name     TEXT,
    ADD COLUMN signer_bin      TEXT,
    ADD COLUMN org_name        TEXT,
    ADD COLUMN signer_type     TEXT,
    ADD COLUMN basis           TEXT,
    ADD COLUMN cert_serial     TEXT,
    ADD COLUMN cert_not_before TIMESTAMPTZ,
    ADD COLUMN cert_not_after  TIMESTAMPTZ,
    ADD COLUMN ca_name         TEXT,
    ADD COLUMN ocsp_status     TEXT,
    ADD COLUMN tsp_time        TIMESTAMPTZ,
    ADD COLUMN sign_format     TEXT,
    ADD COLUMN qr_url          TEXT;
