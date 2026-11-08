package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/subtotalstew/meeting-assistant/internal/domain"
	"github.com/subtotalstew/meeting-assistant/internal/service"
)

// retryCommand повторяет обработку неуспешной встречи.
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

			if a.noWait {
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

	cmd.Flags().BoolVar(&a.noWait, "no-wait", false, "не ждать завершения повторной обработки")
	cmd.Flags().DurationVar(&a.waitTimeout, "wait-timeout", defaultWaitTimeout,
		"сколько ждать завершения повторной обработки")

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

	fmt.Fprintf(a.stdout, "встреча поставлена на повтор: %s\n", meeting.ID)
	fmt.Fprintf(a.stdout, "статус: %s\n", finalStatus)

	if finalStatus == domain.StatusFailed {
		fmt.Fprintf(a.stdout, "ошибка: %s\n", orEmpty(errText))
	}

	return nil
}
