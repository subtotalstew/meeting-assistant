package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/subtotalstew/meeting-assistant/internal/domain"
	"github.com/subtotalstew/meeting-assistant/internal/service"
)

// fakeService реализует Service через функции: тест задаёт только нужные методы.
type fakeService struct {
	ensureUserFunc    func(ctx context.Context, externalID string) (domain.User, error)
	loadMeetingFunc   func(ctx context.Context, userID uuid.UUID, path string) (domain.Meeting, error)
	listMeetingsFunc  func(ctx context.Context, userID uuid.UUID) ([]domain.MeetingItem, error)
	statusFunc        func(ctx context.Context, userID, meetingID uuid.UUID) (service.MeetingStatus, error)
	transcriptionFunc func(ctx context.Context, userID, meetingID uuid.UUID) (domain.Transcription, error)
	summaryFunc       func(ctx context.Context, userID, meetingID uuid.UUID) (domain.Summary, error)
	searchFunc        func(ctx context.Context, userID uuid.UUID, query string) ([]domain.SearchHit, error)
	askFunc           func(ctx context.Context, userID, meetingID uuid.UUID, question string) (string, error)
	retryFunc         func(ctx context.Context, userID, meetingID uuid.UUID) (domain.Meeting, error)
	deleteMeetingFunc func(ctx context.Context, userID, meetingID uuid.UUID) error
	statsFunc         func(ctx context.Context, userID uuid.UUID) (domain.Stats, error)
}

func (f *fakeService) EnsureUser(ctx context.Context, externalID string) (domain.User, error) {
	if f.ensureUserFunc == nil {
		return domain.User{}, errors.New("EnsureUser не ожидался в этом тесте")
	}
	return f.ensureUserFunc(ctx, externalID)
}

func (f *fakeService) LoadMeeting(ctx context.Context, userID uuid.UUID, path string) (domain.Meeting, error) {
	if f.loadMeetingFunc == nil {
		return domain.Meeting{}, errors.New("LoadMeeting не ожидался в этом тесте")
	}
	return f.loadMeetingFunc(ctx, userID, path)
}

func (f *fakeService) ListMeetings(ctx context.Context, userID uuid.UUID) ([]domain.MeetingItem, error) {
	if f.listMeetingsFunc == nil {
		return nil, errors.New("ListMeetings не ожидался в этом тесте")
	}
	return f.listMeetingsFunc(ctx, userID)
}

func (f *fakeService) Status(ctx context.Context, userID, meetingID uuid.UUID) (service.MeetingStatus, error) {
	if f.statusFunc == nil {
		return service.MeetingStatus{}, errors.New("Status не ожидался в этом тесте")
	}
	return f.statusFunc(ctx, userID, meetingID)
}

func (f *fakeService) Transcription(ctx context.Context, userID, meetingID uuid.UUID) (domain.Transcription, error) {
	if f.transcriptionFunc == nil {
		return domain.Transcription{}, errors.New("Transcription не ожидался в этом тесте")
	}
	return f.transcriptionFunc(ctx, userID, meetingID)
}

func (f *fakeService) Summary(ctx context.Context, userID, meetingID uuid.UUID) (domain.Summary, error) {
	if f.summaryFunc == nil {
		return domain.Summary{}, errors.New("Summary не ожидался в этом тесте")
	}
	return f.summaryFunc(ctx, userID, meetingID)
}

func (f *fakeService) Search(ctx context.Context, userID uuid.UUID, query string) ([]domain.SearchHit, error) {
	if f.searchFunc == nil {
		return nil, errors.New("Search не ожидался в этом тесте")
	}
	return f.searchFunc(ctx, userID, query)
}

func (f *fakeService) Ask(ctx context.Context, userID, meetingID uuid.UUID, question string) (string, error) {
	if f.askFunc == nil {
		return "", errors.New("Ask не ожидался в этом тесте")
	}
	return f.askFunc(ctx, userID, meetingID, question)
}

// fakeWorker фиксирует запуск и восстановление фоновой обработки.
type fakeWorker struct {
	mu        sync.Mutex
	started   bool
	recovered bool
	onStart   func()
}

func (w *fakeWorker) Start(context.Context) {
	w.mu.Lock()
	w.started = true
	hook := w.onStart
	w.mu.Unlock()

	if hook != nil {
		hook()
	}
}

func (w *fakeWorker) Stop(context.Context) error {
	return nil
}

func (w *fakeWorker) Recover(context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.recovered = true
	return nil
}

func (w *fakeWorker) state() (bool, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.started, w.recovered
}

// run выполняет CLI с заданными аргументами и возвращает вывод.
func run(t *testing.T, svc Service, worker Worker, args ...string) (string, string, error) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), svc, worker, args, &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

func TestStartRegistersUser(t *testing.T) {
	var gotExternalID string

	svc := &fakeService{
		ensureUserFunc: func(_ context.Context, externalID string) (domain.User, error) {
			gotExternalID = externalID
			return domain.User{ID: uuid.New(), ExternalID: externalID}, nil
		},
	}

	stdout, _, err := run(t, svc, &fakeWorker{}, "start", "--user", "alice")
	if err != nil {
		t.Fatalf("start вернул ошибку: %v", err)
	}
	if gotExternalID != "alice" {
		t.Errorf("в сервис ушёл идентификатор %q", gotExternalID)
	}
	if !strings.Contains(stdout, "alice") {
		t.Errorf("в выводе нет пользователя: %q", stdout)
	}
}

