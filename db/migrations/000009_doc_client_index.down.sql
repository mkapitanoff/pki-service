-- 000009_doc_client_index.down.sql

ALTER TABLE signing_session_documents
  DROP COLUMN IF EXISTS client_index;
