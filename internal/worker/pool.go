package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// Ошибки пула обработчиков.
var (
	// ErrQueueFull — очередь переполнена, задача осталась в базе в статусе created
	// и будет подхвачена восстановлением при следующем старте.
	ErrQueueFull = errors.New("очередь обработки переполнена")
	// ErrStopped — пул остановлен, новые задачи не принимаются.
	ErrStopped = errors.New("пул обработчиков остановлен")
)

// Границы настроек пула.
const (
	minConcurrency     = 1
	maxConcurrency     = 64
	minQueueSize       = 1
	defaultConcurrency = 4
	defaultQueueSize   = 64
)

// Config — настройки пула.
type Config struct {
	// Concurrency — сколько встреч обрабатывается одновременно.
	// Это же значение является лимитом параллелизма: воркеров ровно столько.
	Concurrency int
	// QueueSize — размер буфера очереди ожидающих задач.
	QueueSize int
}

// WithDefaults возвращает настройки с заполненными и проверенными значениями.
func (c Config) WithDefaults() Config {
	if c.Concurrency < minConcurrency {
		c.Concurrency = defaultConcurrency
	}
	if c.Concurrency > maxConcurrency {
		c.Concurrency = maxConcurrency
	}
	if c.QueueSize < minQueueSize {
		c.QueueSize = defaultQueueSize
	}
	return c
}

// taskProcessor описывает то, что умеет обработчик одной встречи.
// Отдельный интерфейс нужен, чтобы пул можно было тестировать без базы и клиентов.
type taskProcessor interface {
	Process(ctx context.Context, meetingID uuid.UUID) error
}

// recoveryStore отдаёт встречи, обработка которых была прервана прошлым запуском.
type recoveryStore interface {
	RecoverPending(ctx context.Context, limit int) ([]uuid.UUID, error)
}

// Pool — пул обработчиков с ограниченным параллелизмом.
//
// Лимит параллелизма задаётся количеством воркеров, а не семафором: лишние задачи
// ждут в буфере очереди, а не порождают новые горутины. Поэтому число горутин
// постоянно и равно Concurrency, сколько бы файлов ни загрузили.
type Pool struct {
	processor taskProcessor
	store     recoveryStore
	log       *slog.Logger
	config    Config

	queue chan uuid.UUID

	startOnce sync.Once
	stopOnce  sync.Once
	cancel    context.CancelFunc
	wg        sync.WaitGroup

	stopped atomic.Bool
	active  atomic.Int64
}

// NewPool создаёт пул обработчиков. Работа начинается только после Start.
//
// store нужен для восстановления задач после перезапуска; если он не задан,
// Recover ничего не делает.
func NewPool(processor taskProcessor, store recoveryStore, logger *slog.Logger, cfg Config) *Pool {
	if logger == nil {
		logger = slog.Default()
	}

	cfg = cfg.WithDefaults()

	return &Pool{
		processor: processor,
		store:     store,
		log:       logger,
		config:    cfg,
		queue:     make(chan uuid.UUID, cfg.QueueSize),
	}
}

// Start запускает воркеры.
//
// Родительский контекст связывает пул с жизненным циклом приложения: его отмена
// останавливает обработку так же, как явный Stop.
func (p *Pool) Start(ctx context.Context) {
	p.startOnce.Do(func() {
		workerCtx, cancel := context.WithCancel(ctx)
		p.cancel = cancel

		for number := 1; number <= p.config.Concurrency; number++ {
			p.wg.Add(1)
			go p.worker(workerCtx, number)
		}

		p.log.Info("пул обработчиков запущен",
			"workers", p.config.Concurrency,
			"queue_size", p.config.QueueSize,
		)
	})
}

// Enqueue ставит встречу в очередь, не блокируя вызывающий код.
//
// Если очередь полна, возвращается ErrQueueFull: лучше сообщить об этом в лог,
// чем держать команду пользователя в ожидании свободного воркера.
func (p *Pool) Enqueue(meetingID uuid.UUID) error {
	if p.stopped.Load() {
		return ErrStopped
	}

	select {
	case p.queue <- meetingID:
		return nil
	default:
		return fmt.Errorf("%w: ожидает обработки %d", ErrQueueFull, len(p.queue))
	}
}

// Active возвращает число задач, обрабатываемых прямо сейчас.
func (p *Pool) Active() int64 {
	return p.active.Load()
}

// Pending возвращает число задач, ожидающих в очереди.
func (p *Pool) Pending() int {
	return len(p.queue)
}

// Recover возвращает в очередь задачи, прерванные предыдущим запуском.
//
// Вызывается после Start, до приёма новых команд. Ставить восстановленные задачи
// нужно блокирующе: если очередь переполнена, лучше подождать, чем потерять
// встречу, которая осталась в статусе created.
func (p *Pool) Recover(ctx context.Context) error {
	if p.store == nil {
		return nil
	}

	pending, err := p.store.RecoverPending(ctx, cap(p.queue))
	if err != nil {
		return fmt.Errorf("восстановление незавершённых задач: %w", err)
	}

	if len(pending) == 0 {
		return nil
	}

	for _, meetingID := range pending {
		select {
		case p.queue <- meetingID:
		case <-ctx.Done():
			return fmt.Errorf("постановка восстановленных задач: %w", ctx.Err())
		}
	}

	p.log.Info("незавершённые задачи возвращены в очередь", "count", len(pending))

	return nil
}

// Stop останавливает пул и ждёт завершения воркеров.
//
// Незавершённые задачи не теряются: они остаются в базе в статусе created
// или failed и будут подхвачены восстановлением при следующем старте.
func (p *Pool) Stop(ctx context.Context) error {
	p.stopOnce.Do(func() {
		p.stopped.Store(true)
		if p.cancel != nil {
			p.cancel()
		}
	})

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		p.log.Info("пул обработчиков остановлен", "pending", p.Pending())
		return nil
	case <-ctx.Done():
		return fmt.Errorf("ожидание остановки пула: %w", ctx.Err())
	}
}

// worker читает задачи из очереди, пока не будет отменён контекст.
func (p *Pool) worker(ctx context.Context, number int) {
	defer p.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case meetingID := <-p.queue:
			p.handle(ctx, number, meetingID)
		}
	}
}

// handle выполняет одну задачу.
//
// Отмена контекста прерывает обработку через processor, поэтому отдельная
// проверка ctx.Done здесь не нужна: горутина не может «зависнуть» на задаче.
func (p *Pool) handle(ctx context.Context, number int, meetingID uuid.UUID) {
	p.active.Add(1)
	defer p.active.Add(-1)

	started := time.Now()

	if err := p.processor.Process(ctx, meetingID); err != nil {
		p.log.Warn("задача обработки завершилась ошибкой",
			"meeting_id", meetingID.String(),
			"worker", number,
			"duration_ms", time.Since(started).Milliseconds(),
			"error", err.Error(),
		)
		return
	}

	p.log.Info("задача обработки выполнена",
		"meeting_id", meetingID.String(),
		"worker", number,
		"duration_ms", time.Since(started).Milliseconds(),
	)
}
