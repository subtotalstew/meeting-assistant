// Command meeting-assistant — «Умный помощник для конспектирования встреч».
//
// Здесь только сборка зависимостей и управление жизненным циклом процесса:
// конфигурация, логгер, подключение к базе, миграции, обработка сигналов завершения.
// Бизнес-логики в этом файле нет.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/subtotalstew/meeting-assistant/internal/config"
	"github.com/subtotalstew/meeting-assistant/internal/logging"
	"github.com/subtotalstew/meeting-assistant/internal/storage/postgres"
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

	// NotifyContext отменяет контекст по Ctrl+C и по SIGTERM — это единственная
	// точка, из которой сигнал завершения растекается по всем слоям.
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

	logger.Info("приложение запущено", slog.String("version", version))

	// Следующие вехи добавляют сюда пул воркеров и CLI.
	<-ctx.Done()

	// Возвращаем стандартную реакцию на сигналы: второй Ctrl+C завершит процесс сразу.
	stop()
	logger.Info("получен сигнал завершения, останавливаемся")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("завершение работы: %w", err)
	}

	logger.Info("приложение остановлено")
	return nil
}

// shutdown останавливает фоновые обработчики и дожидается их завершения.
// Появится на вехе worker; пул соединений закрывается отдельно, после этого шага.
func shutdown(ctx context.Context) error {
	_ = ctx
	return nil
}
