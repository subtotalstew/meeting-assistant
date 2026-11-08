// Package cli — слой пользовательского интерфейса.
//
// Здесь только разбор аргументов и вывод: команды вызывают методы сервиса
// и ничего не знают ни про базу, ни про внешние API. Поэтому CLI можно заменить
// на VK-бота или Telegram-бота, не меняя бизнес-логику.
package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/subtotalstew/meeting-assistant/internal/domain"
	"github.com/subtotalstew/meeting-assistant/internal/service"
)

// Значения по умолчанию для флагов команд.
const (
	defaultWaitTimeout = 5 * time.Minute
)

// pollInterval — период опроса статуса при ожидании обработки.
// Переменная, а не константа: тесты уменьшают её, чтобы не ждать в реальном времени.
var pollInterval = 300 * time.Millisecond

// Service — то, что CLI нужно от бизнес-логики.
//
// Интерфейс объявлен на стороне потребителя: команды можно тестировать на фейке,
// не поднимая PostgreSQL и не вызывая внешние сервисы.
type Service interface {
	EnsureUser(ctx context.Context, externalID string) (domain.User, error)
	LoadMeeting(ctx context.Context, userID uuid.UUID, path string) (domain.Meeting, error)
	ListMeetings(ctx context.Context, userID uuid.UUID) ([]domain.MeetingItem, error)
	Status(ctx context.Context, userID, meetingID uuid.UUID) (service.MeetingStatus, error)
	Transcription(ctx context.Context, userID, meetingID uuid.UUID) (domain.Transcription, error)
	Summary(ctx context.Context, userID, meetingID uuid.UUID) (domain.Summary, error)
	Search(ctx context.Context, userID uuid.UUID, query string) ([]domain.SearchHit, error)
	Ask(ctx context.Context, userID, meetingID uuid.UUID, question string) (string, error)
	Retry(ctx context.Context, userID, meetingID uuid.UUID) (domain.Meeting, error)
	DeleteMeeting(ctx context.Context, userID, meetingID uuid.UUID) error
	Stats(ctx context.Context, userID uuid.UUID) (domain.Stats, error)
}

// Worker — управление фоновой обработкой.
//
// Воркеры живут в том же процессе, что и CLI, поэтому пул запускают только те
// команды, которым нужна обработка (load, serve), а остальные его не трогают.
type Worker interface {
	Start(ctx context.Context)
	Stop(ctx context.Context) error
	Recover(ctx context.Context) error
}

// App — состояние CLI: зависимости и значения флагов.
type App struct {
	svc    Service
	worker Worker
	stdout io.Writer
	stderr io.Writer

	userID      string
	jsonOutput  bool
	noWait      bool
	waitTimeout time.Duration
	chatMeeting string
}

// Execute собирает команды и выполняет их.
func Execute(ctx context.Context, svc Service, worker Worker, args []string, stdout, stderr io.Writer) error {
	app := &App{
		svc:         svc,
		worker:      worker,
		stdout:      stdout,
		stderr:      stderr,
		waitTimeout: defaultWaitTimeout,
	}

	root := app.rootCommand()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	return root.ExecuteContext(ctx)
}

// currentUser возвращает пользователя по флагу --user, создавая его при первом обращении.
func (a *App) currentUser(ctx context.Context) (domain.User, error) {
	externalID := strings.TrimSpace(a.userID)
	if externalID == "" {
		return domain.User{}, errors.New("не указан пользователь: добавьте флаг --user <идентификатор>")
	}

	return a.svc.EnsureUser(ctx, externalID)
}

// parseMeetingID разбирает идентификатор встречи из аргумента команды.
func parseMeetingID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return uuid.Nil, errors.New("некорректный идентификатор встречи: ожидается UUID")
	}
	return id, nil
}
