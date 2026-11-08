package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/subtotalstew/meeting-assistant/internal/client/llm"
	"github.com/subtotalstew/meeting-assistant/internal/client/speech"
	"github.com/subtotalstew/meeting-assistant/internal/domain"
)

// fakeRepo реализует Repository через функции: тест задаёт только те методы,
// которые действительно участвуют в сценарии. Ненастроенный метод — ошибка теста.
type fakeRepo struct {
	Repository

	getUserByExternalIDFunc func(ctx context.Context, externalID string) (domain.User, error)
	upsertUserFunc          func(ctx context.Context, user domain.User) (domain.User, error)
	createMeetingFunc       func(ctx context.Context, meeting domain.Meeting, task domain.Task) error
	getMeetingFunc          func(ctx context.Context, id, userID uuid.UUID) (domain.Meeting, error)
	listMeetingsFunc        func(ctx context.Context, userID uuid.UUID, limit int) ([]domain.MeetingItem, error)
	deleteMeetingFunc       func(ctx context.Context, id, userID uuid.UUID) error
	resetForRetryFunc       func(ctx context.Context, meetingID uuid.UUID) error
	getTaskFunc             func(ctx context.Context, meetingID uuid.UUID) (domain.Task, error)
	getTranscriptionFunc    func(ctx context.Context, meetingID, userID uuid.UUID) (domain.Transcription, error)
	getSummaryFunc          func(ctx context.Context, meetingID, userID uuid.UUID) (domain.Summary, error)
	searchMeetingsFunc      func(ctx context.Context, userID uuid.UUID, query string, limit int) ([]domain.SearchHit, error)
	saveQuestionFunc        func(ctx context.Context, question domain.Question) error
}

func (f *fakeRepo) GetUserByExternalID(ctx context.Context, externalID string) (domain.User, error) {
	if f.getUserByExternalIDFunc == nil {
		return domain.User{}, errors.New("GetUserByExternalID не ожидался в этом тесте")
	}
	return f.getUserByExternalIDFunc(ctx, externalID)
}

func (f *fakeRepo) UpsertUser(ctx context.Context, user domain.User) (domain.User, error) {
	if f.upsertUserFunc == nil {
		return domain.User{}, errors.New("UpsertUser не ожидался в этом тесте")
	}
	return f.upsertUserFunc(ctx, user)
}

func (f *fakeRepo) CreateMeeting(ctx context.Context, meeting domain.Meeting, task domain.Task) error {
	if f.createMeetingFunc == nil {
		return errors.New("CreateMeeting не ожидался в этом тесте")
	}
	return f.createMeetingFunc(ctx, meeting, task)
}

func (f *fakeRepo) GetMeeting(ctx context.Context, id, userID uuid.UUID) (domain.Meeting, error) {
	if f.getMeetingFunc == nil {
		return domain.Meeting{}, errors.New("GetMeeting не ожидался в этом тесте")
	}
	return f.getMeetingFunc(ctx, id, userID)
}

func (f *fakeRepo) ListMeetings(ctx context.Context, userID uuid.UUID, limit int) ([]domain.MeetingItem, error) {
	if f.listMeetingsFunc == nil {
		return nil, errors.New("ListMeetings не ожидался в этом тесте")
	}
	return f.listMeetingsFunc(ctx, userID, limit)
}

func (f *fakeRepo) DeleteMeeting(ctx context.Context, id, userID uuid.UUID) error {
	if f.deleteMeetingFunc == nil {
		return errors.New("DeleteMeeting не ожидался в этом тесте")
	}
	return f.deleteMeetingFunc(ctx, id, userID)
}

func (f *fakeRepo) GetTask(ctx context.Context, meetingID uuid.UUID) (domain.Task, error) {
	if f.getTaskFunc == nil {
		return domain.Task{}, errors.New("GetTask не ожидался в этом тесте")
	}
	return f.getTaskFunc(ctx, meetingID)
}

func (f *fakeRepo) ResetForRetry(ctx context.Context, meetingID uuid.UUID) error {
	if f.resetForRetryFunc == nil {
		return errors.New("ResetForRetry не ожидался в этом тесте")
	}
	return f.resetForRetryFunc(ctx, meetingID)
}

