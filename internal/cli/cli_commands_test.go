package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestFindRendersHits(t *testing.T) {
	svc := userStub(t)
	svc.searchFunc = func(_ context.Context, _ uuid.UUID, query string) ([]domain.SearchHit, error) {
		if query != "релиз" {
			t.Errorf("в сервис ушёл запрос %q", query)
		}
		return []domain.SearchHit{
			{
				MeetingID: uuid.MustParse("22222222-2222-2222-2222-222222222222"),
				Title:     "Итоги спринта",
				Status:    domain.StatusCompleted,
				CreatedAt: time.Now(),
				Snippet:   "Договорились, что релиз выйдет в пятницу",
			},
		}, nil
	}

	stdout, _, err := run(t, svc, &fakeWorker{}, "find", "релиз", "--user", "alice")
	if err != nil {
		t.Fatalf("find вернул ошибку: %v", err)
	}

	for _, want := range []string{"ФРАГМЕНТ", "релиз выйдет в пятницу"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("в выводе нет %q: %q", want, stdout)
		}
	}
}

func TestFindEmptyResult(t *testing.T) {
	svc := userStub(t)
	svc.searchFunc = func(context.Context, uuid.UUID, string) ([]domain.SearchHit, error) {
		return nil, nil
	}

	stdout, _, err := run(t, svc, &fakeWorker{}, "find", "бюджет", "--user", "alice")
	if err != nil {
		t.Fatalf("find вернул ошибку: %v", err)
	}
	if !strings.Contains(stdout, "ничего не найдено") {
		t.Errorf("нет сообщения о пустом результате: %q", stdout)
	}
}

func TestChatPrintsAnswer(t *testing.T) {
	meetingID := uuid.New()

	svc := userStub(t)
	svc.askFunc = func(_ context.Context, _ uuid.UUID, gotMeetingID uuid.UUID, question string) (string, error) {
		if gotMeetingID != meetingID {
			t.Errorf("вопрос ушёл по встрече %s, ожидалась %s", gotMeetingID, meetingID)
		}
		if question != "Когда релиз?" {
			t.Errorf("вопрос = %q", question)
		}
		return "По материалам встречи: релиз выйдет в пятницу.", nil
	}

	stdout, _, err := run(t, svc, &fakeWorker{}, "chat", "Когда релиз?", "--user", "alice", "--meeting", meetingID.String())
	if err != nil {
		t.Fatalf("chat вернул ошибку: %v", err)
	}
	if !strings.Contains(stdout, "пятницу") {
		t.Errorf("в выводе нет ответа: %q", stdout)
	}
}

func TestChatRequiresMeeting(t *testing.T) {
	_, _, err := run(t, userStub(t), &fakeWorker{}, "chat", "Когда релиз?", "--user", "alice")
	if err == nil {
		t.Fatal("ожидалась ошибка об отсутствии встречи")
	}
	if !strings.Contains(err.Error(), "--meeting") {
		t.Errorf("ошибка не подсказывает флаг --meeting: %v", err)
	}
}

func TestChatJSONOutput(t *testing.T) {
	meetingID := uuid.New()

	svc := userStub(t)
	svc.askFunc = func(context.Context, uuid.UUID, uuid.UUID, string) (string, error) {
		return "ответ", nil
	}

	stdout, _, err := run(t, svc, &fakeWorker{}, "chat", "вопрос", "--user", "alice", "-m", meetingID.String(), "--json")
	if err != nil {
		t.Fatalf("chat вернул ошибку: %v", err)
	}
	if !strings.Contains(stdout, "\"answer\": \"ответ\"") {
		t.Errorf("JSON не содержит ответ: %q", stdout)
	}
}

func TestRetryResubmitsMeeting(t *testing.T) {
	fastPolling(t)

	meetingID := uuid.New()
	retried := false

	svc := userStub(t)
	svc.retryFunc = func(context.Context, uuid.UUID, uuid.UUID) (domain.Meeting, error) {
		retried = true
		return domain.Meeting{ID: meetingID, Status: domain.StatusCreated}, nil
	}
	svc.statusFunc = func(context.Context, uuid.UUID, uuid.UUID) (service.MeetingStatus, error) {
		return service.MeetingStatus{
			Meeting: domain.Meeting{ID: meetingID, Status: domain.StatusCompleted},
			Task:    domain.Task{Status: domain.StatusCompleted},
		}, nil
	}

	stdout, _, err := run(t, svc, &fakeWorker{}, "retry", meetingID.String(), "--user", "alice", "--wait-timeout", "2s")
	if err != nil {
		t.Fatalf("retry вернул ошибку: %v", err)
	}
	if !retried {
		t.Error("повтор не запрошен у сервиса")
	}
	if !strings.Contains(stdout, "completed") {
		t.Errorf("в выводе нет итогового статуса: %q", stdout)
	}
}

