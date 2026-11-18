-- +goose Up

CREATE TABLE users (
    id          uuid PRIMARY KEY,
    external_id text        NOT NULL UNIQUE,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE meetings (
    id         uuid PRIMARY KEY,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title      text        NOT NULL DEFAULT '',
    file_name  text        NOT NULL,
    status     text        NOT NULL,
    error      text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT meetings_status_check CHECK (
        status IN ('created', 'processing', 'transcribed', 'summarized', 'completed', 'failed')
    )
);

CREATE TABLE processing_tasks (
    id          uuid PRIMARY KEY,
    meeting_id  uuid        NOT NULL REFERENCES meetings (id) ON DELETE CASCADE,
    status      text        NOT NULL,
    attempts    integer     NOT NULL DEFAULT 0,
    error       text        NOT NULL DEFAULT '',
    started_at  timestamptz,
    finished_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT processing_tasks_status_check CHECK (
        status IN ('created', 'processing', 'transcribed', 'summarized', 'completed', 'failed')
    ),
    CONSTRAINT processing_tasks_attempts_check CHECK (attempts >= 0)
);

CREATE TABLE transcriptions (
    meeting_id uuid PRIMARY KEY REFERENCES meetings (id) ON DELETE CASCADE,
    text       text        NOT NULL,
    language   text        NOT NULL DEFAULT '',
    provider   text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE summaries (
    meeting_id uuid PRIMARY KEY REFERENCES meetings (id) ON DELETE CASCADE,
    text       text        NOT NULL,
    provider   text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE qa_history (
    id         uuid PRIMARY KEY,
    meeting_id uuid        NOT NULL REFERENCES meetings (id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    question   text        NOT NULL,
    answer     text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down

DROP TABLE IF EXISTS qa_history;
DROP TABLE IF EXISTS summaries;
DROP TABLE IF EXISTS transcriptions;
DROP TABLE IF EXISTS processing_tasks;
DROP TABLE IF EXISTS meetings;
DROP TABLE IF EXISTS users;
