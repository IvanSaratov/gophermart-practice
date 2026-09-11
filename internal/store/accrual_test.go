package store

import (
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/ivansaratov/gophermart-practice/internal/bonus"
	"github.com/ivansaratov/gophermart-practice/internal/order"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/require"
)

// Проверяет порядок записи статуса и начисления в одной транзакции, включая отсутствие суммы и ноль.
func TestApplyAccrual(t *testing.T) {
	for _, tt := range []struct {
		name          string
		current, next order.Status
		amount        *bonus.Amount
		sqlAmount     any
	}{
		{"processed from new", order.StatusNew, order.StatusProcessed, new(bonus.Amount(50050)), int64(50050)},
		{"processed from processing", order.StatusProcessing, order.StatusProcessed, new(bonus.Amount(125)), int64(125)},
		{"zero", order.StatusNew, order.StatusProcessed, new(bonus.Amount(0)), int64(0)},
		{"no accrual", order.StatusProcessing, order.StatusProcessed, nil, nil},
		{"invalid", order.StatusProcessing, order.StatusInvalid, nil, nil},
		{"processing", order.StatusNew, order.StatusProcessing, nil, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			database, err := pgxmock.NewPool()
			require.NoError(t, err)
			t.Cleanup(database.Close)
			hash := sha256.Sum256([]byte("12345678903"))
			database.ExpectBegin()
			database.ExpectQuery(`SELECT number, user_id, status FROM orders WHERE number_hash = \$1 FOR UPDATE`).
				WithArgs(hash[:]).WillReturnRows(pgxmock.NewRows([]string{"number", "user_id", "status"}).AddRow("12345678903", int64(42), tt.current))
			database.ExpectExec(`UPDATE orders SET status = \$1, accrual = \$2 WHERE number_hash = \$3`).
				WithArgs(tt.next, tt.sqlAmount, hash[:]).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
			if tt.amount != nil && *tt.amount > 0 {
				database.ExpectExec(`UPDATE users SET balance = balance \+ \$1 WHERE id = \$2`).
					WithArgs(int64(*tt.amount), int64(42)).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
			}
			database.ExpectCommit()
			// Настоящий pgx после commit отвечает, что транзакция уже закрыта.
			database.ExpectRollback().WillReturnError(pgx.ErrTxClosed)

			err = (&Store{pool: database}).ApplyAccrual(t.Context(), "12345678903", tt.next, tt.amount)

			require.NoError(t, err)
			require.NoError(t, database.ExpectationsWereMet())
		})
	}
}

// Проверяет, что повтор и запоздавший результат не меняют уже завершённый заказ и баланс.
func TestApplyAccrualIgnoresFinalOrders(t *testing.T) {
	for _, current := range []order.Status{order.StatusProcessed, order.StatusInvalid} {
		for _, next := range []order.Status{order.StatusProcessed, order.StatusInvalid, order.StatusProcessing} {
			t.Run(string(current)+"/"+string(next), func(t *testing.T) {
				database, err := pgxmock.NewPool()
				require.NoError(t, err)
				t.Cleanup(database.Close)
				hash := sha256.Sum256([]byte("12345678903"))
				database.ExpectBegin()
				database.ExpectQuery(`SELECT number, user_id, status FROM orders WHERE number_hash = \$1 FOR UPDATE`).
					WithArgs(hash[:]).WillReturnRows(pgxmock.NewRows([]string{"number", "user_id", "status"}).AddRow("12345678903", int64(42), current))
				database.ExpectRollback()
				var amount *bonus.Amount
				if next == order.StatusProcessed {
					amount = new(bonus.Amount(50050))
				}

				err = (&Store{pool: database}).ApplyAccrual(t.Context(), "12345678903", next, amount)

				require.NoError(t, err)
				require.NoError(t, database.ExpectationsWereMet())
			})
		}
	}
}

// Проверяет отказ от недопустимого результата до обращения к БД.
func TestApplyAccrualRejectsInvalidResult(t *testing.T) {
	for _, tt := range []struct {
		status order.Status
		amount *bonus.Amount
	}{
		{order.StatusNew, nil}, {order.Status("UNKNOWN"), nil},
		{order.StatusProcessed, new(bonus.Amount(-1))},
		{order.StatusProcessing, new(bonus.Amount(10))},
		{order.StatusInvalid, new(bonus.Amount(0))},
	} {
		database, err := pgxmock.NewPool()
		require.NoError(t, err)
		t.Cleanup(database.Close)
		err = (&Store{pool: database}).ApplyAccrual(t.Context(), "12345678903", tt.status, tt.amount)
		require.ErrorIs(t, err, errInvalidAccrualResult)
		require.NoError(t, database.ExpectationsWereMet())
	}
}

// Проверяет сохранение причины ошибки и откат всех незавершённых изменений, в том числе при переполнении баланса.
func TestApplyAccrualDatabaseErrors(t *testing.T) {
	writeErr := errors.New("database unavailable")
	overflowErr := &pgconn.PgError{Code: "22003", Message: "bigint out of range"}
	for _, tt := range []struct {
		stage string
		cause error
	}{
		{"begin", writeErr}, {"read", writeErr}, {"missing", pgx.ErrNoRows},
		{"collision", errOrderHashCollision}, {"order update", writeErr},
		{"credit", writeErr}, {"overflow", overflowErr}, {"commit", writeErr},
	} {
		t.Run(tt.stage, func(t *testing.T) {
			database, err := pgxmock.NewPool()
			require.NoError(t, err)
			t.Cleanup(database.Close)
			hash := sha256.Sum256([]byte("12345678903"))
			if tt.stage == "begin" {
				database.ExpectBegin().WillReturnError(tt.cause)
			} else {
				database.ExpectBegin()
				read := database.ExpectQuery(`SELECT number, user_id, status FROM orders WHERE number_hash = \$1 FOR UPDATE`).WithArgs(hash[:])
				switch tt.stage {
				case "read", "missing":
					read.WillReturnError(tt.cause)
				case "collision":
					read.WillReturnRows(pgxmock.NewRows([]string{"number", "user_id", "status"}).AddRow("different-number", int64(42), order.StatusNew))
				default:
					read.WillReturnRows(pgxmock.NewRows([]string{"number", "user_id", "status"}).AddRow("12345678903", int64(42), order.StatusNew))
					update := database.ExpectExec(`UPDATE orders SET status = \$1, accrual = \$2 WHERE number_hash = \$3`).WithArgs(order.StatusProcessed, int64(50050), hash[:])
					if tt.stage == "order update" {
						update.WillReturnError(tt.cause)
					} else {
						update.WillReturnResult(pgxmock.NewResult("UPDATE", 1))
						credit := database.ExpectExec(`UPDATE users SET balance = balance \+ \$1 WHERE id = \$2`).WithArgs(int64(50050), int64(42))
						if tt.stage == "commit" {
							credit.WillReturnResult(pgxmock.NewResult("UPDATE", 1))
							database.ExpectCommit().WillReturnError(tt.cause)
						} else {
							credit.WillReturnError(tt.cause)
						}
					}
				}
				database.ExpectRollback()
			}

			err = (&Store{pool: database}).ApplyAccrual(t.Context(), "12345678903", order.StatusProcessed, new(bonus.Amount(50050)))

			require.ErrorIs(t, err, tt.cause)
			require.NoError(t, database.ExpectationsWereMet())
		})
	}
}
