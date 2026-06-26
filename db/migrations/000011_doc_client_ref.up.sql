-- 000011_doc_client_ref.up.sql
--
-- 2.1 из плана интеграции с Lovable: опциональный client_ref на документе.
-- Lovable передаёт строку-идентификатор в документе (доменный ID, не наш UUID),
-- мы возвращаем его как есть в /sign/initiate и /sign/status — клиент
-- использует для маппинга вместо сопоставления-по-индексу.
--
-- Уникальность в рамках сессии — мягкая (валидируется в handler'е, не
-- constraint'ом БД): nullable, может быть пуст у legacy-сессий.

ALTER TABLE signing_session_documents
  ADD COLUMN client_ref TEXT;
