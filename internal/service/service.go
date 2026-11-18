// Package service содержит сценарии приложения (use cases).
//
// Слой не знает ни про CLI, ни про PostgreSQL, ни про конкретные внешние API:
// он работает с интерфейсами, объявленными в этом же пакете. Поэтому unit-тесты
// сервиса идут на фейках — без базы данных и без сети.
package service

import (
	"errors"
	"log/slog"

	"github.com/subtotalstew/meeting-assistant/internal/client/llm"
	"github.com/subtotalstew/meeting-assistant/internal/client/speech"
)

// ErrNotReady — материалы встречи ещё не обработаны.
//
// Отдельная ошибка нужна, чтобы CLI показал «подождите, идёт обработка»,
// а не «не найдено»: пользователь иначе решит, что данные потеряны.
var ErrNotReady = errors.New("материалы встречи ещё не готовы")

// Service — сценарии работы с встречами.
type Service struct {
	repo   Repository
	speech speech.Client
	llm    llm.Client
	queue  TaskQueue
	log    *slog.Logger
	opts   Options
}

// Options — прикладные настройки.
type Options struct {
	// MaxFileSize — предел размера загружаемого файла в байтах.
	MaxFileSize int64
	// AllowedExtensions — расширения, которые принимает load.
	AllowedExtensions []string
	// ListLimit — сколько встреч отдавать в list.
	ListLimit int
	// SearchLimit — предел числа результатов поиска.
	SearchLimit int
}

// DefaultOptions возвращает настройки по умолчанию.
func DefaultOptions() Options {
	return Options{
		MaxFileSize:       200 << 20, // 200 MiB
		AllowedExtensions: []string{".ogg", ".mp3", ".wav", ".m4a", ".opus", ".flac", ".txt"},
		ListLimit:         100,
		SearchLimit:       50,
	}
}

// New собирает сервис.
//
// Все зависимости передаются снаружи: сервис не создаёт их сам и не знает,
// какая реализация speech-клиента или хранилища ему досталась.
func New(
	repo Repository,
	speechClient speech.Client,
	llmClient llm.Client,
	queue TaskQueue,
	logger *slog.Logger,
	opts Options,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}

	return &Service{
		repo:   repo,
		speech: speechClient,
		llm:    llmClient,
		queue:  queue,
		log:    logger,
		opts:   opts,
	}
}