func (f *fakeRepo) GetTranscription(ctx context.Context, meetingID, userID uuid.UUID) (domain.Transcription, error) {
	if f.getTranscriptionFunc == nil {
		return domain.Transcription{}, errors.New("GetTranscription не ожидался в этом тесте")
	}
	return f.getTranscriptionFunc(ctx, meetingID, userID)
}

func (f *fakeRepo) GetSummary(ctx context.Context, meetingID, userID uuid.UUID) (domain.Summary, error) {
	if f.getSummaryFunc == nil {
		return domain.Summary{}, errors.New("GetSummary не ожидался в этом тесте")
	}
	return f.getSummaryFunc(ctx, meetingID, userID)
}

func (f *fakeRepo) SearchMeetings(ctx context.Context, userID uuid.UUID, query string, limit int) ([]domain.SearchHit, error) {
	if f.searchMeetingsFunc == nil {
		return nil, errors.New("SearchMeetings не ожидался в этом тесте")
	}
	return f.searchMeetingsFunc(ctx, userID, query, limit)
}

func (f *fakeRepo) SaveQuestion(ctx context.Context, question domain.Question) error {
	if f.saveQuestionFunc == nil {
		return errors.New("SaveQuestion не ожидался в этом тесте")
	}
	return f.saveQuestionFunc(ctx, question)
}

// fakeQueue запоминает поставленные в очередь встречи.
type fakeQueue struct {
	enqueued []uuid.UUID
	err      error
}

func (q *fakeQueue) Enqueue(meetingID uuid.UUID) error {
	if q.err != nil {
		return q.err
	}
	q.enqueued = append(q.enqueued, meetingID)
	return nil
}

func newTestService(repo Repository, queue TaskQueue, opts Options) *Service {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(repo, speech.NewMock(), llm.NewMock(), queue, logger, opts)
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("подготовка файла %s: %v", name, err)
	}
	return path
}

func TestEnsureUserCreatesOnFirstCall(t *testing.T) {
	var created domain.User

	repo := &fakeRepo{
		getUserByExternalIDFunc: func(context.Context, string) (domain.User, error) {
			return domain.User{}, domain.ErrNotFound
		},
		upsertUserFunc: func(_ context.Context, user domain.User) (domain.User, error) {
			created = user
			return user, nil
		},
	}

	user, err := newTestService(repo, &fakeQueue{}, DefaultOptions()).EnsureUser(context.Background(), "alice")
	if err != nil {
		t.Fatalf("EnsureUser() вернул ошибку: %v", err)
	}

	if user.ExternalID != "alice" {
		t.Errorf("ExternalID = %q", user.ExternalID)
	}
	if created.ID != user.ID {
		t.Error("в хранилище ушёл другой пользователь")
	}
}

func TestEnsureUserReturnsExisting(t *testing.T) {
	existing := domain.User{ID: uuid.New(), ExternalID: "bob"}

	repo := &fakeRepo{
		getUserByExternalIDFunc: func(context.Context, string) (domain.User, error) {
			return existing, nil
		},
		upsertUserFunc: func(context.Context, domain.User) (domain.User, error) {
			return domain.User{}, errors.New("существующего пользователя не нужно создавать заново")
		},
	}

	user, err := newTestService(repo, &fakeQueue{}, DefaultOptions()).EnsureUser(context.Background(), "bob")
	if err != nil {
		t.Fatalf("EnsureUser() вернул ошибку: %v", err)
	}
	if user.ID != existing.ID {
		t.Errorf("вернулся другой пользователь: %s", user.ID)
	}
}

func TestEnsureUserRejectsEmptyIdentifier(t *testing.T) {
	repo := &fakeRepo{
		getUserByExternalIDFunc: func(context.Context, string) (domain.User, error) {
			return domain.User{}, domain.ErrNotFound
		},
	}

	_, err := newTestService(repo, &fakeQueue{}, DefaultOptions()).EnsureUser(context.Background(), "   ")
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("ожидалась ErrValidation, получено: %v", err)
	}
}

