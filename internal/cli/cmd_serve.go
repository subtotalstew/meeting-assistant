package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// serveCommand запускает только фоновую обработку.
//
// Нужен для асинхронного сценария: `load --no-wait` создаёт задачу в одном
// терминале, `serve` разбирает очередь в другом. Остановка — Ctrl+C, дальше
// приложение корректно завершает воркеры и закрывает соединения.
func (a *App) serveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Запустить фоновую обработку встреч и ждать Ctrl+C",
		Long: `Запускает пул обработчиков, возвращает в очередь задачи, прерванные прошлым
запуском, и работает до получения сигнала завершения.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.worker == nil {
				return errors.New("фоновая обработка недоступна: пул не сконфигурирован")
			}

			ctx := cmd.Context()

			a.worker.Start(ctx)
			if err := a.worker.Recover(ctx); err != nil {
				return fmt.Errorf("восстановление задач: %w", err)
			}

			if !a.jsonOutput {
				fmt.Fprintln(a.stdout, "обработчик запущен, остановите по Ctrl+C")
			}

			// Ждём сигнала: остановку пула выполняет main, после возврата команды.
			<-ctx.Done()

			return nil
		},
	}
}
