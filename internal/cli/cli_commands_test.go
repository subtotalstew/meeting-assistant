package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/subtotalstew/meeting-assistant/internal/domain"
	"github.com/subtotalstew/meeting-assistant/internal/service"
)

// fastPolling уменьшает период опроса, чтобы тесты не ждали в реальном времени.
func fastPolling(t *testing.T) {
	t.Helper()

	previous := pollInterval
	pollInterval = time.Millisecond
	t.Cleanup(func() { pollInterval = previous })
}

func userStub(t *testing.T) *fakeService {
	t.Helper()

	return &fakeService{
		ensureUserFunc: func(_ context.Context, externalID string) (domain.User, error) {
			return domain.User{ID: uuid.New(), ExternalID: externalID}, nil
		},
	}
}

func TestLoadWaitsForCompletion(t *testing.T) {
	fastPolling(t)

	meeting := domain.Meeting{
		ID:        uuid.New(),
		FileName:  "sprint-review.ogg",
		Title:     "sprint-review",
		Status:    domain.StatusCreated,
		CreatedAt: time.Now(),
	}

	svc := userStub(t)
	svc.loadMeetingFunc = func(context.Context, uuid.UUID, string) (domain.Meeting, error) {
		return meeting, nil
	}

	polls := 0
	svc.statusFunc = func(context.Context, uuid.UUID, uuid.UUID) (service.MeetingStatus, error) {
		polls++

		current := meeting
		if polls > 1 {
			current.Status = domain.StatusCompleted
		} else {
			current.Status = domain.StatusProcessing
		}

		return service.MeetingStatus{
			Meeting: current,
			Task:    domain.Task{Status: current.Status},
		}, nil
	}

	worker := &fakeWorker{}

	stdout, _, err := run(t, svc, worker, "load", "sprint-review.ogg", "--user", "alice", "--wait-timeout", "2s")
	if err != nil {
		t.Fatalf("load вернул ошибку: %v", err)
	}

	if started, _ := worker.state(); !started {
		t.Error("пул воркеров не запущен")
	}
	if !strings.Contains(stdout, domain.StatusCompleted.String()) {
		t.Errorf("в выводе нет финального статуса: %q", stdout)
	}
	if !strings.Contains(stdout, meeting.ID.String()) {
		t.Errorf("в выводе нет идентификатора встречи: %q", stdout)
	}
	if polls < 2 {
		t.Errorf("статус опрошен %d раз, ожидалось не меньше двух", polls)
	}
}

func TestLoadNoWaitDoesNotStartWorker(t *testing.T) {
	meeting := domain.Meeting{ID: uuid.New(), FileName: "meeting.ogg", Status: domain.StatusCreated}

	svc := userStub(t)
	svc.loadMeetingFunc = func(context.Context, uuid.UUID, string) (domain.Meeting, error) {
		return meeting, nil
	}

	worker := &fakeWorker{}

	stdout, _, err := run(t, svc, worker, "load", "meeting.ogg", "--user", "alice", "--no-wait")
	if err != nil {
		t.Fatalf("load вернул ошибку: %v", err)
	}

	if started, _ := worker.state(); started {
		t.Error("с --no-wait пул запускать не нужно")
	}
	if !strings.Contains(stdout, "serve") {
		t.Errorf("в выводе нет подсказки про serve: %q", stdout)
	}
}

func TestLoadReportsFailedTask(t *testing.T) {
	fastPolling(t)

	meeting := domain.Meeting{ID: uuid.New(), FileName: "broken.ogg", Status: domain.StatusCreated}

	svc := userStub(t)
	svc.loadMeetingFunc = func(context.Context, uuid.UUID, string) (domain.Meeting, error) {
		return meeting, nil
	}
	svc.statusFunc = func(context.Context, uuid.UUID, uuid.UUID) (service.MeetingStatus, error) {
		failed := meeting
		failed.Status = domain.StatusFailed
		failed.Error = "внешний сервис недоступен"

		return service.MeetingStatus{
			Meeting: failed,
			Task:    domain.Task{Status: domain.StatusFailed, Error: failed.Error},
		}, nil
	}

	stdout, _, err := run(t, svc, &fakeWorker{}, "load", "broken.ogg", "--user", "alice", "--wait-timeout", "2s")
	if err != nil {
		t.Fatalf("load вернул ошибку: %v", err)
	}

	if !strings.Contains(stdout, "внешний сервис недоступен") {
		t.Errorf("в выводе нет причины ошибки: %q", stdout)
	}
}

func TestLoadRequiresPath(t *testing.T) {
	if _, _, err := run(t, userStub(t), &fakeWorker{}, "load", "--user", "alice"); err == nil {
		t.Fatal("ожидалась ошибка об отсутствии пути к файлу")
	}
}

func TestLoadJSONOutput(t *testing.T) {
	fastPolling(t)

	meeting := domain.Meeting{ID: uuid.New(), FileName: "meeting.ogg", Status: domain.StatusCreated}

	svc := userStub(t)
	svc.loadMeetingFunc = func(context.Context, uuid.UUID, string) (domain.Meeting, error) {
		return meeting, nil
	}
	svc.statusFunc = func(context.Context, uuid.UUID, uuid.UUID) (service.MeetingStatus, error) {
		completed := meeting
		completed.Status = domain.StatusCompleted
		return service.MeetingStatus{Meeting: completed, Task: domain.Task{Status: completed.Status}}, nil
	}

	stdout, _, err := run(t, svc, &fakeWorker{}, "load", "meeting.ogg", "--user", "alice", "--json", "--wait-timeout", "2s")
	if err != nil {
		t.Fatalf("load вернул ошибку: %v", err)
	}
	if !strings.Contains(stdout, "\"status\": \"completed\"") {
		t.Errorf("JSON не содержит статус: %q", stdout)
	}
}

