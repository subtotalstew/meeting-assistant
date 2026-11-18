-- +goose Up

-- Одна встреча — одна задача обработки.
--
-- До этого ограничения таблица формально допускала две задачи на встречу:
-- GetTask брал последнюю, а markStatus обновлял все. Очередь теперь забирает
-- задачи по одной и меняет статусы в транзакции, поэтому дубль сломал бы учёт
-- попыток и восстановление.
--
-- Индекс processing_tasks_meeting_idx из миграции 00002 покрывает это ограничение
-- целиком, поэтому отдельный индекс не нужен — PostgreSQL удалит его как
-- избыточный.
ALTER TABLE processing_tasks
    ADD CONSTRAINT processing_tasks_meeting_key UNIQUE (meeting_id);

-- Выборка очереди: диспетчер забирает задачи в статусе created в порядке
-- создания. Составной индекс позволяет планировщику не читать лишние строки.
CREATE INDEX processing_tasks_status_created_idx
    ON processing_tasks (status, created_at);

-- +goose Down

DROP INDEX IF EXISTS processing_tasks_status_created_idx;

ALTER TABLE processing_tasks
    DROP CONSTRAINT IF EXISTS processing_tasks_meeting_key;
