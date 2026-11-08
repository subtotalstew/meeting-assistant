package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/subtotalstew/meeting-assistant/internal/domain"
)

const meetingColumns = `
    m.id, m.user_id, m.title, m.file_name, m.status, m.error, m.created_at, m.updated_at`

const qInsertMeeting = `
INSERT INTO meetings (id, user_id, title, file_name, status, error, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

const qInsertTask = `
INSERT INTO processing_tasks (id, meeting_id, status, attempts, error, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)`

const qGetMeeting = `
SELECT` + meetingColumns + `
FROM meetings m
WHERE m.id = $1 AND m.user_id = $2`

const qListMeetings = `
SELECT` + meetingColumns + `
FROM meetings m
WHERE m.user_id = $1
ORDER BY m.created_at DESC, m.id DESC
LIMIT $2`

const qDeleteMeeting = `
DELETE FROM meetings WHERE id = $1 AND user_id = $2`

// CreateMeeting сохраняет встречу и задачу обработки одной транзакцией.
//
// Встреча без задачи обработки — потерянная запись: пользователь видит её в списке,
// но не может ни дождаться результата, ни перезапустить. Поэтому либо обе строки
// появляются, либо ни одной.
func (s *Store) CreateMeeting(ctx context.Context, meeting domain.Meeting, task domain.Task) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, qInsertMeeting,
			meeting.ID,
			meeting.UserID,
			meeting.Title,
			meeting.FileName,
			string(meeting.Status),
			meeting.Error,
			meeting.CreatedAt,
			meeting.UpdatedAt,
		); err != nil {
			return fmt.Errorf("сохранение встречи: %w", err)
		}

		if _, err := tx.Exec(ctx, qInsertTask,
			task.ID,
			task.MeetingID,
			string(task.Status),
			task.Attempts,
			task.Error,
			task.CreatedAt,
			task.UpdatedAt,
		); err != nil {
			return fmt.Errorf("сохранение задачи обработки: %w", err)
		}

		return nil
	})
}

// GetMeeting возвращает встречу пользователя.
//
// Фильтр по user_id стоит прямо в WHERE: чужой идентификатор даёт ErrNotFound,
// и приложение принципиально не различает "не существует" и "принадлежит другому".
func (s *Store) GetMeeting(ctx context.Context, id, userID uuid.UUID) (domain.Meeting, error) {
	meeting, err := scanMeeting(s.pool.QueryRow(ctx, qGetMeeting, id, userID))
	if err != nil {
		return domain.Meeting{}, wrapNotFound(err, "встреча")
	}
	return meeting, nil
}

// ListMeetings возвращает встречи пользователя, новые сверху.
func (s *Store) ListMeetings(ctx context.Context, userID uuid.UUID, limit int) ([]domain.Meeting, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := s.pool.Query(ctx, qListMeetings, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("выборка встреч: %w", err)
	}
	defer rows.Close()

	meetings := make([]domain.Meeting, 0, limit)
	for rows.Next() {
		meeting, err := scanMeeting(rows)
		if err != nil {
			return nil, fmt.Errorf("чтение встречи: %w", err)
		}
		meetings = append(meetings, meeting)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("чтение встреч: %w", err)
	}

	return meetings, nil
}

// DeleteMeeting удаляет встречу вместе со связанными данными.
// Транскрипция, выжимка, задача и история вопросов уходят каскадом на уровне схемы.
func (s *Store) DeleteMeeting(ctx context.Context, id, userID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, qDeleteMeeting, id, userID)
	if err != nil {
		return fmt.Errorf("удаление встречи: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return wrapNotFound(pgx.ErrNoRows, "встреча")
	}
	return nil
}

func scanMeeting(row rowScanner) (domain.Meeting, error) {
	var meeting domain.Meeting
	var status string

	if err := row.Scan(
		&meeting.ID,
		&meeting.UserID,
		&meeting.Title,
		&meeting.FileName,
		&status,
		&meeting.Error,
		&meeting.CreatedAt,
		&meeting.UpdatedAt,
	); err != nil {
		return domain.Meeting{}, err
	}

	meeting.Status = domain.Status(status)
	return meeting, nil
}