func TestStatusShowsErrorText(t *testing.T) {
	meetingID := uuid.New()

	svc := userStub(t)
	svc.statusFunc = func(context.Context, uuid.UUID, uuid.UUID) (service.MeetingStatus, error) {
		return service.MeetingStatus{
			Meeting: domain.Meeting{
				ID:        meetingID,
				Title:     "Итоги спринта",
				Status:    domain.StatusFailed,
				Error:     "внешний сервис не ответил вовремя",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			Task: domain.Task{Status: domain.StatusFailed, Attempts: 2, Error: "внешний сервис не ответил вовремя"},
		}, nil
	}

	stdout, _, err := run(t, svc, &fakeWorker{}, "status", meetingID.String(), "--user", "alice")
	if err != nil {
		t.Fatalf("status вернул ошибку: %v", err)
	}

	if !strings.Contains(stdout, "failed") {
		t.Errorf("в выводе нет статуса: %q", stdout)
	}
	if !strings.Contains(stdout, "не ответил вовремя") {
		t.Errorf("в выводе нет текста ошибки: %q", stdout)
	}
	if !strings.Contains(stdout, "2") {
		t.Errorf("в выводе нет числа попыток: %q", stdout)
	}
}

func TestStatusRejectsMalformedID(t *testing.T) {
	if _, _, err := run(t, userStub(t), &fakeWorker{}, "status", "42", "--user", "alice"); err == nil {
		t.Fatal("ожидалась ошибка разбора идентификатора")
	}
}

func TestListRendersMeetings(t *testing.T) {
	svc := userStub(t)
	svc.listMeetingsFunc = func(context.Context, uuid.UUID) ([]domain.MeetingItem, error) {
		return []domain.MeetingItem{
			{
				Meeting: domain.Meeting{
					ID:        uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					Title:     "Итоги спринта",
					Status:    domain.StatusCompleted,
					CreatedAt: time.Now(),
				},
				SummaryPreview: "Договорились о релизе в пятницу",
			},
		}, nil
	}

	stdout, _, err := run(t, svc, &fakeWorker{}, "list", "--user", "alice")
	if err != nil {
		t.Fatalf("list вернул ошибку: %v", err)
	}

	for _, want := range []string{"ID", "СТАТУС", "completed", "релизе в пятницу"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("в выводе нет %q: %q", want, stdout)
		}
	}
}

func TestListEmpty(t *testing.T) {
	svc := userStub(t)
	svc.listMeetingsFunc = func(context.Context, uuid.UUID) ([]domain.MeetingItem, error) {
		return nil, nil
	}

	stdout, _, err := run(t, svc, &fakeWorker{}, "list", "--user", "alice")
	if err != nil {
		t.Fatalf("list вернул ошибку: %v", err)
	}
	if !strings.Contains(stdout, "встреч пока нет") {
		t.Errorf("нет подсказки для пустого списка: %q", stdout)
	}
}

func TestGetPrintsTranscription(t *testing.T) {
	meetingID := uuid.New()

	svc := userStub(t)
	svc.transcriptionFunc = func(context.Context, uuid.UUID, uuid.UUID) (domain.Transcription, error) {
		return domain.Transcription{
			MeetingID: meetingID,
			Text:      "Обсудили сроки и договорились о релизе.",
			Provider:  "mock",
			CreatedAt: time.Now(),
		}, nil
	}

	stdout, _, err := run(t, svc, &fakeWorker{}, "get", meetingID.String(), "--user", "alice")
	if err != nil {
		t.Fatalf("get вернул ошибку: %v", err)
	}
	if !strings.Contains(stdout, "договорились о релизе") {
		t.Errorf("в выводе нет транскрипции: %q", stdout)
	}
}

func TestGetPropagatesNotReady(t *testing.T) {
	svc := userStub(t)
	svc.transcriptionFunc = func(context.Context, uuid.UUID, uuid.UUID) (domain.Transcription, error) {
		return domain.Transcription{}, errors.Join(service.ErrNotReady, errors.New("транскрипция ещё не получена"))
	}

	if _, _, err := run(t, svc, &fakeWorker{}, "get", uuid.New().String(), "--user", "alice"); err == nil {
		t.Fatal("ожидалась ошибка о неготовности материалов")
	}
}

func TestSummaryPrintsText(t *testing.T) {
	meetingID := uuid.New()

	svc := userStub(t)
	svc.summaryFunc = func(context.Context, uuid.UUID, uuid.UUID) (domain.Summary, error) {
		return domain.Summary{
			MeetingID: meetingID,
			Text:      "Ключевое: релиз в пятницу.",
			Provider:  "mock",
			CreatedAt: time.Now(),
		}, nil
	}

	stdout, _, err := run(t, svc, &fakeWorker{}, "summary", meetingID.String(), "--user", "alice")
	if err != nil {
		t.Fatalf("summary вернул ошибку: %v", err)
	}
	if !strings.Contains(stdout, "релиз в пятницу") {
		t.Errorf("в выводе нет выжимки: %q", stdout)
	}
}
