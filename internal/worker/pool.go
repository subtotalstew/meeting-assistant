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
	// и будет подхвачена диспетчером или восстановлением при следующем старте.
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
	// defaultTaskTimeout — бюджет одной задачи, если он не задан явно.
	// Используется и как основа для аренды захваченной задачи.
	defaultTaskTimeout = 10 * time.Minute
	// defaultPollInterval — как часто диспетчер заглядывает в базу.
	defaultPollInterval = 500 * time.Millisecond
	// maxIdlePollInterval — предел нарастания интервала опроса в простое.
	maxIdlePollInterval = 2 * time.Second
)

// Config — настройки пула.
type Config struct {
	// Concurrency — сколько встреч обрабатывается одновременно.
	// Это же значение является лимитом параллелизма: воркеров ровно столько.
	Concurrency int
	// QueueSize — размер буфера очереди ожидающих задач.
	QueueSize int
	// TaskTimeout — бюджет обработки одной встречи. Задача, захваченная
	// обработчиком, считается зависшей, если висит в processing дольше
	// TaskTimeout + leaseGrace.
	TaskTimeout time.Duration
	// PollInterval — базовый период опроса очереди в базе.
	PollInterval time.Duration
}

// leaseGrace — запас к бюджету задачи перед тем, как считать её зависшей.
// Без запаса живая задача на границе таймаута могла бы быть уведена из-под
// обработчика и запущена повторно.
const leaseGrace = time.Minute

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
	if c.TaskTimeout <= 0 {
		c.TaskTimeout = defaultTaskTimeout
	}
	if c.PollInterval <= 0 {
		c.PollInterval = defaultPollInterval
	}
	return c
}

// taskProcessor описывает то, что умеет обработчик одной встречи.
// Отдельный интерфейс нужен, чтобы пул можно было тестировать без базы и клиентов.
type taskProcessor interface {
	Process(ctx context.Context, meetingID uuid.UUID) error
}

// QueueStore — очередь в базе: то, что пул забирает в работу.
//
// ClaimTasks атомарно переводит задачи из created в processing, поэтому статус
// задачи в базу пишет тот, кто её забрал, а не сам обработчик. ReclaimStale
// возвращает в очередь задачи, зависшие из-за упавшего процесса.
type QueueStore interface {
	ClaimTasks(ctx context.Context, limit int) ([]uuid.UUID, error)
	ReclaimStale(ctx context.Context, lease time.Duration) ([]uuid.UUID, error)
	RecoverPending(ctx context.Context, limit int) ([]uuid.UUID, error)
}

// Pool — пул обработчиков с ограниченным параллелизмом.
//
// Лимит параллелизма задаётся количеством воркеров, а не семафором: лишние задачи
// ждут в буфере очереди, а не порождают новые горутины. Поэтому число горутин
// постоянно и равно Concurrency + 1 (диспетчер), сколько бы файлов ни загрузили.
//
// Задачи попадают в канал двумя путями: из базы через диспетчера (между
// процессами) и напрямую через Enqueue (внутри процесса, чтобы команда load
// не ждала тика опроса).
type Pool struct {
	processor taskProcessor
	store     QueueStore
	log       *slog.Logger
	config    Config

	queue chan uuid.UUID
	// kick будит диспетчера: появилась новая задача, не надо ждать тика.
	kick chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	cancel    context.CancelFunc
	// dispatchWG считает горутины: воркеры и диспетчер.
	dispatchWG sync.WaitGroup

	stopped atomic.Bool
	started atomic.Bool
	active  atomic.Int64

	// inflight — идентификаторы задач, которые уже взяты в работу.
	//
	// Задача может прийти в пул двумя путями: Enqueue от load в этом же процессе
	// и ClaimTasks от диспетчера. Без учёта она попадает в канал дважды и
	// обрабатывается параллельно, портя статусы. Карта закрывает окно между
	// попаданием в канал и началом обработки.
	infMu    sync.Mutex
	inflight map[uuid.UUID]struct{}
}

// claimLocal отмечает задачу как взятую в работу.
//
// Возвращает false, если она уже в работе: тогда повторную постановку
// в канал нужно пропустить, иначе два воркера возьмут одну встречу.
func (p *Pool) claimLocal(meetingID uuid.UUID) bool {
	p.infMu.Lock()
	defer p.infMu.Unlock()

	if _, busy := p.inflight[meetingID]; busy {
		return false
	}
	p.inflight[meetingID] = struct{}{}
	return true
}

// releaseLocal снимает отметку после завершения обработки.
func (p *Pool) releaseLocal(meetingID uuid.UUID) {
	p.infMu.Lock()
	defer p.infMu.Unlock()
	delete(p.inflight, meetingID)
}

// NewPool создаёт пул обработчиков. Работа начинается только после Start.
//
// store нужен для очереди в базе; если он не задан, диспетчер и Recover
// ничего не делают, и пул работает только с Enqueue.
func NewPool(processor taskProcessor, store QueueStore, logger *slog.Logger, cfg Config) *Pool {
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
		kick:      make(chan struct{}, 1),
		inflight:  make(map[uuid.UUID]struct{}, cfg.Concurrency),
	}
}

// Start запускает диспетчера и воркеры.
//
// Порядок важен: диспетчер стартует первым, чтобы с первого же тика отдавать
// задачи в канал; воркеры читают из канала и ни от кого не зависят.
//
// Родительский контекст связывает пул с жизненным циклом приложения: его отмена
// останавливает обработку так же, как явный Stop.
func (p *Pool) Start(ctx context.Context) {
	p.startOnce.Do(func() {
		p.started.Store(true)
		workerCtx, cancel := context.WithCancel(ctx)
		p.cancel = cancel

		if p.store != nil {
			p.dispatchWG.Add(1)
			go p.dispatch(workerCtx)
		}

		for number := 1; number <= p.config.Concurrency; number++ {
			p.dispatchWG.Add(1)
			go p.worker(workerCtx, number)
		}

		p.log.Info("пул обработчиков запущен",
			"workers", p.config.Concurrency,
			"queue_size", p.config.QueueSize,
			"poll_interval", p.config.PollInterval.String(),
		)
	})
}

