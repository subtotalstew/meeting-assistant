package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/subtotalstew/meeting-assistant/internal/config"
	"github.com/subtotalstew/meeting-assistant/internal/domain"
)

// testStore — хранилище, поднятое один раз на весь прогон интеграционных тестов.
var testStore *Store

// testSchema — отдельная схема на прогон. Тесты не трогают данные разработчика
// в public, поэтому их можно запускать на той же базе, что и приложение.
var testSchema string

// TestMain подготавливает схему и накатывает миграции.
// Без TEST_DATABASE_URL тесты не падают, а пропускаются: обычный `go test ./...`
// не должен требовать Docker.
func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		os.Exit(m.Run())
	}

	ctx := context.Background()

	admin, err := New(ctx, config.Database{DSN: dsn, MaxConns: 2, ConnTimeout: 10 * time.Second})
	if err != nil {
		fmt.Fprintf(os.Stderr, "подключение к тестовой базе: %v\n", err)
		os.Exit(1)
	}

	testSchema = fmt.Sprintf("test_%d", time.Now().UnixNano())
	createSchema := fmt.Sprintf("CREATE SCHEMA %s", pgx.Identifier{testSchema}.Sanitize())
	if _, err := admin.Pool().Exec(ctx, createSchema); err != nil {
		fmt.Fprintf(os.Stderr, "создание схемы %s: %v\n", testSchema, err)
		admin.Close()
		os.Exit(1)
	}
	admin.Close()

	testDSN, err := withSearchPath(dsn, testSchema)
	if err != nil {
		fmt.Fprintf(os.Stderr, "подготовка DSN: %v\n", err)
		os.Exit(1)
	}

	store, err := New(ctx, config.Database{DSN: testDSN, MaxConns: 5, ConnTimeout: 10 * time.Second})
	if err != nil {
		fmt.Fprintf(os.Stderr, "подключение к схеме %s: %v\n", testSchema, err)
		os.Exit(1)
	}

	if err := Migrate(ctx, store.Pool()); err != nil {
		fmt.Fprintf(os.Stderr, "миграции тестовой схемы: %v\n", err)
		store.Close()
		os.Exit(1)
	}

	testStore = store
	code := m.Run()

	dropSchema := fmt.Sprintf("DROP SCHEMA %s CASCADE", pgx.Identifier{testSchema}.Sanitize())
	if _, err := store.Pool().Exec(context.Background(), dropSchema); err != nil {
		fmt.Fprintf(os.Stderr, "удаление схемы %s: %v\n", testSchema, err)
	}

	store.Close()
	os.Exit(code)
}

// withSearchPath добавляет в DSN параметр search_path.
func withSearchPath(dsn, schema string) (string, error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("разбор TEST_DATABASE_URL: %w", err)
	}

	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()

	return parsed.String(), nil
}

// storeForTest возвращает хранилище с пустыми таблицами.
func storeForTest(t *testing.T) *Store {
	t.Helper()

	if testStore == nil {
		t.Skip("TEST_DATABASE_URL не задан — интеграционные тесты пропущены")
	}

	if _, err := testStore.Pool().Exec(context.Background(),
		`TRUNCATE users, meetings, processing_tasks, transcriptions, summaries, qa_history CASCADE`,
	); err != nil {
		t.Fatalf("очистка таблиц: %v", err)
	}

	return testStore
}

func createUser(t *testing.T, store *Store, externalID string) domain.User {
	t.Helper()

	user, err := domain.NewUser(externalID)
	if err != nil {
		t.Fatalf("domain.NewUser: %v", err)
	}

	stored, err := store.UpsertUser(context.Background(), user)
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	return stored
}

func createMeeting(t *testing.T, store *Store, user domain.User, fileName string) (domain.Meeting, domain.Task) {
	t.Helper()

	meeting, task, err := domain.NewMeeting(user.ID, "", fileName)
	if err != nil {
		t.Fatalf("domain.NewMeeting: %v", err)
	}

	if err := store.CreateMeeting(context.Background(), meeting, task); err != nil {
		t.Fatalf("CreateMeeting: %v", err)
	}

	return meeting, task
}

func countRows(t *testing.T, store *Store, table string) int {
	t.Helper()

	var count int
	if err := store.Pool().QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("count(%s): %v", table, err)
	}

	return count
}

