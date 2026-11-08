// Package config загружает настройки приложения из переменных окружения.
//
// Явные значения по умолчанию и отказ стартовать при некорректных настройках —
// осознанное решение: молча подставленный дефолт вместо опечатки в DSN означает
// работу не с той базой, а это дороже падения на старте.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidConfig — общая причина ошибок конфигурации. Проверяется через errors.Is.
var ErrInvalidConfig = errors.New("некорректная конфигурация")

// Поддерживаемые форматы логов.
const (
	FormatJSON = "json"
	FormatText = "text"
)

// Провайдеры внешних клиентов. Значение попадает в конфигурацию как есть,
// поэтому список допустимых значений — единственное место, где он описан.
const (
	ProviderMock     = "mock"
	ProviderYandex   = "yandex"
	ProviderGigaChat = "gigachat"
)

// Значения по умолчанию.
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

// Worker — настройки фоновой обработки.
type Worker struct {
	// Concurrency — сколько встреч обрабатывается одновременно.
	// Это же значение ограничивает число горутин обработки.
	Concurrency int
	// QueueSize — размер буфера очереди задач.
	QueueSize int
	// TaskTimeout — предел на весь цикл обработки одной встречи.
	TaskTimeout time.Duration
}

// Client — настройки внешнего клиента (speech или LLM).
type Client struct {
	// Provider — имя реализации: mock, yandex, gigachat.
	Provider string
	// Timeout — предел на один вызов внешнего сервиса.
	Timeout time.Duration
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

// Load читает конфигурацию из окружения и проверяет её.
// Все найденные проблемы возвращаются одной ошибкой, обёрнутой в ErrInvalidConfig.
func Load() (Config, error) {
	l := &loader{}

	cfg := Config{
		Database: Database{
			DSN:         l.dsn("DB_DSN", defaultDSN),
			MaxConns:    int32(l.intInRange("DB_MAX_CONNS", defaultMaxConns, 1, 200)),
			ConnTimeout: l.durationInRange("DB_CONN_TIMEOUT", defaultConnTimeout, 100*time.Millisecond, time.Minute),
		},
		Logger: Logger{
			Level:  l.logLevel("LOG_LEVEL", slog.LevelInfo),
			Format: l.oneOf("LOG_FORMAT", FormatJSON, FormatJSON, FormatText),
		},
		Speech: Client{
			Provider: l.oneOf("SPEECH_PROVIDER", ProviderMock, ProviderMock, ProviderYandex),
			Timeout:  l.durationInRange("SPEECH_TIMEOUT", defaultSpeechTimeout, time.Second, 10*time.Minute),
		},
		LLM: Client{
			Provider: l.oneOf("LLM_PROVIDER", ProviderMock, ProviderMock, ProviderGigaChat),
			Timeout:  l.durationInRange("LLM_TIMEOUT", defaultLLMTimeout, time.Second, 10*time.Minute),
		},
		Worker: Worker{
			Concurrency: l.intInRange("WORKER_CONCURRENCY", defaultConcurrency, 1, 64),
			QueueSize:   l.intInRange("WORKER_QUEUE_SIZE", defaultQueueSize, 1, 10000),
			TaskTimeout: l.durationInRange("TASK_TIMEOUT", defaultTaskTimeout, time.Second, time.Hour),
		},
		ShutdownTimeout: l.durationInRange("SHUTDOWN_TIMEOUT", defaultShutdownTimeout, time.Second, 5*time.Minute),
		AutoMigrate:     l.boolValue("AUTO_MIGRATE", true),
	}

	if err := errors.Join(l.errs...); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// loader накапливает ошибки парсинга, чтобы пользователь увидел все проблемы сразу,
// а не по одной за перезапуск.
type loader struct {
	errs []error
}

func (l *loader) fail(key, value, reason string) {
	l.errs = append(l.errs, fmt.Errorf("%w: %s=%q %s", ErrInvalidConfig, key, value, reason))
}

func (l *loader) str(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func (l *loader) intInRange(key string, def, minVal, maxVal int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		l.fail(key, raw, "не является целым числом")
		return def
	}
	if v < minVal || v > maxVal {
		l.fail(key, raw, fmt.Sprintf("вне диапазона [%d, %d]", minVal, maxVal))
		return def
	}
	return v
}

func (l *loader) durationInRange(key string, def, minVal, maxVal time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		l.fail(key, raw, "не является длительностью (примеры: 500ms, 5s, 2m)")
		return def
	}
	if v < minVal || v > maxVal {
		l.fail(key, raw, fmt.Sprintf("вне диапазона [%s, %s]", minVal, maxVal))
		return def
	}
	return v
}

func (l *loader) boolValue(key string, def bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		l.fail(key, raw, "не является булевым значением (true/false)")
		return def
	}
	return v
}

func (l *loader) oneOf(key, def string, allowed ...string) string {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return def
	}
	for _, a := range allowed {
		if raw == a {
			return raw
		}
	}
	l.fail(key, raw, "допустимые значения: "+strings.Join(allowed, ", "))
	return def
}

func (l *loader) logLevel(key string, def slog.Level) slog.Level {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return def
	}
	switch raw {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		l.fail(key, raw, "допустимые значения: debug, info, warn, error")
		return def
	}
}

// dsn проверяет форму строки подключения и никогда не печатает её целиком:
// пароль не должен попадать в лог даже в сообщении об ошибке.
func (l *loader) dsn(key, def string) string {
	raw := l.str(key, def)

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		l.fail(key, redactDSN(raw), "ожидается DSN вида postgres://user:pass@host:port/db")
		return def
	}
	return raw
}

// redactDSN оставляет от строки подключения только схему, хост и имя базы.
func redactDSN(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<неразбираемый DSN>"
	}
	if u.User != nil {
		u.User = url.User(u.User.Username())
	}
	u.RawQuery = ""
	return u.Redacted()
}
