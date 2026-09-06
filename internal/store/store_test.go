package store

import (
	"context"
	"errors"
	"testing"

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

// Проверяет применение обязательной таблицы пользователей и передачу ошибки PostgreSQL.
func TestInitializeAppliesUsersSchema(t *testing.T) {
	tests := []struct {
		name    string
		execErr error
		wantErr bool
	}{
		{name: "success"},
		{name: "database error", execErr: errors.New("schema unavailable"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, err := pgxmock.NewPool()
			require.NoError(t, err)
			t.Cleanup(database.Close)

			expected := database.ExpectExec(`(?s)CREATE TABLE IF NOT EXISTS users.*login TEXT NOT NULL UNIQUE.*password_hash TEXT NOT NULL.*created_at TIMESTAMPTZ NOT NULL DEFAULT NOW\(\)`)
			if tt.execErr != nil {
				expected.WillReturnError(tt.execErr)
			} else {
				expected.WillReturnResult(pgxmock.NewResult("CREATE", 0))
			}

			err = (&Store{pool: database}).Initialize(context.Background())

			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorContains(t, err, "initialize database schema")
			} else {
				require.NoError(t, err)
			}
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
