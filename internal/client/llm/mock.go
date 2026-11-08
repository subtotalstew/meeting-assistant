package llm

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/subtotalstew/meeting-assistant/internal/client"
)

// ProviderMock — имя тестового провайдера.
const ProviderMock = "mock"

// Ограничения тестовой выжимки: она должна выглядеть как настоящая,
// но не зависеть от внешнего сервиса.
const (
	maxSummaryPoints = 4
	minSentenceRunes = 25
)

// Mock — тестовая реализация LLM-клиента.
//
// Выжимка и ответ строятся детерминированно из текста встречи: это позволяет
// проверять сценарий целиком в тестах и на демонстрации без доступа к модели.
type Mock struct {
	// Summary — если задан, возвращается как готовая выжимка.
	Summary string
	// Delay имитирует долгий ответ модели.
	Delay time.Duration
	// Err имитирует недоступность сервиса.
	Err error
}

// NewMock создаёт тестовый клиент.
func NewMock() *Mock {
	return &Mock{}
}

// Provider возвращает имя провайдера.
func (m *Mock) Provider() string {
	return ProviderMock
}

// Summarize собирает выжимку из первых содержательных предложений.
func (m *Mock) Summarize(ctx context.Context, req SummaryRequest) (SummaryResult, error) {
	if err := m.wait(ctx, "summarize"); err != nil {
		return SummaryResult{}, err
	}

	transcript := strings.TrimSpace(req.Transcript)
	if transcript == "" {
		return SummaryResult{}, client.New(ProviderMock, "summarize", client.ErrInvalidRequest, nil)
	}

	if text := strings.TrimSpace(m.Summary); text != "" {
		return SummaryResult{Text: text, Provider: ProviderMock}, nil
	}

	return SummaryResult{Text: buildSummary(req.MeetingTitle, transcript), Provider: ProviderMock}, nil
}

// Answer находит в материалах встречи предложение, ближайшее к вопросу.
func (m *Mock) Answer(ctx context.Context, req AnswerRequest) (AnswerResult, error) {
	if err := m.wait(ctx, "answer"); err != nil {
		return AnswerResult{}, err
	}

	question := strings.TrimSpace(req.Question)
	contextText := strings.TrimSpace(req.Context)

	if question == "" || contextText == "" {
		return AnswerResult{}, client.New(ProviderMock, "answer", client.ErrInvalidRequest, nil)
	}

	if sentence := findRelevantSentence(contextText, question); sentence != "" {
		return AnswerResult{
			Text:     "По материалам встречи: " + sentence,
			Provider: ProviderMock,
		}, nil
	}

	return AnswerResult{
		Text:     "В материалах встречи нет ответа на этот вопрос.",
		Provider: ProviderMock,
	}, nil
}

// wait имитирует задержку и проверяет отмену контекста.
func (m *Mock) wait(ctx context.Context, op string) error {
	if m.Delay > 0 {
		timer := time.NewTimer(m.Delay)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return client.New(ProviderMock, op, client.ErrTimeout, ctx.Err())
		case <-timer.C:
		}
	}

	if err := ctx.Err(); err != nil {
		return client.New(ProviderMock, op, client.ErrTimeout, err)
	}

	if m.Err != nil {
		return client.New(ProviderMock, op, client.ErrUnavailable, m.Err)
	}

	return nil
}

// buildSummary делает маркированный список из первых содержательных предложений.
func buildSummary(title, transcript string) string {
	sentences := splitSentences(transcript)

	points := make([]string, 0, maxSummaryPoints)
	for _, sentence := range sentences {
		if len([]rune(sentence)) < minSentenceRunes {
			continue
		}
		points = append(points, ". "+strings.TrimSpace(sentence))
		if len(points) == maxSummaryPoints {
			break
		}
	}

	if len(points) == 0 {
		// Короткий текст: отдаём его целиком, чтобы выжимка не оказалась пустой.
		return truncateRunes(transcript, 300)
	}

	header := "Ключевое из встречи"
	if title != "" {
		header += " «" + title + "»"
	}

	return header + ":" + strings.Join(points, "\n")
}

// splitSentences режет текст на предложения по знакам конца предложения.
func splitSentences(text string) []string {
	var sentences []string
	var current strings.Builder

	for _, r := range text {
		current.WriteRune(r)
		switch r {
		case '.', '!', '?', '\n':
			if sentence := strings.TrimSpace(current.String()); sentence != "" {
				sentences = append(sentences, sentence)
			}
			current.Reset()
		}
	}

	if sentence := strings.TrimSpace(current.String()); sentence != "" {
		sentences = append(sentences, sentence)
	}

	return sentences
}

// findRelevantSentence выбирает предложение с наибольшим числом совпавших слов.
func findRelevantSentence(contextText, question string) string {
	keywords := significantWords(question)
	if len(keywords) == 0 {
		return ""
	}

	best := ""
	bestScore := 0

	for _, sentence := range splitSentences(contextText) {
		score := 0
		lowered := strings.ToLower(sentence)
		for _, keyword := range keywords {
			if strings.Contains(lowered, keyword) {
				score++
			}
		}
		if score > bestScore {
			best, bestScore = sentence, score
		}
	}

	return best
}

// significantWords отбрасывает короткие и служебные слова: они дают ложные совпадения.
func significantWords(text string) []string {
	stopWords := map[string]struct{}{
		"что": {}, "как": {}, "когда": {}, "где": {}, "какие": {}, "какой": {},
		"есть": {}, "было": {}, "были": {}, "этот": {}, "этом": {}, "для": {},
		"или": {}, "нам": {}, "нас": {}, "про": {}, "чем": {},
	}

	seen := make(map[string]struct{})
	words := make([]string, 0, 8)

	for _, field := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len([]rune(field)) < 4 {
			continue
		}
		if _, stop := stopWords[field]; stop {
			continue
		}
		if _, duplicate := seen[field]; duplicate {
			continue
		}
		seen[field] = struct{}{}
		words = append(words, field)
	}

	return words
}

// truncateRunes обрезает строку по количеству символов, а не байт.
func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return fmt.Sprintf("%s…", string(runes[:limit]))
}
