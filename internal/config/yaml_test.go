package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeConfig создаёт временный YAML-файл и делает его активным.
func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("подготовка конфигурации: %v", err)
	}

	t.Setenv(FileEnv, path)
	return path
}

const fullConfig = `
database:
  dsn: postgres://app:secret@db.local:6543/meetings?sslmode=require
  max_conns: 25
  conn_timeout: 2s
logging:
  level: debug
  format: text
speech:
  provider: yandex
  timeout: 30s
llm:
  provider: gigachat
  timeout: 2m
worker:
  concurrency: 6
  queue_size: 128
  task_timeout: 5m
shutdown_timeout: 20s
auto_migrate: false
`

func TestLoadFromFile(t *testing.T) {
	resetEnv(t)
	writeConfig(t, fullConfig)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() вернул ошибку: %v", err)
	}

	if cfg.Database.DSN != "postgres://app:secret@db.local:6543/meetings?sslmode=require" {
		t.Errorf("DSN = %q", cfg.Database.DSN)
	}
	if cfg.Database.MaxConns != 25 {
		t.Errorf("MaxConns = %d, ожидалось 25", cfg.Database.MaxConns)
	}
	if cfg.Database.ConnTimeout != 2*time.Second {
		t.Errorf("ConnTimeout = %s, ожидалось 2s", cfg.Database.ConnTimeout)
	}
	if cfg.Logger.Level != slog.LevelDebug {
		t.Errorf("Level = %s, ожидался debug", cfg.Logger.Level)
	}
	if cfg.Logger.Format != FormatText {
		t.Errorf("Format = %q", cfg.Logger.Format)
	}
	if cfg.Speech.Provider != ProviderYandex || cfg.Speech.Timeout != 30*time.Second {
		t.Errorf("Speech = %+v", cfg.Speech)
	}
	if cfg.LLM.Provider != ProviderGigaChat || cfg.LLM.Timeout != 2*time.Minute {
		t.Errorf("LLM = %+v", cfg.LLM)
	}
	if cfg.Worker.Concurrency != 6 || cfg.Worker.QueueSize != 128 || cfg.Worker.TaskTimeout != 5*time.Minute {
		t.Errorf("Worker = %+v", cfg.Worker)
	}
	if cfg.ShutdownTimeout != 20*time.Second {
		t.Errorf("ShutdownTimeout = %s", cfg.ShutdownTimeout)
	}
	if cfg.AutoMigrate {
		t.Error("AutoMigrate = true, ожидалось false")
	}
}

// Окружение должно побеждать файл: в контейнере так проще переопределить пару ключей.
func TestEnvOverridesFile(t *testing.T) {
	resetEnv(t)
	writeConfig(t, fullConfig)

	t.Setenv("WORKER_CONCURRENCY", "2")
	t.Setenv("LOG_LEVEL", "error")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() вернул ошибку: %v", err)
	}

	if cfg.Worker.Concurrency != 2 {
		t.Errorf("Concurrency = %d, ожидалось 2 из окружения", cfg.Worker.Concurrency)
	}
	if cfg.Logger.Level != slog.LevelError {
		t.Errorf("Level = %s, ожидался error из окружения", cfg.Logger.Level)
	}
	// Остальные поля файла не должны пострадать.
	if cfg.Worker.QueueSize != 128 {
		t.Errorf("QueueSize = %d, ожидалось 128 из файла", cfg.Worker.QueueSize)
	}
}

// Пустой файл не должен затирать значения по умолчанию.
func TestEmptyFileKeepsDefaults(t *testing.T) {
	resetEnv(t)
	writeConfig(t, "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() вернул ошибку: %v", err)
	}

	if cfg.Database.MaxConns != defaultMaxConns || cfg.Worker.Concurrency != defaultConcurrency {
		t.Errorf("значения по умолчанию потерялись: %+v", cfg)
	}
}

func TestFileErrors(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantIn  string
	}{
		{
			name:    "неизвестный ключ",
			content: "worker:\n  concurrencyy: 4\n",
			wantIn:  "concurrencyy",
		},
		{
			name:    "длительность без единиц",
			content: "database:\n  conn_timeout: 5\n",
			wantIn:  "длительность",
		},
		{
			name:    "неизвестный провайдер",
			content: "llm:\n  provider: openai\n",
			wantIn:  "llm.provider",
		},
		{
			name:    "значение вне диапазона",
			content: "worker:\n  concurrency: 500\n",
			wantIn:  "worker.concurrency",
		},
		{
			name:    "неизвестный уровень логов",
			content: "logging:\n  level: loud\n",
			wantIn:  "logging.level",
		},
		{
			name:    "битый yaml",
			content: "database: [\n",
			wantIn:  "разбор файла",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetEnv(t)
			path := writeConfig(t, tc.content)

			_, err := Load()
			if err == nil {
				t.Fatal("ожидалась ошибка конфигурации")
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("в ошибке нет %q: %v", tc.wantIn, err)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("в ошибке нет пути к файлу: %v", err)
			}
		})
	}
}

func TestMissingFile(t *testing.T) {
	resetEnv(t)

	t.Setenv(FileEnv, filepath.Join(t.TempDir(), "nope.yaml"))

	_, err := Load()
	if err == nil {
		t.Fatal("ожидалась ошибка чтения файла")
	}
	if !strings.Contains(err.Error(), "nope.yaml") {
		t.Errorf("в ошибке нет имени файла: %v", err)
	}
}

// Пароль из файла не должен попадать в сообщение об ошибке.
func TestFileRedactsCredentials(t *testing.T) {
	resetEnv(t)
	writeConfig(t, "database:\n  dsn: mysql://user:supersecret@localhost/db\n")

	_, err := Load()
	if err == nil {
		t.Fatal("ожидалась ошибка конфигурации")
	}
	if strings.Contains(err.Error(), "supersecret") {
		t.Fatalf("пароль попал в текст ошибки: %v", err)
	}
}

func TestFileWithoutFileUsesDefaults(t *testing.T) {
	resetEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() вернул ошибку: %v", err)
	}
	if cfg.Database.DSN != defaultDSN {
		t.Errorf("DSN = %q", cfg.Database.DSN)
	}
}