func TestLoadMeetingRejectsInvalidFile(t *testing.T) {
	emptyFile := writeFile(t, "empty.ogg", "")
	unsupported := writeFile(t, "notes.pdf", "не аудио")
	tooBig := writeFile(t, "big.ogg", strings.Repeat("a", 2048))

	cases := []struct {
		name string
		path string
		opts Options
	}{
		{name: "пустой путь", path: "   ", opts: DefaultOptions()},
		{name: "файла нет", path: "no-such-meeting.ogg", opts: DefaultOptions()},
		{name: "каталог вместо файла", path: t.TempDir(), opts: DefaultOptions()},
		{name: "пустой файл", path: emptyFile, opts: DefaultOptions()},
		{name: "неподдерживаемый формат", path: unsupported, opts: DefaultOptions()},
		{name: "слишком большой файл", path: tooBig, opts: Options{MaxFileSize: 1024, AllowedExtensions: []string{".ogg"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepo{
				createMeetingFunc: func(context.Context, domain.Meeting, domain.Task) error {
					return errors.New("встречу нельзя создавать для некорректного файла")
				},
			}

			svc := newTestService(repo, &fakeQueue{}, tc.opts)
			_, err := svc.LoadMeeting(context.Background(), uuid.New(), tc.path)
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("ожидалась ErrValidation, получено: %v", err)
			}
		})
	}
}

func TestLoadMeetingCreatesMeetingAndEnqueues(t *testing.T) {
	path := writeFile(t, "sprint-review.ogg", "аудио")

	var savedMeeting domain.Meeting
	var savedTask domain.Task

	repo := &fakeRepo{
		createMeetingFunc: func(_ context.Context, meeting domain.Meeting, task domain.Task) error {
			savedMeeting, savedTask = meeting, task
			return nil
		},
	}
	queue := &fakeQueue{}

	meeting, err := newTestService(repo, queue, DefaultOptions()).LoadMeeting(context.Background(), uuid.New(), path)
	if err != nil {
		t.Fatalf("LoadMeeting() вернул ошибку: %v", err)
	}

	if savedMeeting.ID != meeting.ID {
		t.Error("сохранена другая встреча")
	}
	if savedTask.MeetingID != meeting.ID {
		t.Error("задача не связана со встречей")
	}
	if savedTask.Status != domain.StatusCreated {
		t.Errorf("статус задачи = %q, ожидался created", savedTask.Status)
	}
	if len(queue.enqueued) != 1 || queue.enqueued[0] != meeting.ID {
		t.Errorf("в очередь поставлено %v, ожидалась встреча %s", queue.enqueued, meeting.ID)
	}
}

// Переполненная очередь не должна отменять уже сохранённую встречу:
// задачу подхватит восстановление незавершённых задач.
func TestLoadMeetingToleratesQueueFailure(t *testing.T) {
	path := writeFile(t, "meeting.ogg", "аудио")

	repo := &fakeRepo{
		createMeetingFunc: func(context.Context, domain.Meeting, domain.Task) error { return nil },
	}

	queue := &fakeQueue{err: errors.New("очередь переполнена")}

	meeting, err := newTestService(repo, queue, DefaultOptions()).LoadMeeting(context.Background(), uuid.New(), path)
	if err != nil {
		t.Fatalf("LoadMeeting() вернул ошибку: %v", err)
	}
	if meeting.ID == uuid.Nil {
		t.Error("встреча не создана")
	}
}

func TestTranscriptionNotReady(t *testing.T) {
	repo := &fakeRepo{
		getMeetingFunc: func(context.Context, uuid.UUID, uuid.UUID) (domain.Meeting, error) {
			return domain.Meeting{Status: domain.StatusProcessing}, nil
		},
		getTranscriptionFunc: func(context.Context, uuid.UUID, uuid.UUID) (domain.Transcription, error) {
			return domain.Transcription{}, domain.ErrNotFound
		},
	}

	_, err := newTestService(repo, &fakeQueue{}, DefaultOptions()).Transcription(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("ожидалась ErrNotReady, получено: %v", err)
	}
}

func TestTranscriptionHiddenForForeignMeeting(t *testing.T) {
	repo := &fakeRepo{
		getMeetingFunc: func(context.Context, uuid.UUID, uuid.UUID) (domain.Meeting, error) {
			return domain.Meeting{}, domain.ErrNotFound
		},
	}

	_, err := newTestService(repo, &fakeQueue{}, DefaultOptions()).Transcription(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ожидалась ErrNotFound, получено: %v", err)
	}
}

