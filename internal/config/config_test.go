package config

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// envKeys — все переменные, которые читает Load. Перед каждым тестом их надо
// сбросить, иначе результат зависит от окружения машины разработчика.
var envKeys = []string{
	"DB_DSN",
	"DB_MAX_CONNS",
	"DB_CONN_TIMEOUT",
	"LOG_LEVEL",
	"LOG_FORMAT",
	"SHUTDOWN_TIMEOUT",
	"AUTO_MIGRATE",
	"SPEECH_PROVIDER",
	"SPEECH_TIMEOUT",
	"LLM_PROVIDER",
	"LLM_TIMEOUT",
}

func resetEnv(t *testing.T) {
	t.Helper()
	for _, key := range envKeys {
		t.Setenv(key, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	resetEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() вернул ошибку: %v", err)
	}

	if cfg.Database.DSN != defaultDSN {
		t.Errorf("DSN = %q, ожидался %q", cfg.Database.DSN, defaultDSN)
	}
	if cfg.Database.MaxConns != defaultMaxConns {
		t.Errorf("MaxConns = %d, ожидалось %d", cfg.Database.MaxConns, defaultMaxConns)
	}
	if cfg.Database.ConnTimeout != defaultConnTimeout {
		t.Errorf("ConnTimeout = %s, ожидалось %s", cfg.Database.ConnTimeout, defaultConnTimeout)
	}
	if cfg.Logger.Level != slog.LevelInfo {
		t.Errorf("Level = %s, ожидался info", cfg.Logger.Level)
	}
	if cfg.Logger.Format != FormatJSON {
		t.Errorf("Format = %q, ожидался %q", cfg.Logger.Format, FormatJSON)
	}
	if cfg.ShutdownTimeout != defaultShutdownTimeout {
		t.Errorf("ShutdownTimeout = %s, ожидалось %s", cfg.ShutdownTimeout, defaultShutdownTimeout)
	}
	if !cfg.AutoMigrate {
		t.Error("AutoMigrate = false, ожидалось true")
	}
	if cfg.Speech.Provider != ProviderMock {
		t.Errorf("Speech.Provider = %q, ожидался %q", cfg.Speech.Provider, ProviderMock)
	}
	if cfg.Speech.Timeout != defaultSpeechTimeout {
		t.Errorf("Speech.Timeout = %s, ожидалось %s", cfg.Speech.Timeout, defaultSpeechTimeout)
	}
	if cfg.LLM.Provider != ProviderMock {
		t.Errorf("LLM.Provider = %q, ожидался %q", cfg.LLM.Provider, ProviderMock)
	}
	if cfg.LLM.Timeout != defaultLLMTimeout {
		t.Errorf("LLM.Timeout = %s, ожидалось %s", cfg.LLM.Timeout, defaultLLMTimeout)
	}
}

func TestLoadOverrides(t *testing.T) {
	resetEnv(t)
	t.Setenv("DB_DSN", "postgres://user:pass@db:6543/meetings?sslmode=require")
	t.Setenv("DB_MAX_CONNS", "42")
	t.Setenv("DB_CONN_TIMEOUT", "2s")
	t.Setenv("LOG_LEVEL", "DEBUG")
	t.Setenv("LOG_FORMAT", "TEXT")
	t.Setenv("SHUTDOWN_TIMEOUT", "3s")
	t.Setenv("AUTO_MIGRATE", "false")
	t.Setenv("SPEECH_PROVIDER", "YANDEX")
	t.Setenv("SPEECH_TIMEOUT", "30s")
	t.Setenv("LLM_PROVIDER", "gigachat")
	t.Setenv("LLM_TIMEOUT", "2m")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() вернул ошибку: %v", err)
	}

	if cfg.Database.DSN != "postgres://user:pass@db:6543/meetings?sslmode=require" {
		t.Errorf("DSN = %q", cfg.Database.DSN)
	}
	if cfg.Database.MaxConns != 42 {
		t.Errorf("MaxConns = %d, ожидалось 42", cfg.Database.MaxConns)
	}
	if cfg.Database.ConnTimeout != 2*time.Second {
		t.Errorf("ConnTimeout = %s, ожидалось 2s", cfg.Database.ConnTimeout)
	}
	// Значения регистронезависимы: DEBUG и TEXT должны приниматься.
	if cfg.Logger.Level != slog.LevelDebug {
		t.Errorf("Level = %s, ожидался debug", cfg.Logger.Level)
	}
	if cfg.Logger.Format != FormatText {
		t.Errorf("Format = %q, ожидался %q", cfg.Logger.Format, FormatText)
	}
	if cfg.ShutdownTimeout != 3*time.Second {
		t.Errorf("ShutdownTimeout = %s, ожидалось 3s", cfg.ShutdownTimeout)
	}
	if cfg.AutoMigrate {
		t.Error("AutoMigrate = true, ожидалось false")
	}
	if cfg.Speech.Provider != ProviderYandex {
		t.Errorf("Speech.Provider = %q, ожидался %q", cfg.Speech.Provider, ProviderYandex)
	}
	if cfg.Speech.Timeout != 30*time.Second {
		t.Errorf("Speech.Timeout = %s, ожидалось 30s", cfg.Speech.Timeout)
	}
	if cfg.LLM.Provider != ProviderGigaChat {
		t.Errorf("LLM.Provider = %q, ожидался %q", cfg.LLM.Provider, ProviderGigaChat)
	}
	if cfg.LLM.Timeout != 2*time.Minute {
		t.Errorf("LLM.Timeout = %s, ожидалось 2m", cfg.LLM.Timeout)
	}
}

