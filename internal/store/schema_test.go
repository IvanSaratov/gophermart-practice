package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

// Проверяет передачу причины отмены запуска без подключения к PostgreSQL.
func TestInitializePreservesContextError(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	expired, cancelDeadline := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancelDeadline()

	for _, tt := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{name: "canceled", ctx: canceled, want: context.Canceled},
		{name: "expired", ctx: expired, want: context.DeadlineExceeded},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config, err := pgx.ParseConfig("postgres://user:password@localhost/gophermart?sslmode=disable")
			require.NoError(t, err)
			storage := &Store{migrationConfig: config}

			err = storage.Initialize(tt.ctx)

			require.ErrorIs(t, err, tt.want)
			require.ErrorContains(t, err, "migrate database schema")
		})
	}
}
