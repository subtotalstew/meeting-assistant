package domain

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Ограничения на размеры пользовательских полей.
const (
	maxExternalIDLen = 128
	maxTitleLen      = 200
	maxFileNameLen   = 255
)

// User — пользователь системы. В CLI его идентифицирует external_id,
// в мессенджерах — идентификатор пользователя мессенджера.
type User struct {
	ID         uuid.UUID
	ExternalID string
	CreatedAt  time.Time
}

// Meeting — загруженная встреча вместе с текущим статусом обработки.
type Meeting struct {
	ID       uuid.UUID
	UserID   uuid.UUID
	Title    string
	FileName string
	// SourcePath — путь к исходному файлу: по нему фоновый обработчик читает
	// запись, а повторная попытка может взять тот же файл.
	SourcePath string
	Status     Status
	Error      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Task — задача обработки встречи. Отдельная сущность, потому что у неё
// собственная история: количество попыток, время старта и завершения.
type Task struct {
	ID         uuid.UUID
	MeetingID  uuid.UUID
	Status     Status
	Attempts   int
	Error      string
	StartedAt  *time.Time
	FinishedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Transcription — результат распознавания речи.
type Transcription struct {
	MeetingID uuid.UUID
	Text      string
	Language  string
	Provider  string
	CreatedAt time.Time
}

// Summary — краткая выжимка встречи.
type Summary struct {
	MeetingID uuid.UUID
	Text      string
	Provider  string
	CreatedAt time.Time
}

// Question — вопрос пользователя по материалам встречи и ответ LLM.
type Question struct {
	ID        uuid.UUID
	MeetingID uuid.UUID
	UserID    uuid.UUID
	Question  string
	Answer    string
	CreatedAt time.Time
}

// SearchHit — результат поиска по материалам пользователя.
type SearchHit struct {
	MeetingID uuid.UUID
	Title     string
	Status    Status
	CreatedAt time.Time
	Snippet   string
}

// MeetingItem — элемент списка встреч: сама встреча и фрагмент выжимки.
//
// Выжимка приходит тем же запросом, а не отдельным на каждую встречу: иначе
// список из 50 встреч превращается в 51 обращение к базе.
type MeetingItem struct {
	Meeting
	SummaryPreview string
}

// Stats — агрегаты по встречам пользователя.
//
// Считаются в базе, а не в приложении: иначе ради трёх чисел пришлось бы
// вытянуть все встречи пользователя.
type Stats struct {
	// Total — сколько всего встреч у пользователя.
	Total int
	// ByStatus — распределение встреч по статусам обработки.
	ByStatus map[Status]int
	// Questions — сколько вопросов задано по материалам встреч.
	Questions int
	// AvgProcessing — среднее время обработки завершённых задач.
	AvgProcessing time.Duration
}

// NewUser создаёт пользователя с новым идентификатором.
//
// Идентификатор — UUIDv7: в первых байтах время, поэтому вставки идут в конец
// B-дерева первичного ключа и индекс не фрагментируется.
func NewUser(externalID string) (User, error) {
	externalID = strings.TrimSpace(externalID)
	if externalID == "" {
		return User{}, fmt.Errorf("%w: пустой идентификатор пользователя", ErrValidation)
	}
	if utf8.RuneCountInString(externalID) > maxExternalIDLen {
		return User{}, fmt.Errorf("%w: идентификатор пользователя длиннее %d символов", ErrValidation, maxExternalIDLen)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return User{}, fmt.Errorf("генерация идентификатора пользователя: %w", err)
	}

	return User{
		ID:         id,
		ExternalID: externalID,
		CreatedAt:  time.Now().UTC(),
	}, nil
}

// NewMeeting создаёт встречу и связанную с ней задачу обработки.
//
// Обе сущности возвращаются вместе, потому что в базе они создаются одной
// транзакцией: встреча без задачи обработки — это потерянная запись,
// которую пользователь не сможет ни увидеть в работе, ни перезапустить.
//
// sourcePath — путь к исходному файлу: из него выводится имя файла и название,
// если название не задано явно.
func NewMeeting(userID uuid.UUID, title, sourcePath string) (Meeting, Task, error) {
	if userID == uuid.Nil {
		return Meeting{}, Task{}, fmt.Errorf("%w: не указан пользователь", ErrValidation)
	}

	sourcePath = strings.TrimSpace(sourcePath)
	fileName := filepath.Base(sourcePath)
	if sourcePath == "" || fileName == "" || fileName == "." {
		return Meeting{}, Task{}, fmt.Errorf("%w: не указан файл встречи", ErrValidation)
	}
	if utf8.RuneCountInString(fileName) > maxFileNameLen {
		return Meeting{}, Task{}, fmt.Errorf("%w: имя файла длиннее %d символов", ErrValidation, maxFileNameLen)
	}

	title = strings.TrimSpace(title)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(fileName), filepath.Ext(fileName))
	}
	if utf8.RuneCountInString(title) > maxTitleLen {
		title = string([]rune(title)[:maxTitleLen])
	}

	meetingID, err := uuid.NewV7()
	if err != nil {
		return Meeting{}, Task{}, fmt.Errorf("генерация идентификатора встречи: %w", err)
	}
	taskID, err := uuid.NewV7()
	if err != nil {
		return Meeting{}, Task{}, fmt.Errorf("генерация идентификатора задачи: %w", err)
	}

	now := time.Now().UTC()
	meeting := Meeting{
		ID:         meetingID,
		UserID:     userID,
		Title:      title,
		FileName:   fileName,
		SourcePath: sourcePath,
		Status:     StatusCreated,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	task := Task{
		ID:        taskID,
		MeetingID: meetingID,
		Status:    StatusCreated,
		CreatedAt: now,
		UpdatedAt: now,
	}

	return meeting, task, nil
}

// NewQuestion создаёт запись истории вопросов и ответов.
func NewQuestion(meetingID, userID uuid.UUID, question, answer string) (Question, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return Question{}, fmt.Errorf("%w: пустой вопрос", ErrValidation)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return Question{}, fmt.Errorf("генерация идентификатора вопроса: %w", err)
	}

	return Question{
		ID:        id,
		MeetingID: meetingID,
		UserID:    userID,
		Question:  question,
		Answer:    answer,
		CreatedAt: time.Now().UTC(),
	}, nil
}
