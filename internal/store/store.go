package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/ivansaratov/gophermart-practice/internal/order"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	errDatabaseURIRequired = errors.New("database URI is required")
	errInvalidDatabaseURI  = errors.New("invalid database URI")
	errOrderHashCollision  = errors.New("order number hash collision")
)

// Подменный интерфейс для моков в тестах.
type databasePool interface {
	Begin(context.Context) (pgx.Tx, error)
	Ping(context.Context) error
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Close()
}

// Владеет пулом соединений с PostgreSQL.
type Store struct {
	pool databasePool
	// Нужен чисто для открытия миграции goose, так как она использует другой интерфейс.
	migrationConfig *pgx.ConnConfig
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

	return &Store{pool: pool, migrationConfig: config.ConnConfig.Copy()}, nil
}

// Проверяет доступность PostgreSQL через свободное соединение пула.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// Освобождает все соединения пула при остановке приложения.
func (s *Store) Close() {
	s.pool.Close()
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

// Сохраняет номер заказа либо возвращает владельца уже существующего номера.
func (s *Store) CreateOrder(ctx context.Context, userID int64, number string) (int64, bool, error) {
	const insertQuery = `
		INSERT INTO orders (number_hash, number, user_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (number_hash) DO NOTHING
		RETURNING user_id
	`
	// PostgreSQL не сможет поместить длинный TEXT в ключ, поэтому индексируем
	// фиксированные 32 байта, а исходный номер ниже сверяем после конфликта хеша.
	numberHash := sha256.Sum256([]byte(number))

	var ownerID int64
	if err := s.pool.QueryRow(ctx, insertQuery, numberHash[:], number, userID).Scan(&ownerID); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, false, fmt.Errorf("create order: %w", err)
		}

		// После разрешения конфликта новый запрос видит владельца победившей вставки.
		const ownerQuery = `SELECT number, user_id FROM orders WHERE number_hash = $1`
		var storedNumber string
		if err := s.pool.QueryRow(ctx, ownerQuery, numberHash[:]).Scan(&storedNumber, &ownerID); err != nil {
			return 0, false, fmt.Errorf("load order owner: %w", err)
		}
		// Сверка исходной строки не позволяет принять коллизию хеша за тот же заказ.
		if storedNumber != number {
			return 0, false, errOrderHashCollision
		}

		return ownerID, false, nil
	}

	return ownerID, true, nil
}

// Возвращает заказы владельца от самых новых к самым старым.
func (s *Store) UserOrders(ctx context.Context, userID int64) ([]order.Order, error) {
	const query = `
		SELECT number, status, uploaded_at
		FROM orders
		WHERE user_id = $1
		ORDER BY uploaded_at DESC
	`

	rows, err := s.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("query user orders: %w", err)
	}
	defer rows.Close()

	orders := make([]order.Order, 0)
	for rows.Next() {
		var item order.Order
		if err := rows.Scan(&item.Number, &item.Status, &item.UploadedAt); err != nil {
			return nil, fmt.Errorf("scan user order: %w", err)
		}
		orders = append(orders, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user orders: %w", err)
	}

	return orders, nil
}