func TestLoadInvalid(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{name: "не целое число", key: "DB_MAX_CONNS", value: "много"},
		{name: "ниже диапазона", key: "DB_MAX_CONNS", value: "0"},
		{name: "выше диапазона", key: "DB_MAX_CONNS", value: "1000"},
		{name: "длительность без единиц", key: "DB_CONN_TIMEOUT", value: "5"},
		{name: "неизвестный уровень логов", key: "LOG_LEVEL", value: "loud"},
		{name: "неизвестный формат логов", key: "LOG_FORMAT", value: "yaml"},
		{name: "не булево значение", key: "AUTO_MIGRATE", value: "maybe"},
		{name: "чужая схема DSN", key: "DB_DSN", value: "mysql://user@localhost/db"},
		{name: "DSN без хоста", key: "DB_DSN", value: "postgres:///meetings"},
		{name: "неизвестный speech-провайдер", key: "SPEECH_PROVIDER", value: "salute"},
		{name: "неизвестный LLM-провайдер", key: "LLM_PROVIDER", value: "openai"},
		{name: "таймаут speech без единиц", key: "SPEECH_TIMEOUT", value: "60"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetEnv(t)
			t.Setenv(tc.key, tc.value)

			if _, err := Load(); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("ожидалась ошибка ErrInvalidConfig, получено: %v", err)
			}
		})
	}
}

func TestLoadReportsAllProblemsAtOnce(t *testing.T) {
	resetEnv(t)
	t.Setenv("DB_MAX_CONNS", "0")
	t.Setenv("LOG_LEVEL", "loud")

	_, err := Load()
	if err == nil {
		t.Fatal("ожидалась ошибка")
	}

	msg := err.Error()
	for _, key := range []string{"DB_MAX_CONNS", "LOG_LEVEL"} {
		if !strings.Contains(msg, key) {
			t.Errorf("в ошибке нет упоминания %s: %s", key, msg)
		}
	}
}

// Пароль не должен утекать в лог даже через сообщение об ошибке конфигурации.
func TestLoadRedactsCredentials(t *testing.T) {
	resetEnv(t)
	t.Setenv("DB_DSN", "mysql://user:supersecret@localhost/db")

	_, err := Load()
	if err == nil {
		t.Fatal("ожидалась ошибка")
	}
	if strings.Contains(err.Error(), "supersecret") {
		t.Fatalf("пароль попал в текст ошибки: %v", err)
	}
}
