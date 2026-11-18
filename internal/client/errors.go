// Package client содержит общий для всех внешних клиентов формат ошибок.
//
// Speech-клиент и LLM-клиент возвращают ошибки в одном виде, поэтому слой сервиса
// решает, повторять ли операцию, через errors.Is, а не разбором текста ошибки.
package client

import (
	"errors"
	"fmt"
)

// Виды сбоев внешнего сервиса. Проверяются через errors.Is.
var (
	// ErrUnavailable — сервис недоступен (сеть, 5xx, отказ соединения).
	ErrUnavailable = errors.New("внешний сервис недоступен")
	// ErrTimeout — сервис не ответил за отведённое время.
	ErrTimeout = errors.New("внешний сервис не ответил вовремя")
	// ErrRejected — сервис отклонил запрос (неверный формат, лимит, авторизация).
	ErrRejected = errors.New("внешний сервис отклонил запрос")
	// ErrInvalidRequest — запрос неверен ещё до обращения к сервису.
	ErrInvalidRequest = errors.New("некорректный запрос к внешнему сервису")
)

// Error — единый формат ошибки внешнего клиента.
type Error struct {
	// Provider — имя провайдера (mock, yandex, gigachat).
	Provider string
	// Op — операция: transcribe, summarize, answer.
	Op string
	// Kind — один из ErrUnavailable, ErrTimeout, ErrRejected, ErrInvalidRequest.
	Kind error
	// Err — причина, если она есть.
	Err error
}

// Error реализует error.
func (e *Error) Error() string {
	message := fmt.Sprintf("%s(%s): %s", e.Op, e.Provider, e.Kind)
	if e.Err != nil {
		return message + ": " + e.Err.Error()
	}
	return message
}

// Unwrap отдаёт обе причины, поэтому errors.Is(err, ErrTimeout) и
// errors.Is(err, context.DeadlineExceeded) работают одновременно.
func (e *Error) Unwrap() []error {
	if e.Err == nil {
		return []error{e.Kind}
	}
	return []error{e.Kind, e.Err}
}

// New собирает ошибку внешнего клиента.
func New(provider, op string, kind, cause error) error {
	return &Error{Provider: provider, Op: op, Kind: kind, Err: cause}
}

// IsTemporary сообщает, имеет ли смысл повторять операцию.
// Лимиты и отказы авторизации повторять бессмысленно, недоступность и таймаут — да.
func IsTemporary(err error) bool {
	return errors.Is(err, ErrUnavailable) || errors.Is(err, ErrTimeout)
}