func TestAskUsesMaterialsAndSavesHistory(t *testing.T) {
	var saved domain.Question

	repo := &fakeRepo{
		getMeetingFunc: func(context.Context, uuid.UUID, uuid.UUID) (domain.Meeting, error) {
			return domain.Meeting{Status: domain.StatusCompleted}, nil
		},
		getTranscriptionFunc: func(context.Context, uuid.UUID, uuid.UUID) (domain.Transcription, error) {
			return domain.Transcription{
				Text: "Договорились, что релиз выйдет в пятницу, до среды нужно закрыть код-ревью.",
			}, nil
		},
		getSummaryFunc: func(context.Context, uuid.UUID, uuid.UUID) (domain.Summary, error) {
			return domain.Summary{}, domain.ErrNotFound
		},
		saveQuestionFunc: func(_ context.Context, question domain.Question) error {
			saved = question
			return nil
		},
	}

	answer, err := newTestService(repo, &fakeQueue{}, DefaultOptions()).
		Ask(context.Background(), uuid.New(), uuid.New(), "Когда релиз?")
	if err != nil {
		t.Fatalf("Ask() вернул ошибку: %v", err)
	}

	if !strings.Contains(answer, "пятницу") {
		t.Errorf("ответ не опирается на материалы встречи: %q", answer)
	}
	if saved.Question != "Когда релиз?" {
		t.Errorf("в историю ушёл вопрос %q", saved.Question)
	}
	if saved.Answer != answer {
		t.Error("в историю ушёл другой ответ")
	}
}

func TestAskNotReady(t *testing.T) {
	repo := &fakeRepo{
		getMeetingFunc: func(context.Context, uuid.UUID, uuid.UUID) (domain.Meeting, error) {
			return domain.Meeting{}, nil
		},
		getTranscriptionFunc: func(context.Context, uuid.UUID, uuid.UUID) (domain.Transcription, error) {
			return domain.Transcription{}, domain.ErrNotFound
		},
	}

	_, err := newTestService(repo, &fakeQueue{}, DefaultOptions()).
		Ask(context.Background(), uuid.New(), uuid.New(), "О чём договорились?")
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("ожидалась ErrNotReady, получено: %v", err)
	}
}

func TestSearchRejectsEmptyQuery(t *testing.T) {
	repo := &fakeRepo{}

	_, err := newTestService(repo, &fakeQueue{}, DefaultOptions()).Search(context.Background(), uuid.New(), "   ")
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("ожидалась ErrValidation, получено: %v", err)
	}
}

func TestSearchPassesConfiguredLimit(t *testing.T) {
	var gotLimit int

	repo := &fakeRepo{
		searchMeetingsFunc: func(_ context.Context, _ uuid.UUID, query string, limit int) ([]domain.SearchHit, error) {
			gotLimit = limit
			return []domain.SearchHit{{Title: query}}, nil
		},
	}

	opts := DefaultOptions()
	opts.SearchLimit = 7

	hits, err := newTestService(repo, &fakeQueue{}, opts).Search(context.Background(), uuid.New(), "релиз")
	if err != nil {
		t.Fatalf("Search() вернул ошибку: %v", err)
	}
	if gotLimit != 7 {
		t.Errorf("предел поиска = %d, ожидалось 7", gotLimit)
	}
	if len(hits) != 1 {
		t.Errorf("результатов %d, ожидался 1", len(hits))
	}
}

func TestListMeetingsPassesConfiguredLimit(t *testing.T) {
	var gotLimit int

	repo := &fakeRepo{
		listMeetingsFunc: func(_ context.Context, _ uuid.UUID, limit int) ([]domain.MeetingItem, error) {
			gotLimit = limit
			return nil, nil
		},
	}

	opts := DefaultOptions()
	opts.ListLimit = 5

	if _, err := newTestService(repo, &fakeQueue{}, opts).ListMeetings(context.Background(), uuid.New()); err != nil {
		t.Fatalf("ListMeetings() вернул ошибку: %v", err)
	}
	if gotLimit != 5 {
		t.Errorf("предел списка = %d, ожидалось 5", gotLimit)
	}
}

