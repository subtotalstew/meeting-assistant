package domain

import (
	"errors"
	"testing"
)

func TestStatusValid(t *testing.T) {
	for _, s := range AllStatuses() {
		if !s.Valid() {
			t.Errorf("%q должен быть валидным статусом", s)
		}
	}

	for _, s := range []Status{"", "done", "processing ", "FAILED"} {
		if s.Valid() {
			t.Errorf("%q не должен считаться валидным статусом", s)
		}
	}
}

func TestStatusTerminal(t *testing.T) {
	cases := map[Status]bool{
		StatusCreated:     false,
		StatusProcessing:  false,
		StatusTranscribed: false,
		StatusSummarized:  false,
		StatusCompleted:   true,
		StatusFailed:      true,
	}

	for status, want := range cases {
		if got := status.Terminal(); got != want {
			t.Errorf("%q.Terminal() = %v, ожидалось %v", status, got, want)
		}
	}
}

func TestStatusInProgress(t *testing.T) {
	cases := map[Status]bool{
		StatusCreated:     true,
		StatusProcessing:  true,
		StatusTranscribed: true,
		StatusSummarized:  true,
		StatusCompleted:   false,
		StatusFailed:      false,
	}

	for status, want := range cases {
		if got := status.InProgress(); got != want {
			t.Errorf("%q.InProgress() = %v, ожидалось %v", status, got, want)
		}
	}
}

func TestStatusTransition(t *testing.T) {
	cases := []struct {
		name string
		from Status
		to   Status
		ok   bool
	}{
		{name: "создана в работу", from: StatusCreated, to: StatusProcessing, ok: true},
		{name: "работа в транскрипцию", from: StatusProcessing, to: StatusTranscribed, ok: true},
		{name: "транскрипция в выжимку", from: StatusTranscribed, to: StatusSummarized, ok: true},
		{name: "выжимка в завершение", from: StatusSummarized, to: StatusCompleted, ok: true},
		{name: "падание из работы", from: StatusProcessing, to: StatusFailed, ok: true},
		{name: "retry после ошибки", from: StatusFailed, to: StatusCreated, ok: true},
		{name: "нельзя перепрыгнуть этап", from: StatusCreated, to: StatusCompleted, ok: false},
		{name: "нельзя назад", from: StatusTranscribed, to: StatusProcessing, ok: false},
		{name: "завершённую не перезапустить", from: StatusCompleted, to: StatusProcessing, ok: false},
		{name: "нельзя в неизвестный статус", from: StatusCreated, to: Status("done"), ok: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.from.CanTransitionTo(tc.to); got != tc.ok {
				t.Fatalf("CanTransitionTo() = %v, ожидалось %v", got, tc.ok)
			}

			next, err := tc.from.Transition(tc.to)
			if tc.ok {
				if err != nil {
					t.Fatalf("Transition() вернул ошибку: %v", err)
				}
				if next != tc.to {
					t.Fatalf("Transition() = %q, ожидался %q", next, tc.to)
				}
				return
			}

			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("ожидалась ErrInvalidTransition, получено: %v", err)
			}
			if next != tc.from {
				t.Fatalf("при ошибке статус изменился: %q", next)
			}
		})
	}
}

// Из failed и completed не должно быть перехода вперед по жизненному циклу.
func TestTerminalStatusesHaveNoForwardTransitions(t *testing.T) {
	for _, status := range []Status{StatusCompleted} {
		for _, next := range AllStatuses() {
			if status.CanTransitionTo(next) {
				t.Errorf("из %q допустим переход в %q", status, next)
			}
		}
	}
}
