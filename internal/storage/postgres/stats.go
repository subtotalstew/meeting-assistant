package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/subtotalstew/meeting-assistant/internal/domain"
)

// qCountByStatus считает встречи пользователя по статусам.
const qCountByStatus = `
SELECT status, count(*)
FROM meetings
WHERE user_id = $1
GROUP BY status`

// qAverageProcessingTime считает среднее время обработки завершённых задач.
// EXTRACT(EPOCH ...) даёт секунды с дробной частью — их удобно перевести в Duration.
const qAverageProcessingTime = `
SELECT COALESCE(avg(EXTRACT(EPOCH FROM (t.finished_at - t.started_at))), 0)
FROM processing_tasks t
JOIN meetings m ON m.id = t.meeting_id
WHERE m.user_id = $1
  AND t.started_at IS NOT NULL
  AND t.finished_at IS NOT NULL`

const qCountQuestions = `
SELECT count(*)
FROM qa_history q
JOIN meetings m ON m.id = q.meeting_id
WHERE m.user_id = $1`

// Stats собирает агрегаты по встречам пользователя.
func (s *Store) Stats(ctx context.Context, userID uuid.UUID) (domain.Stats, error) {
	stats := domain.Stats{ByStatus: make(map[domain.Status]int)}

	rows, err := s.pool.Query(ctx, qCountByStatus, userID)
	if err != nil {
		return domain.Stats{}, fmt.Errorf("статистика по статусам: %w", err)
	}

	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			rows.Close()
			return domain.Stats{}, fmt.Errorf("чтение статистики по статусам: %w", err)
		}
		stats.ByStatus[domain.Status(status)] = count
		stats.Total += count
	}

	if err := rows.Err(); err != nil {
		rows.Close()
		return domain.Stats{}, fmt.Errorf("статистика по статусам: %w", err)
	}
	rows.Close()

	var averageSeconds float64
	if err := s.pool.QueryRow(ctx, qAverageProcessingTime, userID).Scan(&averageSeconds); err != nil {
		return domain.Stats{}, fmt.Errorf("среднее время обработки: %w", err)
	}
	stats.AvgProcessing = time.Duration(averageSeconds * float64(time.Second))

	if err := s.pool.QueryRow(ctx, qCountQuestions, userID).Scan(&stats.Questions); err != nil {
		return domain.Stats{}, fmt.Errorf("подсчёт вопросов: %w", err)
	}

	return stats, nil
}
