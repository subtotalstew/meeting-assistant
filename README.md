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

## Конфигурация

Источников три, применяются по порядку: **умолчания → YAML-файл → переменные окружения**.
Окружение побеждает файл: в контейнере и CI проще переопределить пару ключей, чем
править файл.

```bash
CONFIG_FILE=config.example.yaml ./bin/meeting-assistant list --user alice
```

Пример со всеми полями и комментариями — [config.example.yaml](config.example.yaml):

```yaml
database:
  dsn: postgres://meeting:meeting@localhost:5432/meeting?sslmode=disable
  max_conns: 10
  conn_timeout: 5s
worker:
  concurrency: 4
  queue_size: 64
  task_timeout: 10m
auto_migrate: true
```

Длительности задаются строками (`500ms`, `5s`, `2m`). Опечатка в имени ключа —
ошибка, а не молча проигнорированная строка.

## Переменные окружения

Каждая переменная перекрывает значение из YAML-файла.

| Переменная | По умолчанию | Смысл |
|---|---|---|
| `CONFIG_FILE` | — | путь к YAML-файлу настроек |
| `DB_DSN` | `postgres://meeting:meeting@localhost:5432/meeting?sslmode=disable` | строка подключения |
| `DB_MAX_CONNS` | `10` | размер пула соединений (1–200) |
| `DB_CONN_TIMEOUT` | `5s` | таймаут установки соединения |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | `json` | `json` или `text` |
| `SHUTDOWN_TIMEOUT` | `15s` | сколько ждём корректного завершения |
| `AUTO_MIGRATE` | `true` | накатывать миграции при старте |
| `SPEECH_PROVIDER` | `mock` | реализация speech-клиента: `mock` |
| `SPEECH_TIMEOUT` | `60s` | таймаут одного вызова распознавания |
| `LLM_PROVIDER` | `mock` | реализация LLM-клиента: `mock` |
| `LLM_TIMEOUT` | `90s` | таймаут одного вызова модели |
| `WORKER_CONCURRENCY` | `4` | сколько встреч обрабатывается одновременно (1–64) |
| `WORKER_QUEUE_SIZE` | `64` | размер буфера очереди задач |
| `TASK_TIMEOUT` | `10m` | предел на весь цикл обработки одной встречи |
| `TEST_DATABASE_URL` | — | база для интеграционных тестов; без неё они пропускаются |

Некорректное значение — отказ стартовать с перечислением **всех** проблем сразу.
Пароль из `DB_DSN` в сообщения об ошибках не попадает.

## Команды CLI

| Команда | Что делает |
|---|---|
| `start --user <id>` | регистрирует пользователя и печатает его идентификатор |
| `load <path> --user <id>` | создаёт встречу и задачу, ждёт завершения обработки |
| `list --user <id>` | встречи пользователя: id, дата, статус, описание |
| `status <id> --user <id>` | статус, даты, число попыток, текст ошибки |
| `get <id> --user <id>` | полная транскрипция |
| `summary <id> --user <id>` | краткая выжимка |
| `find <keyword> --user <id>` | поиск по своим встречам с фрагментом текста |
| `chat "<вопрос>" -m <id> --user <id>` | вопрос по материалам встречи |
| `retry <id> --user <id>` | повторная обработка встречи в статусе failed |
| `delete <id> --user <id>` | удаление встречи и связанных данных |
| `export <id> --user <id> [--out file]` | сохранение транскрипции в файл |
| `stats --user <id>` | статусы, среднее время обработки, число вопросов |
| `serve` | только фоновая обработка, до Ctrl+C |

Флаг `--json` включает машинный вывод, `--user` задаёт пользователя.

### Поиск

`find` сначала выполняет полнотекстовый поиск по GIN-индексам (`to_tsvector('russian', ...)`,
сортировка по `ts_rank`, подсветка найденных слов через `ts_headline`), а если он ничего
не нашёл — поиск по подстроке. Так «релиз» находится как лексема, а «рели» — как часть слова.

### Как получить упавшую задачу для демонстрации

Тестовый speech-клиент детерминированно имитирует сбой распознавания для файлов,
в имени которых есть `fail`:

```bash
echo "demo" > fail-meeting.ogg
meeting-assistant load fail-meeting.ogg --user alice   # статус failed
meeting-assistant status <id> --user alice              # текст ошибки, попыток: 1
meeting-assistant retry <id> --user alice               # попыток: 2, снова failed
```

### Где живёт фоновая обработка

Воркеры запускаются в том же процессе, что и CLI, поэтому `load` по умолчанию
дожидается завершения: иначе процесс завершится и задача останется в очереди.
Для асинхронного сценария — два процесса:

```bash
meeting-assistant load meeting.ogg --user alice --no-wait   # только создать задачу
meeting-assistant serve                                     # разобрать очередь
meeting-assistant status <id> --user alice                  # наблюдать прогресс
```

`serve` при старте возвращает в очередь задачи, прерванные прошлым запуском,
поэтому «потерянных» встреч не остаётся.

### Сквозной сценарий

```bash
meeting-assistant start --user alice
meeting-assistant load ./meeting.ogg --user alice
meeting-assistant list --user alice
meeting-assistant summary <id> --user alice
meeting-assistant find "релиз" --user alice
meeting-assistant chat "Когда релиз?" -m <id> --user alice
```

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

## Документация

| Документ | Содержание |
|---|---|
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | слои и зависимости, ER-схема, жизненный цикл статусов, асинхронность, права доступа, обработка ошибок |
| [docs/DEMO.md](docs/DEMO.md) | пошаговый сценарий демонстрации под защиту |
| [config.example.yaml](config.example.yaml) | конфигурация со всеми полями и комментариями |

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
