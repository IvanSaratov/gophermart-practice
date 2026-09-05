package observability

import "go.uber.org/zap"

// Создаёт production-логгер с JSON-кодированием и информационным уровнем.
func NewLogger() (*zap.Logger, error) {
	return zap.NewProduction()
}
