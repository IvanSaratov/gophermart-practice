package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ivansaratov/gophermart-practice/internal/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// Проверяет ответ liveness-пробы.
func TestHealth(t *testing.T) {
	router := NewRouter(zap.NewNop(), observability.NewMetrics(), func(context.Context) error {
		return nil
	})

	response := performRequest(router, http.MethodGet, "/health")

	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "text/plain; charset=utf-8", response.Header().Get("Content-Type"))
	assert.Equal(t, "ok\n", response.Body.String())
}

// Проверяет успешный и неуспешный ответ readiness-пробы.
func TestReadiness(t *testing.T) {
	tests := []struct {
		name       string
		checkErr   error
		wantStatus int
		wantBody   string
		wantLogs   int
	}{
		{name: "ready", wantStatus: http.StatusOK, wantBody: "ok\n"},
		{name: "not ready", checkErr: errors.New("database password leaked"), wantStatus: http.StatusServiceUnavailable, wantBody: "not ready\n", wantLogs: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.WarnLevel)
			router := NewRouter(zap.New(core), observability.NewMetrics(), func(context.Context) error {
				return tt.checkErr
			})

			response := performRequest(router, http.MethodGet, "/ready")

			require.Equal(t, tt.wantStatus, response.Code)
			assert.Equal(t, "text/plain; charset=utf-8", response.Header().Get("Content-Type"))
			assert.Equal(t, tt.wantBody, response.Body.String())
			assert.NotContains(t, response.Body.String(), "database password leaked")
			assert.Len(t, logs.FilterMessage("Readiness check failed").All(), tt.wantLogs)
		})
	}
}

// Проверяет ограничение времени ожидания внешних зависимостей на уровне HTTP API.
func TestReadinessBoundsCheckContext(t *testing.T) {
	var deadline time.Time
	var hasDeadline bool
	router := NewRouter(zap.NewNop(), observability.NewMetrics(), func(ctx context.Context) error {
		deadline, hasDeadline = ctx.Deadline()
		return nil
	})

	startedAt := time.Now()
	response := performRequest(router, http.MethodGet, "/ready")

	require.Equal(t, http.StatusOK, response.Code)
	require.True(t, hasDeadline)
	assert.WithinDuration(t, startedAt.Add(2*time.Second), deadline, 250*time.Millisecond)
}

// Проверяет выгрузку метрик из изолированного registry.
func TestMetrics(t *testing.T) {
	metrics := observability.NewMetrics()
	router := NewRouter(zap.NewNop(), metrics, func(context.Context) error {
		return nil
	})
	performRequest(router, http.MethodGet, "/health")

	response := performRequest(router, http.MethodGet, "/metrics")

	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), "gophermart_http_requests_total")
	assert.Contains(t, response.Body.String(), "go_goroutines")
}

// Выполняет тестовый HTTP-запрос без запуска сетевого сервера.
func performRequest(handler http.Handler, method, target string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(method, target, nil))
	return response
}
