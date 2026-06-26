-- 000011_doc_client_ref.down.sql

ALTER TABLE signing_session_documents
  DROP COLUMN IF EXISTS client_ref;