func TestStatusCombinesMeetingAndTask(t *testing.T) {
	meetingID := uuid.New()

	repo := &fakeRepo{
		getMeetingFunc: func(context.Context, uuid.UUID, uuid.UUID) (domain.Meeting, error) {
			return domain.Meeting{ID: meetingID, Status: domain.StatusFailed, Error: "speech недоступен"}, nil
		},
		getTaskFunc: func(context.Context, uuid.UUID) (domain.Task, error) {
			return domain.Task{MeetingID: meetingID, Status: domain.StatusFailed, Attempts: 2}, nil
		},
	}

	status, err := newTestService(repo, &fakeQueue{}, DefaultOptions()).Status(context.Background(), uuid.New(), meetingID)
	if err != nil {
		t.Fatalf("Status() вернул ошибку: %v", err)
	}
	if status.Task.Attempts != 2 {
		t.Errorf("попыток = %d, ожидалось 2", status.Task.Attempts)
	}
	if status.ErrorMessage() != "speech недоступен" {
		t.Errorf("текст ошибки = %q", status.ErrorMessage())
	}
}

func TestRetryResetsFailedMeeting(t *testing.T) {
	meetingID := uuid.New()
	reset := false
	queue := &fakeQueue{}

	repo := &fakeRepo{
		getMeetingFunc: func(context.Context, uuid.UUID, uuid.UUID) (domain.Meeting, error) {
			return domain.Meeting{ID: meetingID, Status: domain.StatusFailed, Error: "внешний сервис недоступен"}, nil
		},
		resetForRetryFunc: func(_ context.Context, gotID uuid.UUID) error {
			if gotID != meetingID {
				t.Errorf("сброшена встреча %s, ожидалась %s", gotID, meetingID)
			}
			reset = true
			return nil
		},
	}

	meeting, err := newTestService(repo, queue, DefaultOptions()).
		Retry(context.Background(), uuid.New(), meetingID)
	if err != nil {
		t.Fatalf("Retry() вернул ошибку: %v", err)
	}

	if !reset {
		t.Error("задача не сброшена для повтора")
	}
	if meeting.Status != domain.StatusCreated {
		t.Errorf("статус после повтора = %q, ожидался created", meeting.Status)
	}
	if len(queue.enqueued) != 1 || queue.enqueued[0] != meetingID {
		t.Errorf("в очередь поставлено %v, ожидалась %s", queue.enqueued, meetingID)
	}
}

func TestRetryRejectsCompletedMeeting(t *testing.T) {
	repo := &fakeRepo{
		getMeetingFunc: func(context.Context, uuid.UUID, uuid.UUID) (domain.Meeting, error) {
			return domain.Meeting{Status: domain.StatusCompleted}, nil
		},
		resetForRetryFunc: func(context.Context, uuid.UUID) error {
			return errors.New("успешную обработку перезапускать нельзя")
		},
	}

	_, err := newTestService(repo, &fakeQueue{}, DefaultOptions()).
		Retry(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("ожидалась ErrInvalidTransition, получено: %v", err)
	}
}

func TestRetryForQueuedMeetingOnlyEnqueues(t *testing.T) {
	meetingID := uuid.New()
	queue := &fakeQueue{}

	repo := &fakeRepo{
		getMeetingFunc: func(context.Context, uuid.UUID, uuid.UUID) (domain.Meeting, error) {
			return domain.Meeting{ID: meetingID, Status: domain.StatusCreated}, nil
		},
		resetForRetryFunc: func(context.Context, uuid.UUID) error {
			return errors.New("для created сброс не нужен")
		},
	}

	if _, err := newTestService(repo, queue, DefaultOptions()).
		Retry(context.Background(), uuid.New(), meetingID); err != nil {
		t.Fatalf("Retry() вернул ошибку: %v", err)
	}

	if len(queue.enqueued) != 1 {
		t.Errorf("задача не поставлена в очередь: %v", queue.enqueued)
	}
}

func TestRetryHiddenForForeignMeeting(t *testing.T) {
	repo := &fakeRepo{
		getMeetingFunc: func(context.Context, uuid.UUID, uuid.UUID) (domain.Meeting, error) {
			return domain.Meeting{}, domain.ErrNotFound
		},
	}

	_, err := newTestService(repo, &fakeQueue{}, DefaultOptions()).
		Retry(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ожидалась ErrNotFound, получено: %v", err)
	}
}
