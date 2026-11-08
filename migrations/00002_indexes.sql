-- +goose Up

-- Список встреч пользователя: команда list, сортировка по дате создания.
CREATE INDEX meetings_user_created_idx ON meetings (user_id, created_at DESC);

-- Фильтрация списка по статусу (команда stats и выборки по статусу).
CREATE INDEX meetings_user_status_idx ON meetings (user_id, status);

-- Восстановление незавершённых задач после рестарта: выборка по статусу.
CREATE INDEX processing_tasks_status_idx ON processing_tasks (status);

-- Поиск задачи по встрече: status <id>, retry <id>.
CREATE INDEX processing_tasks_meeting_idx ON processing_tasks (meeting_id);

-- История вопросов по встрече в порядке добавления.
CREATE INDEX qa_history_meeting_created_idx ON qa_history (meeting_id, created_at);

-- +goose Down

DROP INDEX IF EXISTS qa_history_meeting_created_idx;
DROP INDEX IF EXISTS processing_tasks_meeting_idx;
DROP INDEX IF EXISTS processing_tasks_status_idx;
DROP INDEX IF EXISTS meetings_user_status_idx;
DROP INDEX IF EXISTS meetings_user_created_idx;
