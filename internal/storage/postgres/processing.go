package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/subtotalstew/meeting-assistant/internal/domain"
)

const taskColumns = `
    id, meeting_id, status, attempts, error, started_at, finished_at, created_at, updated_at`

// qStartProcessing переводит задачу в работу, увеличивая счётчик попыток.
// Условие status = 'created' не даёт двум обработчикам взять одну задачу дважды.
const qStartProcessing = `
UPDATE processing_tasks
SET status       = 'processing',
    attempts     = attempts + 1,
    started_at   = now(),
    finished_at  = NULL,
    error        = '',
    updated_at   = now()
WHERE meeting_id = $1 AND status = 'created'
RETURNING` + taskColumns

const qGetTaskByMeeting = `
SELECT` + taskColumns + `
FROM processing_tasks
WHERE meeting_id = $1
ORDER BY created_at DESC
LIMIT 1`

// qResetUnfinishedTasks возвращает в очередь задачи, прерванные остановкой приложения.
// В рабочем статусе их держать нельзя: следующий запуск не взял бы их в работу.
const qResetUnfinishedTasks = `
UPDATE processing_tasks
SET status      = 'created',
    started_at  = NULL,
    finished_at = NULL,
    updated_at  = now()
WHERE status IN ('processing', 'transcribed', 'summarized')`

const qResetUnfinishedMeetings = `
UPDATE meetings
SET status = 'created', error = '', updated_at = now()
WHERE status IN ('processing', 'transcribed', 'summarized')`

const qPendingMeetingIDs = `
SELECT meeting_id
FROM processing_tasks
WHERE status = 'created'
ORDER BY created_at
LIMIT $1`

const qUpsertTranscription = `
INSERT INTO transcriptions (meeting_id, text, language, provider, created_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (meeting_id) DO UPDATE
SET text       = EXCLUDED.text,
    language   = EXCLUDED.language,
    provider   = EXCLUDED.provider,
    created_at = EXCLUDED.created_at`

const qUpsertSummary = `
INSERT INTO summaries (meeting_id, text, provider, created_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (meeting_id) DO UPDATE
SET text       = EXCLUDED.text,
    provider   = EXCLUDED.provider,
    created_at = EXCLUDED.created_at`

// StartProcessing переводит задачу в статус processing.
//
// Статус задачи и статус встречи меняются в одной транзакции: пользователь смотрит
// на встречу, а обработчик — на задачу, и расхождение между ними выглядит как зависание.
func (s *Store) StartProcessing(ctx context.Context, meetingID uuid.UUID) (domain.Task, error) {
	var task domain.Task

	err := s.withTx(ctx, func(tx pgx.Tx) error {
		updated, err := scanTask(tx.QueryRow(ctx, qStartProcessing, meetingID))
		if err != nil {
			return wrapNotFound(err, "задача обработки в статусе created")
		}
		task = updated

		return markStatus(ctx, tx, meetingID, domain.StatusProcessing, "", false)
	})
	if err != nil {
		return domain.Task{}, err
	}

	return task, nil
}

// SaveTranscription сохраняет транскрипцию и переводит задачу в transcribed.
func (s *Store) SaveTranscription(ctx context.Context, transcription domain.Transcription) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, qUpsertTranscription,
			transcription.MeetingID,
			transcription.Text,
			transcription.Language,
			transcription.Provider,
			transcription.CreatedAt,
		); err != nil {
			return fmt.Errorf("сохранение транскрипции: %w", err)
		}

		return markStatus(ctx, tx, transcription.MeetingID, domain.StatusTranscribed, "", false)
	})
}

// SaveSummary сохраняет краткую выжимку и переводит задачу в summarized.
func (s *Store) SaveSummary(ctx context.Context, summary domain.Summary) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, qUpsertSummary,
			summary.MeetingID,
			summary.Text,
			summary.Provider,
			summary.CreatedAt,
		); err != nil {
			return fmt.Errorf("сохранение выжимки: %w", err)
		}

		return markStatus(ctx, tx, summary.MeetingID, domain.StatusSummarized, "", false)
	})
}

