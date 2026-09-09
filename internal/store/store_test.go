package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ivansaratov/gophermart-practice/internal/bonus"
	"github.com/ivansaratov/gophermart-practice/internal/order"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Проверяет отказ при отсутствии обязательного адреса базы.
func TestOpenRejectsEmptyDatabaseURI(t *testing.T) {
	_, err := Open(context.Background(), "  ")

	require.ErrorIs(t, err, errDatabaseURIRequired)
}

// Проверяет безопасную диагностику синтаксически неверного адреса базы.
func TestOpenRejectsInvalidDatabaseURIWithoutExposingPassword(t *testing.T) {
	const password = "top-secret-password"
	databaseURI := "postgres://gophermart:" + password + "@localhost:not-a-port/gophermart"

	_, err := Open(context.Background(), databaseURI)

	require.ErrorIs(t, err, errInvalidDatabaseURI)
	assert.NotContains(t, err.Error(), password)
	assert.NotContains(t, err.Error(), databaseURI)
}

// Проверяет сохранение нового заказа и определение владельца уже существующего.
func TestCreateOrderReportsOwnerAndWhetherInsertHappened(t *testing.T) {
	t.Run("created", func(t *testing.T) {
		database, err := pgxmock.NewPool()
		require.NoError(t, err)
		t.Cleanup(database.Close)
		number := strings.Repeat("0", 8<<10)
		numberHash := sha256.Sum256([]byte(number))

		database.ExpectQuery(`INSERT INTO orders \(number_hash, number, user_id\).*ON CONFLICT \(number_hash\) DO NOTHING.*RETURNING user_id`).
			WithArgs(numberHash[:], number, int64(42)).
			WillReturnRows(pgxmock.NewRows([]string{"user_id"}).AddRow(int64(42)))

		ownerID, created, err := (&Store{pool: database}).CreateOrder(context.Background(), 42, number)

		require.NoError(t, err)
		assert.Equal(t, int64(42), ownerID)
		assert.True(t, created)
		require.NoError(t, database.ExpectationsWereMet())
	})

	t.Run("already exists", func(t *testing.T) {
		database, err := pgxmock.NewPool()
		require.NoError(t, err)
		t.Cleanup(database.Close)

		numberHash := sha256.Sum256([]byte("12345678903"))
		database.ExpectQuery(`INSERT INTO orders \(number_hash, number, user_id\).*ON CONFLICT \(number_hash\) DO NOTHING.*RETURNING user_id`).
			WithArgs(numberHash[:], "12345678903", int64(42)).
			WillReturnError(pgx.ErrNoRows)
		database.ExpectQuery(`SELECT number, user_id FROM orders WHERE number_hash = \$1`).
			WithArgs(numberHash[:]).
			WillReturnRows(pgxmock.NewRows([]string{"number", "user_id"}).AddRow("12345678903", int64(73)))

		ownerID, created, err := (&Store{pool: database}).CreateOrder(context.Background(), 42, "12345678903")

		require.NoError(t, err)
		assert.Equal(t, int64(73), ownerID)
		assert.False(t, created)
		require.NoError(t, database.ExpectationsWereMet())
	})

	t.Run("hash collision", func(t *testing.T) {
		database, err := pgxmock.NewPool()
		require.NoError(t, err)
		t.Cleanup(database.Close)
		numberHash := sha256.Sum256([]byte("12345678903"))

		database.ExpectQuery(`INSERT INTO orders \(number_hash, number, user_id\).*ON CONFLICT \(number_hash\) DO NOTHING.*RETURNING user_id`).
			WithArgs(numberHash[:], "12345678903", int64(42)).
			WillReturnError(pgx.ErrNoRows)
		database.ExpectQuery(`SELECT number, user_id FROM orders WHERE number_hash = \$1`).
			WithArgs(numberHash[:]).
			WillReturnRows(pgxmock.NewRows([]string{"number", "user_id"}).AddRow("different-number", int64(73)))

		_, _, err = (&Store{pool: database}).CreateOrder(context.Background(), 42, "12345678903")

		require.Error(t, err)
		assert.ErrorContains(t, err, "hash collision")
		require.NoError(t, database.ExpectationsWereMet())
	})
}

