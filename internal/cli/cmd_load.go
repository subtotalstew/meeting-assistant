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

// loadCommand загружает файл встречи и ставит задачу обработки.
//
// Воркеры живут в этом же процессе, поэтому по умолчанию команда дожидается
// завершения обработки: иначе процесс завершится и задача останется в очереди.
// Для асинхронного сценария есть --no-wait в паре с командой serve.
func (a *App) loadCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "load <path>",
		Short: "Загрузить аудио- или тестовый файл встречи",
		Long: `Создаёт встречу и задачу обработки, ставит задачу в очередь и ждёт завершения.

Обработку выполняет пул воркеров этого же процесса. Чтобы загрузка не ждала
распознавания, используйте --no-wait и отдельный процесс: meeting-assistant serve.`,
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

			if a.noWait {
				return a.printLoaded(meeting, nil)
			}

			// Пул нужен до ожидания: без него задача никем не будет обработана.
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

	cmd.Flags().BoolVar(&a.noWait, "no-wait", false, "не ждать завершения обработки")
	cmd.Flags().DurationVar(&a.waitTimeout, "wait-timeout", defaultWaitTimeout,
		"сколько ждать завершения обработки")

	return cmd
}

// startWorker запускает фоновую обработку и восстановление прерванных задач.
func (a *App) startWorker(ctx context.Context) error {
	if a.worker == nil {
		return errors.New("фоновая обработка недоступна: пул не сконфигурирован")
	}

	a.worker.Start(ctx)

	if err := a.worker.Recover(ctx); err != nil {
		return fmt.Errorf("восстановление задач: %w", err)
	}

	return nil
}

// waitForCompletion опрашивает статус встречи, пока обработка не завершится.
//
// Пул воркеров работает в этом же процессе, поэтому ожидание — это опрос базы,
// а не синхронный вызов распознавания. Пользователь при этом может смотреть
// статус из другого терминала: все переходы фиксируются в базе.
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

		if time.Now().After(deadline) {
			return status, fmt.Errorf(
				"обработка не завершилась за %s, проверьте статус: meeting-assistant status %s --user %s",
				a.waitTimeout, meetingID, a.userID,
			)
		}

		select {
		case <-ctx.Done():
			return status, fmt.Errorf("ожидание обработки прервано: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// printLoaded выводит результат загрузки.
func (a *App) printLoaded(meeting domain.Meeting, status *service.MeetingStatus) error {
	out := a.printer()

	finalStatus := meeting.Status
	errText := ""
	if status != nil {
		finalStatus = status.Meeting.Status
		errText = status.ErrorMessage()
	}

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

	fmt.Fprintf(a.stdout, "встреча создана: %s\n", meeting.ID)
	fmt.Fprintf(a.stdout, "файл: %s\n", meeting.FileName)
	fmt.Fprintf(a.stdout, "статус: %s\n", finalStatus)

	switch {
	case a.noWait:
		fmt.Fprintf(a.stdout, "обработка пойдёт в режиме serve; статус: meeting-assistant status %s --user %s\n",
			meeting.ID, a.userID)
	case finalStatus == domain.StatusFailed:
		fmt.Fprintf(a.stdout, "ошибка: %s\n", orEmpty(errText))
	default:
		fmt.Fprintf(a.stdout, "транскрипция: meeting-assistant get %s --user %s\n", meeting.ID, a.userID)
		fmt.Fprintf(a.stdout, "выжимка:     meeting-assistant summary %s --user %s\n", meeting.ID, a.userID)
	}

	return nil
}
