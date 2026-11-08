package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// fileConfig повторяет Config, но все поля — указатели.
//
// Указатели нужны, чтобы отличить «поле не задано в файле» от нулевого значения:
// иначе отсутствующий ключ затирал бы значение по умолчанию.
type fileConfig struct {
	Database struct {
		DSN         *string   `yaml:"dsn"`
		MaxConns    *int      `yaml:"max_conns"`
		ConnTimeout *duration `yaml:"conn_timeout"`
	} `yaml:"database"`

	Logging struct {
		Level  *string `yaml:"level"`
		Format *string `yaml:"format"`
	} `yaml:"logging"`

	Speech struct {
		Provider *string   `yaml:"provider"`
		Timeout  *duration `yaml:"timeout"`
	} `yaml:"speech"`

	LLM struct {
		Provider *string   `yaml:"provider"`
		Timeout  *duration `yaml:"timeout"`
	} `yaml:"llm"`

	Worker struct {
		Concurrency *int      `yaml:"concurrency"`
		QueueSize   *int      `yaml:"queue_size"`
		TaskTimeout *duration `yaml:"task_timeout"`
	} `yaml:"worker"`

	ShutdownTimeout *duration `yaml:"shutdown_timeout"`
	AutoMigrate     *bool     `yaml:"auto_migrate"`
}

// duration разбирает длительность из строки вида "5s", "2m", "1h30m".
//
// В YAML нет типа «длительность», а число наносекунд в конфиге читать невозможно,
// поэтому формат строковый — как в аргументах Go-флагов.
type duration time.Duration

// UnmarshalYAML реализует интерфейс разбора YAML.
func (d *duration) UnmarshalYAML(value *yaml.Node) error {
	var raw string
	if err := value.Decode(&raw); err != nil {
		return fmt.Errorf("ожидается строка с длительностью (примеры: 500ms, 5s, 2m)")
	}

	parsed, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("не удалось разобрать длительность %q: ожидается вид 500ms, 5s, 2m", raw)
	}

	*d = duration(parsed)
	return nil
}

// applyFile читает YAML-файл и переносит заданные в нём поля в конфигурацию.
func applyFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w: чтение файла %s: %v", ErrInvalidConfig, path, err)
	}

	var file fileConfig

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	// Опечатка в имени ключа должна быть ошибкой, а не тихо проигнорированной строкой.
	decoder.KnownFields(true)

	if err := decoder.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: разбор файла %s: %v", ErrInvalidConfig, path, err)
	}

	applyDatabase(cfg, file)
	applyClients(cfg, file)
	applyWorker(cfg, file)

	if file.Logging.Level != nil {
		level, err := parseLevel(*file.Logging.Level)
		if err != nil {
			return fmt.Errorf("%s: %w", path, fieldError("logging.level", *file.Logging.Level, err.Error()))
		}
		cfg.Logger.Level = level
	}
	if file.Logging.Format != nil {
		cfg.Logger.Format = strings.ToLower(strings.TrimSpace(*file.Logging.Format))
	}

	if file.ShutdownTimeout != nil {
		cfg.ShutdownTimeout = time.Duration(*file.ShutdownTimeout)
	}
	if file.AutoMigrate != nil {
		cfg.AutoMigrate = *file.AutoMigrate
	}

	return nil
}

func applyDatabase(cfg *Config, file fileConfig) {
	if file.Database.DSN != nil {
		cfg.Database.DSN = strings.TrimSpace(*file.Database.DSN)
	}
	if file.Database.MaxConns != nil {
		cfg.Database.MaxConns = int32(*file.Database.MaxConns)
	}
	if file.Database.ConnTimeout != nil {
		cfg.Database.ConnTimeout = time.Duration(*file.Database.ConnTimeout)
	}
}

func applyClients(cfg *Config, file fileConfig) {
	if file.Speech.Provider != nil {
		cfg.Speech.Provider = strings.ToLower(strings.TrimSpace(*file.Speech.Provider))
	}
	if file.Speech.Timeout != nil {
		cfg.Speech.Timeout = time.Duration(*file.Speech.Timeout)
	}
	if file.LLM.Provider != nil {
		cfg.LLM.Provider = strings.ToLower(strings.TrimSpace(*file.LLM.Provider))
	}
	if file.LLM.Timeout != nil {
		cfg.LLM.Timeout = time.Duration(*file.LLM.Timeout)
	}
}

func applyWorker(cfg *Config, file fileConfig) {
	if file.Worker.Concurrency != nil {
		cfg.Worker.Concurrency = *file.Worker.Concurrency
	}
	if file.Worker.QueueSize != nil {
		cfg.Worker.QueueSize = *file.Worker.QueueSize
	}
	if file.Worker.TaskTimeout != nil {
		cfg.Worker.TaskTimeout = time.Duration(*file.Worker.TaskTimeout)
	}
}
