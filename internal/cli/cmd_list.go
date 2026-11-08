package cli

import (
	"github.com/spf13/cobra"

	"github.com/subtotalstew/meeting-assistant/internal/domain"
)

// listPreviewLen — сколько символов выжимки показываем в списке встреч.
const listPreviewLen = 60

// listCommand печатает встречи пользователя.
func (a *App) listCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Показать сохранённые встречи пользователя",
		Long: `Выводит идентификатор встречи, дату создания, статус обработки и краткое описание:
фрагмент выжимки, если она уже получена, иначе название встречи.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			user, err := a.currentUser(cmd.Context())
			if err != nil {
				return err
			}

			meetings, err := a.svc.ListMeetings(cmd.Context(), user.ID)
			if err != nil {
				return err
			}

			out := a.printer()

			if out.json {
				items := make([]map[string]string, 0, len(meetings))
				for _, meeting := range meetings {
					items = append(items, map[string]string{
						"id":         meeting.ID.String(),
						"title":      meeting.Title,
						"status":     meeting.Status.String(),
						"created_at": formatTime(meeting.CreatedAt),
						"summary":    meeting.SummaryPreview,
					})
				}
				return out.writeJSON(items)
			}

			if len(meetings) == 0 {
				a.println("встреч пока нет: загрузите файл командой load <path>")
				return nil
			}

			rows := make([][]string, 0, len(meetings))
			for _, meeting := range meetings {
				rows = append(rows, []string{
					meeting.ID.String(),
					formatTime(meeting.CreatedAt),
					meeting.Status.String(),
					describeMeeting(meeting),
				})
			}

			return out.writeTable([]string{"ID", "СОЗДАНА", "СТАТУС", "ОПИСАНИЕ"}, rows)
		},
	}
}

// describeMeeting выбирает, что показать в колонке описания.
func describeMeeting(meeting domain.MeetingItem) string {
	if meeting.SummaryPreview != "" {
		return truncateText(meeting.SummaryPreview, listPreviewLen)
	}
	if meeting.Title != "" {
		return truncateText(meeting.Title, listPreviewLen)
	}
	return orEmpty(meeting.FileName)
}
