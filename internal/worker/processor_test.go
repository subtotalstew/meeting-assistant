package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/subtotalstew/meeting-assistant/internal/client"
	"github.com/subtotalstew/meeting-assistant/internal/client/llm"
	"github.com/subtotalstew/meeting-assistant/internal/client/speech"
	"github.com/subtotalstew/meeting-assistant/internal/domain"
)

// fakeStore фиксирует последовательность изменений состояния задачи.
type fakeStore struct {
	mu sync.Mutex

	meeting     domain.Meeting
	getErr      error
	startErr    error
	saveErr     error
	summaryErr  error
	completeErr error

	statuses      []domain.Status
	transcription domain.Transcription
	summary       domain.Summary
	failureReason string
	savedFailure  bool
	requeued      bool
}

func (f *fakeStore) record(status domain.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses = append(f.statuses, status)
}

func (f *fakeStore) GetMeetingByID(context.Context, uuid.UUID) (domain.Meeting, error) {
	if f.getErr != nil {
		return domain.Meeting{}, f.getErr
	}
	return f.meeting, nil
}

func (f *fakeStore) StartProcessing(context.Context, uuid.UUID) (domain.Task, error) {
	// Обработчик его больше не вызывает: интерфейс Store сузился. Метод оставлен,
	// чтобы тест на лишний вызов компилировался.
	if f.startErr != nil {
		return domain.Task{}, f.startErr
	}
	f.record(domain.StatusProcessing)
	return domain.Task{Status: domain.StatusProcessing, Attempts: 1}, nil
}

func (f *fakeStore) RequeueInterrupted(_ context.Context, _ uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requeued = true
	return nil
}

func (f *fakeStore) SaveTranscription(_ context.Context, transcription domain.Transcription) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.transcription = transcription
	f.statuses = append(f.statuses, domain.StatusTranscribed)
	return nil
}

func (f *fakeStore) SaveSummary(_ context.Context, summary domain.Summary) error {
	if f.summaryErr != nil {
		return f.summaryErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.summary = summary
	f.statuses = append(f.statuses, domain.StatusSummarized)
	return nil
}

func (f *fakeStore) CompleteProcessing(context.Context, uuid.UUID) error {
	if f.completeErr != nil {
		return f.completeErr
	}
	f.record(domain.StatusCompleted)
	return nil
}

func (f *fakeStore) MarkFailed(_ context.Context, _ uuid.UUID, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.savedFailure = true
	f.failureReason = reason
	f.statuses = append(f.statuses, domain.StatusFailed)
	return nil
}

func (f *fakeStore) statusSequence() []domain.Status {
	f.mu.Lock()
	defer f.mu.Unlock()

	result := make([]domain.Status, len(f.statuses))
	copy(result, f.statuses)
	return result
}

func newTestProcessor(store Store, speechClient speech.Client, llmClient llm.Client, timeout time.Duration) *Processor {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewProcessor(store, speechClient, llmClient, logger, ProcessorConfig{TaskTimeout: timeout})
}

func testMeeting() domain.Meeting {
	return domain.Meeting{
		ID:         uuid.New(),
		Title:      "Итоги спринта",
		FileName:   "sprint-review.ogg",
		SourcePath: `C:\audio\sprint-review.ogg`,
		Status:     domain.StatusCreated,
	}
}

func TestProcessFullCycle(t *testing.T) {
	meeting := testMeeting()
	store := &fakeStore{meeting: meeting}

	processor := newTestProcessor(store, speech.NewMock(), llm.NewMock(), time.Minute)

	if err := processor.Process(context.Background(), meeting.ID); err != nil {
		t.Fatalf("Process() вернул ошибку: %v", err)
	}

	want := []domain.Status{
		domain.StatusTranscribed,
		domain.StatusSummarized,
		domain.StatusCompleted,
	}

	got := store.statusSequence()
	if len(got) != len(want) {
		t.Fatalf("переходы статусов = %v, ожидалось %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("переходы статусов = %v, ожидалось %v", got, want)
		}
	}

	if store.transcription.Text == "" {
		t.Error("транскрипция не сохранена")
	}
	if store.summary.Text == "" {
		t.Error("выжимка не сохранена")
	}
	if store.savedFailure {
		t.Error("успешная обработка не должна сохранять ошибку")
	}
}

// Внешний сервис недоступен: задача должна уйти в failed с понятным текстом.
func TestProcessSpeechFailure(t *testing.T) {
	meeting := testMeeting()
	store := &fakeStore{meeting: meeting}

	speechClient := &speech.Mock{Err: errors.New("соединение сброшено")}
	processor := newTestProcessor(store, speechClient, llm.NewMock(), time.Minute)

	err := processor.Process(context.Background(), meeting.ID)
	if err == nil {
		t.Fatal("ожидалась ошибка обработки")
	}

	if !store.savedFailure {
		t.Fatal("ошибка не сохранена в задаче")
	}
	if store.failureReason != "внешний сервис недоступен" {
		t.Errorf("текст ошибки = %q, ожидался понятный пользователю", store.failureReason)
	}
	if store.transcription.Text != "" {
		t.Error("транскрипция не должна сохраняться при сбое распознавания")
	}
	if store.summary.Text != "" {
		t.Error("выжимка не должна сохраняться при сбое распознавания")
	}
}

func TestProcessLLMFailure(t *testing.T) {
	meeting := testMeeting()
	store := &fakeStore{meeting: meeting}

	llmClient := &llm.Mock{Err: errors.New("503")}
	processor := newTestProcessor(store, speech.NewMock(), llmClient, time.Minute)

	if err := processor.Process(context.Background(), meeting.ID); err == nil {
		t.Fatal("ожидалась ошибка обработки")
	}

	if store.transcription.Text == "" {
		t.Error("транскрипция должна сохраниться до сбоя выжимки")
	}
	if !store.savedFailure {
		t.Fatal("ошибка не сохранена в задаче")
	}
}

// Таймаут задачи: статус failed должен оказаться в базе, хотя контекст задачи мёртв.
func TestProcessTimeoutRecordsFailure(t *testing.T) {
	meeting := testMeeting()
	store := &fakeStore{meeting: meeting}

	slowSpeech := &speech.Mock{Delay: 2 * time.Second, Default: "текст"}
	processor := newTestProcessor(store, slowSpeech, llm.NewMock(), 30*time.Millisecond)

	start := time.Now()
	err := processor.Process(context.Background(), meeting.ID)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("ожидалась ошибка по таймауту")
	}
	if elapsed > time.Second {
		t.Errorf("обработка не прервалась по таймауту: %s", elapsed)
	}
	if !store.savedFailure {
		t.Fatal("по таймауту задача обязана перейти в failed")
	}
	if store.failureReason != "внешний сервис не ответил вовремя" {
		t.Errorf("текст ошибки = %q", store.failureReason)
	}
}

