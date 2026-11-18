package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/subtotalstew/meeting-assistant/internal/domain"
	"github.com/subtotalstew/meeting-assistant/internal/service"
)

// loadCommand загружает файл встречи и ставит задачу в очередь.
//
// По умолчанию команда только создаёт встречу и задачу и сразу возвращает
// управление: обработку заберёт диспетчер — в этом процессе, если он был
// запущен (--wait), или в отдельном serve. Для демонстрации в одном окне
// есть --wait: он поднимает локальный пул и дожидается результата.
func (a *App) loadCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "load <path>",
		Short: "Загрузить аудио- или тестовый файл встречи",
		Long: `Создаёт встречу и задачу обработки и ставит задачу в очередь.

Дальше задачу забирает диспетчер: в отдельном процессе serve или, с флагом
--wait, в этом же процессе. Статус в любой момент виден командой status.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			user, err := a.currentUser(cmd.Context())
			if err != nil {
				return err
			}

			meeting, err := a.svc.LoadMeeting(cmd.Context(), user.ID, args[0])
			if err != nil {
				return err
			}

			// В интерактивном режиме запоминаем встречу: следующие команды
			// можно писать без идентификатора.
			a.lastMeeting = meeting.ID.String()

			if !a.waitForResult {
				return a.printLoaded(meeting, nil)
			}

			// Пул нужен до ожидания: без него задача никем не будет обработана,
			// если отдельный serve не запущен.
			if err := a.startWorker(cmd.Context()); err != nil {
				return err
			}

			status, err := a.waitForCompletion(cmd.Context(), user.ID, meeting.ID)
			if err != nil {
				return err
			}

			return a.printLoaded(meeting, &status)
		},
	}

	cmd.Flags().BoolVar(&a.waitForResult, "wait", false,
		"дождаться завершения обработки в этом процессе")
	cmd.Flags().DurationVar(&a.waitTimeout, "wait-timeout", defaultWaitTimeout,
		"сколько ждать завершения обработки (с --wait)")

	return cmd
}

// startWorker запускает фоновую обработку в этом процессе.
//
// Recover здесь не вызывается: восстановление прерванных задач делает serve
// при старте. Сброс всех processing из load сбрасывал бы задачи, которые прямо
// сейчас обрабатывает другой процесс.
func (a *App) startWorker(ctx context.Context) error {
	if a.worker == nil {
		return errors.New("фоновая обработка недоступна: пул не сконфигурирован")
	}

	a.worker.Start(ctx)

	return nil
}

// waitForCompletion опрашивает статус встречи, пока обработка не завершится.
//
// Ожидание — это опрос базы, а не синхронный вызов распознавания: задачу в это
// время обрабатывает пул, в этом процессе или в отдельном serve. Пользователь
// при этом может смотреть статус из другого терминала: все переходы в базе.
func (a *App) waitForCompletion(ctx context.Context, userID, meetingID uuid.UUID) (service.MeetingStatus, error) {
	deadline := time.Now().Add(a.waitTimeout)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		status, err := a.svc.Status(ctx, userID, meetingID)
		if err != nil {
			return service.MeetingStatus{}, err
		}

		if status.Meeting.Status.Terminal() {
			return status, nil
		}

		if time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return status, fmt.Errorf("ожидание обработки прервано: %w", ctx.Err())
			case <-ticker.C:
				continue
			}
		}

		return status, fmt.Errorf(
			"обработка не завершилась за %s, проверьте статус: meeting-assistant status %s --user %s",
			a.waitTimeout, meetingID, a.userID,
		)
	}
}

// finalState сводит встречу и её актуальный статус к состоянию для вывода.
func finalState(meeting domain.Meeting, status *service.MeetingStatus) (domain.Status, string) {
	if status == nil {
		return meeting.Status, meeting.Error
	}
	return status.Meeting.Status, status.ErrorMessage()
}

// printLoaded выводит результат загрузки.
func (a *App) printLoaded(meeting domain.Meeting, status *service.MeetingStatus) error {
	out := a.printer()

	finalStatus, errText := finalState(meeting, status)

	if out.json {
		payload := map[string]any{
			"id":         meeting.ID.String(),
			"file_name":  meeting.FileName,
			"title":      meeting.Title,
			"status":     finalStatus.String(),
			"created_at": formatTime(meeting.CreatedAt),
		}
		if errText != "" {
			payload["error"] = errText
		}
		return out.writeJSON(payload)
	}

	a.printf("встреча создана: %s\n", meeting.ID)
	a.printf("файл: %s\n", meeting.FileName)
	a.printf("статус: %s\n", finalStatus)

	switch finalStatus {
	case domain.StatusCreated:
		a.printf("обработку заберёт диспетчер (serve); статус: meeting-assistant status %s --user %s\n",
			meeting.ID, a.userID)
	case domain.StatusFailed:
		a.printf("ошибка: %s\n", orEmpty(errText))
	default:
		a.printf("транскрипция: meeting-assistant get %s --user %s\n", meeting.ID, a.userID)
		a.printf("выжимка:     meeting-assistant summary %s --user %s\n", meeting.ID, a.userID)
	}

	return nil
}
