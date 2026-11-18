package cli

import (
	"github.com/spf13/cobra"

	"github.com/subtotalstew/meeting-assistant/internal/domain"
	"github.com/subtotalstew/meeting-assistant/internal/service"
)

// retryCommand повторяет обработку неуспешной встречи.
//
// Команда только возвращает задачу в очередь: дальше её заберёт диспетчер.
// С --wait она ещё и дожидается результата в этом процессе.
func (a *App) retryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retry <id>",
		Short: "Повторить обработку встречи, завершившейся ошибкой",
		Long: `Повторно ставит в очередь встречу в статусе failed. Успешную обработку
перезапустить нельзя: результат уже получен и сохранён.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			user, err := a.currentUser(cmd.Context())
			if err != nil {
				return err
			}

			meetingID, err := parseMeetingID(args[0])
			if err != nil {
				return err
			}

			meeting, err := a.svc.Retry(cmd.Context(), user.ID, meetingID)
			if err != nil {
				return err
			}

			if !a.waitForResult {
				return a.printRetried(meeting, nil)
			}

			if err := a.startWorker(cmd.Context()); err != nil {
				return err
			}

			status, err := a.waitForCompletion(cmd.Context(), user.ID, meeting.ID)
			if err != nil {
				return err
			}

			return a.printRetried(meeting, &status)
		},
	}

	cmd.Flags().BoolVar(&a.waitForResult, "wait", false,
		"дождаться завершения повторной обработки в этом процессе")
	cmd.Flags().DurationVar(&a.waitTimeout, "wait-timeout", defaultWaitTimeout,
		"сколько ждать завершения повторной обработки (с --wait)")

	return cmd
}

// printRetried выводит результат повторной постановки в очередь.
func (a *App) printRetried(meeting domain.Meeting, status *service.MeetingStatus) error {
	out := a.printer()

	finalStatus, errText := finalState(meeting, status)

	if out.json {
		payload := map[string]any{
			"id":     meeting.ID.String(),
			"status": finalStatus.String(),
		}
		if errText != "" {
			payload["error"] = errText
		}
		return out.writeJSON(payload)
	}

	a.printf("встреча поставлена на повтор: %s\n", meeting.ID)
	a.printf("статус: %s\n", finalStatus)

	if finalStatus == domain.StatusFailed {
		a.printf("ошибка: %s\n", orEmpty(errText))
	}

	return nil
}
