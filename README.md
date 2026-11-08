# meeting-assistant

Умный помощник для конспектирования встреч: принимает аудио- или тестовый файл, получает
транскрипцию через абстрактный speech-клиент, делает краткую выжимку через LLM-клиент,
сохраняет результат в PostgreSQL, ищет по сохранённым материалам и отвечает на вопросы
по ним.

Выпускной проект курса «Go-разработчик» (уровень Middle). Интерфейс — **CLI**.

## Стек

| Слой | Решение | Почему |
|---|---|---|
| CLI | `spf13/cobra` | 11 команд со своими флагами; `RunE`, `ExecuteContext`, готовый `--help` |
| Бизнес-логика | чистая Go, без зависимостей | не знает ни про cobra, ни про pgx, ни про HTTP |
| БД | PostgreSQL 16, `jackc/pgx/v5` | нативный протокол, `pgxpool`, `BeginFunc`, типизированные ошибки |
| Миграции | `pressly/goose/v3` + `//go:embed` | транзакция на миграцию, нет `dirty`-состояния |
| Внешние API | интерфейсы + `net/http` | `mock` для тестов и демо, реальные провайдеры — через конфигурацию |
| Логи | `log/slog`, JSON | структурированные события в stderr |

Внешние зависимости живут только в `internal/storage/postgres` и `internal/client/*`.
`internal/service`, `internal/domain`, `internal/worker` не импортируют ничего, кроме stdlib.

## Требования

- Go 1.26+ (столько требуют `pgx v5.12` и `goose v3.29`)
- Docker (для PostgreSQL и интеграционных тестов)
- GNU Make (опционально: все цели продублированы обычными командами `go`)

## Быстрый старт

```bash
make db          # PostgreSQL 16 в Docker с healthcheck
make build       # бинарник в bin/
make test        # тесты; интеграционные пропускаются без TEST_DATABASE_URL
```

Или без make:

```bash
docker compose up -d --wait
go build -o bin/meeting-assistant ./cmd/meeting-assistant
go test ./...
```

## Переменные окружения

| Переменная | По умолчанию | Смысл |
|---|---|---|
| `DB_DSN` | `postgres://meeting:meeting@localhost:5432/meeting?sslmode=disable` | строка подключения |
| `DB_MAX_CONNS` | `10` | размер пула соединений (1–200) |
| `DB_CONN_TIMEOUT` | `5s` | таймаут установки соединения |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | `json` | `json` или `text` |
| `SHUTDOWN_TIMEOUT` | `15s` | сколько ждём корректного завершения |
| `AUTO_MIGRATE` | `true` | накатывать миграции при старте |
| `TEST_DATABASE_URL` | — | база для интеграционных тестов; без неё они пропускаются |

Некорректное значение — отказ стартовать с перечислением **всех** проблем сразу.
Пароль из `DB_DSN` в сообщения об ошибках не попадает.

## Команды CLI

Появятся в вехе «cli»: `start`, `load <path>`, `list`, `status <id>`, `get <id>`,
`find <keyword>`, `chat <text>`, плюс `retry`, `delete`, `export`, `stats`.

## Тесты

```bash
go test ./...                    # unit-тесты, интеграционные пропускаются
TEST_DATABASE_URL='postgres://meeting:meeting@localhost:5432/meeting?sslmode=disable' \
  go test -race -run Integration ./...
```

Интеграционные тесты создают собственную схему `test_<timestamp>` и удаляют её после
прогона, поэтому не мешают данным приложения в `public` и не требуют отдельной базы.

## Миграции

SQL-файлы лежат в `migrations/` и встроены в бинарник через `go:embed`.
Накат выполняется при старте (`AUTO_MIGRATE=true`) через goose: каждая миграция
применяется в транзакции, поэтому «грязного» состояния схемы не бывает.

## Архитектура

```
cmd/meeting-assistant   сборка зависимостей, graceful shutdown
internal/config         конфигурация из окружения
internal/logging        структурированный логгер
internal/domain         сущности, статусы, доменные ошибки      ← веха storage
internal/storage        PostgreSQL: репозитории, миграции        ← веха storage
internal/client         SpeechClient / LLMClient + mock + real   ← веха clients
internal/service        use cases, проверка прав доступа         ← веха service
internal/worker         пул обработчиков, лимит параллелизма     ← веха worker
internal/cli            cobra-команды                            ← веха cli
```

## Разработка

Работа идёт вехами, каждая веха — отдельная ветка и отдельный PR:

| Ветка | Содержание |
|---|---|
| `feature/bootstrap` | go.mod, CI, docker-compose, Makefile, конфигурация, логирование |
| `feature/storage` | миграции, домен, репозитории, интеграционные тесты |
| `feature/clients` | интерфейсы speech/LLM и mock-реализации |
| `feature/service` | сценарии, проверка прав доступа |
| `feature/worker` | асинхронная обработка, лимит, graceful shutdown |
| `feature/cli` | cobra-команды и сквозной сценарий |
| `feature/search-and-extras` | полнотекстовый поиск, retry, delete, export, stats |
| `feature/real-providers` | адаптеры Yandex SpeechKit и GigaChat за конфигурацией |

## CI

GitHub Actions (`.github/workflows/ci.yml`): `lint` (gofmt, `go vet`, golangci-lint),
`test` (`go test -race -short`, покрытие и порог 60%), `integration` (PostgreSQL 16
как service, `go test -race -run Integration`). Мерж в `main` — только с зелёным CI.
