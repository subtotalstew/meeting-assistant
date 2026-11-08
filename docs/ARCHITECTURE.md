# Архитектура

Документ отвечает на вопросы, которые задают на защите: как разделены слои, как
устроена модель данных, где живёт конкурентность, как проверяются права доступа.

## Слои и зависимости

```mermaid
flowchart TD
    CLI["internal/cli<br/>cobra, вывод, флаги"]
    SVC["internal/service<br/>сценарии, проверка прав"]
    DOM["internal/domain<br/>сущности, статусы, ошибки"]
    PG["internal/storage/postgres<br/>SQL, транзакции, миграции"]
    CLI2["internal/client/speech<br/>SpeechClient + mock"]
    CLI3["internal/client/llm<br/>LLMClient + mock"]
    WK["internal/worker<br/>очередь, пул, обработка"]

    CLI --> SVC
    SVC --> DOM
    SVC -.->|порты| PG
    SVC -.->|TaskQueue| WK
    WK --> DOM
    WK -.->|портированные интерфейсы| PG
    WK --> CLI2
    WK --> CLI3
    PG --> DOM
```

Правило зависимостей: `cli → service → domain`, инфраструктура знает только про
`domain`. Внешние пакеты (`pgx`, `goose`, `cobra`, `yaml`) живут в трёх местах —
`internal/cli`, `internal/storage/postgres` и `internal/config`. Поэтому
`internal/domain`, `internal/service` и `internal/worker` читаются без знания
библиотек.

Интерфейсы объявлены на стороне потребителя:

| Интерфейс | Где объявлен | Кто реализует |
|---|---|---|
| `Service` | `internal/cli` | `*service.Service` |
| `Worker` | `internal/cli` | `*worker.Pool` |
| `Repository` | `internal/service` | `*postgres.Store` |
| `TaskQueue` | `internal/service` | `*worker.Pool` |
| `Store` | `internal/worker` | `*postgres.Store` |
| `SpeechClient` | `internal/client/speech` | `speech.Mock` |
| `LLMClient` | `internal/client/llm` | `llm.Mock` |

## Модель данных

```mermaid
erDiagram
    users ||--o{ meetings : "владеет"
    meetings ||--|| processing_tasks : "задача обработки"
    meetings ||--o| transcriptions : "транскрипция"
    meetings ||--o| summaries : "выжимка"
    meetings ||--o{ qa_history : "вопросы"

    users {
        uuid id PK
        text external_id UK
        timestamptz created_at
    }
    meetings {
        uuid id PK
        uuid user_id FK
        text title
        text file_name
        text source_path
        text status
        text error
        timestamptz created_at
        timestamptz updated_at
    }
    processing_tasks {
        uuid id PK
        uuid meeting_id FK
        text status
        int attempts
        text error
        timestamptz started_at
        timestamptz finished_at
    }
    transcriptions {
        uuid meeting_id PK
        text text
        text language
        text provider
    }
    summaries {
        uuid meeting_id PK
        text text
        text provider
    }
    qa_history {
        uuid id PK
        uuid meeting_id FK
        uuid user_id FK
        text question
        text answer
    }
```

Индексы:

| Индекс | Зачем |
|---|---|
| `meetings(user_id, created_at desc)` | `list` |
| `meetings(user_id, status)` | `stats`, выборки по статусу |
| `processing_tasks(status)` | восстановление незавершённых задач |
| `processing_tasks(meeting_id)` | `status <id>`, `retry <id>` |
| `qa_history(meeting_id, created_at)` | история вопросов |
| GIN `to_tsvector('russian', text)` на `transcriptions` и `summaries` | полнотекстовый `find` |

## Жизненный цикл обработки

```mermaid
stateDiagram-v2
    [*] --> created
    created --> processing: воркер взял задачу
    processing --> transcribed: транскрипция сохранена
    transcribed --> summarized: выжимка сохранена
    summarized --> completed: обработка завершена
    processing --> failed: ошибка клиента
    transcribed --> failed: ошибка LLM
    summarized --> failed: ошибка сохранения
    failed --> created: retry <id>
```

