package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/subtotalstew/meeting-assistant/internal/domain"
)

// MeetingStatus — состояние обработки встречи для команды status.
type MeetingStatus struct {
	Meeting domain.Meeting
	Task    domain.Task
}

// ErrorMessage возвращает текст ошибки обработки, если он есть.
// У встречи и задачи он дублируется, поэтому берём первый непустой.
func (s MeetingStatus) ErrorMessage() string {
	if s.Meeting.Error != "" {
		return s.Meeting.Error
	}
	return s.Task.Error
}

// EnsureUser возвращает пользователя по внешнему идентификатору, создавая его
// при первом обращении. Это и есть регистрация: отдельной команды для CLI не нужно.
func (s *Service) EnsureUser(ctx context.Context, externalID string) (domain.User, error) {
	existing, err := s.repo.GetUserByExternalID(ctx, externalID)
	switch {
	case err == nil:
		return existing, nil
	case !errors.Is(err, domain.ErrNotFound):
		return domain.User{}, fmt.Errorf("поиск пользователя: %w", err)
	}

	candidate, err := domain.NewUser(externalID)
	if err != nil {
		return domain.User{}, err
	}

	user, err := s.repo.UpsertUser(ctx, candidate)
	if err != nil {
		return domain.User{}, fmt.Errorf("регистрация пользователя: %w", err)
	}

	s.log.Info("пользователь зарегистрирован",
		"user_id", user.ID.String(),
		"external_id", user.ExternalID,
	)

	return user, nil
}

// LoadMeeting проверяет файл, создаёт встречу с задачей обработки и ставит задачу
// в очередь. Возвращается сразу, не дожидаясь распознавания.
//
// Путь к файлу хранится абсолютным: задачу может забрать другой процесс
// (serve), запущенный из другого каталога, и относительный путь там не найдётся.
func (s *Service) LoadMeeting(ctx context.Context, userID uuid.UUID, path string) (domain.Meeting, error) {
	if err := s.validateFile(path); err != nil {
		return domain.Meeting{}, err
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return domain.Meeting{}, fmt.Errorf("%w: путь к файлу: %v", domain.ErrValidation, err)
	}
	path = absolute

	meeting, task, err := domain.NewMeeting(userID, "", path)
	if err != nil {
		return domain.Meeting{}, err
	}

	if err := s.repo.CreateMeeting(ctx, meeting, task); err != nil {
		return domain.Meeting{}, fmt.Errorf("сохранение встречи: %w", err)
	}

	s.log.Info("встреча загружена",
		"meeting_id", meeting.ID.String(),
		"user_id", userID.String(),
		"file_name", meeting.FileName,
	)

	// Очередь может быть переполнена. Встреча и задача уже сохранены, поэтому
	// задачу заберёт диспетчер из базы — терять загрузку нельзя.
	if err := s.queue.Enqueue(meeting.ID); err != nil {
		s.log.Warn("не удалось поставить задачу в очередь",
			"meeting_id", meeting.ID.String(),
			"error", err.Error(),
		)
	}

	return meeting, nil
}

// ListMeetings возвращает встречи пользователя.
func (s *Service) ListMeetings(ctx context.Context, userID uuid.UUID) ([]domain.MeetingItem, error) {
	meetings, err := s.repo.ListMeetings(ctx, userID, s.opts.ListLimit)
	if err != nil {
		return nil, fmt.Errorf("список встреч: %w", err)
	}
	return meetings, nil
}

// Status возвращает состояние обработки встречи.
func (s *Service) Status(ctx context.Context, userID, meetingID uuid.UUID) (MeetingStatus, error) {
	meeting, err := s.repo.GetMeeting(ctx, meetingID, userID)
	if err != nil {
		return MeetingStatus{}, err
	}

	task, err := s.repo.GetTask(ctx, meetingID)
	if err != nil {
		return MeetingStatus{}, fmt.Errorf("состояние задачи: %w", err)
	}

	return MeetingStatus{Meeting: meeting, Task: task}, nil
}

// Transcription возвращает транскрипцию встречи.
func (s *Service) Transcription(ctx context.Context, userID, meetingID uuid.UUID) (domain.Transcription, error) {
	// Сначала проверяем доступ к встрече: иначе «нет транскрипции» и «нет встречи»
	// неразличимы и пользователь получит неверную подсказку.
	if _, err := s.repo.GetMeeting(ctx, meetingID, userID); err != nil {
		return domain.Transcription{}, err
	}

	transcription, err := s.repo.GetTranscription(ctx, meetingID, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Transcription{}, fmt.Errorf("%w: транскрипция ещё не получена", ErrNotReady)
		}
		return domain.Transcription{}, fmt.Errorf("транскрипция: %w", err)
	}

	return transcription, nil
}

