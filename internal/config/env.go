package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// env применяет переменные окружения поверх уже собранной конфигурации.
//
// Каждый ключ читается независимо: пустое значение означает «не задано», поэтому
// переменная без значения не затирает данные из файла. Ошибки парсинга копятся,
// чтобы пользователь увидел все опечатки сразу.
type env struct {
	errs []error
}

// apply переносит значения окружения в конфигурацию.
func (e *env) apply(cfg *Config) {
	if value, ok := e.str("DB_DSN"); ok {
		cfg.Database.DSN = value
	}
	if value, ok := e.intInRange("DB_MAX_CONNS", connsMin, connsMax); ok {
		cfg.Database.MaxConns = int32(value)
	}
	if value, ok := e.durationInRange("DB_CONN_TIMEOUT", connTimeoutRange.min, connTimeoutRange.max); ok {
		cfg.Database.ConnTimeout = value
	}
	if value, ok := e.level("LOG_LEVEL"); ok {
		cfg.Logger.Level = value
	}
	if value, ok := e.oneOf("LOG_FORMAT", FormatJSON, FormatText); ok {
		cfg.Logger.Format = value
	}
	if value, ok := e.oneOf("SPEECH_PROVIDER", ProviderMock, ProviderYandex); ok {
		cfg.Speech.Provider = value
	}
	if value, ok := e.durationInRange("SPEECH_TIMEOUT", clientTimeoutRange.min, clientTimeoutRange.max); ok {
		cfg.Speech.Timeout = value
	}
	if value, ok := e.oneOf("LLM_PROVIDER", ProviderMock, ProviderGigaChat); ok {
		cfg.LLM.Provider = value
	}
	if value, ok := e.durationInRange("LLM_TIMEOUT", clientTimeoutRange.min, clientTimeoutRange.max); ok {
		cfg.LLM.Timeout = value
	}
	if value, ok := e.intInRange("WORKER_CONCURRENCY", workersMin, workersMax); ok {
		cfg.Worker.Concurrency = value
	}
	if value, ok := e.intInRange("WORKER_QUEUE_SIZE", queueMin, queueMax); ok {
		cfg.Worker.QueueSize = value
	}
	if value, ok := e.durationInRange("TASK_TIMEOUT", taskTimeoutRange.min, taskTimeoutRange.max); ok {
		cfg.Worker.TaskTimeout = value
	}
	if value, ok := e.durationInRange("SHUTDOWN_TIMEOUT", shutdownTimeoutRange.min, shutdownTimeoutRange.max); ok {
		cfg.ShutdownTimeout = value
	}
	if value, ok := e.boolValue("AUTO_MIGRATE"); ok {
		cfg.AutoMigrate = value
	}
}

// fail записывает ошибку разбора переменной окружения.
func (e *env) fail(key, value, reason string) {
	e.errs = append(e.errs, fieldError(key, value, reason))
}

// str возвращает непустое значение переменной.
func (e *env) str(key string) (string, bool) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return "", false
	}
	return value, true
}

// intInRange читает целое число и проверяет диапазон.
func (e *env) intInRange(key string, minValue, maxValue int) (int, bool) {
	raw, ok := e.str(key)
	if !ok {
		return 0, false
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		e.fail(key, raw, "не является целым числом")
		return 0, false
	}

	if value < minValue || value > maxValue {
		e.fail(key, raw, fmt.Sprintf("вне диапазона [%d, %d]", minValue, maxValue))
		return 0, false
	}

	return value, true
}

// durationInRange читает длительность вида 5s и проверяет диапазон.
func (e *env) durationInRange(key string, minValue, maxValue time.Duration) (time.Duration, bool) {
	raw, ok := e.str(key)
	if !ok {
		return 0, false
	}

	value, err := time.ParseDuration(raw)
	if err != nil {
		e.fail(key, raw, "не является длительностью (примеры: 500ms, 5s, 2m)")
		return 0, false
	}

	if value < minValue || value > maxValue {
		e.fail(key, raw, fmt.Sprintf("вне диапазона [%s, %s]", minValue, maxValue))
		return 0, false
	}

	return value, true
}

// boolValue читает булево значение.
func (e *env) boolValue(key string) (bool, bool) {
	raw, ok := e.str(key)
	if !ok {
		return false, false
	}

	value, err := strconv.ParseBool(raw)
	if err != nil {
		e.fail(key, raw, "не является булевым значением (true/false)")
		return false, false
	}

	return value, true
}

// oneOf проверяет, что значение входит в список допустимых.
func (e *env) oneOf(key string, allowed ...string) (string, bool) {
	raw, ok := e.str(key)
	if !ok {
		return "", false
	}

	value := strings.ToLower(raw)
	for _, candidate := range allowed {
		if value == candidate {
			return value, true
		}
	}

	e.fail(key, raw, "допустимые значения: "+strings.Join(allowed, ", "))
	return "", false
}

// level читает уровень логирования.
func (e *env) level(key string) (slog.Level, bool) {
	raw, ok := e.str(key)
	if !ok {
		return 0, false
	}

	level, err := parseLevel(raw)
	if err != nil {
		e.fail(key, raw, err.Error())
		return 0, false
	}

	return level, true
}

// parseLevel переводит название уровня в slog.Level.
func parseLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("допустимые значения: debug, info, warn, error")
	}
}
