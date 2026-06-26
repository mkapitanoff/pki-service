-- 000009_doc_client_index.up.sql
--
-- 1.5 из плана интеграции с Lovable: сохранить порядок документов,
-- который клиент прислал в /sign/initiate.documents[]. Старый код
-- возвращал их в порядке вставки (ORDER BY created_at), что не давало
-- гарантии при параллельной вставке. С новой колонкой client_index
-- ListSessionDocuments сортирует по ней, NULLS LAST для legacy-сессий.

ALTER TABLE signing_session_documents
  ADD COLUMN client_index INT;
