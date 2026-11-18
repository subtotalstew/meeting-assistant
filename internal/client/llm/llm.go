// Package llm описывает клиент языковой модели: краткую выжимку и ответы на вопросы.
//
// Как и speech, пакет содержит только абстракцию. Конкретные провайдеры (mock,
// gigachat) лежат рядом и выбираются по конфигурации, поэтому бизнес-логика
// не зависит от поставщика модели.
package llm

import "context"

// Client — клиент языковой модели.
type Client interface {
	// Summarize делает краткую выжимку по транскрипции встречи.
	Summarize(ctx context.Context, req SummaryRequest) (SummaryResult, error)
	// Answer отвечает на вопрос по переданному контексту.
	Answer(ctx context.Context, req AnswerRequest) (AnswerResult, error)
	// Provider возвращает имя провайдера: оно попадает в лог и в базу.
	Provider() string
}

// SummaryRequest — запрос на краткую выжимку.
type SummaryRequest struct {
	MeetingTitle string
	Transcript   string
	Language     string
}

// SummaryResult — краткая выжимка в едином формате.
type SummaryResult struct {
	Text     string
	Provider string
}

// AnswerRequest — вопрос пользователя и контекст, по которому надо ответить.
type AnswerRequest struct {
	Question string
	// Context — материалы встречи: транскрипция и/или выжимка.
	Context string
}

// AnswerResult — ответ модели в едином формате.
type AnswerResult struct {
	Text     string
	Provider string
}
