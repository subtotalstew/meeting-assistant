// Package config загружает настройки приложения.
//
// Источников три, и применяются они строго по порядку: значения по умолчанию,
// YAML-файл, переменные окружения. Окружение побеждает файл, потому что в
// контейнере и CI проще переопределить пару ключей, чем править файл.
// Некорректная настройка — отказ стартовать: молча подставленный дефолт вместо
// опечатки в DSN означает работу не с той базой, а это дороже падения на старте.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// FileEnv — переменная окружения с путём к YAML-файлу настроек.
const FileEnv = "CONFIG_FILE"

// Поддерживаемые форматы логов.
const (
	FormatJSON = "json"
	FormatText = "text"
)

// Провайдеры внешних клиентов.
const (
	ProviderMock     = "mock"
	ProviderYandex   = "yandex"
	ProviderGigaChat = "gigachat"
)

// Значения по умолчанию. Они попадают в Config, если ни файл, ни окружение
// не задают поле явно.
const (
	defaultDSN             = "postgres://meeting:meeting@localhost:5432/meeting?sslmode=disable"
	defaultMaxConns        = 10
	defaultConnTimeout     = 5 * time.Second
	defaultShutdownTimeout = 15 * time.Second
	defaultSpeechTimeout   = 60 * time.Second
	defaultLLMTimeout      = 90 * time.Second
	defaultConcurrency     = 4
	defaultQueueSize       = 64
	defaultTaskTimeout     = 10 * time.Minute
	defaultPollInterval    = 500 * time.Millisecond
	defaultLogFormat       = FormatJSON
	defaultSpeechProvider  = ProviderMock
	defaultLLMProvider     = ProviderMock
)

// Config — полная конфигурация приложения.
type Config struct {
	Database        Database
	Logger          Logger
	Speech          Client
	LLM             Client
	Worker          Worker
	ShutdownTimeout time.Duration
	AutoMigrate     bool
}

// Database — параметры подключения к PostgreSQL.
type Database struct {
	DSN         string
	MaxConns    int32
	ConnTimeout time.Duration
}

// Logger — параметры структурированного логирования.
type Logger struct {
	Level  slog.Level
	Format string
}

// Client — настройки внешнего клиента (speech или LLM).
type Client struct {
	// Provider — имя реализации: mock, yandex, gigachat.
	Provider string
	// Timeout — предел на один вызов внешнего сервиса.
	Timeout time.Duration
}

// Worker — настройки фоновой обработки.
type Worker struct {
	// Concurrency — сколько встреч обрабатывается одновременно.
	// Это же значение ограничивает число горутин обработки.
	Concurrency int
	// QueueSize — размер буфера очереди задач.
	QueueSize int
	// TaskTimeout — предел на весь цикл обработки одной встречи.
	TaskTimeout time.Duration
	// PollInterval — период опроса очереди в базе диспетчером.
	PollInterval time.Duration
}

// defaults возвращает конфигурацию со значениями по умолчанию.
func defaults() Config {
	return Config{
		Database: Database{
			DSN:         defaultDSN,
			MaxConns:    defaultMaxConns,
			ConnTimeout: defaultConnTimeout,
		},
		Logger: Logger{
			Level:  slog.LevelInfo,
			Format: defaultLogFormat,
		},
		Speech: Client{
			Provider: defaultSpeechProvider,
			Timeout:  defaultSpeechTimeout,
		},
		LLM: Client{
			Provider: defaultLLMProvider,
			Timeout:  defaultLLMTimeout,
		},
		Worker: Worker{
			Concurrency:  defaultConcurrency,
			QueueSize:    defaultQueueSize,
			TaskTimeout:  defaultTaskTimeout,
			PollInterval: defaultPollInterval,
		},
		ShutdownTimeout: defaultShutdownTimeout,
		AutoMigrate:     true,
	}
}

// Load собирает конфигурацию из значений по умолчанию, YAML-файла и окружения.
//
// Все найденные проблемы возвращаются одной ошибкой, обёрнутой в ErrInvalidConfig:
// пользователь должен увидеть полный список опечаток сразу, а не по одной за запуск.
func Load() (Config, error) {
	cfg := defaults()

	path := strings.TrimSpace(os.Getenv(FileEnv))
	if path != "" {
		if err := applyFile(&cfg, path); err != nil {
			return Config{}, err
		}
	}

	envLoader := &env{}
	envLoader.apply(&cfg)

	if err := errors.Join(envLoader.errs...); err != nil {
		return Config{}, err
	}

	if err := cfg.validate(); err != nil {
		// Проверка не знает, откуда пришло значение, поэтому источник дописываем
		// здесь: иначе непонятно, искать опечатку в файле или в окружении.
		if path != "" {
			return Config{}, fmt.Errorf("%s: %w", path, err)
		}
		return Config{}, err
	}

	return cfg, nil
}
