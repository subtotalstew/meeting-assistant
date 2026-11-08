package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// startCommand регистрирует пользователя.
//
// В CLI отдельная регистрация не обязательна: пользователь создаётся при первом
// обращении с новым --user. Команда нужна, чтобы показать идентификатор заранее
// и чтобы сценарий совпадал с мессенджерами, где есть явная команда start.
func (a *App) startCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Зарегистрировать пользователя и показать его идентификатор",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			user, err := a.currentUser(cmd.Context())
			if err != nil {
				return err
			}

			if a.jsonOutput {
				return a.printer().writeJSON(map[string]string{
					"id":          user.ID.String(),
					"external_id": user.ExternalID,
					"created_at":  formatTime(user.CreatedAt),
				})
			}

			fmt.Fprintf(a.stdout, "пользователь %s зарегистрирован\n", user.ExternalID)
			fmt.Fprintf(a.stdout, "идентификатор: %s\n", user.ID)
			return nil
		},
	}
}
