package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/require"
)

// Проверяет порцию ожидающих заказов, продолжение обхода и передачу ошибок БД.
func TestPendingOrders(t *testing.T) {
	databaseErr := errors.New("database unavailable")
	for _, after := range []string{"", "123"} {
		for _, fail := range []bool{false, true} {
			t.Run(after+map[bool]string{false: "success", true: "error"}[fail], func(t *testing.T) {
				db, err := pgxmock.NewPool()
				require.NoError(t, err)
				t.Cleanup(db.Close)
				var cursor []byte
				if after != "" {
					hash := sha256.Sum256([]byte(after))
					cursor = hash[:]
				}
				query := db.ExpectQuery(`(?s)SELECT number FROM orders.*status IN \('NEW', 'PROCESSING'\).*number_hash > \$1.*ORDER BY number_hash.*LIMIT \$2`).WithArgs(cursor, 2)
				if fail {
					query.WillReturnError(databaseErr)
				} else {
					query.WillReturnRows(pgxmock.NewRows([]string{"number"}).AddRow("456").AddRow("789"))
				}
				got, err := (&Store{pool: db}).PendingOrders(context.Background(), after, 2)
				if fail {
					require.ErrorIs(t, err, databaseErr)
				} else {
					require.NoError(t, err)
					require.Equal(t, []string{"456", "789"}, got)
				}
				require.NoError(t, db.ExpectationsWereMet())
			})
		}
	}
}

// Проверяет пустую очередь и ошибки чтения строк.
func TestPendingOrdersRows(t *testing.T) {
	rowErr := errors.New("read interrupted")
	for _, tt := range []struct {
		name    string
		rows    *pgxmock.Rows
		wantErr error
	}{
		{name: "empty", rows: pgxmock.NewRows([]string{"number"})},
		{name: "iteration", rows: pgxmock.NewRows([]string{"number"}).AddRow("123").RowError(0, rowErr), wantErr: rowErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, err := pgxmock.NewPool()
			require.NoError(t, err)
			t.Cleanup(db.Close)
			db.ExpectQuery(`SELECT number FROM orders`).WithArgs([]byte(nil), 2).WillReturnRows(tt.rows).RowsWillBeClosed()
			got, err := (&Store{pool: db}).PendingOrders(context.Background(), "", 2)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
				require.Empty(t, got)
			}
			require.NoError(t, db.ExpectationsWereMet())
		})
	}
}