// Summary возвращает краткую выжимку встречи.
func (s *Service) Summary(ctx context.Context, userID, meetingID uuid.UUID) (domain.Summary, error) {
	if _, err := s.repo.GetMeeting(ctx, meetingID, userID); err != nil {
		return domain.Summary{}, err
	}

	summary, err := s.repo.GetSummary(ctx, meetingID, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Summary{}, fmt.Errorf("%w: краткая выжимка ещё не получена", ErrNotReady)
		}
		return domain.Summary{}, fmt.Errorf("краткая выжимка: %w", err)
	}

	return summary, nil
}

// Retry возвращает встречу в очередь обработки.
//
// Повтор разрешён для failed и created: первая попытка могла упасть из-за
// недоступности внешнего сервиса, а created означает, что задача ещё не
// обрабатывалась. Успешную встречу перезапускать нельзя — это защита от
// потери уже полученного результата.
func (s *Service) Retry(ctx context.Context, userID, meetingID uuid.UUID) (domain.Meeting, error) {
	meeting, err := s.repo.GetMeeting(ctx, meetingID, userID)
	if err != nil {
		return domain.Meeting{}, err
	}

	switch meeting.Status {
	case domain.StatusFailed:
		if err := s.repo.ResetForRetry(ctx, meetingID); err != nil {
			return domain.Meeting{}, err
		}
	case domain.StatusCreated:
		// Задача уже в очереди, достаточно поставить её ещё раз.
	default:
		return domain.Meeting{}, fmt.Errorf(
			"%w: встреча в статусе %s, повтор не требуется",
			domain.ErrInvalidTransition, meeting.Status,
		)
	}

	if err := s.queue.Enqueue(meetingID); err != nil {
		// Задача осталась в created и будет подхвачена восстановлением.
		s.log.Warn("не удалось поставить задачу на повтор в очередь",
			"meeting_id", meetingID.String(),
			"error", err.Error(),
		)
	}

	s.log.Info("встреча поставлена на повторную обработку",
		"meeting_id", meetingID.String(),
		"user_id", userID.String(),
	)

	meeting.Status = domain.StatusCreated
	meeting.Error = ""

	return meeting, nil
}

// DeleteMeeting удаляет встречу пользователя вместе со связанными данными.
func (s *Service) DeleteMeeting(ctx context.Context, userID, meetingID uuid.UUID) error {
	if err := s.repo.DeleteMeeting(ctx, meetingID, userID); err != nil {
		return err
	}

	s.log.Info("встреча удалена",
		"meeting_id", meetingID.String(),
		"user_id", userID.String(),
	)

	return nil
}

// validateFile проверяет, что файл существует, не пустой и поддерживаемого формата.
//
// Проверки делаются до создания встречи: лучше отказать сразу, чем показать
// пользователю встречу, которая гарантированно упадёт в обработке.
func (s *Service) validateFile(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("%w: не указан путь к файлу", domain.ErrValidation)
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: файл не найден: %s", domain.ErrValidation, path)
		}
		return fmt.Errorf("проверка файла %s: %w", path, err)
	}

	if info.IsDir() {
		return fmt.Errorf("%w: путь указывает на каталог: %s", domain.ErrValidation, path)
	}

	if info.Size() == 0 {
		return fmt.Errorf("%w: файл пустой: %s", domain.ErrValidation, path)
	}

	if s.opts.MaxFileSize > 0 && info.Size() > s.opts.MaxFileSize {
		return fmt.Errorf("%w: файл больше %d МиБ", domain.ErrValidation, s.opts.MaxFileSize>>20)
	}

	extension := strings.ToLower(filepath.Ext(path))
	if !s.extensionAllowed(extension) {
		return fmt.Errorf("%w: формат %q не поддерживается, допустимые: %s",
			domain.ErrValidation, extension, strings.Join(s.opts.AllowedExtensions, ", "))
	}

	return nil
}

func (s *Service) extensionAllowed(extension string) bool {
	for _, allowed := range s.opts.AllowedExtensions {
		if strings.EqualFold(allowed, extension) {
			return true
		}
	}
	return false
}
