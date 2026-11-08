package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/subtotalstew/meeting-assistant/migrations"
)

// migrationsDir — каталог внутри встроенной файловой системы (корень FS).
const migrationsDir = "."

// Migrate накатывает все неприменённые миграции.
//
// goose работает через *sql.DB, а приложение — через pgxpool. Второй пул для этого
// не нужен: stdlib.OpenDBFromPool оборачивает уже существующий пул, поэтому миграции
// используют те же соединения.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer func() {
		_ = db.Close()
	}()

	goose.SetBaseFS(migrations.FS)
	// Свой вывод goose не нужен: приложение логирует накат миграций само.
	goose.SetLogger(goose.NopLogger())

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("диалект goose: %w", err)
	}

	if err := goose.UpContext(ctx, db, migrationsDir); err != nil && !errors.Is(err, goose.ErrNoMigrations) {
		return fmt.Errorf("накат миграций: %w", err)
	}

	return nil
}

// MigrateDown откатывает все миграции. Используется в тестах и при локальной отладке.
func MigrateDown(ctx context.Context, pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer func() {
		_ = db.Close()
	}()

	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("диалект goose: %w", err)
	}

	if err := goose.DownContext(ctx, db, migrationsDir); err != nil {
		return fmt.Errorf("откат миграций: %w", err)
	}

	return nil
}

// MigrationStatus возвращает текущую версию схемы — полезно в команде stats.
func MigrationStatus(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	db := stdlib.OpenDBFromPool(pool)
	defer func() {
		_ = db.Close()
	}()

	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())

	if err := goose.SetDialect("postgres"); err != nil {
		return 0, fmt.Errorf("диалект goose: %w", err)
	}

	version, err := goose.GetDBVersionContext(ctx, db)
	if err != nil {
		return 0, fmt.Errorf("версия схемы: %w", err)
	}
	return version, nil
}
