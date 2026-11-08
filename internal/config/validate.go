package config

import (
	"errors"
	"fmt"
	"net/url"
	"time"
)

// Допустимые диапазоны значений. Проверяются для любого источника: и для файла,
// и для окружения, поэтому «правильные» границы описаны в одном месте.
const (
	connsMin, connsMax     = 1, 200
	workersMin, workersMax = 1, 64
	queueMin, queueMax     = 1, 10000
)

// span — диапазон длительности.
type span struct {
	min time.Duration
	max time.Duration
}

var (
	connTimeoutRange     = span{100 * time.Millisecond, time.Minute}
	clientTimeoutRange   = span{time.Second, 10 * time.Minute}
	taskTimeoutRange     = span{time.Second, time.Hour}
	shutdownTimeoutRange = span{time.Second, 5 * time.Minute}
)

// contains сообщает, попадает ли значение в диапазон.
func (s span) contains(value time.Duration) bool {
	return value >= s.min && value <= s.max
}

// validate проверяет согласованность конфигурации после всех источников.
func (c Config) validate() error {
	var errs []error

	if err := validateDSN(c.Database.DSN); err != nil {
		errs = append(errs, err)
	}

	if c.Database.MaxConns < connsMin || c.Database.MaxConns > connsMax {
		errs = append(errs, fieldError("database.max_conns", c.Database.MaxConns,
			fmt.Sprintf("вне диапазона [%d, %d]", connsMin, connsMax)))
	}

	if !connTimeoutRange.contains(c.Database.ConnTimeout) {
		errs = append(errs, fieldError("database.conn_timeout", c.Database.ConnTimeout,
			fmt.Sprintf("вне диапазона [%s, %s]", connTimeoutRange.min, connTimeoutRange.max)))
	}

	if _, err := parseLevel(c.Logger.Level.String()); err != nil {
		errs = append(errs, fieldError("logging.level", c.Logger.Level, "допустимые значения: debug, info, warn, error"))
	}

	if c.Logger.Format != FormatJSON && c.Logger.Format != FormatText {
		errs = append(errs, fieldError("logging.format", c.Logger.Format,
			"допустимые значения: "+FormatJSON+", "+FormatText))
	}

	if c.Speech.Provider != ProviderMock && c.Speech.Provider != ProviderYandex {
		errs = append(errs, fieldError("speech.provider", c.Speech.Provider,
			"допустимые значения: "+ProviderMock+", "+ProviderYandex))
	}

	if c.LLM.Provider != ProviderMock && c.LLM.Provider != ProviderGigaChat {
		errs = append(errs, fieldError("llm.provider", c.LLM.Provider,
			"допустимые значения: "+ProviderMock+", "+ProviderGigaChat))
	}

	if !clientTimeoutRange.contains(c.Speech.Timeout) {
		errs = append(errs, fieldError("speech.timeout", c.Speech.Timeout,
			fmt.Sprintf("вне диапазона [%s, %s]", clientTimeoutRange.min, clientTimeoutRange.max)))
	}

	if !clientTimeoutRange.contains(c.LLM.Timeout) {
		errs = append(errs, fieldError("llm.timeout", c.LLM.Timeout,
			fmt.Sprintf("вне диапазона [%s, %s]", clientTimeoutRange.min, clientTimeoutRange.max)))
	}

	if c.Worker.Concurrency < workersMin || c.Worker.Concurrency > workersMax {
		errs = append(errs, fieldError("worker.concurrency", c.Worker.Concurrency,
			fmt.Sprintf("вне диапазона [%d, %d]", workersMin, workersMax)))
	}

	if c.Worker.QueueSize < queueMin || c.Worker.QueueSize > queueMax {
		errs = append(errs, fieldError("worker.queue_size", c.Worker.QueueSize,
			fmt.Sprintf("вне диапазона [%d, %d]", queueMin, queueMax)))
	}

	if !taskTimeoutRange.contains(c.Worker.TaskTimeout) {
		errs = append(errs, fieldError("worker.task_timeout", c.Worker.TaskTimeout,
			fmt.Sprintf("вне диапазона [%s, %s]", taskTimeoutRange.min, taskTimeoutRange.max)))
	}

	if !shutdownTimeoutRange.contains(c.ShutdownTimeout) {
		errs = append(errs, fieldError("shutdown_timeout", c.ShutdownTimeout,
			fmt.Sprintf("вне диапазона [%s, %s]", shutdownTimeoutRange.min, shutdownTimeoutRange.max)))
	}

	return errors.Join(errs...)
}

// validateDSN проверяет форму строки подключения.
//
// В сообщение попадает только отредактированная версия: пароль не должен
// оказаться в логах даже из-за опечатки в схеме.
func validateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" {
		return fieldError("database.dsn", redactDSN(dsn),
			"ожидается DSN вида postgres://user:pass@host:port/db")
	}
	return nil
}
