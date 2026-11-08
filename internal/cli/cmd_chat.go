package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// chatCommand задаёт вопрос по материалам встречи.
//
// Привязка к встрече задаётся флагом --meeting: без него непонятно, по каким
// материалам отвечать, а спрашивать «по всем встречам сразу» — другая задача.
func (a *App) chatCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   `chat "<вопрос>"`,
		Short: "Задать вопрос по материалам встречи",
		Long: `Передаёт вопрос LLM-клиенту вместе с транскрипцией и краткой выжимкой встречи.
Вопрос и ответ сохраняются в истории встречи.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.chatMeeting == "" {
				return errors.New("не указана встреча: добавьте флаг --meeting <id>")
			}

			meetingID, err := parseMeetingID(a.chatMeeting)
			if err != nil {
				return err
			}

			user, err := a.currentUser(cmd.Context())
			if err != nil {
				return err
			}

			answer, err := a.svc.Ask(cmd.Context(), user.ID, meetingID, args[0])
			if err != nil {
				return err
			}

			out := a.printer()

			if out.json {
				return out.writeJSON(map[string]string{
					"meeting_id": meetingID.String(),
					"question":   args[0],
					"answer":     answer,
				})
			}

			fmt.Fprintf(a.stdout, "%s\n", answer)
			return nil
		},
	}

	cmd.Flags().StringVarP(&a.chatMeeting, "meeting", "m", "",
		"идентификатор встречи, по материалам которой задаётся вопрос")

	return cmd
}
