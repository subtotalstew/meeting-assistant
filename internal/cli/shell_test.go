package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/subtotalstew/meeting-assistant/internal/domain"
	"github.com/subtotalstew/meeting-assistant/internal/service"
)

// runShellWith прогоняет интерактивный режим на заранее заданном вводе.
func runShellWith(t *testing.T, svc Service, worker Worker, input string) (string, error) {
	t.Helper()
	return runShellWithPump(t, svc, worker, input, false)
}

// runShellWithPump прогоняет оболочку и, если нужно, имитирует старт пула,
// который настоящая оболочка выполняет при входе.
func runShellWithPump(t *testing.T, svc Service, worker Worker, input string, startWorker bool) (string, error) {
	t.Helper()

	var stdout bytes.Buffer

	app := newApp(svc, worker, &stdout, &bytes.Buffer{})
	app.userID = "alice"
	app.shell = true

	if startWorker && worker != nil {
		worker.Start(context.Background())
	}

	err := app.runShellFrom(context.Background(), "alice", strings.NewReader(input))
	return stdout.String(), err
}

// В оболочке команды идут без --user: он подставляется из сессии.
func TestShellRunsCommandsWithoutUserFlag(t *testing.T) {
	meeting := domain.Meeting{
		ID:       uuid.New(),
		FileName: "meeting.ogg",
		Status:   domain.StatusCreated,
	}

	svc := userStub(t)
	svc.loadMeetingFunc = func(context.Context, uuid.UUID, string) (domain.Meeting, error) {
		return meeting, nil
	}

	stdout, err := runShellWith(t, svc, &fakeWorker{}, "load meeting.ogg\nexit\n")
	if err != nil {
		t.Fatalf("оболочка вернула ошибку: %v", err)
	}
	if !strings.Contains(stdout, meeting.ID.String()) {
		t.Errorf("в выводе нет идентификатора встречи: %q", stdout)
	}
	if !strings.Contains(stdout, "alice> ") {
		t.Errorf("в выводе нет приглашения: %q", stdout)
	}
}

// Последняя загруженная встреча подставляется в status: идентификатор
// в сессии можно не писать.
func TestShellRemembersLastMeeting(t *testing.T) {
	meetingID := uuid.New()

	svc := userStub(t)
	svc.loadMeetingFunc = func(context.Context, uuid.UUID, string) (domain.Meeting, error) {
		return domain.Meeting{ID: meetingID, FileName: "plan.ogg", Status: domain.StatusCreated}, nil
	}

	var gotMeetingID uuid.UUID
	svc.statusFunc = func(_ context.Context, _ uuid.UUID, meetingID uuid.UUID) (service.MeetingStatus, error) {
		gotMeetingID = meetingID
		return service.MeetingStatus{
			Meeting: domain.Meeting{ID: meetingID, Status: domain.StatusCompleted},
			Task:    domain.Task{Status: domain.StatusCompleted},
		}, nil
	}

	if _, err := runShellWith(t, svc, &fakeWorker{}, "load plan.ogg\nstatus\nexit\n"); err != nil {
		t.Fatalf("оболочка вернула ошибку: %v", err)
	}
	if gotMeetingID != meetingID {
		t.Errorf("status запросил встречу %s, ожидалась последняя %s", gotMeetingID, meetingID)
	}
}

// Явно указанный идентификатор важнее последней встречи.
func TestShellExplicitMeetingIDWins(t *testing.T) {
	loaded := uuid.New()
	explicit := uuid.New()

	svc := userStub(t)
	svc.loadMeetingFunc = func(context.Context, uuid.UUID, string) (domain.Meeting, error) {
		return domain.Meeting{ID: loaded, FileName: "plan.ogg"}, nil
	}

	var got uuid.UUID
	svc.statusFunc = func(_ context.Context, _ uuid.UUID, meetingID uuid.UUID) (service.MeetingStatus, error) {
		got = meetingID
		return service.MeetingStatus{Meeting: domain.Meeting{ID: meetingID}}, nil
	}

	_, err := runShellWith(t, svc, &fakeWorker{}, "load plan.ogg\nstatus "+explicit.String()+"\nexit\n")
	if err != nil {
		t.Fatalf("оболочка вернула ошибку: %v", err)
	}
	if got != explicit {
		t.Errorf("status запросил %s, ожидался явно указанный %s", got, explicit)
	}
}

// Ошибка команды не завершает оболочку: следующая команда выполняется.
func TestShellContinuesAfterCommandError(t *testing.T) {
	svc := userStub(t)
	svc.statusFunc = func(context.Context, uuid.UUID, uuid.UUID) (service.MeetingStatus, error) {
		return service.MeetingStatus{}, errors.New("встреча не найдена")
	}

	stdout, err := runShellWith(t, svc, &fakeWorker{}, "status "+uuid.NewString()+"\nexit\n")
	if err != nil {
		t.Fatalf("оболочка не должна падать на ошибке команды: %v", err)
	}
	if !strings.Contains(stdout, "ошибка: встреча не найдена") {
		t.Errorf("ошибка команды не показана: %q", stdout)
	}
}

// Незакрытая кавычка — понятная ошибка, а не тихий обрыв ввода.
func TestShellReportsUnclosedQuote(t *testing.T) {
	stdout, err := runShellWith(t, userStub(t), &fakeWorker{}, "chat \"когда релиз\nexit\n")
	if err != nil {
		t.Fatalf("оболочка вернула ошибку: %v", err)
	}
	if !strings.Contains(stdout, "незакрытая кавычка") {
		t.Errorf("нет сообщения о кавычке: %q", stdout)
	}
}

func TestSplitArgs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{name: "пустая строка", in: "", want: nil},
		{name: "одно слово", in: "list", want: []string{"list"}},
		{name: "флаги", in: "load file.ogg --wait", want: []string{"load", "file.ogg", "--wait"}},
		{name: "кавычки", in: `chat "когда выйдет релиз"`, want: []string{"chat", "когда выйдет релиз"}},
		{name: "лишние пробелы", in: "  find   релиз  ", want: []string{"find", "релиз"}},
		{name: "пустые кавычки", in: `chat ""`, want: []string{"chat", ""}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := splitArgs(tc.in)
			if err != nil {
				t.Fatalf("splitArgs(%q): %v", tc.in, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("splitArgs(%q) = %q, ожидалось %q", tc.in, got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("splitArgs(%q) = %q, ожидалось %q", tc.in, got, tc.want)
				}
			}
		})
	}
}

// Вход в оболочку поднимает пул: задача в сессии обрабатывается без serve.
func TestShellStartsWorker(t *testing.T) {
	worker := &fakeWorker{}
	svc := userStub(t)

	app := newApp(svc, worker, &bytes.Buffer{}, &bytes.Buffer{})
	app.userID = "alice"
	app.shell = true

	if err := app.runShell(context.Background(), "alice"); err != nil {
		t.Fatalf("оболочка вернула ошибку: %v", err)
	}
	if !worker.started {
		t.Error("в оболочке пул воркеров должен быть запущен")
	}
}
