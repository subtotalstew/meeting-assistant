// Package logging собирает структурированный логгер приложения.
package logging

import (
	"log/slog"
	"os"

	"github.com/subtotalstew/meeting-assistant/internal/config"
)

// New создаёт slog-логгер по настройкам конфигурации.
//
// Логгер всегда пишет в stderr: stdout остаётся за данными, которые CLI отдаёт
// пользователю (транскрипция, выжимка, список встреч), и их не надо смешивать с логом.
func New(cfg config.Logger) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.Level}

	if cfg.Format == config.FormatText {
		return slog.New(slog.NewTextHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, opts))
}

// WithComponent помечает логгер слоем приложения (cli, service, worker, storage).
// Один атрибут в каждой строке сильно упрощает разбор инцидента по логам.
func WithComponent(logger *slog.Logger, name string) *slog.Logger {
	return logger.With(slog.String("component", name))
}