// Отмена контекста — это остановка приложения, а не сбой обработки: задачу
// нужно вернуть в очередь, чтобы следующий запуск её продолжил.
func TestProcessCancelledRequeuesTask(t *testing.T) {
	meeting := testMeeting()
	store := &fakeStore{meeting: meeting}

	processor := newTestProcessor(store, &speech.Mock{Delay: time.Second}, llm.NewMock(), time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	if err := processor.Process(ctx, meeting.ID); err == nil {
		t.Fatal("ожидалась ошибка отмены")
	}
	if !store.requeued {
		t.Fatal("после остановки задача должна вернуться в очередь")
	}
	if store.savedFailure {
		t.Error("остановка не должна помечать задачу failed")
	}
}

// Если встречу не удалось прочитать, брать задачу в работу не нужно.
func TestProcessUnknownMeeting(t *testing.T) {
	store := &fakeStore{getErr: domain.ErrNotFound}
	processor := newTestProcessor(store, speech.NewMock(), llm.NewMock(), time.Minute)

	if err := processor.Process(context.Background(), uuid.New()); err == nil {
		t.Fatal("ожидалась ошибка")
	}

	for _, status := range store.statusSequence() {
		if status == domain.StatusProcessing {
			t.Fatal("задачу нельзя переводить в processing для неизвестной встречи")
		}
	}
}

// Статус processing выставляет тот, кто забрал задачу из очереди, а не обработчик.
func TestProcessDoesNotStartTask(t *testing.T) {
	meeting := testMeeting()
	store := &fakeStore{meeting: meeting, startErr: errors.New("не должен вызываться")}

	processor := newTestProcessor(store, speech.NewMock(), llm.NewMock(), time.Minute)

	if err := processor.Process(context.Background(), meeting.ID); err != nil {
		t.Fatalf("Process() вернул ошибку: %v", err)
	}

	for _, status := range store.statusSequence() {
		if status == domain.StatusProcessing {
			t.Fatal("обработчик не должен сам переводить задачу в processing")
		}
	}
}

func TestHumanizeError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "таймаут",
			err:  client.New("mock", "transcribe", client.ErrTimeout, context.DeadlineExceeded),
			want: "внешний сервис не ответил вовремя",
		},
		{
			name: "недоступность",
			err:  client.New("mock", "summarize", client.ErrUnavailable, errors.New("503")),
			want: "внешний сервис недоступен",
		},
		{
			name: "не найдено",
			err:  domain.ErrNotFound,
			want: "встреча или её данные не найдены",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := humanizeError(tc.err); got != tc.want {
				t.Errorf("humanizeError() = %q, ожидалось %q", got, tc.want)
			}
		})
	}
}

func TestHumanizeErrorTruncatesLongMessage(t *testing.T) {
	long := errors.New(strings.Repeat("я", maxErrorRunes+100))

	got := humanizeError(long)
	if len([]rune(got)) > maxErrorRunes+1 {
		t.Errorf("длина текста ошибки = %d, предел %d", len([]rune(got)), maxErrorRunes)
	}
}

// Таймаут отдельного вызова клиента должен срабатывать раньше общего таймаута задачи
// и попадать в failed тем же понятным текстом.
func TestProcessSpeechCallTimeout(t *testing.T) {
	meeting := testMeeting()
	store := &fakeStore{meeting: meeting}

	slowSpeech := &speech.Mock{Delay: 2 * time.Second, Default: "текст"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	processor := NewProcessor(store, slowSpeech, llm.NewMock(), logger, ProcessorConfig{
		TaskTimeout:   time.Minute,
		SpeechTimeout: 30 * time.Millisecond,
	})

	start := time.Now()
	if err := processor.Process(context.Background(), meeting.ID); err == nil {
		t.Fatal("ожидалась ошибка по таймауту вызова")
	}

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("таймаут вызова не сработал: %s", elapsed)
	}
	if !store.savedFailure {
		t.Fatal("задача обязана перейти в failed")
	}
	if store.failureReason != "внешний сервис не ответил вовремя" {
		t.Errorf("текст ошибки = %q", store.failureReason)
	}
}
