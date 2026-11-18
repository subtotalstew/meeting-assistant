package domain

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNewUser(t *testing.T) {
	t.Run("создаёт пользователя", func(t *testing.T) {
		user, err := NewUser("  alice  ")
		if err != nil {
			t.Fatalf("NewUser() вернул ошибку: %v", err)
		}
		if user.ExternalID != "alice" {
			t.Errorf("ExternalID = %q, ожидалось %q", user.ExternalID, "alice")
		}
		if user.ID == uuid.Nil {
			t.Error("идентификатор не сгенерирован")
		}
		if user.CreatedAt.IsZero() {
			t.Error("время создания не заполнено")
		}
	})

	t.Run("отклоняет пустой идентификатор", func(t *testing.T) {
		if _, err := NewUser("   "); !errors.Is(err, ErrValidation) {
			t.Fatalf("ожидалась ErrValidation, получено: %v", err)
		}
	})

	t.Run("отклоняет слишком длинный идентификатор", func(t *testing.T) {
		if _, err := NewUser(strings.Repeat("a", maxExternalIDLen+1)); !errors.Is(err, ErrValidation) {
			t.Fatalf("ожидалась ErrValidation, получено: %v", err)
		}
	})
}

func TestNewMeeting(t *testing.T) {
	userID := uuid.New()

	t.Run("выводит название из имени файла", func(t *testing.T) {
		meeting, task, err := NewMeeting(userID, "", "sprint-review.ogg")
		if err != nil {
			t.Fatalf("NewMeeting() вернул ошибку: %v", err)
		}
		if meeting.Title != "sprint-review" {
			t.Errorf("Title = %q, ожидалось %q", meeting.Title, "sprint-review")
		}
		if meeting.Status != StatusCreated {
			t.Errorf("Status = %q, ожидался created", meeting.Status)
		}
		if task.MeetingID != meeting.ID {
			t.Error("задача не связана со встречей")
		}
		if task.Status != StatusCreated {
			t.Errorf("статус задачи = %q, ожидался created", task.Status)
		}
		if task.Attempts != 0 {
			t.Errorf("Attempts = %d, ожидалось 0", task.Attempts)
		}
	})

	t.Run("сохраняет явное название", func(t *testing.T) {
		meeting, _, err := NewMeeting(userID, "Планирование спринта", "audio.ogg")
		if err != nil {
			t.Fatalf("NewMeeting() вернул ошибку: %v", err)
		}
		if meeting.Title != "Планирование спринта" {
			t.Errorf("Title = %q", meeting.Title)
		}
	})

	t.Run("усекает длинное название", func(t *testing.T) {
		long := strings.Repeat("я", maxTitleLen+50)
		meeting, _, err := NewMeeting(userID, long, "audio.ogg")
		if err != nil {
			t.Fatalf("NewMeeting() вернул ошибку: %v", err)
		}
		if len([]rune(meeting.Title)) != maxTitleLen {
			t.Errorf("длина названия = %d, ожидалось %d", len([]rune(meeting.Title)), maxTitleLen)
		}
	})

	t.Run("отклоняет встречу без пользователя", func(t *testing.T) {
		if _, _, err := NewMeeting(uuid.Nil, "t", "a.ogg"); !errors.Is(err, ErrValidation) {
			t.Fatalf("ожидалась ErrValidation, получено: %v", err)
		}
	})

	t.Run("отклоняет встречу без файла", func(t *testing.T) {
		if _, _, err := NewMeeting(userID, "t", "   "); !errors.Is(err, ErrValidation) {
			t.Fatalf("ожидалась ErrValidation, получено: %v", err)
		}
	})
}

func TestNewQuestion(t *testing.T) {
	if _, err := NewQuestion(uuid.New(), uuid.New(), "  ", "ответ"); !errors.Is(err, ErrValidation) {
		t.Fatalf("ожидалась ErrValidation, получено: %v", err)
	}

	question, err := NewQuestion(uuid.New(), uuid.New(), "О чём договорились?", "О сроках.")
	if err != nil {
		t.Fatalf("NewQuestion() вернул ошибку: %v", err)
	}
	if question.Answer != "О сроках." {
		t.Errorf("Answer = %q", question.Answer)
	}
}