func TestCommandsRequireUser(t *testing.T) {
	svc := &fakeService{}

	stdout, _, err := run(t, svc, &fakeWorker{}, "start")
	if err == nil {
		t.Fatal("ожидалась ошибка об отсутствии пользователя")
	}
	if !strings.Contains(err.Error(), "--user") {
		t.Errorf("ошибка не подсказывает флаг --user: %v", err)
	}
	if stdout != "" {
		t.Errorf("при ошибке ничего не должно печататься в stdout: %q", stdout)
	}
}

func TestStartJSONOutput(t *testing.T) {
	userID := uuid.New()

	svc := &fakeService{
		ensureUserFunc: func(_ context.Context, externalID string) (domain.User, error) {
			return domain.User{ID: userID, ExternalID: externalID}, nil
		},
	}

	stdout, _, err := run(t, svc, &fakeWorker{}, "start", "--user", "bob", "--json")
	if err != nil {
		t.Fatalf("start вернул ошибку: %v", err)
	}

	var payload map[string]string
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("вывод не является JSON: %v (%q)", err, stdout)
	}
	if payload["id"] != userID.String() {
		t.Errorf("id = %q, ожидался %q", payload["id"], userID)
	}
	if payload["external_id"] != "bob" {
		t.Errorf("external_id = %q", payload["external_id"])
	}
}

func TestServeStartsWorkerAndRecovers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	worker := &fakeWorker{onStart: func() {
		// Отменяем контекст, чтобы serve вернулся, а не ждал сигнала.
		cancel()
	}}

	var stdout bytes.Buffer
	err := Execute(ctx, &fakeService{}, worker, []string{"serve"}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("serve вернул ошибку: %v", err)
	}

	started, recovered := worker.state()
	if !started {
		t.Error("пул не запущен")
	}
	if !recovered {
		t.Error("восстановление задач не выполнено")
	}
}

func TestServeWithoutWorker(t *testing.T) {
	if _, _, err := run(t, &fakeService{}, nil, "serve"); err == nil {
		t.Fatal("ожидалась ошибка об отсутствии пула")
	}
}

func TestUnknownCommand(t *testing.T) {
	if _, _, err := run(t, &fakeService{}, &fakeWorker{}, "nosuchcommand"); err == nil {
		t.Fatal("ожидалась ошибка неизвестной команды")
	}
}

func TestParseMeetingID(t *testing.T) {
	want := uuid.New()

	got, err := parseMeetingID(want.String())
	if err != nil {
		t.Fatalf("parseMeetingID: %v", err)
	}
	if got != want {
		t.Errorf("идентификатор = %s, ожидался %s", got, want)
	}

	if _, err := parseMeetingID("не-uuid"); err == nil {
		t.Fatal("ожидалась ошибка разбора")
	}
}

func TestTableOutputAlignsColumns(t *testing.T) {
	var buffer bytes.Buffer

	out := printer{out: &buffer}
	if err := out.writeTable([]string{"ID", "СТАТУС"}, [][]string{{"a1b2", "completed"}, {"c3", "failed"}}); err != nil {
		t.Fatalf("writeTable: %v", err)
	}

	lines := strings.Split(strings.TrimRight(buffer.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("строк в таблице %d, ожидалось 4: %q", len(lines), buffer.String())
	}
	if !strings.Contains(lines[2], "completed") || !strings.Contains(lines[3], "failed") {
		t.Errorf("данные не попали в таблицу: %q", buffer.String())
	}
}

func TestFormatHelpers(t *testing.T) {
	if got := orEmpty("   "); got != emptyValue {
		t.Errorf("orEmpty() = %q, ожидался прочерк", got)
	}
	if got := truncateText("одно   два\nтри", 100); got != "одно два три" {
		t.Errorf("truncateText() = %q", got)
	}
	if got := truncateText(strings.Repeat("я", 10), 5); got != "яяяяя…" {
		t.Errorf("truncateText() = %q", got)
	}
}

func (f *fakeService) Retry(ctx context.Context, userID, meetingID uuid.UUID) (domain.Meeting, error) {
	if f.retryFunc == nil {
		return domain.Meeting{}, errors.New("Retry не ожидался в этом тесте")
	}
	return f.retryFunc(ctx, userID, meetingID)
}

func (f *fakeService) DeleteMeeting(ctx context.Context, userID, meetingID uuid.UUID) error {
	if f.deleteMeetingFunc == nil {
		return errors.New("DeleteMeeting не ожидался в этом тесте")
	}
	return f.deleteMeetingFunc(ctx, userID, meetingID)
}

func (f *fakeService) Stats(ctx context.Context, userID uuid.UUID) (domain.Stats, error) {
	if f.statsFunc == nil {
		return domain.Stats{}, errors.New("Stats не ожидался в этом тесте")
	}
	return f.statsFunc(ctx, userID)
}
