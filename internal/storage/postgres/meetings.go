package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/subtotalstew/meeting-assistant/internal/domain"
)

const meetingColumns = `
    m.id, m.user_id, m.title, m.file_name, m.source_path, m.status, m.error, m.created_at, m.updated_at`

// summaryPreviewLen — сколько символов выжимки показываем в списке встреч.
const summaryPreviewLen = 200

const qInsertMeeting = `
INSERT INTO meetings (id, user_id, title, file_name, source_path, status, error, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

const qInsertTask = `
INSERT INTO processing_tasks (id, meeting_id, status, attempts, error, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)`

const qGetMeeting = `
SELECT` + meetingColumns + `
FROM meetings m
WHERE m.id = $1 AND m.user_id = $2`

const qGetMeetingByID = `
SELECT` + meetingColumns + `
FROM meetings m
WHERE m.id = $1`

// qListMeetings отдаёт встречи вместе с фрагментом выжимки: LEFT JOIN, потому что
// у свежей встречи выжимки ещё нет, но из списка её терять нельзя.
var qListMeetings = fmt.Sprintf(`
SELECT%[1]s,
    COALESCE(left(s.text, %[2]d), '') AS summary_preview
FROM meetings m
LEFT JOIN summaries s ON s.meeting_id = m.id
WHERE m.user_id = $1
ORDER BY m.created_at DESC, m.id DESC
LIMIT $2`, meetingColumns, summaryPreviewLen)

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
			meeting.SourcePath,
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

// GetMeetingByID возвращает встречу без проверки владельца.
//
// Метод нужен фоновому обработчику: он действует от имени системы, а не
// пользователя, поэтому фильтр по user_id здесь не применяется. Все
// пользовательские сценарии обязаны идти через GetMeeting.
func (s *Store) GetMeetingByID(ctx context.Context, id uuid.UUID) (domain.Meeting, error) {
	meeting, err := scanMeeting(s.pool.QueryRow(ctx, qGetMeetingByID, id))
	if err != nil {
		return domain.Meeting{}, wrapNotFound(err, "встреча")
	}
	return meeting, nil
}

// ListMeetings возвращает встречи пользователя вместе с фрагментами выжимок, новые сверху.
func (s *Store) ListMeetings(ctx context.Context, userID uuid.UUID, limit int) ([]domain.MeetingItem, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := s.pool.Query(ctx, qListMeetings, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("выборка встреч: %w", err)
	}
	defer rows.Close()

	meetings := make([]domain.MeetingItem, 0, limit)
	for rows.Next() {
		item, err := scanMeetingItem(rows)
		if err != nil {
			return nil, fmt.Errorf("чтение встречи: %w", err)
		}
		meetings = append(meetings, item)
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
		&meeting.SourcePath,
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

// scanMeetingItem читает встречу вместе с фрагментом выжимки.
func scanMeetingItem(row rowScanner) (domain.MeetingItem, error) {
	var item domain.MeetingItem
	var status string

	if err := row.Scan(
		&item.ID,
		&item.UserID,
		&item.Title,
		&item.FileName,
		&item.SourcePath,
		&status,
		&item.Error,
		&item.CreatedAt,
		&item.UpdatedAt,
		&item.SummaryPreview,
	); err != nil {
		return domain.MeetingItem{}, err
	}

	item.Status = domain.Status(status)
	return item, nil
}
