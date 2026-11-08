// Package domain содержит сущности и правила предметной области.
//
// Пакет намеренно не импортирует ничего, кроме стандартной библиотеки и uuid:
// бизнес-логика не должна знать ни про cobra, ни про pgx, ни про HTTP-клиенты.
package domain

// Status — статус обработки встречи.
type Status string

// Жизненный цикл обработки. Переходы описаны в allowedTransitions.
const (
	// StatusCreated — задача создана и ждёт свободного обработчика.
	StatusCreated Status = "created"
	// StatusProcessing — файл отправлен в speech-клиент.
	StatusProcessing Status = "processing"
	// StatusTranscribed — транскрипция получена и сохранена.
	StatusTranscribed Status = "transcribed"
	// StatusSummarized — краткая выжимка получена и сохранена.
	StatusSummarized Status = "summarized"
	// StatusCompleted — обработка полностью завершена.
	StatusCompleted Status = "completed"
	// StatusFailed — обработка завершилась ошибкой.
	StatusFailed Status = "failed"
)

// AllStatuses перечисляет статусы в порядке жизненного цикла.
// Используется в проверках схемы и в команде stats.
func AllStatuses() []Status {
	return []Status{
		StatusCreated,
		StatusProcessing,
		StatusTranscribed,
		StatusSummarized,
		StatusCompleted,
		StatusFailed,
	}
}

// String реализует fmt.Stringer — удобно для логов и вывода CLI.
func (s Status) String() string {
	return string(s)
}

// Valid сообщает, что статус входит в известный набор.
func (s Status) Valid() bool {
	for _, known := range AllStatuses() {
		if s == known {
			return true
		}
	}
	return false
}

// Terminal сообщает, что автоматическая обработка дальше не пойдёт.
// Для failed это значит, что нужен явный retry.
func (s Status) Terminal() bool {
	return s == StatusCompleted || s == StatusFailed
}

// InProgress сообщает, что задача ещё в работе.
func (s Status) InProgress() bool {
	switch s {
	case StatusCreated, StatusProcessing, StatusTranscribed, StatusSummarized:
		return true
	default:
		return false
	}
}

// allowedTransitions — граф допустимых переходов.
//
// Правило: двигаемся только вперёд по жизненному циклу, в failed можно упасть
// из любого рабочего статуса, а из failed — вернуться в created для retry.
var allowedTransitions = map[Status][]Status{
	StatusCreated:     {StatusProcessing, StatusFailed},
	StatusProcessing:  {StatusTranscribed, StatusFailed},
	StatusTranscribed: {StatusSummarized, StatusFailed},
	StatusSummarized:  {StatusCompleted, StatusFailed},
	StatusCompleted:   nil,
	StatusFailed:      {StatusCreated, StatusProcessing},
}

// CanTransitionTo сообщает, допустим ли переход из текущего статуса в next.
func (s Status) CanTransitionTo(next Status) bool {
	if !next.Valid() {
		return false
	}
	for _, allowed := range allowedTransitions[s] {
		if allowed == next {
			return true
		}
	}
	return false
}

// Transition проверяет переход и возвращает ошибку с ErrInvalidTransition,
// если такой переход запрещён. Так невалидный переход невозможно "забыть" проверить.
func (s Status) Transition(next Status) (Status, error) {
	if !s.CanTransitionTo(next) {
		return s, invalidTransition(s, next)
	}
	return next, nil
}