// CompleteProcessing фиксирует успешное завершение обработки.
func (s *Store) CompleteProcessing(ctx context.Context, meetingID uuid.UUID) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		return markStatus(ctx, tx, meetingID, domain.StatusCompleted, "", true)
	})
}

// MarkFailed сохраняет текст ошибки и переводит задачу и встречу в failed.
//
// Текст ошибки виден пользователю через status <id>, поэтому он должен быть
// понятным человеку, а не дампом стека.
func (s *Store) MarkFailed(ctx context.Context, meetingID uuid.UUID, reason string) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		return markStatus(ctx, tx, meetingID, domain.StatusFailed, reason, true)
	})
}

// GetTask возвращает задачу обработки встречи.
func (s *Store) GetTask(ctx context.Context, meetingID uuid.UUID) (domain.Task, error) {
	task, err := scanTask(s.pool.QueryRow(ctx, qGetTaskByMeeting, meetingID))
	if err != nil {
		return domain.Task{}, wrapNotFound(err, "задача обработки")
	}
	return task, nil
}

// markStatus обновляет статус задачи и встречи. Вызывается только внутри транзакции.
func markStatus(ctx context.Context, tx pgx.Tx, meetingID uuid.UUID, status domain.Status, errText string, finished bool) error {
	const qTask = `
UPDATE processing_tasks
SET status      = $2,
    error       = $3,
    updated_at  = now(),
    finished_at = CASE WHEN $4 THEN now() ELSE finished_at END
WHERE meeting_id = $1`

	if _, err := tx.Exec(ctx, qTask, meetingID, string(status), errText, finished); err != nil {
		return fmt.Errorf("обновление задачи: %w", err)
	}

	const qMeeting = `
UPDATE meetings
SET status = $2, error = $3, updated_at = now()
WHERE id = $1`

	if _, err := tx.Exec(ctx, qMeeting, meetingID, string(status), errText); err != nil {
		return fmt.Errorf("обновление встречи: %w", err)
	}

	return nil
}

// RecoverPending возвращает в очередь задачи, прерванные предыдущим запуском,
// и отдаёт идентификаторы встреч, которые нужно обработать.
//
// Вызывается один раз при старте приложения, до приёма новых команд. Без такого
// восстановления встреча, прерванная остановкой сервиса, осталась бы навсегда
// в статусе processing и никто бы её не перезапустил.
func (s *Store) RecoverPending(ctx context.Context, limit int) ([]uuid.UUID, error) {
	if limit <= 0 {
		limit = 1000
	}

	var pending []uuid.UUID

	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, qResetUnfinishedTasks); err != nil {
			return fmt.Errorf("сброс незавершённых задач: %w", err)
		}

		if _, err := tx.Exec(ctx, qResetUnfinishedMeetings); err != nil {
			return fmt.Errorf("сброс статусов встреч: %w", err)
		}

		rows, err := tx.Query(ctx, qPendingMeetingIDs, limit)
		if err != nil {
			return fmt.Errorf("выборка задач в очереди: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var meetingID uuid.UUID
			if err := rows.Scan(&meetingID); err != nil {
				return fmt.Errorf("чтение идентификатора встречи: %w", err)
			}
			pending = append(pending, meetingID)
		}

		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return pending, nil
}

func scanTask(row rowScanner) (domain.Task, error) {
	var task domain.Task
	var status string

	if err := row.Scan(
		&task.ID,
		&task.MeetingID,
		&status,
		&task.Attempts,
		&task.Error,
		&task.StartedAt,
		&task.FinishedAt,
		&task.CreatedAt,
		&task.UpdatedAt,
	); err != nil {
		return domain.Task{}, err
	}

	task.Status = domain.Status(status)
	return task, nil
}
