package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// getCommand печатает полную транскрипцию встречи.
func (a *App) getCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Показать транскрипцию встречи",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			user, err := a.currentUser(cmd.Context())
			if err != nil {
				return err
			}

			meetingID, err := parseMeetingID(args[0])
			if err != nil {
				return err
			}

			transcription, err := a.svc.Transcription(cmd.Context(), user.ID, meetingID)
			if err != nil {
				return err
			}

			out := a.printer()

			if out.json {
				return out.writeJSON(map[string]string{
					"id":         transcription.MeetingID.String(),
					"text":       transcription.Text,
					"language":   transcription.Language,
					"provider":   transcription.Provider,
					"created_at": formatTime(transcription.CreatedAt),
				})
			}

			// В человекочитаемом режиме печатается только текст: так его удобно
			// перенаправлять в файл и искать глазами, без служебных строк.
			_, err = fmt.Fprintln(a.stdout, transcription.Text)
			return err
		},
	}
}

// summaryCommand печатает краткую выжимку встречи.
func (a *App) summaryCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "summary <id>",
		Short: "Показать краткую выжимку встречи",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			user, err := a.currentUser(cmd.Context())
			if err != nil {
				return err
			}

			meetingID, err := parseMeetingID(args[0])
			if err != nil {
				return err
			}

			summary, err := a.svc.Summary(cmd.Context(), user.ID, meetingID)
			if err != nil {
				return err
			}

			out := a.printer()

			if out.json {
				return out.writeJSON(map[string]string{
					"id":         summary.MeetingID.String(),
					"text":       summary.Text,
					"provider":   summary.Provider,
					"created_at": formatTime(summary.CreatedAt),
				})
			}

			_, err = fmt.Fprintln(a.stdout, summary.Text)
			return err
		},
	}
}
