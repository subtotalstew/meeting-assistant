package cli

import (
	"github.com/spf13/cobra"
)

// startCommand регистрирует пользователя.
//
// В CLI отдельная регистрация не обязательна: пользователь создаётся при первом
// обращении с новым --user. Команда нужна, чтобы показать идентификатор заранее
// и чтобы сценарий совпадал с мессенджерами, где есть явная команда start.
//
// С флагом --shell после регистрации открывается интерактивный режим: один
// процесс, команды без --user, соединение с базой и пул воркеров подняты один раз.
func (a *App) startCommand() *cobra.Command {
	cmd := &cobra.Command{
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

			a.printf("пользователь %s зарегистрирован\n", user.ExternalID)
			a.printf("идентификатор: %s\n", user.ID)

			return nil
		},
	}

	return cmd
}
