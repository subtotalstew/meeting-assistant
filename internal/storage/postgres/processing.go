package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/subtotalstew/meeting-assistant/internal/domain"
)

const taskColumns = `
    id, meeting_id, status, attempts, error, started_at, finished_at, created_at, updated_at`

// qClaimTasks атомарно забирает из очереди до $2 задач и переводит их в работу.
//
// FOR UPDATE SKIP LOCKED — ключевая часть: строку блокирует один диспетчер,
// остальные её пропускают и берут следующую, а не ждут освобождения блокировки.
// Поэтому несколько процессов (несколько serve) делят очередь между собой.
//
// attempts увеличивается здесь, а не в обработчике: попытка начинается в момент
// захвата, и если процесс упадёт сразу после него, попытка всё равно учтена.
const qClaimTasks = `
UPDATE processing_tasks t
SET status     = 'processing',
    attempts   = t.attempts + 1,
    started_at = now(),
    updated_at = now()
FROM (
    SELECT id
    FROM processing_tasks
    WHERE status = 'created'
    ORDER BY created_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT $1
) picked
WHERE t.id = picked.id
RETURNING t.meeting_id`

// qTouchMeetings переводит встречи захваченных задач в processing.
// Отдельным запросом вместо цикла: батч забирается пачкой, пачкой же обновляется.
const qTouchMeetings = `
UPDATE meetings
SET status = 'processing', error = '', updated_at = now()
WHERE id = ANY($1) AND status IN ('created', 'failed')`

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

// qResetForRetryTask возвращает неуспешную задачу в очередь.
// Условие status = 'failed' не даёт случайно перезапустить успешную обработку.
const qResetForRetryTask = `
UPDATE processing_tasks
SET status      = 'created',
    started_at  = NULL,
    finished_at = NULL,
    error       = '',
    updated_at  = now()
WHERE meeting_id = $1 AND status = 'failed'
RETURNING id`

const qResetForRetryMeeting = `
UPDATE meetings
SET status = 'created', error = '', updated_at = now()
WHERE id = $1`

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

// ClaimTasks забирает из очереди до limit задач и переводит их в processing.
//
// Это замена StartProcessing для воркеров очереди: переход created -> processing
// выполняет именно тот, кто забрал задачу, ровно один раз. Статус встречи
// меняется в той же транзакции, что и статус задачи: пользователь смотрит на
// встречу (команда status), а обработчик — на задачу, и расхождение между ними
// выглядит как зависание.
func (s *Store) ClaimTasks(ctx context.Context, limit int) ([]uuid.UUID, error) {
	if limit <= 0 {
		return nil, nil
	}

	var claimed []uuid.UUID

	err := s.withTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, qClaimTasks, limit)
		if err != nil {
			return fmt.Errorf("выборка задач из очереди: %w", err)
		}

		for rows.Next() {
			var meetingID uuid.UUID
			if err := rows.Scan(&meetingID); err != nil {
				rows.Close()
				return fmt.Errorf("чтение идентификатора встречи: %w", err)
			}
			claimed = append(claimed, meetingID)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("чтение задач из очереди: %w", err)
		}

		if len(claimed) == 0 {
			return nil
		}

		if _, err := tx.Exec(ctx, qTouchMeetings, claimed); err != nil {
			return fmt.Errorf("перевод встреч в обработку: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return claimed, nil
}

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

// ResetForRetry возвращает неуспешную встречу в очередь обработки.
//
// Повторяется только failed: успешную обработку перезапускать нельзя без явного
// обоснования, а незавершённую задачу в created восстанавливает RecoverPending.
func (s *Store) ResetForRetry(ctx context.Context, meetingID uuid.UUID) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var taskID uuid.UUID
		if err := tx.QueryRow(ctx, qResetForRetryTask, meetingID).Scan(&taskID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: повтор возможен только для задачи в статусе failed", domain.ErrInvalidTransition)
			}
			return fmt.Errorf("сброс задачи для повтора: %w", err)
		}

		if _, err := tx.Exec(ctx, qResetForRetryMeeting, meetingID); err != nil {
			return fmt.Errorf("сброс статуса встречи: %w", err)
		}

		return nil
	})
}

const qReclaimStaleTasks = `
UPDATE processing_tasks
SET status     = 'created',
    started_at = NULL,
    finished_at = NULL,
    updated_at = now()
WHERE status = 'processing'
  AND started_at < now() - make_interval(secs => $1)
RETURNING meeting_id`

const qReclaimStaleMeetings = `
UPDATE meetings
SET status = 'created', error = '', updated_at = now()
WHERE id = ANY($1) AND status = 'processing'`

// ReclaimStale возвращает в очередь задачи, которые «зависли» в processing.
//
// Признак зависания — started_at старше lease. Так выглядит процесс, убитый
// посреди обработки: строку никто не освободит, и без такого возврата встреча
// навсегда осталась бы в processing. Глобальный сброс всех processing не
// используется: он сбрасывал бы и задачи живого обработчика в другом процессе.
func (s *Store) ReclaimStale(ctx context.Context, lease time.Duration) ([]uuid.UUID, error) {
	if lease <= 0 {
		return nil, nil
	}

	var reclaimed []uuid.UUID

	err := s.withTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, qReclaimStaleTasks, lease.Seconds())
		if err != nil {
			return fmt.Errorf("выборка зависших задач: %w", err)
		}

		for rows.Next() {
			var meetingID uuid.UUID
			if err := rows.Scan(&meetingID); err != nil {
				rows.Close()
				return fmt.Errorf("чтение идентификатора встречи: %w", err)
			}
			reclaimed = append(reclaimed, meetingID)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("чтение зависших задач: %w", err)
		}

		if len(reclaimed) == 0 {
			return nil
		}

		if _, err := tx.Exec(ctx, qReclaimStaleMeetings, reclaimed); err != nil {
			return fmt.Errorf("возврат встреч в очередь: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return reclaimed, nil
}

const qRequeueInterruptedMeetings = `
UPDATE meetings
SET status = 'created', error = '', updated_at = now()
WHERE id = $1 AND status = 'processing'`

const qRequeueInterruptedTask = `
UPDATE processing_tasks
SET status      = 'created',
    started_at  = NULL,
    finished_at = NULL,
    error       = '',
    updated_at  = now()
WHERE meeting_id = $1 AND status = 'processing'
RETURNING id`

// RequeueInterrupted возвращает задачу в очередь, если обработка была прервана
// остановкой приложения. Вызывается обработчиком при отмене контекста.
//
// Возврат в created, а не failed: обработка не завершилась сбоем, её просто
// прервали, и следующий запуск должен её продолжить. Если задача уже успела
// уйти в терминальный статус (её довёл другой обработчик), запрос ничего не
// сделает — это ожидаемо.
func (s *Store) RequeueInterrupted(ctx context.Context, meetingID uuid.UUID) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var taskID uuid.UUID
		err := tx.QueryRow(ctx, qRequeueInterruptedTask, meetingID).Scan(&taskID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("возврат задачи в очередь: %w", err)
		}

		if _, err := tx.Exec(ctx, qRequeueInterruptedMeetings, meetingID); err != nil {
			return fmt.Errorf("возврат встречи в очередь: %w", err)
		}

		return nil
	})
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
