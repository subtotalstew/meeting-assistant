package cli

import (
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/subtotalstew/meeting-assistant/internal/domain"
)

// statsCommand показывает агрегаты по встречам пользователя.
func (a *App) statsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Показать статистику по встречам: статусы, время обработки, вопросы",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			user, err := a.currentUser(cmd.Context())
			if err != nil {
				return err
			}

			stats, err := a.svc.Stats(cmd.Context(), user.ID)
			if err != nil {
				return err
			}

			out := a.printer()

			if out.json {
				byStatus := make(map[string]int, len(stats.ByStatus))
				for status, count := range stats.ByStatus {
					byStatus[status.String()] = count
				}

				return out.writeJSON(map[string]any{
					"total":                  stats.Total,
					"by_status":              byStatus,
					"questions":              stats.Questions,
					"avg_processing_seconds": int(stats.AvgProcessing.Seconds()),
				})
			}

			if stats.Total == 0 {
				a.println("встреч пока нет: загрузите файл командой load <path>")
				return nil
			}

			rows := make([][]string, 0, len(stats.ByStatus))
			for _, status := range sortedStatuses(stats.ByStatus) {
				rows = append(rows, []string{status.String(), fmt.Sprintf("%d", stats.ByStatus[status])})
			}

			if err := out.writeTable([]string{"СТАТУС", "КОЛИЧЕСТВО"}, rows); err != nil {
				return err
			}

			a.printf("\nвсего встреч:          %d\n", stats.Total)
			a.printf("среднее время:         %s\n", formatDuration(stats.AvgProcessing))
			a.printf("вопросов по встречам:  %d\n", stats.Questions)

			return nil
		},
	}
}

// sortedStatuses возвращает статусы в порядке жизненного цикла, а не в порядке карты.
func sortedStatuses(byStatus map[domain.Status]int) []domain.Status {
	order := make(map[domain.Status]int)
	for index, status := range domain.AllStatuses() {
		order[status] = index
	}

	statuses := make([]domain.Status, 0, len(byStatus))
	for status := range byStatus {
		statuses = append(statuses, status)
	}

	sort.Slice(statuses, func(i, j int) bool {
		return order[statuses[i]] < order[statuses[j]]
	})

	return statuses
}

// formatDuration печатает длительность по-человечески.
func formatDuration(value time.Duration) string {
	if value <= 0 {
		return emptyValue
	}
	return value.Round(time.Millisecond).String()
}
