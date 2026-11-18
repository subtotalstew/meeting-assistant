// Package worker выполняет фоновую обработку встреч.
//
// Обработка идёт в отдельной горутине и не блокирует приём команд: сервис создаёт
// задачу и сразу отвечает, а пользователь узнаёт состояние командой status.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/subtotalstew/meeting-assistant/internal/client"
	"github.com/subtotalstew/meeting-assistant/internal/client/llm"
	"github.com/subtotalstew/meeting-assistant/internal/client/speech"
	"github.com/subtotalstew/meeting-assistant/internal/domain"
)

// Размеры, влияющие на поведение обработчика.
const (
	// failSaveTimeout — сколько даём на сохранение ошибки обработки.
	// Контекст самой задачи к этому моменту может быть уже отменён по таймауту.
	failSaveTimeout = 5 * time.Second
	// maxErrorRunes — предел длины текста ошибки для пользователя.
	maxErrorRunes = 500
)

// Store — то, что обработчику нужно от хранилища.
type Store interface {
	GetMeetingByID(ctx context.Context, id uuid.UUID) (domain.Meeting, error)
	SaveTranscription(ctx context.Context, transcription domain.Transcription) error
	SaveSummary(ctx context.Context, summary domain.Summary) error
	CompleteProcessing(ctx context.Context, meetingID uuid.UUID) error
	MarkFailed(ctx context.Context, meetingID uuid.UUID, reason string) error
	RequeueInterrupted(ctx context.Context, meetingID uuid.UUID) error
}

// Processor выполняет полный цикл обработки одной встречи.
type Processor struct {
	store  Store
	speech speech.Client
	llm    llm.Client
	log    *slog.Logger
	config ProcessorConfig
}

// ProcessorConfig — настройки обработчика.
type ProcessorConfig struct {
	// TaskTimeout ограничивает весь цикл обработки одной встречи.
	TaskTimeout time.Duration
	// SpeechTimeout ограничивает один вызов speech-клиента.
	SpeechTimeout time.Duration
	// LLMTimeout ограничивает один вызов LLM-клиента.
	LLMTimeout time.Duration
}

// NewProcessor создаёт обработчик.
//
// Таймауты двух уровней: TaskTimeout ограничивает цикл целиком, таймауты клиентов —
// каждый отдельный вызов. Так зависший внешний сервис не съедает весь бюджет задачи,
// и в тексте ошибки видно, какой именно вызов не уложился.
func NewProcessor(store Store, speechClient speech.Client, llmClient llm.Client, logger *slog.Logger, cfg ProcessorConfig) *Processor {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.TaskTimeout <= 0 {
		cfg.TaskTimeout = defaultTaskTimeout
	}

	return &Processor{
		store:  store,
		speech: speechClient,
		llm:    llmClient,
		log:    logger,
		config: cfg,
	}
}

// Process обрабатывает встречу: транскрипция, выжимка, завершение.
//
// Задачу в работу переводит не обработчик, а тот, кто её забрал из очереди
// (ClaimTasks или Enqueue внутри процесса). Раньше это делал StartProcessing
// прямо здесь, и у задачи оказывалось два владельца: взявший из очереди и
// взявший при старте обработки. Теперь статус processing означает «задача
// захвачена», и повторно её никто не возьмёт.
func (p *Processor) Process(ctx context.Context, meetingID uuid.UUID) error {
	taskCtx, cancel := context.WithTimeout(ctx, p.config.TaskTimeout)
	defer cancel()

	meeting, err := p.store.GetMeetingByID(taskCtx, meetingID)
	if err != nil {
		return p.fail(ctx, meetingID, fmt.Errorf("встреча недоступна: %w", err))
	}

	p.log.Info("обработка встречи начата",
		"meeting_id", meetingID.String(),
		"file_name", meeting.FileName,
	)

	transcription, err := p.transcribe(taskCtx, meeting)
	if err != nil {
		return p.fail(ctx, meetingID, err)
	}

	summary, err := p.summarize(taskCtx, meeting, transcription.Text)
	if err != nil {
		return p.fail(ctx, meetingID, err)
	}

	if err := p.store.CompleteProcessing(taskCtx, meetingID); err != nil {
		return p.fail(ctx, meetingID, fmt.Errorf("фиксация завершения: %w", err))
	}

	p.log.Info("обработка встречи завершена",
		"meeting_id", meetingID.String(),
		"summary_length", len([]rune(summary.Text)),
	)

	return nil
}

