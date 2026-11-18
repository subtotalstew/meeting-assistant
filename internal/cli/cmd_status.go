package cli

import (
	"github.com/spf13/cobra"
)

// statusCommand показывает текущее состояние обработки встречи.
func (a *App) statusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status <id>",
		Short: "Показать статус обработки встречи",
		Long: `Показывает идентификатор встречи, текущий статус, дату создания, дату последнего
изменения статуса, число попыток обработки и текст ошибки, если она была.`,
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

			status, err := a.svc.Status(cmd.Context(), user.ID, meetingID)
			if err != nil {
				return err
			}

			out := a.printer()

			if out.json {
				return out.writeJSON(map[string]any{
					"id":         status.Meeting.ID.String(),
					"title":      status.Meeting.Title,
					"status":     status.Meeting.Status.String(),
					"created_at": formatTime(status.Meeting.CreatedAt),
					"updated_at": formatTime(status.Meeting.UpdatedAt),
					"attempts":   status.Task.Attempts,
					"error":      status.ErrorMessage(),
				})
			}

			a.printf("встреча:      %s\n", status.Meeting.ID)
			a.printf("название:     %s\n", orEmpty(status.Meeting.Title))
			a.printf("статус:       %s\n", status.Meeting.Status)
			a.printf("создана:      %s\n", formatTime(status.Meeting.CreatedAt))
			a.printf("изменена:     %s\n", formatTime(status.Meeting.UpdatedAt))
			a.printf("попыток:      %d\n", status.Task.Attempts)

			if message := status.ErrorMessage(); message != "" {
				a.printf("ошибка:       %s\n", message)
			}

			return nil
		},
	}
}
