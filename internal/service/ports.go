package service

import (
	"context"

	"github.com/google/uuid"

	"github.com/subtotalstew/meeting-assistant/internal/domain"
)

// Repository — то, что сервису нужно от хранилища.
//
// Интерфейсы объявлены на стороне потребителя, а не хранилища: сервису нужен
// не весь Store, а конкретные методы. Реализация (postgres.Store) подходит
// неявно, и её можно заменить фейком в unit-тестах без базы.
type Repository interface {
	userRepository
	meetingRepository
	documentRepository
}

type userRepository interface {
	UpsertUser(ctx context.Context, user domain.User) (domain.User, error)
	GetUserByExternalID(ctx context.Context, externalID string) (domain.User, error)
}

type meetingRepository interface {
	CreateMeeting(ctx context.Context, meeting domain.Meeting, task domain.Task) error
	GetMeeting(ctx context.Context, id, userID uuid.UUID) (domain.Meeting, error)
	ListMeetings(ctx context.Context, userID uuid.UUID, limit int) ([]domain.MeetingItem, error)
	DeleteMeeting(ctx context.Context, id, userID uuid.UUID) error
	GetTask(ctx context.Context, meetingID uuid.UUID) (domain.Task, error)
	ResetForRetry(ctx context.Context, meetingID uuid.UUID) error
}

type documentRepository interface {
	GetTranscription(ctx context.Context, meetingID, userID uuid.UUID) (domain.Transcription, error)
	GetSummary(ctx context.Context, meetingID, userID uuid.UUID) (domain.Summary, error)
	SearchMeetings(ctx context.Context, userID uuid.UUID, query string, limit int) ([]domain.SearchHit, error)
	SaveQuestion(ctx context.Context, question domain.Question) error
	ListQuestions(ctx context.Context, meetingID, userID uuid.UUID, limit int) ([]domain.Question, error)
}

// TaskQueue — очередь фоновой обработки.
//
// Интерфейс живёт здесь, а реализация — в пакете worker: сервис не должен знать,
// чем именно является очередь (канал, таблица в базе, брокер).
type TaskQueue interface {
	Enqueue(meetingID uuid.UUID) error
}