// Enqueue ставит встречу в очередь, не блокируя вызывающий код.
//
// Если очередь полна, возвращается ErrQueueFull: задача уже сохранена в базе
// в статусе created, поэтому её подхватит диспетчер — терять загрузку нельзя.
//
// Возвращает nil и для задачи, которую уже обрабатывает этот пул: диспетчер мог
// забрать её из базы раньше, чем сюда дошёл вызов. Дубль в канале означал бы
// две параллельные обработки одной встречи.
func (p *Pool) Enqueue(meetingID uuid.UUID) error {
	if p.stopped.Load() {
		return ErrStopped
	}

	if !p.claimLocal(meetingID) {
		return nil
	}

	select {
	case p.queue <- meetingID:
		p.Kick()
		return nil
	default:
		p.releaseLocal(meetingID)
		return fmt.Errorf("%w: ожидает обработки %d", ErrQueueFull, len(p.queue))
	}
}

// Kick будит диспетчера, чтобы он не ждал очередного тика.
//
// Неблокирующая отправка: если пробуждение уже лежит в канале, второе не нужно.
func (p *Pool) Kick() {
	select {
	case p.kick <- struct{}{}:
	default:
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
// Вызывается один раз при старте serve, до приёма новых команд. Ставить
// восстановленные задачи нужно блокирующе: если очередь переполнена, лучше
// подождать, чем потерять встречу, которая осталась в статусе created.
//
// Из load и retry Recover вызывать нельзя: он сбрасывал бы задачи, которые
// прямо сейчас обрабатывает другой процесс.
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

// dispatch забирает задачи из базы и раскладывает их в канал.
//
// Одна горутина: в базу ходит ровно один запрос за такт, сколько бы воркеров
// ни было. Пачка ограничена числом свободных слотов — диспетчер не заберёт
// из базы больше, чем воркеры смогут начать обрабатывать. Это backpressure
// в терминах очереди: лишнее остаётся в created, а не висит в памяти процесса.
//
// Такт адаптивный: пока работа находится — базовый интервал, в простое —
// он нарастает до maxIdlePollInterval, чтобы не опрашивать базу впустую.
func (p *Pool) dispatch(ctx context.Context) {
	defer p.dispatchWG.Done()

	interval := p.config.PollInterval
	timer := time.NewTimer(interval)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-p.kick:
		}

		claimed := p.claimOnce(ctx)

		if claimed {
			interval = p.config.PollInterval
		} else {
			interval *= 2
			if interval > maxIdlePollInterval {
				interval = maxIdlePollInterval
			}
		}

		timer.Reset(interval)
	}
}

// claimOnce забирает пачку задач и раскладывает её в канал.
// Возвращает true, если задачи были.
func (p *Pool) claimOnce(ctx context.Context) bool {
	limit := p.freeSlots()
	if limit <= 0 {
		return false
	}

	lease := p.config.TaskTimeout + leaseGrace

	// Задачи, зависшие из-за упавшего процесса, возвращаются в очередь.
	// Ошибка здесь не ломает диспетчера: следующий такт попробует снова.
	if _, err := p.store.ReclaimStale(ctx, lease); err != nil {
		if !errors.Is(err, context.Canceled) {
			p.log.Warn("возврат зависших задач", "error", err.Error())
		}
	}

	tasks, err := p.store.ClaimTasks(ctx, limit)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			p.log.Warn("выборка задач из очереди", "error", err.Error())
		}
		return false
	}

	for _, meetingID := range tasks {
		if !p.claimLocal(meetingID) {
			// Эту задачу уже обрабатывает этот процесс: load положил её
			// в канал напрямую, а диспетчер успел забрать её из базы.
			continue
		}

		select {
		case p.queue <- meetingID:
		case <-ctx.Done():
			p.releaseLocal(meetingID)
			// Захваченная задача осталась в processing и вернётся в очередь
			// по lease при следующем старте.
			return len(tasks) > 0
		}
	}

	if len(tasks) > 0 {
		p.log.Debug("очередь пополнена", "count", len(tasks))
	}

	return len(tasks) > 0
}

// freeSlots считает, сколько задач воркеры смогут начать прямо сейчас.
//
// Считаем через буфер канала и счётчик активных задач, а не через
// «Concurrency − active»: задача, уже лежащая в канале, тоже ждёт воркера,
// и если её не учесть, диспетчер наберёт лишнего.
func (p *Pool) freeSlots() int {
	slots := p.config.Concurrency - int(p.active.Load()) - len(p.queue)
	if slots <= 0 {
		return 0
	}
	return slots
}

// Stop останавливает пул и ждёт завершения воркеров.
//
// Незавершённые задачи не теряются: они остаются в базе в статусе created
// или failed и будут подхвачены восстановлением при следующем старте.
func (p *Pool) Stop(ctx context.Context) error {
	// Пул мог не запускаться: main останавливает его всегда, а большинству
	// команд фоновая обработка не нужна. В этом случае ждать нечего.
	if !p.started.Load() {
		return nil
	}

	p.stopOnce.Do(func() {
		p.stopped.Store(true)
		if p.cancel != nil {
			p.cancel()
		}
	})

	done := make(chan struct{})
	go func() {
		p.dispatchWG.Wait()
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
	defer p.dispatchWG.Done()

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
	defer p.releaseLocal(meetingID)

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
