package observability

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Проверяет, что production-логгер создаётся и принимает информационные сообщения.
func TestNewLogger(t *testing.T) {
	logger, err := NewLogger()

	require.NoError(t, err)
	require.NotNil(t, logger)
	require.True(t, logger.Core().Enabled(zap.InfoLevel))
}
