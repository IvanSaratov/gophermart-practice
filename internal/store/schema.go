package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Отдельный ключ для предотвращения race condiction миграции
const migrationLockID int64 = 0x676f706865726d

// Применяет недостающие миграции по порядку.
func (s *Store) Initialize(ctx context.Context) error {
	files, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("open embedded migrations: %w", err)
	}
	// Выделенное соединение закрывается после миграций: сессионная блокировка
	// не должна остаться на соединении рабочего pgxpool даже при ошибке отмены.
	db := stdlib.OpenDB(*s.migrationConfig)
	db.SetMaxOpenConns(1)
	defer db.Close()

	locker, err := lock.NewPostgresSessionLocker(lock.WithLockID(migrationLockID), lock.WithLockTimeout(1, 10))
	if err != nil {
		return fmt.Errorf("create migration lock: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files,
		goose.WithTableName("gophermart_schema_version"),
		goose.WithSessionLocker(locker),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("migrate database schema: %w", err)
	}
	return nil
}