func TestRetryNoWait(t *testing.T) {
	meetingID := uuid.New()

	svc := userStub(t)
	svc.retryFunc = func(context.Context, uuid.UUID, uuid.UUID) (domain.Meeting, error) {
		return domain.Meeting{ID: meetingID, Status: domain.StatusCreated}, nil
	}

	worker := &fakeWorker{}
	stdout, _, err := run(t, svc, worker, "retry", meetingID.String(), "--user", "alice", "--no-wait")
	if err != nil {
		t.Fatalf("retry вернул ошибку: %v", err)
	}
	if started, _ := worker.state(); started {
		t.Error("с --no-wait пул запускать не нужно")
	}
	if !strings.Contains(stdout, "created") {
		t.Errorf("в выводе нет статуса: %q", stdout)
	}
}

func TestDeleteRemovesMeeting(t *testing.T) {
	meetingID := uuid.New()
	deleted := false

	svc := userStub(t)
	svc.deleteMeetingFunc = func(_ context.Context, _ uuid.UUID, gotID uuid.UUID) error {
		if gotID != meetingID {
			t.Errorf("удалена встреча %s, ожидалась %s", gotID, meetingID)
		}
		deleted = true
		return nil
	}

	stdout, _, err := run(t, svc, &fakeWorker{}, "delete", meetingID.String(), "--user", "alice")
	if err != nil {
		t.Fatalf("delete вернул ошибку: %v", err)
	}
	if !deleted {
		t.Error("удаление не выполнено")
	}
	if !strings.Contains(stdout, "встреча удалена") {
		t.Errorf("нет подтверждения удаления: %q", stdout)
	}
}

func TestExportWritesTranscription(t *testing.T) {
	meetingID := uuid.New()

	svc := userStub(t)
	svc.transcriptionFunc = func(context.Context, uuid.UUID, uuid.UUID) (domain.Transcription, error) {
		return domain.Transcription{MeetingID: meetingID, Text: "текст встречи"}, nil
	}

	target := filepath.Join(t.TempDir(), "out.txt")

	stdout, _, err := run(t, svc, &fakeWorker{}, "export", meetingID.String(), "--user", "alice", "--out", target)
	if err != nil {
		t.Fatalf("export вернул ошибку: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("файл не создан: %v", err)
	}
	if strings.TrimSpace(string(data)) != "текст встречи" {
		t.Errorf("в файле %q", string(data))
	}
	if !strings.Contains(stdout, "транскрипция сохранена") {
		t.Errorf("нет подтверждения: %q", stdout)
	}
}

func TestStatsRendersTable(t *testing.T) {
	svc := userStub(t)
	svc.statsFunc = func(context.Context, uuid.UUID) (domain.Stats, error) {
		return domain.Stats{
			Total: 3,
			ByStatus: map[domain.Status]int{
				domain.StatusCompleted: 2,
				domain.StatusFailed:    1,
			},
			Questions:     4,
			AvgProcessing: 1500 * time.Millisecond,
		}, nil
	}

	stdout, _, err := run(t, svc, &fakeWorker{}, "stats", "--user", "alice")
	if err != nil {
		t.Fatalf("stats вернул ошибку: %v", err)
	}

	for _, want := range []string{"СТАТУС", "completed", "failed", "всего встреч", "вопросов по встречам", "1.5s"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("в выводе нет %q: %q", want, stdout)
		}
	}
}

func TestStatsEmpty(t *testing.T) {
	svc := userStub(t)
	svc.statsFunc = func(context.Context, uuid.UUID) (domain.Stats, error) {
		return domain.Stats{ByStatus: map[domain.Status]int{}}, nil
	}

	stdout, _, err := run(t, svc, &fakeWorker{}, "stats", "--user", "alice")
	if err != nil {
		t.Fatalf("stats вернул ошибку: %v", err)
	}
	if !strings.Contains(stdout, "встреч пока нет") {
		t.Errorf("нет подсказки для пустой статистики: %q", stdout)
	}
}
