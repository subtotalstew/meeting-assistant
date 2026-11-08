// Command meeting-assistant — «Умный помощник для конспектирования встреч».
//
// Здесь только сборка зависимостей и управление жизненным циклом процесса:
// конфигурация, логгер, подключение к базе, миграции, выбор реализаций внешних
// клиентов, запуск CLI и корректное завершение. Бизнес-логики в этом файле нет.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/subtotalstew/meeting-assistant/internal/cli"
	"github.com/subtotalstew/meeting-assistant/internal/client/llm"
	"github.com/subtotalstew/meeting-assistant/internal/client/speech"
	"github.com/subtotalstew/meeting-assistant/internal/config"
	"github.com/subtotalstew/meeting-assistant/internal/logging"
	"github.com/subtotalstew/meeting-assistant/internal/service"
	"github.com/subtotalstew/meeting-assistant/internal/storage/postgres"
	"github.com/subtotalstew/meeting-assistant/internal/worker"
)

// version подставляется линкером: -ldflags "-X main.version=1.0.0".
var version = "dev"

func main() {
	if err := run(); err != nil {
		// Логгер может быть ещё не создан, поэтому пишем в stderr напрямую.
		fmt.Fprintf(os.Stderr, "ошибка: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("загрузка конфигурации: %w", err)
	}

	logger := logging.New(cfg.Logger)
	slog.SetDefault(logger)

	// NotifyContext отменяет контекст по Ctrl+C и по SIGTERM — единственная точка,
	// из которой сигнал завершения растекается по всем слоям.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := postgres.New(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("подключение к хранилищу: %w", err)
	}
	defer func() {
		store.Close()
		logger.Info("соединение с PostgreSQL закрыто")
	}()

	if cfg.AutoMigrate {
		if err := postgres.Migrate(ctx, store.Pool()); err != nil {
			return fmt.Errorf("миграции: %w", err)
		}
		logger.Info("схема базы данных актуальна")
	}

	// Реализации внешних клиентов выбираются по конфигурации: ни обработчик,
	// ни бизнес-логика про конкретного провайдера не знают.
	speechClient, err := newSpeechClient(cfg)
	if err != nil {
		return err
	}
	llmClient, err := newLLMClient(cfg)
	if err != nil {
		return err
	}

	processor := worker.NewProcessor(store, speechClient, llmClient,
		logging.WithComponent(logger, "worker"), worker.ProcessorConfig{
			TaskTimeout:   cfg.Worker.TaskTimeout,
			SpeechTimeout: cfg.Speech.Timeout,
			LLMTimeout:    cfg.LLM.Timeout,
		})

	pool := worker.NewPool(processor, store, logging.WithComponent(logger, "worker"), worker.Config{
		Concurrency: cfg.Worker.Concurrency,
		QueueSize:   cfg.Worker.QueueSize,
	})

	// Пул не запускается здесь: его запускают команды, которым нужна обработка
	// (load, serve). Остальным командам фоновые воркеры не нужны.
	svc := service.New(store, speechClient, llmClient, pool,
		logging.WithComponent(logger, "service"), service.DefaultOptions())

	logger.Debug("запуск CLI", slog.String("version", version))

	cmdErr := cli.Execute(ctx, svc, pool, os.Args[1:], os.Stdout, os.Stderr)

	// Останавливаем фоновую обработку, если её запускала команда, и только после
	// этого закрываем базу: воркеры не должны увидеть закрытое соединение.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := shutdown(shutdownCtx, pool); err != nil {
		return err
	}

	return cmdErr
}

// shutdown останавливает фоновую обработку.
//
// Пул соединений закрывается отдельно и позже, уже через defer: порядок задан явно,
// потому что defer выполнится только после возврата из этой функции.
func shutdown(ctx context.Context, pool *worker.Pool) error {
	if err := pool.Stop(ctx); err != nil {
		return fmt.Errorf("остановка пула обработчиков: %w", err)
	}
	return nil
}

// newSpeechClient выбирает реализацию speech-клиента по конфигурации.
func newSpeechClient(cfg config.Config) (speech.Client, error) {
	switch cfg.Speech.Provider {
	case config.ProviderMock:
		return speech.NewMock(), nil
	default:
		return nil, fmt.Errorf(
			"speech-провайдер %q пока не подключён: используйте SPEECH_PROVIDER=%s",
			cfg.Speech.Provider, config.ProviderMock,
		)
	}
}

// newLLMClient выбирает реализацию LLM-клиента по конфигурации.
func newLLMClient(cfg config.Config) (llm.Client, error) {
	switch cfg.LLM.Provider {
	case config.ProviderMock:
		return llm.NewMock(), nil
	default:
		return nil, fmt.Errorf(
			"LLM-провайдер %q пока не подключён: используйте LLM_PROVIDER=%s",
			cfg.LLM.Provider, config.ProviderMock,
		)
	}
}
