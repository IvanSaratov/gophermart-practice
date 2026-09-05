package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	errDatabaseURIRequired = errors.New("database URI is required")
	errInvalidDatabaseURI  = errors.New("invalid database URI")
)

// Владеет пулом соединений с PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

// Открывает пул и подтверждает доступность базы до возврата управления.
func Open(ctx context.Context, databaseURI string) (*Store, error) {
	databaseURI = strings.TrimSpace(databaseURI)
	if databaseURI == "" {
		return nil, errDatabaseURIRequired
	}

	config, err := pgxpool.ParseConfig(databaseURI)
	if err != nil {
		// Исходная ошибка pgx может содержать URI вместе с паролем, поэтому наружу возвращается безопасный config.
		return nil, fmt.Errorf("parse database configuration: %w", errInvalidDatabaseURI)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}

	// Только Ping гарантирует, что PostgreSQL действительно доступна.
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &Store{pool: pool}, nil
}

// Проверяет доступность PostgreSQL через свободное соединение пула.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// Освобождает все соединения пула при остановке приложения.
func (s *Store) Close() {
	s.pool.Close()
}
