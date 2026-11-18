package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/subtotalstew/meeting-assistant/internal/client/llm"
	"github.com/subtotalstew/meeting-assistant/internal/domain"
)

// Search ищет встречи пользователя по ключевому слову или фразе.
func (s *Service) Search(ctx context.Context, userID uuid.UUID, query string) ([]domain.SearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("%w: пустой поисковый запрос", domain.ErrValidation)
	}

	hits, err := s.repo.SearchMeetings(ctx, userID, query, s.opts.SearchLimit)
	if err != nil {
		return nil, fmt.Errorf("поиск: %w", err)
	}

	return hits, nil
}

// Ask отвечает на вопрос по материалам встречи и сохраняет вопрос в историю.
//
// В контекст модели уходит и транскрипция, и выжимка: выжимка короткая и обычно
// содержит формулировки договорённостей, а транскрипция — детали, которых в ней нет.
func (s *Service) Ask(ctx context.Context, userID, meetingID uuid.UUID, question string) (string, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return "", fmt.Errorf("%w: пустой вопрос", domain.ErrValidation)
	}

	// Проверка доступа: чужая встреча не должна давать даже намёка на содержимое.
	if _, err := s.repo.GetMeeting(ctx, meetingID, userID); err != nil {
		return "", err
	}

	transcription, err := s.repo.GetTranscription(ctx, meetingID, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", fmt.Errorf("%w: транскрипция ещё не получена", ErrNotReady)
		}
		return "", fmt.Errorf("транскрипция: %w", err)
	}

	materials := transcription.Text

	summary, err := s.repo.GetSummary(ctx, meetingID, userID)
	switch {
	case err == nil:
		materials += "\n\nКраткая выжимка:\n" + summary.Text
	case errors.Is(err, domain.ErrNotFound):
		// Выжимки может не быть: отвечаем по одной транскрипции.
	default:
		return "", fmt.Errorf("краткая выжимка: %w", err)
	}

	answer, err := s.llm.Answer(ctx, llm.AnswerRequest{Question: question, Context: materials})
	if err != nil {
		return "", fmt.Errorf("ответ на вопрос: %w", err)
	}

	s.saveQuestion(ctx, meetingID, userID, question, answer.Text)

	s.log.Info("получен ответ на вопрос",
		"meeting_id", meetingID.String(),
		"user_id", userID.String(),
		"provider", s.llm.Provider(),
	)

	return answer.Text, nil
}

// History возвращает историю вопросов и ответов по встрече.
func (s *Service) History(ctx context.Context, userID, meetingID uuid.UUID) ([]domain.Question, error) {
	if _, err := s.repo.GetMeeting(ctx, meetingID, userID); err != nil {
		return nil, err
	}

	questions, err := s.repo.ListQuestions(ctx, meetingID, userID, s.opts.ListLimit)
	if err != nil {
		return nil, fmt.Errorf("история вопросов: %w", err)
	}

	return questions, nil
}

// saveQuestion пишет историю, но не роняет уже полученный ответ из-за её сбоя:
// пользователь ждёт ответ, а не запись в журнал.
func (s *Service) saveQuestion(ctx context.Context, meetingID, userID uuid.UUID, question, answer string) {
	record, err := domain.NewQuestion(meetingID, userID, question, answer)
	if err != nil {
		s.log.Warn("некорректная запись истории вопросов", "error", err.Error())
		return
	}

	if err := s.repo.SaveQuestion(ctx, record); err != nil {
		s.log.Warn("не удалось сохранить историю вопросов",
			"meeting_id", meetingID.String(),
			"error", err.Error(),
		)
	}
}

// Stats возвращает агрегаты по встречам пользователя.
//
// Отдельного кэша нет: запросы дешёвые (индексы по user_id), а расхождение
// с реальностью в диагностике хуже, чем лишний запрос.
func (s *Service) Stats(ctx context.Context, userID uuid.UUID) (domain.Stats, error) {
	stats, err := s.repo.Stats(ctx, userID)
	if err != nil {
		return domain.Stats{}, fmt.Errorf("статистика: %w", err)
	}
	return stats, nil
}
