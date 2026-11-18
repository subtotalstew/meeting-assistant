-- +goose Up

-- Полнотекстовый поиск по материалам встреч.
--
-- Индекс по выражению: to_tsvector обязана быть immutable, поэтому конфигурация
-- ('russian') указывается литералом, а не берётся из default_text_search_config.
-- В запросе используется ровно то же выражение, иначе планировщик индекс не возьмёт.
CREATE INDEX transcriptions_text_fts_idx ON transcriptions
    USING gin (to_tsvector('russian', text));

CREATE INDEX summaries_text_fts_idx ON summaries
    USING gin (to_tsvector('russian', text));

-- +goose Down

DROP INDEX IF EXISTS summaries_text_fts_idx;
DROP INDEX IF EXISTS transcriptions_text_fts_idx;
