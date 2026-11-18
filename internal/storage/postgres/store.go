// Package postgres реализует хранилище на PostgreSQL.
//
// Пакет — единственное место, где приложение знает про SQL. Наружу он отдаёт
// и принимает только типы domain, поэтому бизнес-логика не зависит от драйвера.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/subtotalstew/meeting-assistant/internal/config"
	"github.com/subtotalstew/meeting-assistant/internal/domain"
)

// Store — хранилище на PostgreSQL: один пул соединений на процесс.
type Store struct {
	pool *pgxpool.Pool
}

// New открывает пул соединений и проверяет, что база отвечает.
func New(ctx context.Context, cfg config.Database) (*Store, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		// Сообщение pgx может содержать строку подключения вместе с паролем,
		// поэтому наружу отдаём общее сообщение. Сама форма DSN уже проверена
		// в config.Load, так что сюда попадаем только в экзотических случаях.
		return nil, errors.New("некорректная строка подключения к PostgreSQL")
	}

	poolConfig.MaxConns = cfg.MaxConns
	poolConfig.ConnConfig.ConnectTimeout = cfg.ConnTimeout
	poolConfig.MaxConnLifetime = time.Hour
	poolConfig.MaxConnIdleTime = 15 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("создание пула соединений: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnTimeout)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("подключение к PostgreSQL: %w", err)
	}

	return &Store{pool: pool}, nil
}

// Pool отдаёт пул соединений: он нужен миграциям и интеграционным тестам.
func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

// Close закрывает пул. Вызывается последним при завершении приложения.
func (s *Store) Close() {
	s.pool.Close()
}

// Ping проверяет доступность базы.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// withTx выполняет fn в транзакции: коммит при nil, откат при ошибке.
// Отдельный хелпер нужен, чтобы в сценариях не забыть про откат.
func (s *Store) withTx(ctx context.Context, fn func(pgx.Tx) error) (err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("начало транзакции: %w", err)
	}

	// Откат выполняется только если коммита не было: pgx возвращает
	// ErrTxClosed при попытке откатить уже закоммиченную транзакцию.
	defer func() {
		if err == nil {
			return
		}
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			err = errors.Join(err, fmt.Errorf("откат транзакции: %w", rollbackErr))
		}
	}()

	if err = fn(tx); err != nil {
		return err
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("коммит транзакции: %w", err)
	}

	return nil
}

// rowScanner описывает то, что умеют pgx.Row и pgx.Rows — упрощает переиспользование
// функций сканирования.
type rowScanner interface {
	Scan(dest ...any) error
}

// wrapNotFound превращает pgx.ErrNoRows в доменную ошибку ErrNotFound.
// Все выборки, доступные пользователю, фильтруются по user_id, поэтому
// "чужая встреча" и "несуществующая" неразличимы для вызывающего слоя.
func wrapNotFound(err error, what string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s", domain.ErrNotFound, what)
	}
	return err
}