// Проверяет сохранение контекста ошибок обеих операций с PostgreSQL.
func TestCreateOrderPropagatesDatabaseErrors(t *testing.T) {
	databaseErr := errors.New("database unavailable")
	tests := []struct {
		name       string
		expect     func(pgxmock.PgxPoolIface)
		wantDetail string
	}{
		{
			name: "insert",
			expect: func(database pgxmock.PgxPoolIface) {
				numberHash := sha256.Sum256([]byte("12345678903"))
				database.ExpectQuery(`INSERT INTO orders \(number_hash, number, user_id\).*ON CONFLICT \(number_hash\) DO NOTHING.*RETURNING user_id`).
					WithArgs(numberHash[:], "12345678903", int64(42)).
					WillReturnError(databaseErr)
			},
			wantDetail: "create order",
		},
		{
			name: "owner lookup",
			expect: func(database pgxmock.PgxPoolIface) {
				numberHash := sha256.Sum256([]byte("12345678903"))
				database.ExpectQuery(`INSERT INTO orders \(number_hash, number, user_id\).*ON CONFLICT \(number_hash\) DO NOTHING.*RETURNING user_id`).
					WithArgs(numberHash[:], "12345678903", int64(42)).
					WillReturnError(pgx.ErrNoRows)
				database.ExpectQuery(`SELECT number, user_id FROM orders WHERE number_hash = \$1`).
					WithArgs(numberHash[:]).
					WillReturnError(databaseErr)
			},
			wantDetail: "load order owner",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, err := pgxmock.NewPool()
			require.NoError(t, err)
			t.Cleanup(database.Close)
			tt.expect(database)

			_, _, err = (&Store{pool: database}).CreateOrder(context.Background(), 42, "12345678903")

			require.ErrorIs(t, err, databaseErr)
			assert.ErrorContains(t, err, tt.wantDetail)
			require.NoError(t, database.ExpectationsWereMet())
		})
	}
}

// Проверяет возврат только заказов пользователя в порядке от новых к старым.
func TestUserOrdersReturnsNewestFirst(t *testing.T) {
	newer := time.Date(2026, time.September, 6, 12, 1, 0, 0, time.UTC)
	older := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		rows *pgxmock.Rows
		want []order.Order
	}{
		{
			name: "orders",
			rows: pgxmock.NewRows([]string{"number", "status", "uploaded_at", "accrual"}).
				AddRow("12345678903", "PROCESSING", newer, nil).
				AddRow("9278923470", "NEW", older, nil),
			want: []order.Order{
				{Number: "12345678903", Status: order.StatusProcessing, UploadedAt: newer},
				{Number: "9278923470", Status: order.StatusNew, UploadedAt: older},
			},
		},
		{
			name: "accrual values",
			rows: pgxmock.NewRows([]string{"number", "status", "uploaded_at", "accrual"}).
				AddRow("111", "PROCESSED", newer, int64(50050)).
				AddRow("222", "PROCESSED", older, int64(0)).
				AddRow("333", "PROCESSED", older, nil).
				AddRow("444", "INVALID", older, nil),
			want: []order.Order{
				{Number: "111", Status: order.StatusProcessed, UploadedAt: newer, Accrual: new(bonus.Amount(50050))},
				{Number: "222", Status: order.StatusProcessed, UploadedAt: older, Accrual: new(bonus.Amount(0))},
				{Number: "333", Status: order.StatusProcessed, UploadedAt: older},
				{Number: "444", Status: order.StatusInvalid, UploadedAt: older},
			},
		},
		{
			name: "empty",
			rows: pgxmock.NewRows([]string{"number", "status", "uploaded_at", "accrual"}),
			want: []order.Order{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, err := pgxmock.NewPool()
			require.NoError(t, err)
			t.Cleanup(database.Close)
			database.ExpectQuery(`(?s)SELECT number, status, uploaded_at, accrual.*FROM orders.*WHERE user_id = \$1.*ORDER BY uploaded_at DESC`).
				WithArgs(int64(42)).
				WillReturnRows(tt.rows)

			got, err := (&Store{pool: database}).UserOrders(context.Background(), 42)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			require.NoError(t, database.ExpectationsWereMet())
		})
	}
}