func TestIntegrationUserUpsertIsIdempotent(t *testing.T) {
	store := storeForTest(t)
	ctx := context.Background()

	first := createUser(t, store, "alice")

	// Повторное обращение с тем же external_id не должно создавать дубликат
	// и не должно менять идентификатор пользователя.
	candidate, err := domain.NewUser("alice")
	if err != nil {
		t.Fatalf("domain.NewUser: %v", err)
	}

	second, err := store.UpsertUser(ctx, candidate)
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	if second.ID != first.ID {
		t.Fatalf("идентификатор изменился: %s != %s", second.ID, first.ID)
	}
	if got := countRows(t, store, "users"); got != 1 {
		t.Fatalf("в таблице users %d строк, ожидалась 1", got)
	}
}

func TestIntegrationCreateMeetingWithTask(t *testing.T) {
	store := storeForTest(t)
	ctx := context.Background()

	user := createUser(t, store, "bob")
	meeting, task := createMeeting(t, store, user, "sprint-review.ogg")

	stored, err := store.GetMeeting(ctx, meeting.ID, user.ID)
	if err != nil {
		t.Fatalf("GetMeeting: %v", err)
	}

	if stored.Status != domain.StatusCreated {
		t.Errorf("статус встречи = %q, ожидался created", stored.Status)
	}
	if stored.Title != "sprint-review" {
		t.Errorf("название = %q, ожидалось %q", stored.Title, "sprint-review")
	}
	// Путь к исходному файлу нужен фоновому обработчику, чтобы прочитать запись.
	if stored.SourcePath != meeting.SourcePath {
		t.Errorf("SourcePath = %q, ожидался %q", stored.SourcePath, meeting.SourcePath)
	}
	if stored.FileName != "sprint-review.ogg" {
		t.Errorf("FileName = %q, ожидалось %q", stored.FileName, "sprint-review.ogg")
	}

	storedTask, err := store.GetTask(ctx, meeting.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	if storedTask.ID != task.ID {
		t.Errorf("идентификатор задачи = %s, ожидался %s", storedTask.ID, task.ID)
	}
	if storedTask.Status != domain.StatusCreated {
		t.Errorf("статус задачи = %q, ожидался created", storedTask.Status)
	}
	if storedTask.Attempts != 0 {
		t.Errorf("попыток = %d, ожидалось 0", storedTask.Attempts)
	}
}

// Если задача не проходит ограничение схемы, встреча тоже не должна сохраниться.
func TestIntegrationCreateMeetingRollsBackOnInvalidTask(t *testing.T) {
	store := storeForTest(t)
	ctx := context.Background()

	user := createUser(t, store, "carol")
	meeting, task, err := domain.NewMeeting(user.ID, "", "broken.ogg")
	if err != nil {
		t.Fatalf("domain.NewMeeting: %v", err)
	}

	task.Status = domain.Status("bogus")

	if err := store.CreateMeeting(ctx, meeting, task); err == nil {
		t.Fatal("ожидалась ошибка ограничения схемы")
	}

	if got := countRows(t, store, "meetings"); got != 0 {
		t.Fatalf("после отката в meetings %d строк, ожидалось 0", got)
	}
	if got := countRows(t, store, "processing_tasks"); got != 0 {
		t.Fatalf("после отката в processing_tasks %d строк, ожидалось 0", got)
	}
}

func TestIntegrationMeetingIsolatedByUser(t *testing.T) {
	store := storeForTest(t)
	ctx := context.Background()

	alice := createUser(t, store, "alice")
	bob := createUser(t, store, "bob")

	meeting, _ := createMeeting(t, store, alice, "alice-meeting.ogg")

	if _, err := store.GetMeeting(ctx, meeting.ID, bob.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("чужую встречу можно получить: %v", err)
	}

	bobMeetings, err := store.ListMeetings(ctx, bob.ID, 10)
	if err != nil {
		t.Fatalf("ListMeetings: %v", err)
	}
	if len(bobMeetings) != 0 {
		t.Fatalf("у bob %d встреч, ожидалось 0", len(bobMeetings))
	}

	if err := store.DeleteMeeting(ctx, meeting.ID, bob.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("чужую встречу можно удалить: %v", err)
	}
}

func TestIntegrationProcessingLifecycle(t *testing.T) {
	store := storeForTest(t)
	ctx := context.Background()

	user := createUser(t, store, "dave")
	meeting, _ := createMeeting(t, store, user, "planning.ogg")

	started, err := store.StartProcessing(ctx, meeting.ID)
	if err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}
	if started.Status != domain.StatusProcessing {
		t.Errorf("статус задачи = %q, ожидался processing", started.Status)
	}
	if started.Attempts != 1 {
		t.Errorf("попыток = %d, ожидалось 1", started.Attempts)
	}
	if started.StartedAt == nil {
		t.Error("время старта не заполнено")
	}

	// Повторный запуск той же задачи не должен её перехватывать.
	if _, err := store.StartProcessing(ctx, meeting.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("задачу можно взять в работу дважды: %v", err)
	}

	transcription := domain.Transcription{
		MeetingID: meeting.ID,
		Text:      "Обсудили сроки и договорились о релизе в пятницу.",
		Language:  "ru-RU",
		Provider:  "mock",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.SaveTranscription(ctx, transcription); err != nil {
		t.Fatalf("SaveTranscription: %v", err)
	}

	afterTranscription, err := store.GetTask(ctx, meeting.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if afterTranscription.Status != domain.StatusTranscribed {
		t.Fatalf("статус = %q, ожидался transcribed", afterTranscription.Status)
	}

	summary := domain.Summary{
		MeetingID: meeting.ID,
		Text:      "Договорились о релизе в пятницу.",
		Provider:  "mock",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.SaveSummary(ctx, summary); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}

	if err := store.CompleteProcessing(ctx, meeting.ID); err != nil {
		t.Fatalf("CompleteProcessing: %v", err)
	}

	finalMeeting, err := store.GetMeeting(ctx, meeting.ID, user.ID)
	if err != nil {
		t.Fatalf("GetMeeting: %v", err)
	}
	if finalMeeting.Status != domain.StatusCompleted {
		t.Fatalf("статус встречи = %q, ожидался completed", finalMeeting.Status)
	}

	finalTask, err := store.GetTask(ctx, meeting.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if finalTask.FinishedAt == nil {
		t.Error("время завершения не заполнено")
	}

	storedTranscription, err := store.GetTranscription(ctx, meeting.ID, user.ID)
	if err != nil {
		t.Fatalf("GetTranscription: %v", err)
	}
	if storedTranscription.Text != transcription.Text {
		t.Errorf("транскрипция не совпала: %q", storedTranscription.Text)
	}

	storedSummary, err := store.GetSummary(ctx, meeting.ID, user.ID)
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}
	if storedSummary.Text != summary.Text {
		t.Errorf("выжимка не совпала: %q", storedSummary.Text)
	}
}

func TestIntegrationProcessingFailure(t *testing.T) {
	store := storeForTest(t)
	ctx := context.Background()

	user := createUser(t, store, "erin")
	meeting, _ := createMeeting(t, store, user, "failed.ogg")

	if _, err := store.StartProcessing(ctx, meeting.ID); err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}

	reason := "speech-клиент недоступен: превышен таймаут"
	if err := store.MarkFailed(ctx, meeting.ID, reason); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	task, err := store.GetTask(ctx, meeting.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != domain.StatusFailed {
		t.Errorf("статус задачи = %q, ожидался failed", task.Status)
	}
	if task.Error != reason {
		t.Errorf("текст ошибки = %q, ожидался %q", task.Error, reason)
	}
	if task.FinishedAt == nil {
		t.Error("время завершения не заполнено")
	}

	failed, err := store.GetMeeting(ctx, meeting.ID, user.ID)
	if err != nil {
		t.Fatalf("GetMeeting: %v", err)
	}
	if failed.Status != domain.StatusFailed {
		t.Errorf("статус встречи = %q, ожидался failed", failed.Status)
	}
	if failed.Error != reason {
		t.Errorf("ошибка встречи = %q, ожидалась %q", failed.Error, reason)
	}
}

func TestIntegrationSearchRespectsOwnership(t *testing.T) {
	store := storeForTest(t)
	ctx := context.Background()

	alice := createUser(t, store, "alice")
	bob := createUser(t, store, "bob")

	aliceMeeting, _ := createMeeting(t, store, alice, "alice.ogg")
	bobMeeting, _ := createMeeting(t, store, bob, "bob.ogg")

	aliceText := "Договорились перенести релиз на следующую неделю."
	bobText := "Обсудили бюджет на следующий квартал."

	if err := store.SaveTranscription(ctx, domain.Transcription{
		MeetingID: aliceMeeting.ID, Text: aliceText, Provider: "mock", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveTranscription(alice): %v", err)
	}
	if err := store.SaveTranscription(ctx, domain.Transcription{
		MeetingID: bobMeeting.ID, Text: bobText, Provider: "mock", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveTranscription(bob): %v", err)
	}

	aliceHits, err := store.SearchMeetings(ctx, alice.ID, "релиз", 10)
	if err != nil {
		t.Fatalf("SearchMeetings(alice): %v", err)
	}
	if len(aliceHits) != 1 {
		t.Fatalf("alice нашла %d встреч, ожидалась 1", len(aliceHits))
	}
	if aliceHits[0].MeetingID != aliceMeeting.ID {
		t.Error("alice получила чужую встречу")
	}

	bobHits, err := store.SearchMeetings(ctx, bob.ID, "релиз", 10)
	if err != nil {
		t.Fatalf("SearchMeetings(bob): %v", err)
	}
	if len(bobHits) != 0 {
		t.Fatalf("bob нашёл %d встреч по чужому слову, ожидалось 0", len(bobHits))
	}

	// Символы шаблона ILIKE должны экранироваться, иначе "%" найдёт всё.
	wildcardHits, err := store.SearchMeetings(ctx, alice.ID, "%", 10)
	if err != nil {
		t.Fatalf("SearchMeetings(%%): %v", err)
	}
	if len(wildcardHits) != 0 {
		t.Fatalf("запрос \"%%\" вернул %d встреч, ожидалось 0", len(wildcardHits))
	}
}

func TestIntegrationDeleteCascades(t *testing.T) {
	store := storeForTest(t)
	ctx := context.Background()

	user := createUser(t, store, "frank")
	meeting, _ := createMeeting(t, store, user, "to-delete.ogg")

	if _, err := store.StartProcessing(ctx, meeting.ID); err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}
	if err := store.SaveTranscription(ctx, domain.Transcription{
		MeetingID: meeting.ID, Text: "текст", Provider: "mock", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveTranscription: %v", err)
	}

	question, err := domain.NewQuestion(meeting.ID, user.ID, "О чём договорились?", "О релизе.")
	if err != nil {
		t.Fatalf("domain.NewQuestion: %v", err)
	}
	if err := store.SaveQuestion(ctx, question); err != nil {
		t.Fatalf("SaveQuestion: %v", err)
	}

	if err := store.DeleteMeeting(ctx, meeting.ID, user.ID); err != nil {
		t.Fatalf("DeleteMeeting: %v", err)
	}

	for _, table := range []string{"meetings", "processing_tasks", "transcriptions", "qa_history"} {
		if got := countRows(t, store, table); got != 0 {
			t.Errorf("после удаления в %s осталось %d строк", table, got)
		}
	}

	if _, err := store.GetMeeting(ctx, meeting.ID, user.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("удалённая встреча всё ещё доступна: %v", err)
	}
}

// Проверяем, что ответ LLM сохраняется вместе с вопросом и читается обратно.
func TestIntegrationQuestionsHistory(t *testing.T) {
	store := storeForTest(t)
	ctx := context.Background()

	user := createUser(t, store, "grace")
	meeting, _ := createMeeting(t, store, user, "questions.ogg")

	question, err := domain.NewQuestion(meeting.ID, user.ID, "Какие сроки?", "Релиз в пятницу.")
	if err != nil {
		t.Fatalf("domain.NewQuestion: %v", err)
	}
	if err := store.SaveQuestion(ctx, question); err != nil {
		t.Fatalf("SaveQuestion: %v", err)
	}

	stored, err := store.ListQuestions(ctx, meeting.ID, user.ID, 10)
	if err != nil {
		t.Fatalf("ListQuestions: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("вопросов %d, ожидался 1", len(stored))
	}
	if stored[0].Answer != "Релиз в пятницу." {
		t.Errorf("ответ = %q", stored[0].Answer)
	}

	other := createUser(t, store, "heidi")
	otherQuestions, err := store.ListQuestions(ctx, meeting.ID, other.ID, 10)
	if err != nil {
		t.Fatalf("ListQuestions(other): %v", err)
	}
	if len(otherQuestions) != 0 {
		t.Fatalf("чужой пользователь видит %d вопросов", len(otherQuestions))
	}
}

// Восстановление после перезапуска: задачи, прерванные остановкой приложения,
// должны вернуться в очередь, иначе встреча навсегда останется в processing.
func TestIntegrationRecoverPending(t *testing.T) {
	store := storeForTest(t)
	ctx := context.Background()

	user := createUser(t, store, "ivan")

	interrupted, _ := createMeeting(t, store, user, "interrupted.ogg")
	if _, err := store.StartProcessing(ctx, interrupted.ID); err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}

	waiting, _ := createMeeting(t, store, user, "waiting.ogg")

	pending, err := store.RecoverPending(ctx, 10)
	if err != nil {
		t.Fatalf("RecoverPending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("восстановлено %d задач, ожидалось 2", len(pending))
	}

	for _, meetingID := range []uuid.UUID{interrupted.ID, waiting.ID} {
		task, err := store.GetTask(ctx, meetingID)
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		if task.Status != domain.StatusCreated {
			t.Errorf("статус задачи = %q, ожидался created", task.Status)
		}
		if task.StartedAt != nil {
			t.Errorf("время старта не сброшено для %s", meetingID)
		}

		meeting, err := store.GetMeeting(ctx, meetingID, user.ID)
		if err != nil {
			t.Fatalf("GetMeeting: %v", err)
		}
		if meeting.Status != domain.StatusCreated {
			t.Errorf("статус встречи = %q, ожидался created", meeting.Status)
		}
	}

	// Повторный вызов безопасен: задачи всё ещё created, их снова поставят в очередь.
	again, err := store.RecoverPending(ctx, 10)
	if err != nil {
		t.Fatalf("повторный RecoverPending: %v", err)
	}
	if len(again) != 2 {
		t.Fatalf("повторное восстановление вернуло %d задач, ожидалось 2", len(again))
	}
}

// Очередь в PostgreSQL: два «процесса» делят задачи между собой, каждая
// забирается ровно один раз, а зависшая в processing задача возвращается по lease.
func TestIntegrationClaimTasks(t *testing.T) {
	store := storeForTest(t)
	ctx := context.Background()

	user := createUser(t, store, "queue-user")

	const tasks = 20
	created := make(map[uuid.UUID]bool, tasks)
	for i := 0; i < tasks; i++ {
		meeting, _ := createMeeting(t, store, user, "meeting.ogg")
		created[meeting.ID] = true
	}

	// Два забора по 5 — как два диспетчера в разных процессах.
	first, err := store.ClaimTasks(ctx, 5)
	if err != nil {
		t.Fatalf("ClaimTasks #1: %v", err)
	}
	second, err := store.ClaimTasks(ctx, 5)
	if err != nil {
		t.Fatalf("ClaimTasks #2: %v", err)
	}

	if len(first) != 5 || len(second) != 5 {
		t.Fatalf("захвачено %d и %d задач, ожидалось по 5", len(first), len(second))
	}

	seen := make(map[uuid.UUID]bool)
	for _, id := range append(append([]uuid.UUID{}, first...), second...) {
		if seen[id] {
			t.Fatalf("встреча %s захвачена дважды", id)
		}
		seen[id] = true

		meeting, err := store.GetMeeting(ctx, id, user.ID)
		if err != nil {
			t.Fatalf("GetMeeting: %v", err)
		}
		if meeting.Status != domain.StatusProcessing {
			t.Errorf("статус встречи = %q, ожидался processing", meeting.Status)
		}

		task, err := store.GetTask(ctx, id)
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		if task.Attempts != 1 {
			t.Errorf("попыток = %d, ожидалась 1", task.Attempts)
		}
	}

	// Освобождённые задачи уже не захватываются повторно.
	again, err := store.ClaimTasks(ctx, 100)
	if err != nil {
		t.Fatalf("ClaimTasks #3: %v", err)
	}
	if len(again) != tasks-10 {
		t.Fatalf("повторно захвачено %d задач, ожидалось %d", len(again), tasks-10)
	}
	for _, id := range again {
		if seen[id] {
			t.Fatalf("захвачена уже обработанная встреча %s", id)
		}
	}

	// Протухшая аренда: все processing старше нуля секунд возвращаются в created.
	reclaimed, err := store.ReclaimStale(ctx, time.Nanosecond)
	if err != nil {
		t.Fatalf("ReclaimStale: %v", err)
	}
	if len(reclaimed) != tasks {
		t.Fatalf("возвращено %d задач, ожидалось %d", len(reclaimed), tasks)
	}
}

// Восстановление: задача, прерванная остановкой, возвращается в очередь
// и захватывается повторно, увеличивая счётчик попыток.
func TestIntegrationRequeueAndReclaim(t *testing.T) {
	store := storeForTest(t)
	ctx := context.Background()

	user := createUser(t, store, "reclaim-user")
	meeting, _ := createMeeting(t, store, user, "interrupted.ogg")

	claimed, err := store.ClaimTasks(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimTasks: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("захвачено %d задач, ожидалась 1", len(claimed))
	}

	// Остановка приложения посреди обработки.
	if err := store.RequeueInterrupted(ctx, meeting.ID); err != nil {
		t.Fatalf("RequeueInterrupted: %v", err)
	}

	task, err := store.GetTask(ctx, meeting.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != domain.StatusCreated {
		t.Fatalf("статус задачи = %q, ожидался created", task.Status)
	}
	if task.Attempts != 1 {
		t.Errorf("попыток = %d, ожидалась 1: попытка уже была начата", task.Attempts)
	}

	// Повторный возврат для задачи, которая уже не processing, ничего не портит.
	if err := store.RequeueInterrupted(ctx, meeting.ID); err != nil {
		t.Fatalf("повторный RequeueInterrupted: %v", err)
	}

	// Задача снова в очереди и захватывается как обычная.
	again, err := store.ClaimTasks(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimTasks #2: %v", err)
	}
	if len(again) != 1 || again[0] != meeting.ID {
		t.Fatalf("повторно захвачено %v, ожидалось [%s]", again, meeting.ID)
	}

	task, err = store.GetTask(ctx, meeting.ID)
	if err != nil {
		t.Fatalf("GetTask #2: %v", err)
	}
	if task.Attempts != 2 {
		t.Errorf("попыток = %d, ожидалось 2", task.Attempts)
	}
}

// Восстановление после жёсткой остановки: задача, зависшая в processing,
// возвращается арендой и обрабатывается заново с увеличением попыток.
func TestIntegrationReclaimStaleRetriesInterrupted(t *testing.T) {
	store := storeForTest(t)
	ctx := context.Background()

	user := createUser(t, store, "stale-user")
	meeting, _ := createMeeting(t, store, user, "killed.ogg")

	if _, err := store.ClaimTasks(ctx, 10); err != nil {
		t.Fatalf("ClaimTasks: %v", err)
	}

	// Процесс убили посреди обработки: строка осталась в processing.
	reclaimed, err := store.ReclaimStale(ctx, time.Nanosecond)
	if err != nil {
		t.Fatalf("ReclaimStale: %v", err)
	}
	if len(reclaimed) != 1 || reclaimed[0] != meeting.ID {
		t.Fatalf("возвращено %v, ожидалось [%s]", reclaimed, meeting.ID)
	}

	// Мгновенная аренда не трогает живые задачи.
	if again, err := store.ReclaimStale(ctx, time.Hour); err != nil || len(again) != 0 {
		t.Fatalf("ReclaimStale(hour) = %v, %v; ожидалось пусто", again, err)
	}

	task, err := store.GetTask(ctx, meeting.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != domain.StatusCreated {
		t.Fatalf("статус задачи = %q, ожидался created", task.Status)
	}
	if task.Attempts != 1 {
		t.Errorf("попыток = %d, ожидалась 1", task.Attempts)
	}

	if _, err := store.ClaimTasks(ctx, 10); err != nil {
		t.Fatalf("ClaimTasks #2: %v", err)
	}
	task, err = store.GetTask(ctx, meeting.ID)
	if err != nil {
		t.Fatalf("GetTask #2: %v", err)
	}
	if task.Attempts != 2 {
		t.Errorf("попыток = %d, ожидалось 2", task.Attempts)
	}
}

func TestIntegrationSearchFullTextAndSubstring(t *testing.T) {
	store := storeForTest(t)
	ctx := context.Background()

	user := createUser(t, store, "olga")
	meeting, _ := createMeeting(t, store, user, "planning.ogg")

	if err := store.SaveTranscription(ctx, domain.Transcription{
		MeetingID: meeting.ID,
		Text:      "Договорились перенести релиз на следующую неделю. Бюджет обсудим отдельно.",
		Provider:  "mock",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveTranscription: %v", err)
	}

	// Полное слово: работает полнотекстовый поиск, найденное подсвечено.
	hits, err := store.SearchMeetings(ctx, user.ID, "релиз", 10)
	if err != nil {
		t.Fatalf("SearchMeetings(релиз): %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("найдено %d встреч, ожидалась 1", len(hits))
	}
	if !strings.Contains(hits[0].Snippet, "<<релиз>>") {
		t.Errorf("нет подсветки найденного слова: %q", hits[0].Snippet)
	}

	// Неполное слово: полнотекстовый поиск не находит лексему, включается подстрока.
	hits, err = store.SearchMeetings(ctx, user.ID, "рели", 10)
	if err != nil {
		t.Fatalf("SearchMeetings(рели): %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("по подстроке найдено %d встреч, ожидалась 1", len(hits))
	}

	// Ничего не найдено ни одним способом.
	hits, err = store.SearchMeetings(ctx, user.ID, "ипотека", 10)
	if err != nil {
		t.Fatalf("SearchMeetings(ипотека): %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("найдено %d встреч по отсутствующему слову", len(hits))
	}
}

// Агрегаты считаются в базе и не смешиваются между пользователями.
func TestIntegrationStats(t *testing.T) {
	store := storeForTest(t)
	ctx := context.Background()

	user := createUser(t, store, "petr")
	other := createUser(t, store, "guest")

	var completedID uuid.UUID
	for i, name := range []string{"first.ogg", "second.ogg"} {
		meeting, _ := createMeeting(t, store, user, name)
		if i == 0 {
			completedID = meeting.ID
		}

		if _, err := store.StartProcessing(ctx, meeting.ID); err != nil {
			t.Fatalf("StartProcessing: %v", err)
		}
		if err := store.SaveTranscription(ctx, domain.Transcription{
			MeetingID: meeting.ID, Text: "текст встречи", Provider: "mock", CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("SaveTranscription: %v", err)
		}
		if err := store.SaveSummary(ctx, domain.Summary{
			MeetingID: meeting.ID, Text: "выжимка", Provider: "mock", CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("SaveSummary: %v", err)
		}
		if err := store.CompleteProcessing(ctx, meeting.ID); err != nil {
			t.Fatalf("CompleteProcessing: %v", err)
		}
	}

	failed, _ := createMeeting(t, store, user, "broken.ogg")
	if _, err := store.StartProcessing(ctx, failed.ID); err != nil {
		t.Fatalf("StartProcessing(failed): %v", err)
	}
	if err := store.MarkFailed(ctx, failed.ID, "внешний сервис недоступен"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	// Встреча другого пользователя не должна попасть в статистику.
	if _, foreign := createMeeting(t, store, other, "foreign.ogg"); foreign.ID == uuid.Nil {
		t.Fatal("встреча другого пользователя не создана")
	}

	question, err := domain.NewQuestion(completedID, user.ID, "О чём договорились?", "О релизе.")
	if err != nil {
		t.Fatalf("domain.NewQuestion: %v", err)
	}
	if err := store.SaveQuestion(ctx, question); err != nil {
		t.Fatalf("SaveQuestion: %v", err)
	}

	stats, err := store.Stats(ctx, user.ID)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}

	if stats.Total != 3 {
		t.Errorf("всего встреч = %d, ожидалось 3", stats.Total)
	}
	if stats.ByStatus[domain.StatusCompleted] != 2 {
		t.Errorf("completed = %d, ожидалось 2", stats.ByStatus[domain.StatusCompleted])
	}
	if stats.ByStatus[domain.StatusFailed] != 1 {
		t.Errorf("failed = %d, ожидалось 1", stats.ByStatus[domain.StatusFailed])
	}
	if stats.Questions != 1 {
		t.Errorf("вопросов = %d, ожидался 1", stats.Questions)
	}
	if stats.AvgProcessing < 0 {
		t.Errorf("среднее время обработки отрицательное: %s", stats.AvgProcessing)
	}
}
