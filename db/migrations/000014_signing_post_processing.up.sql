-- 000014_signing_post_processing.up.sql
--
-- Асинхронный постпроцессинг подписания (QR-штамп + Лист подписей + аплоад).
-- См. план: /Users/user/.claude/plans/synthetic-launching-blanket.md
--
-- Проблема: /sign/complete сегодня делает верификацию CMS через NCANode
-- (быстро, юридически значимо) и следом 3 полных прохода pdfcpu (QR-штамп
-- на все страницы, рендер Листа подписей, merge) + аплоад клиенту — всё
-- синхронно в одном HTTP-запросе. Для многостраничных документов это долго,
-- хотя с точки зрения права документ уже подписан сразу после NCANode.
--
-- Решение: отделить "подписан" (status='signed', сразу) от "артефакт готов"
-- (status='uploaded', в фоне). Колонки ниже — приватная бухгалтерия воркера
-- постпроцессинга, по аналогии с verification_status/verification_next_at
-- из 000008 (статус + счётчик попыток + время следующей попытки, sweep через
-- поллер, а не через Nack-реквьюг очереди).
ALTER TABLE signing_session_documents
  ADD COLUMN postprocess_status     VARCHAR(30),
  ADD COLUMN postprocess_error      TEXT,
  ADD COLUMN postprocess_error_code VARCHAR(50),
  ADD COLUMN postprocess_attempts   INT NOT NULL DEFAULT 0,
  ADD COLUMN postprocess_next_at    TIMESTAMPTZ,
  ADD COLUMN postprocess_started_at TIMESTAMPTZ;

CREATE INDEX idx_ssd_postprocess_pending
  ON signing_session_documents(postprocess_next_at)
  WHERE postprocess_status IN ('queued', 'retrying');
