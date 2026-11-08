// Command meeting-assistant — «Умный помощник для конспектирования встреч».
//
// Здесь только сборка зависимостей и управление жизненным циклом процесса:
// конфигурация, логгер, обработка сигналов завершения. Бизнес-логики в этом файле нет.
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

	logger.Info("приложение запущено",
		slog.String("version", version),
		slog.String("log_format", cfg.Logger.Format),
	)

	// Следующие вехи добавляют сюда пул PostgreSQL, фоновые обработчики и CLI.
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

// shutdown освобождает ресурсы в порядке, обратном их созданию.
// По мере появления слоёв здесь появятся остановка пула воркеров и закрытие БД.
func shutdown(ctx context.Context) error {
	_ = ctx
	return nil
}
