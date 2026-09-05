package store

import (
	"context"
	"testing"

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
