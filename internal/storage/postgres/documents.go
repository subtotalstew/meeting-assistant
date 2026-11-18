package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/subtotalstew/meeting-assistant/internal/domain"
)

// searchSnippetLen — сколько символов текста отдаём как фрагмент результата.
const searchSnippetLen = 240

// defaultSearchLimit — предел числа результатов поиска.
const defaultSearchLimit = 50

// Все выборки ниже джойнятся с meetings и фильтруются по m.user_id:
// права проверяются в SQL, поэтому забыть фильтр в новом сценарии не получится.
const qGetTranscription = `
SELECT t.meeting_id, t.text, t.language, t.provider, t.created_at
FROM transcriptions t
JOIN meetings m ON m.id = t.meeting_id
WHERE t.meeting_id = $1 AND m.user_id = $2`

const qGetSummary = `
SELECT s.meeting_id, s.text, s.provider, s.created_at
FROM summaries s
JOIN meetings m ON m.id = s.meeting_id
WHERE s.meeting_id = $1 AND m.user_id = $2`

// qSearchMeetingsFullText — полнотекстовый поиск по индексам из миграции 00003.
//
// Выражение to_tsvector('russian', ...) в предикате должно совпадать с выражением
// индекса символ в символ, иначе планировщик GIN-индекс не возьмёт.
// Сравнение идёт по каждой таблице отдельно: t.text и s.text могут быть NULL
// из-за LEFT JOIN, и тогда @@ даёт NULL, что в WHERE равносильно false.
const qSearchMeetingsFullText = `
SELECT m.id,
       m.title,
       m.status,
       m.created_at,
       ts_headline('russian',
                   COALESCE(t.text, s.text, m.title),
                   websearch_to_tsquery('russian', $2),
                   'StartSel=<<,StopSel=>>,MaxFragments=1,MaxWords=25,MinWords=8') AS snippet
FROM meetings m
LEFT JOIN transcriptions t ON t.meeting_id = m.id
LEFT JOIN summaries s ON s.meeting_id = m.id
WHERE m.user_id = $1
  AND (to_tsvector('russian', t.text) @@ websearch_to_tsquery('russian', $2)
       OR to_tsvector('russian', s.text) @@ websearch_to_tsquery('russian', $2)
       OR to_tsvector('russian', m.title) @@ websearch_to_tsquery('russian', $2))
ORDER BY ts_rank(to_tsvector('russian',
                             COALESCE(t.text, '') || ' ' ||
                             COALESCE(s.text, '') || ' ' || m.title),
                 websearch_to_tsquery('russian', $2)) DESC,
         m.created_at DESC
LIMIT $3`

// qSearchMeetingsSubstring — запасной поиск по подстроке.
//
// Нужен, когда полнотекстовый поиск ничего не нашёл: он ищет по лексемам, поэтому
// запрос «рели» не найдёт «релиз». Подстрока закрывает этот случай, а LIMIT
// не даёт одному запросу вытянуть все материалы пользователя.
var qSearchMeetingsSubstring = fmt.Sprintf(`
SELECT m.id,
       m.title,
       m.status,
       m.created_at,
       COALESCE(NULLIF(left(t.text, %[1]d), ''), left(s.text, %[1]d), '') AS snippet
FROM meetings m
LEFT JOIN transcriptions t ON t.meeting_id = m.id
LEFT JOIN summaries s ON s.meeting_id = m.id
WHERE m.user_id = $1
  AND (t.text ILIKE $2 ESCAPE '\' OR s.text ILIKE $2 ESCAPE '\' OR m.title ILIKE $2 ESCAPE '\')
ORDER BY m.created_at DESC
LIMIT $3`, searchSnippetLen)

const qInsertQuestion = `
INSERT INTO qa_history (id, meeting_id, user_id, question, answer, created_at)
VALUES ($1, $2, $3, $4, $5, $6)`

const qListQuestions = `
SELECT q.id, q.meeting_id, q.user_id, q.question, q.answer, q.created_at
FROM qa_history q
JOIN meetings m ON m.id = q.meeting_id
WHERE q.meeting_id = $1 AND m.user_id = $2
ORDER BY q.created_at DESC
LIMIT $3`

