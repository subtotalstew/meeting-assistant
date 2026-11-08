package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// countingProcessor считает одновременные обработки и запоминает пиковое значение.
type countingProcessor struct {
	delay time.Duration
	err   error

	mu       sync.Mutex
	inFlight int
	maxSeen  int
	handled  atomic.Int64
}

func (c *countingProcessor) Process(ctx context.Context, _ uuid.UUID) error {
	c.mu.Lock()
	c.inFlight++
	if c.inFlight > c.maxSeen {
		c.maxSeen = c.inFlight
	}
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.inFlight--
		c.mu.Unlock()
	}()

	if c.delay > 0 {
		timer := time.NewTimer(c.delay)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}

	c.handled.Add(1)
	return c.err
}

func (c *countingProcessor) peak() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maxSeen
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Главная проверка блока «Асинхронная обработка»: одновременно выполняется
// не больше задач, чем воркеров, сколько бы их ни поставили в очередь.
func TestPoolRespectsConcurrencyLimit(t *testing.T) {
	processor := &countingProcessor{delay: 20 * time.Millisecond}

	pool := NewPool(processor, testLogger(), Config{Concurrency: 2, QueueSize: 32})
	pool.Start(context.Background())
	defer func() {
		if err := pool.Stop(context.Background()); err != nil {
			t.Errorf("Stop(): %v", err)
		}
	}()

	const tasks = 8
	for i := 0; i < tasks; i++ {
		if err := pool.Enqueue(uuid.New()); err != nil {
			t.Fatalf("Enqueue(): %v", err)
		}
	}

	waitFor(t, 5*time.Second, func() bool {
		return processor.handled.Load() == tasks
	})

	if peak := processor.peak(); peak > 2 {
		t.Fatalf("одновременных обработок %d, лимит 2", peak)
	}
	if peak := processor.peak(); peak < 2 {
		t.Logf("пик одновременных обработок = %d (может быть меньше лимита на быстрых задачах)", peak)
	}
}

func TestPoolEnqueueFailsWhenQueueFull(t *testing.T) {
	// Пул не запущен, поэтому очередь не разбирается.
	pool := NewPool(&countingProcessor{}, testLogger(), Config{Concurrency: 1, QueueSize: 2})

	for i := 0; i < 2; i++ {
		if err := pool.Enqueue(uuid.New()); err != nil {
			t.Fatalf("Enqueue() #%d: %v", i, err)
		}
	}

	if err := pool.Enqueue(uuid.New()); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("ожидалась ErrQueueFull, получено: %v", err)
	}
}

func TestPoolRejectsEnqueueAfterStop(t *testing.T) {
	pool := NewPool(&countingProcessor{}, testLogger(), Config{Concurrency: 1, QueueSize: 4})
	pool.Start(context.Background())

	if err := pool.Stop(context.Background()); err != nil {
		t.Fatalf("Stop(): %v", err)
	}

	if err := pool.Enqueue(uuid.New()); !errors.Is(err, ErrStopped) {
		t.Fatalf("ожидалась ErrStopped, получено: %v", err)
	}
}

func TestPoolStopIsIdempotent(t *testing.T) {
	pool := NewPool(&countingProcessor{}, testLogger(), Config{Concurrency: 2})
	pool.Start(context.Background())

	for i := 0; i < 2; i++ {
		if err := pool.Stop(context.Background()); err != nil {
			t.Fatalf("повторный Stop() #%d: %v", i, err)
		}
	}
}

// Остановка должна прерывать текущую обработку, а не ждать её конца.
func TestPoolStopInterruptsInFlightTask(t *testing.T) {
	processor := &countingProcessor{delay: 5 * time.Second}
	pool := NewPool(processor, testLogger(), Config{Concurrency: 1, QueueSize: 1})
	pool.Start(context.Background())

	if err := pool.Enqueue(uuid.New()); err != nil {
		t.Fatalf("Enqueue(): %v", err)
	}

	// Дожидаемся, что задача действительно начала обрабатываться.
	waitFor(t, 2*time.Second, func() bool { return pool.Active() == 1 })

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := pool.Stop(ctx); err != nil {
		t.Fatalf("Stop(): %v", err)
	}

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("остановка заняла %s, ожидалось меньше секунды", elapsed)
	}
	if pool.Active() != 0 {
		t.Errorf("после остановки активных задач %d", pool.Active())
	}
}

func TestPoolConfigWithDefaults(t *testing.T) {
	cases := []struct {
		name string
		in   Config
		want Config
	}{
		{name: "нули", in: Config{}, want: Config{Concurrency: defaultConcurrency, QueueSize: defaultQueueSize}},
		{name: "отрицательные", in: Config{Concurrency: -5, QueueSize: -1}, want: Config{Concurrency: defaultConcurrency, QueueSize: defaultQueueSize}},
		{name: "выше предела", in: Config{Concurrency: 1000, QueueSize: 10}, want: Config{Concurrency: maxConcurrency, QueueSize: 10}},
		{name: "корректные", in: Config{Concurrency: 3, QueueSize: 7}, want: Config{Concurrency: 3, QueueSize: 7}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.WithDefaults(); got != tc.want {
				t.Errorf("WithDefaults() = %+v, ожидалось %+v", got, tc.want)
			}
		})
	}
}

// waitFor ждёт выполнения условия или роняет тест по таймауту.
func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("условие не выполнено за %s", timeout)
}
