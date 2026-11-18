package domain

import (
	"errors"
	"fmt"
)

// Доменные ошибки. Слой сервиса и CLI проверяют их через errors.Is,
// поэтому ни один слой не разбирает текст ошибки.
var (
	// ErrNotFound — сущность не существует или недоступна пользователю.
	// Отдельно ErrForbidden не возвращается наружу: наличие чужой встречи
	// не подтверждаем, чтобы не подсказывать идентификаторы.
	ErrNotFound = errors.New("не найдено")
	// ErrValidation — данные не прошли проверку.
	ErrValidation = errors.New("некорректные данные")
	// ErrInvalidTransition — попытка недопустимого перехода статуса.
	ErrInvalidTransition = errors.New("недопустимый переход статуса")
	// ErrConflict — сущность уже существует или изменена конкурентно.
	ErrConflict = errors.New("конфликт данных")
)

// invalidTransition собирает ошибку с указанием обоих статусов.
func invalidTransition(from, to Status) error {
	return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
}
