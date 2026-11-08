package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// deleteCommand удаляет встречу и связанные с ней данные.
func (a *App) deleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Удалить встречу вместе с транскрипцией, выжимкой и историей вопросов",
		Long: `Удаляет встречу пользователя. Связанные строки (задача обработки, транскрипция,
выжимка, история вопросов) удаляются каскадом на уровне схемы, поэтому
неконсистентных данных не остаётся.`,
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

			if err := a.svc.DeleteMeeting(cmd.Context(), user.ID, meetingID); err != nil {
				return err
			}

			out := a.printer()

			if out.json {
				return out.writeJSON(map[string]string{"deleted": meetingID.String()})
			}

			fmt.Fprintf(a.stdout, "встреча удалена: %s\n", meetingID)
			return nil
		},
	}
}

// exportCommand сохраняет транскрипцию встречи в файл.
func (a *App) exportCommand() *cobra.Command {
	var outPath string

	cmd := &cobra.Command{
		Use:   "export <id>",
		Short: "Сохранить транскрипцию встречи в файл",
		Long: `Записывает полную транскрипцию в файл. Без --out имя формируется из
идентификатора встречи и текущего каталога.`,
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

			transcription, err := a.svc.Transcription(cmd.Context(), user.ID, meetingID)
			if err != nil {
				return err
			}

			target := strings.TrimSpace(outPath)
			if target == "" {
				target = fmt.Sprintf("meeting-%s.txt", meetingID.String()[:8])
			}

			if err := os.WriteFile(target, []byte(transcription.Text+"\n"), 0o644); err != nil {
				return fmt.Errorf("запись файла %s: %w", target, err)
			}

			absolute, err := filepath.Abs(target)
			if err != nil {
				absolute = target
			}

			out := a.printer()

			if out.json {
				return out.writeJSON(map[string]string{
					"id":   meetingID.String(),
					"path": absolute,
					"size": fmt.Sprintf("%d", len(transcription.Text)),
				})
			}

			fmt.Fprintf(a.stdout, "транскрипция сохранена: %s\n", absolute)
			return nil
		},
	}

	cmd.Flags().StringVarP(&outPath, "out", "o", "", "путь к файлу для сохранения транскрипции")

	return cmd
}
