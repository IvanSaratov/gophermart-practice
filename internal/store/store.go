package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	errDatabaseURIRequired = errors.New("database URI is required")
	errInvalidDatabaseURI  = errors.New("invalid database URI")
)

// Подменный интерфейс для моков в тестах.
type databasePool interface {
	Ping(context.Context) error
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Close()
}

// Владеет пулом соединений с PostgreSQL.
type Store struct {
	pool databasePool
}

// Открывает пул и подтверждает доступность базы до возврата управления.
func Open(ctx context.Context, databaseURI string) (*Store, error) {
	databaseURI = strings.TrimSpace(databaseURI)
	if databaseURI == "" {
		return nil, errDatabaseURIRequired
	}

	config, err := pgxpool.ParseConfig(databaseURI)
	if err != nil {
		// Исходная ошибка pgx может содержать URI вместе с паролем, поэтому наружу нужно возвращать без трейса.
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

// Приводит пустую базу к актуальной.
func (s *Store) Initialize(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("initialize database schema: %w", err)
	}

	return nil
}

// Сохраняет нового пользователя и сообщает, был ли логин свободен.
func (s *Store) CreateUser(ctx context.Context, login, passwordHash string) (int64, bool, error) {
	const query = `
		INSERT INTO users (login, password_hash)
		VALUES ($1, $2)
		ON CONFLICT (login) DO NOTHING
		RETURNING id
	`

	var userID int64
	if err := s.pool.QueryRow(ctx, query, login, passwordHash).Scan(&userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("create user: %w", err)
	}

	return userID, true, nil
}

// Загружает идентификатор и хеш пароля, не считая отсутствие логина системной ошибкой,
// так как мы не должны нашей системой сообщать что такой логин существует или нет.
func (s *Store) UserCredentials(ctx context.Context, login string) (int64, string, bool, error) {
	const query = `SELECT id, password_hash FROM users WHERE login = $1`

	var userID int64
	var passwordHash string
	if err := s.pool.QueryRow(ctx, query, login).Scan(&userID, &passwordHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, "", false, nil
		}
		return 0, "", false, fmt.Errorf("load user credentials: %w", err)
	}

	return userID, passwordHash, true, nil
}
