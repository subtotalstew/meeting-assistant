package speech

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/subtotalstew/meeting-assistant/internal/client"
)

// ProviderMock — имя тестового провайдера в конфигурации и в базе.
const ProviderMock = "mock"

// failureMarker — часть имени файла, включающая имитацию сбоя распознавания.
const failureMarker = "fail"

// failureInjected сообщает, нужно ли сымитировать сбой для этого запроса.
func failureInjected(req Request) bool {
	name := strings.ToLower(req.FileName)
	if name == "" {
		name = strings.ToLower(filepath.Base(req.Path))
	}
	return strings.Contains(name, failureMarker)
}

// DefaultTranscript — транскрипция по умолчанию.
//
// Нужна, чтобы демонстрация и тесты не зависели от файлов на диске: загрузили
// любой файл — получили осмысленный текст, на котором видно работу выжимки и поиска.
const DefaultTranscript = `Коллеги, начнём. Обсуждаем итоги спринта и планы на следующий.
По задачам: интеграция с сервисом распознавания готова, остались тесты на отмену по таймауту.
Договорились, что релиз выйдет в пятницу, до среды нужно закрыть код-ревью.
Мария подготовит инструкцию для новых сотрудников и выложит её в общую базу знаний.
По рискам: если внешний API недоступен, обработка уходит в статус failed и её можно перезапустить.
Вопрос по бюджету переносим на следующую встречу, нужны данные от финансового отдела.
Решили, что еженедельный отчёт будем собирать автоматически из сохранённых встреч.
Следующая встреча — в четверг в одиннадцать, повестку пришлёт Сергей.`

// Mock — тестовая реализация клиента распознавания речи.
//
// Используется и в тестах, и в демонстрации проекта: реальные внешние API
// не вызываются, но весь сценарий (задача, статусы, транскрипция, выжимка) проходит.
type Mock struct {
	// Dir — каталог с подготовленными транскрипциями: для файла meeting.ogg
	// берётся meeting.txt. Пустое значение — использовать Default.
	Dir string
	// Default — текст, если подготовленного файла нет.
	Default string
	// Delay имитирует долгое распознавание: по нему проверяются статус processing
	// и отмена по таймауту.
	Delay time.Duration
	// Err имитирует недоступность внешнего сервиса.
	Err error
}

// NewMock создаёт тестовый клиент с транскрипцией по умолчанию.
func NewMock() *Mock {
	return &Mock{Default: DefaultTranscript}
}

// Provider возвращает имя провайдера.
func (m *Mock) Provider() string {
	return ProviderMock
}

// Transcribe возвращает заранее подготовленный текст.
func (m *Mock) Transcribe(ctx context.Context, req Request) (Result, error) {
	if strings.TrimSpace(req.Path) == "" && strings.TrimSpace(req.FileName) == "" {
		return Result{}, client.New(ProviderMock, "transcribe", client.ErrInvalidRequest, nil)
	}

	if m.Delay > 0 {
		timer := time.NewTimer(m.Delay)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return Result{}, client.New(ProviderMock, "transcribe", client.ErrTimeout, ctx.Err())
		case <-timer.C:
		}
	}

	// Контекст мог быть отменён, пока ждали: проверяем перед возвратом результата.
	if err := ctx.Err(); err != nil {
		return Result{}, client.New(ProviderMock, "transcribe", client.ErrTimeout, err)
	}

	if m.Err != nil {
		return Result{}, client.New(ProviderMock, "transcribe", client.ErrUnavailable, m.Err)
	}

	// Имитация сбоя распознавания. Нужна, чтобы показать обработку ошибки и команду
	// retry без реального сервиса: срабатывает детерминированно на файлах,
	// в имени которых есть "fail".
	if failureInjected(req) {
		return Result{}, client.New(ProviderMock, "transcribe", client.ErrUnavailable,
			fmt.Errorf("имитация сбоя распознавания: имя файла содержит %q", failureMarker))
	}

	text, err := m.transcript(req)
	if err != nil {
		return Result{}, err
	}

	language := req.Language
	if language == "" {
		language = "ru-RU"
	}

	return Result{Text: text, Language: language, Provider: ProviderMock}, nil
}

// transcript ищет подготовленный текст рядом с файлом, иначе отдаёт Default.
func (m *Mock) transcript(req Request) (string, error) {
	if m.Dir != "" {
		name := filepath.Base(req.FileName)
		if name == "" || name == "." {
			name = filepath.Base(req.Path)
		}
		name = strings.TrimSuffix(name, filepath.Ext(name)) + ".txt"

		path := filepath.Join(m.Dir, name)
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			if text := strings.TrimSpace(string(data)); text != "" {
				return text, nil
			}
		case os.IsNotExist(err):
			// Подготовленного файла нет — это нормальный сценарий, отдаём Default.
		default:
			return "", client.New(ProviderMock, "transcribe", client.ErrUnavailable,
				fmt.Errorf("чтение подготовленной транскрипции %s: %w", name, err))
		}
	}

	if text := strings.TrimSpace(m.Default); text != "" {
		return text, nil
	}

	return DefaultTranscript, nil
}