// transcribe получает текст встречи и сохраняет его.
func (p *Processor) transcribe(ctx context.Context, meeting domain.Meeting) (domain.Transcription, error) {
	started := time.Now()

	p.log.Info("запрос в speech-клиент",
		"meeting_id", meeting.ID.String(),
		"provider", p.speech.Provider(),
	)

	callCtx := ctx
	if p.config.SpeechTimeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, p.config.SpeechTimeout)
		defer cancel()
	}

	result, err := p.speech.Transcribe(callCtx, speech.Request{
		Path:     meeting.SourcePath,
		FileName: meeting.FileName,
	})
	if err != nil {
		return domain.Transcription{}, fmt.Errorf("распознавание речи: %w", err)
	}

	transcription := domain.Transcription{
		MeetingID: meeting.ID,
		Text:      result.Text,
		Language:  result.Language,
		Provider:  result.Provider,
		CreatedAt: time.Now().UTC(),
	}

	if err := p.store.SaveTranscription(ctx, transcription); err != nil {
		return domain.Transcription{}, fmt.Errorf("сохранение транскрипции: %w", err)
	}

	p.log.Info("транскрипция получена",
		"meeting_id", meeting.ID.String(),
		"provider", result.Provider,
		"duration_ms", time.Since(started).Milliseconds(),
	)

	return transcription, nil
}

// summarize получает краткую выжимку и сохраняет её.
func (p *Processor) summarize(ctx context.Context, meeting domain.Meeting, transcript string) (domain.Summary, error) {
	started := time.Now()

	p.log.Info("запрос в LLM-клиент",
		"meeting_id", meeting.ID.String(),
		"provider", p.llm.Provider(),
	)

	callCtx := ctx
	if p.config.LLMTimeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, p.config.LLMTimeout)
		defer cancel()
	}

	result, err := p.llm.Summarize(callCtx, llm.SummaryRequest{
		MeetingTitle: meeting.Title,
		Transcript:   transcript,
	})
	if err != nil {
		return domain.Summary{}, fmt.Errorf("краткая выжимка: %w", err)
	}

	summary := domain.Summary{
		MeetingID: meeting.ID,
		Text:      result.Text,
		Provider:  result.Provider,
		CreatedAt: time.Now().UTC(),
	}

	if err := p.store.SaveSummary(ctx, summary); err != nil {
		return domain.Summary{}, fmt.Errorf("сохранение выжимки: %w", err)
	}

	p.log.Info("краткая выжимка получена",
		"meeting_id", meeting.ID.String(),
		"provider", result.Provider,
		"duration_ms", time.Since(started).Milliseconds(),
	)

	return summary, nil
}

// fail сохраняет ошибку и переводит задачу в failed.
//
// Отмена по остановке приложения — отдельный случай: это не сбой обработки,
// а прерванная работа. Такую задачу возвращаем в created, чтобы следующий
// запуск её продолжил. Таймаут задачи отменой не считается: там обработка
// действительно не уложилась, и статус failed честнее.
//
// Для записи используется контекст без отмены: контекст задачи к этому моменту
// уже может быть мёртв, а статус всё равно должен оказаться в базе.
func (p *Processor) fail(ctx context.Context, meetingID uuid.UUID, cause error) error {
	reason := humanizeError(cause)

	failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), failSaveTimeout)
	defer cancel()

	if p.interrupted(ctx, cause) {
		if err := p.store.RequeueInterrupted(failCtx, meetingID); err != nil {
			return errors.Join(cause, fmt.Errorf("возврат прерванной задачи в очередь: %w", err))
		}

		p.log.Info("обработка встречи прервана остановкой, задача возвращена в очередь",
			"meeting_id", meetingID.String(),
		)

		return cause
	}

	if err := p.store.MarkFailed(failCtx, meetingID, reason); err != nil {
		return errors.Join(cause, fmt.Errorf("сохранение ошибки обработки: %w", err))
	}

	p.log.Warn("обработка встречи завершилась ошибкой",
		"meeting_id", meetingID.String(),
		"error", reason,
	)

	return cause
}

// interrupted сообщает, прервана ли задача остановкой приложения.
//
// Признак — контекст приложения отменён, а ошибка пришла от отмены, а не от
// собственного таймаута задачи. Таймаут задачи — это ошибка обработки, отмена
// родительского контекста — остановка процесса (Ctrl+C, SIGTERM).
func (p *Processor) interrupted(ctx context.Context, cause error) bool {
	if !errors.Is(cause, context.Canceled) {
		return false
	}
	return ctx.Err() == context.Canceled
}

// humanizeError делает текст ошибки понятным пользователю.
//
// Сырые ошибки драйверов и HTTP-клиентов бесполезны в выводе команды status,
// но нужны в логах. Поэтому известные виды сбоев заменяются короткой фразой,
// а незнакомые уходят как есть — их лучше показать, чем потерять.
func humanizeError(err error) string {
	var message string

	switch {
	case errors.Is(err, client.ErrTimeout):
		message = "внешний сервис не ответил вовремя"
	case errors.Is(err, client.ErrUnavailable):
		message = "внешний сервис недоступен"
	case errors.Is(err, client.ErrRejected):
		message = "внешний сервис отклонил запрос"
	case errors.Is(err, client.ErrInvalidRequest):
		message = "некорректный запрос к внешнему сервису"
	case errors.Is(err, domain.ErrNotFound):
		message = "встреча или её данные не найдены"
	default:
		message = err.Error()
	}

	return truncate(message, maxErrorRunes)
}

// truncate обрезает текст по количеству символов.
func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}
