-- +goose Up

-- Путь к исходному файлу встречи.
--
-- Нужен фоновому обработчику: он читает файл с диска, а имя файла без пути
-- недостаточно. Хранится и для повторной обработки после ошибки.
ALTER TABLE meetings
    ADD COLUMN source_path text NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE meetings DROP COLUMN IF EXISTS source_path;