// Проверяет ошибки начала запроса и последующей итерации по строкам.
func TestUserOrdersPropagatesDatabaseErrors(t *testing.T) {
	databaseErr := errors.New("database unavailable")
	tests := []struct {
		name   string
		expect func(pgxmock.PgxPoolIface)
	}{
		{
			name: "query",
			expect: func(database pgxmock.PgxPoolIface) {
				database.ExpectQuery(`(?s)SELECT number, status, uploaded_at, accrual.*FROM orders.*WHERE user_id = \$1.*ORDER BY uploaded_at DESC`).
					WithArgs(int64(42)).
					WillReturnError(databaseErr)
			},
		},
		{
			name: "iteration",
			expect: func(database pgxmock.PgxPoolIface) {
				database.ExpectQuery(`(?s)SELECT number, status, uploaded_at, accrual.*FROM orders.*WHERE user_id = \$1.*ORDER BY uploaded_at DESC`).
					WithArgs(int64(42)).
					WillReturnRows(pgxmock.NewRows([]string{"number", "status", "uploaded_at", "accrual"}).
						AddRow("12345678903", "NEW", time.Time{}, nil).
						RowError(0, databaseErr))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, err := pgxmock.NewPool()
			require.NoError(t, err)
			t.Cleanup(database.Close)
			tt.expect(database)

			_, err = (&Store{pool: database}).UserOrders(context.Background(), 42)

			require.ErrorIs(t, err, databaseErr)
			require.NoError(t, database.ExpectationsWereMet())
		})
	}
}

// Проверяет различение созданного пользователя и занятого логина.
func TestCreateUserReportsWhetherInsertHappened(t *testing.T) {
	tests := []struct {
		name        string
		queryErr    error
		returnedID  int64
		wantID      int64
		wantCreated bool
	}{
		{name: "created", returnedID: 42, wantID: 42, wantCreated: true},
		{name: "login already exists", queryErr: pgx.ErrNoRows},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, err := pgxmock.NewPool()
			require.NoError(t, err)
			t.Cleanup(database.Close)

			expected := database.ExpectQuery(`INSERT INTO users \(login, password_hash\).*ON CONFLICT \(login\) DO NOTHING.*RETURNING id`).
				WithArgs("alice", "bcrypt-hash")
			if tt.queryErr != nil {
				expected.WillReturnError(tt.queryErr)
			} else {
				expected.WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(tt.returnedID))
			}

			userID, created, err := (&Store{pool: database}).CreateUser(context.Background(), "alice", "bcrypt-hash")

			require.NoError(t, err)
			assert.Equal(t, tt.wantID, userID)
			assert.Equal(t, tt.wantCreated, created)
			require.NoError(t, database.ExpectationsWereMet())
		})
	}
}

// Проверяет загрузку хеша и безопасное представление отсутствующего пользователя.
func TestUserCredentialsReportsWhetherUserExists(t *testing.T) {
	tests := []struct {
		name         string
		queryErr     error
		returnedID   int64
		returnedHash string
		wantID       int64
		wantHash     string
		wantFound    bool
	}{
		{
			name:         "found",
			returnedID:   17,
			returnedHash: "bcrypt-hash",
			wantID:       17,
			wantHash:     "bcrypt-hash",
			wantFound:    true,
		},
		{name: "not found", queryErr: pgx.ErrNoRows},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, err := pgxmock.NewPool()
			require.NoError(t, err)
			t.Cleanup(database.Close)

			expected := database.ExpectQuery(`SELECT id, password_hash FROM users WHERE login = \$1`).WithArgs("alice")
			if tt.queryErr != nil {
				expected.WillReturnError(tt.queryErr)
			} else {
				expected.WillReturnRows(pgxmock.NewRows([]string{"id", "password_hash"}).AddRow(tt.returnedID, tt.returnedHash))
			}

			userID, passwordHash, found, err := (&Store{pool: database}).UserCredentials(context.Background(), "alice")

			require.NoError(t, err)
			assert.Equal(t, tt.wantID, userID)
			assert.Equal(t, tt.wantHash, passwordHash)
			assert.Equal(t, tt.wantFound, found)
			require.NoError(t, database.ExpectationsWereMet())
		})
	}
}