// GetTranscription возвращает транскрипцию встречи пользователя.
func (s *Store) GetTranscription(ctx context.Context, meetingID, userID uuid.UUID) (domain.Transcription, error) {
	var transcription domain.Transcription

	err := s.pool.QueryRow(ctx, qGetTranscription, meetingID, userID).Scan(
		&transcription.MeetingID,
		&transcription.Text,
		&transcription.Language,
		&transcription.Provider,
		&transcription.CreatedAt,
	)
	if err != nil {
		return domain.Transcription{}, wrapNotFound(err, "транскрипция встречи")
	}

	return transcription, nil
}

// GetSummary возвращает краткую выжимку встречи пользователя.
func (s *Store) GetSummary(ctx context.Context, meetingID, userID uuid.UUID) (domain.Summary, error) {
	var summary domain.Summary

	err := s.pool.QueryRow(ctx, qGetSummary, meetingID, userID).Scan(
		&summary.MeetingID,
		&summary.Text,
		&summary.Provider,
		&summary.CreatedAt,
	)
	if err != nil {
		return domain.Summary{}, wrapNotFound(err, "выжимка встречи")
	}

	return summary, nil
}

// SearchMeetings ищет встречи пользователя по тексту.
//
// Сначала работает полнотекстовый поиск по индексам, и только если он ничего
// не нашёл — поиск по подстроке. Такой порядок даёт и релевантную выдачу
// с подсветкой найденных слов, и ожидаемое поведение для неполных слов.
func (s *Store) SearchMeetings(ctx context.Context, userID uuid.UUID, query string, limit int) ([]domain.SearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("%w: пустой поисковый запрос", domain.ErrValidation)
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	hits, err := s.search(ctx, qSearchMeetingsFullText, userID, query, limit)
	if err != nil {
		return nil, err
	}
	if len(hits) > 0 {
		return hits, nil
	}

	pattern := "%" + escapeLike(query) + "%"

	return s.search(ctx, qSearchMeetingsSubstring, userID, pattern, limit)
}

// search выполняет один из поисковых запросов и читает результат.
func (s *Store) search(ctx context.Context, query string, userID uuid.UUID, argument any, limit int) ([]domain.SearchHit, error) {
	rows, err := s.pool.Query(ctx, query, userID, argument, limit)
	if err != nil {
		return nil, fmt.Errorf("поиск по встречам: %w", err)
	}
	defer rows.Close()

	hits := make([]domain.SearchHit, 0, limit)
	for rows.Next() {
		var hit domain.SearchHit
		var status string

		if err := rows.Scan(&hit.MeetingID, &hit.Title, &status, &hit.CreatedAt, &hit.Snippet); err != nil {
			return nil, fmt.Errorf("чтение результата поиска: %w", err)
		}

		hit.Status = domain.Status(status)
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("поиск по встречам: %w", err)
	}

	return hits, nil
}

// SaveQuestion сохраняет вопрос и ответ на него в историю встречи.
func (s *Store) SaveQuestion(ctx context.Context, question domain.Question) error {
	_, err := s.pool.Exec(ctx, qInsertQuestion,
		question.ID,
		question.MeetingID,
		question.UserID,
		question.Question,
		question.Answer,
		question.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("сохранение вопроса: %w", err)
	}
	return nil
}

// ListQuestions возвращает историю вопросов по встрече пользователя.
func (s *Store) ListQuestions(ctx context.Context, meetingID, userID uuid.UUID, limit int) ([]domain.Question, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.pool.Query(ctx, qListQuestions, meetingID, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("выборка истории вопросов: %w", err)
	}
	defer rows.Close()

	questions := make([]domain.Question, 0, limit)
	for rows.Next() {
		var question domain.Question
		if err := rows.Scan(
			&question.ID,
			&question.MeetingID,
			&question.UserID,
			&question.Question,
			&question.Answer,
			&question.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("чтение вопроса: %w", err)
		}
		questions = append(questions, question)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("выборка истории вопросов: %w", err)
	}

	return questions, nil
}

// likeEscaper экранирует символы, которые ILIKE считает шаблонными.
// Без этого запрос "%" вернул бы все встречи пользователя.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func escapeLike(value string) string {
	return likeEscaper.Replace(value)
}
