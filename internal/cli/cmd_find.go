package cli

import (
	"github.com/spf13/cobra"
)

// findPreviewLen — сколько символов фрагмента показываем в результатах поиска.
const findPreviewLen = 80

// findCommand ищет встречи по ключевому слову или фразе.
func (a *App) findCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "find <keyword>",
		Short: "Найти встречи по ключевому слову или фразе",
		Long: `Ищет в транскрипциях, кратких выжимках и названиях встреч текущего пользователя.
Поиск по чужим встречам невозможен: фильтр по пользователю стоит в самом запросе.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			user, err := a.currentUser(cmd.Context())
			if err != nil {
				return err
			}

			hits, err := a.svc.Search(cmd.Context(), user.ID, args[0])
			if err != nil {
				return err
			}

			out := a.printer()

			if out.json {
				items := make([]map[string]string, 0, len(hits))
				for _, hit := range hits {
					items = append(items, map[string]string{
						"id":         hit.MeetingID.String(),
						"title":      hit.Title,
						"status":     hit.Status.String(),
						"created_at": formatTime(hit.CreatedAt),
						"snippet":    hit.Snippet,
					})
				}
				return out.writeJSON(items)
			}

			if len(hits) == 0 {
				a.printf("по запросу %q ничего не найдено\n", args[0])
				return nil
			}

			rows := make([][]string, 0, len(hits))
			for _, hit := range hits {
				rows = append(rows, []string{
					hit.MeetingID.String(),
					formatTime(hit.CreatedAt),
					hit.Status.String(),
					truncateText(orEmpty(hit.Snippet), findPreviewLen),
				})
			}

			return out.writeTable([]string{"ID", "СОЗДАНА", "СТАТУС", "ФРАГМЕНТ"}, rows)
		},
	}
}