Переходы описаны в `internal/domain/status.go` и проверяются методом
`Status.Transition`. Статус встречи и статус задачи меняются в одной транзакции:
пользователь смотрит на встречу, обработчик — на задачу, и расхождение выглядит
как зависание.

## Асинхронная обработка

```mermaid
sequenceDiagram
    participant U as Пользователь
    participant S as service
    participant DB as PostgreSQL
    participant P as worker.Pool
    participant C as speech/LLM

    U->>S: load meeting.ogg
    S->>DB: INSERT meeting + task (одна транзакция)
    S->>P: Enqueue(meetingID)
    S-->>U: встреча создана, статус created
    P->>DB: UPDATE task -> processing
    P->>C: Transcribe
    C-->>P: текст
    P->>DB: INSERT transcription + статус transcribed
    P->>C: Summarize
    C-->>P: выжимка
    P->>DB: INSERT summary + summarized, затем completed
    U->>S: status <id>
    S-->>U: completed
```

- Очередь — буферизованный канал, пул — `WORKER_CONCURRENCY` горутин.
- Лимит параллелизма задаётся числом воркеров, а не семафором: количество горутин
  постоянно, лишние задачи ждут в буфере. Переполнение даёт `ErrQueueFull`,
  задача при этом уже сохранена в базе.
- `TASK_TIMEOUT` ограничивает весь цикл, `SPEECH_TIMEOUT`/`LLM_TIMEOUT` — каждый
  вызов. Ошибка записывается в контексте `context.WithoutCancel`: если задачу убил
  таймаут, её контекст уже мёртв, а статус `failed` записать нужно.
- `serve` при старте вызывает `RecoverPending`: задачи, застрявшие в
  `processing/transcribed/summarized`, возвращаются в `created`.

## Права доступа

Проверка идёт на двух уровнях, и оба обязательны:

1. **SQL.** Все пользовательские выборки фильтруются по `user_id` в `WHERE`:
   `GetMeeting`, `ListMeetings`, `DeleteMeeting`, `GetTranscription`, `GetSummary`,
   `SearchMeetings`, `ListQuestions`. Метода без `user_id` для пользовательских
   сценариев не существует.
2. **Сервис.** Материалы (`Transcription`, `Summary`, `Ask`) читаются только после
   `GetMeeting(ctx, id, userID)`. Иначе на чужой идентификатор вернулось бы
   «транскрипция ещё не готова» — это подсказка о существовании встречи.

Чужой идентификатор неотличим от несуществующего: `ErrNotFound` в обоих случаях.
Отдельно существует `GetMeetingByID` без фильтра — только для фонового обработчика,
который действует от имени системы.

## Обработка ошибок

### Ошибки внешних клиентов

`internal/client` объявляет виды сбоев: `ErrUnavailable`, `ErrTimeout`,
`ErrRejected`, `ErrInvalidRequest`. Клиент оборачивает причину в `*client.Error`,
у которого `Unwrap() []error`, поэтому одновременно работают
`errors.Is(err, client.ErrTimeout)` и `errors.Is(err, context.DeadlineExceeded)`.

Решение о повторе принимает `client.IsTemporary`. В базу попадает человеческий
текст («внешний сервис не ответил вовремя»), а подробности остаются в логах.

### Прочие ошибки

- `domain.ErrNotFound / ErrValidation / ErrInvalidTransition` — проверяются через
  `errors.Is` в сервисе и CLI, текст ошибки не разбирается.
- Ошибки БД оборачиваются с слоем (`fmt.Errorf("...: %w", err)`), `pgconn.PgError`
  разбирается через `errors.As` там, где важен код.
- `ErrNotReady` отделяет «ещё обрабатывается» от «не найдено».
- CLI возвращает ошибку в `main`, печатает её один раз в stderr, ставит код 1.

## Конфигурация

Три источника, применяются по порядку: умолчания → YAML (`CONFIG_FILE`) →
переменные окружения. Проверка диапазонов и форматов выполняется для любого
источника, все найденные проблемы возвращаются одной ошибкой. Пароль из `DB_DSN`
редактируется перед попаданием в текст ошибки.
