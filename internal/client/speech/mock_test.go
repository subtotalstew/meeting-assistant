package speech

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/subtotalstew/meeting-assistant/internal/client"
)

func TestMockTranscribeReturnsDefaultTranscript(t *testing.T) {
	mock := NewMock()

	result, err := mock.Transcribe(context.Background(), Request{Path: "testdata/meeting.ogg", FileName: "meeting.ogg"})
	if err != nil {
		t.Fatalf("Transcribe() вернул ошибку: %v", err)
	}

	if result.Text != DefaultTranscript {
		t.Error("вернулся не текст по умолчанию")
	}
	if result.Provider != ProviderMock {
		t.Errorf("Provider = %q, ожидался %q", result.Provider, ProviderMock)
	}
	if result.Language != "ru-RU" {
		t.Errorf("Language = %q, ожидался ru-RU", result.Language)
	}
}

func TestMockTranscribeUsesPreparedFile(t *testing.T) {
	dir := t.TempDir()
	prepared := "Подготовленная транскрипция для этой встречи."
	if err := os.WriteFile(filepath.Join(dir, "weekly.txt"), []byte(prepared), 0o600); err != nil {
		t.Fatalf("подготовка файла: %v", err)
	}

	mock := &Mock{Dir: dir, Default: "не этот текст"}

	result, err := mock.Transcribe(context.Background(), Request{Path: "weekly.ogg", FileName: "weekly.ogg"})
	if err != nil {
		t.Fatalf("Transcribe() вернул ошибку: %v", err)
	}
	if result.Text != prepared {
		t.Errorf("Text = %q, ожидался %q", result.Text, prepared)
	}
}

func TestMockTranscribeFallsBackWhenPreparedFileMissing(t *testing.T) {
	mock := &Mock{Dir: t.TempDir(), Default: "текст по умолчанию"}

	result, err := mock.Transcribe(context.Background(), Request{Path: "unknown.ogg"})
	if err != nil {
		t.Fatalf("Transcribe() вернул ошибку: %v", err)
	}
	if result.Text != "текст по умолчанию" {
		t.Errorf("Text = %q", result.Text)
	}
}

func TestMockTranscribeRejectsEmptyRequest(t *testing.T) {
	_, err := NewMock().Transcribe(context.Background(), Request{})
	if !errors.Is(err, client.ErrInvalidRequest) {
		t.Fatalf("ожидалась ErrInvalidRequest, получено: %v", err)
	}
}

func TestMockTranscribeReportsUnavailable(t *testing.T) {
	mock := &Mock{Err: errors.New("соединение сброшено")}

	_, err := mock.Transcribe(context.Background(), Request{Path: "a.ogg"})
	if !errors.Is(err, client.ErrUnavailable) {
		t.Fatalf("ожидалась ErrUnavailable, получено: %v", err)
	}
	if !client.IsTemporary(err) {
		t.Error("недоступность сервиса должна считаться временной ошибкой")
	}
	if !strings.Contains(err.Error(), ProviderMock) {
		t.Errorf("в тексте ошибки нет провайдера: %v", err)
	}
}

// Клиент обязан уважать отмену контекста: иначе фоновую задачу нельзя остановить.
func TestMockTranscribeRespectsCancellation(t *testing.T) {
	mock := &Mock{Delay: time.Second, Default: "текст"}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := mock.Transcribe(ctx, Request{Path: "a.ogg"})
	elapsed := time.Since(start)

	if !errors.Is(err, client.ErrTimeout) {
		t.Fatalf("ожидалась ErrTimeout, получено: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Error("ошибка должна оборачивать context.DeadlineExceeded")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("отмена заняла %s, ожидалось около 20ms", elapsed)
	}
}

func TestMockTranscribeUsesRequestedLanguage(t *testing.T) {
	result, err := NewMock().Transcribe(context.Background(), Request{Path: "a.ogg", Language: "en-US"})
	if err != nil {
		t.Fatalf("Transcribe() вернул ошибку: %v", err)
	}
	if result.Language != "en-US" {
		t.Errorf("Language = %q, ожидался en-US", result.Language)
	}
}

// Имитация сбоя нужна для демонстрации обработки ошибки и команды retry.
func TestMockTranscribeFailureInjectedByName(t *testing.T) {
	mock := NewMock()

	_, err := mock.Transcribe(context.Background(), Request{Path: "/tmp/fail-meeting.ogg", FileName: "fail-meeting.ogg"})
	if !errors.Is(err, client.ErrUnavailable) {
		t.Fatalf("ожидалась ErrUnavailable, получено: %v", err)
	}
	if !strings.Contains(err.Error(), "имитация") {
		t.Errorf("ошибка не сообщает об имитации: %v", err)
	}

	// Обычное имя файла обрабатывается нормально.
	if _, err := mock.Transcribe(context.Background(), Request{Path: "/tmp/meeting.ogg", FileName: "meeting.ogg"}); err != nil {
		t.Fatalf("обычный файл обработан с ошибкой: %v", err)
	}
}
