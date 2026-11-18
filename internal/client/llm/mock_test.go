package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/subtotalstew/meeting-assistant/internal/client"
)

const testTranscript = `Коллеги, обсуждаем итоги спринта и планы на следующий.
Договорились, что релиз выйдет в пятницу, до среды нужно закрыть код-ревью.
Мария подготовит инструкцию для новых сотрудников и выложит её в базу знаний.
Если внешний API недоступен, обработка уходит в статус failed и её можно перезапустить.`

func TestMockSummarizeBuildsPoints(t *testing.T) {
	result, err := NewMock().Summarize(context.Background(), SummaryRequest{
		MeetingTitle: "Итоги спринта",
		Transcript:   testTranscript,
	})
	if err != nil {
		t.Fatalf("Summarize() вернул ошибку: %v", err)
	}

	if result.Provider != ProviderMock {
		t.Errorf("Provider = %q", result.Provider)
	}
	if !strings.Contains(result.Text, "Итоги спринта") {
		t.Errorf("в выжимке нет названия встречи: %q", result.Text)
	}
	if !strings.Contains(result.Text, "пятницу") {
		t.Errorf("в выжимке нет ключевого пункта: %q", result.Text)
	}
	if lines := strings.Count(result.Text, "\n") + 1; lines > maxSummaryPoints+1 {
		t.Errorf("пунктов больше предела: %d", lines)
	}
}

func TestMockSummarizeUsesProvidedSummary(t *testing.T) {
	mock := &Mock{Summary: "готовая выжимка"}

	result, err := mock.Summarize(context.Background(), SummaryRequest{Transcript: testTranscript})
	if err != nil {
		t.Fatalf("Summarize() вернул ошибку: %v", err)
	}
	if result.Text != "готовая выжимка" {
		t.Errorf("Text = %q", result.Text)
	}
}

func TestMockSummarizeRejectsEmptyTranscript(t *testing.T) {
	if _, err := NewMock().Summarize(context.Background(), SummaryRequest{}); !errors.Is(err, client.ErrInvalidRequest) {
		t.Fatalf("ожидалась ErrInvalidRequest, получено: %v", err)
	}
}

func TestMockAnswerFindsRelevantSentence(t *testing.T) {
	result, err := NewMock().Answer(context.Background(), AnswerRequest{
		Question: "Когда релиз?",
		Context:  testTranscript,
	})
	if err != nil {
		t.Fatalf("Answer() вернул ошибку: %v", err)
	}
	if !strings.Contains(result.Text, "пятницу") {
		t.Errorf("ответ не содержит нужного предложения: %q", result.Text)
	}
}

func TestMockAnswerWithoutContext(t *testing.T) {
	result, err := NewMock().Answer(context.Background(), AnswerRequest{
		Question: "Что решили по бюджету?",
		Context:  testTranscript,
	})
	if err != nil {
		t.Fatalf("Answer() вернул ошибку: %v", err)
	}
	if !strings.Contains(result.Text, "нет ответа") {
		t.Errorf("ожидался ответ об отсутствии данных: %q", result.Text)
	}
}

func TestMockAnswerRejectsEmptyRequest(t *testing.T) {
	if _, err := NewMock().Answer(context.Background(), AnswerRequest{}); !errors.Is(err, client.ErrInvalidRequest) {
		t.Fatalf("ожидалась ErrInvalidRequest, получено: %v", err)
	}
}

func TestMockReportsUnavailable(t *testing.T) {
	mock := &Mock{Err: errors.New("503 Service Unavailable")}

	_, err := mock.Summarize(context.Background(), SummaryRequest{Transcript: testTranscript})
	if !errors.Is(err, client.ErrUnavailable) {
		t.Fatalf("ожидалась ErrUnavailable, получено: %v", err)
	}
	if !client.IsTemporary(err) {
		t.Error("недоступность LLM должна считаться временной ошибкой")
	}
}

func TestMockRespectsCancellation(t *testing.T) {
	mock := &Mock{Delay: time.Second}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := mock.Summarize(ctx, SummaryRequest{Transcript: testTranscript})
	if !errors.Is(err, client.ErrTimeout) {
		t.Fatalf("ожидалась ErrTimeout, получено: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("отмена заняла %s", elapsed)
	}
}
