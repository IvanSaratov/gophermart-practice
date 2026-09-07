package observability

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// Проверяет структурный access log и метки метрик по шаблону маршрута.
func TestHTTPMiddleware(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)
	metrics := NewMetrics()

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(metrics.Middleware(logger))
	router.Get("/orders/{number}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/orders/123", nil))

	require.Equal(t, http.StatusNoContent, response.Code)
	entries := logs.FilterMessage("HTTP request").All()
	require.Len(t, entries, 1)
	fields := entries[0].ContextMap()
	assert.NotEmpty(t, fields["request_id"])
	assert.Equal(t, http.MethodGet, fields["method"])
	assert.Equal(t, "/orders/123", fields["path"])
	assert.Equal(t, "/orders/{number}", fields["route"])
	assert.EqualValues(t, http.StatusNoContent, fields["status"])
	assert.Contains(t, fields, "duration")

	metricsResponse := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, metricsResponse.Code)
	assert.Contains(t, metricsResponse.Body.String(), `gophermart_http_requests_total{method="GET",route="/orders/{number}",status="204"} 1`)
	assert.Contains(t, metricsResponse.Body.String(), "gophermart_http_request_duration_seconds")
}

// Проверяет, что неизвестные методы не создают отдельные ряды метрик.
func TestHTTPMiddlewareBoundsMethodLabels(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	metrics := NewMetrics()
	router := chi.NewRouter()
	router.Use(metrics.Middleware(zap.New(core)))
	router.Handle("/orders", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	requests := []struct {
		method     string
		wantStatus int
	}{
		{method: "GET", wantStatus: http.StatusNoContent},
		{method: "HEAD", wantStatus: http.StatusNoContent},
		{method: "POST", wantStatus: http.StatusNoContent},
		{method: "PUT", wantStatus: http.StatusNoContent},
		{method: "PATCH", wantStatus: http.StatusNoContent},
		{method: "DELETE", wantStatus: http.StatusNoContent},
		{method: "CONNECT", wantStatus: http.StatusNoContent},
		{method: "OPTIONS", wantStatus: http.StatusNoContent},
		{method: "TRACE", wantStatus: http.StatusNoContent},
		{method: "CUSTOM1", wantStatus: http.StatusMethodNotAllowed},
		{method: "CUSTOM2", wantStatus: http.StatusMethodNotAllowed},
		{method: "CUSTOM1", wantStatus: http.StatusMethodNotAllowed},
		{method: "get", wantStatus: http.StatusMethodNotAllowed},
	}
	for _, request := range requests {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(request.method, "/orders", nil))
		assert.Equal(t, request.wantStatus, response.Code, request.method)
	}

	wantCounts := map[string]float64{
		"GET": 1, "HEAD": 1, "POST": 1, "PUT": 1, "PATCH": 1,
		"DELETE": 1, "CONNECT": 1, "OPTIONS": 1, "TRACE": 1, "OTHER": 4,
	}
	families, err := metrics.registry.Gather()
	require.NoError(t, err)
	checkedFamilies := 0
	for _, family := range families {
		name := family.GetName()
		if name != "gophermart_http_requests_total" && name != "gophermart_http_request_duration_seconds" {
			continue
		}
		checkedFamilies++
		assert.Equal(t, len(wantCounts), len(family.Metric), name)
		gotCounts := make(map[string]float64)
		for _, metric := range family.Metric {
			var method string
			for _, label := range metric.Label {
				if label.GetName() == "method" {
					method = label.GetValue()
				}
			}
			if name == "gophermart_http_requests_total" {
				gotCounts[method] = metric.GetCounter().GetValue()
			} else {
				gotCounts[method] = float64(metric.GetHistogram().GetSampleCount())
			}
		}
		assert.Equal(t, wantCounts, gotCounts, name)
	}
	assert.Equal(t, 2, checkedFamilies)

	entries := logs.FilterMessage("HTTP request").All()
	require.Len(t, entries, len(requests))
	for i, entry := range entries {
		assert.Equal(t, requests[i].method, entry.ContextMap()["method"])
	}
}

// Проверяет, что panic превращается в HTTP 500 и попадает в лог со stack trace.
func TestRecoverer(t *testing.T) {
	core, logs := observer.New(zapcore.ErrorLevel)
	logger := zap.New(core)
	handler := Recoverer(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panic", nil))

	require.Equal(t, http.StatusInternalServerError, response.Code)
	entries := logs.FilterMessage("HTTP panic recovered").All()
	require.Len(t, entries, 1)
	fields := entries[0].ContextMap()
	assert.Equal(t, "boom", fields["panic"])
	assert.NotEmpty(t, fields["stack"])
}

// Проверяет аварийное завершение уже начатого ответа без дописывания ложного HTTP 500.
func TestRecovererAbortsCommittedResponse(t *testing.T) {
	core, logs := observer.New(zapcore.ErrorLevel)
	logger := zap.New(core)
	response := httptest.NewRecorder()
	wrapped := middleware.NewWrapResponseWriter(response, 1)
	handler := Recoverer(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("partial"))
		panic("boom")
	}))

	assert.PanicsWithValue(t, http.ErrAbortHandler, func() {
		handler.ServeHTTP(wrapped, httptest.NewRequest(http.MethodGet, "/panic", nil))
	})

	assert.Equal(t, http.StatusAccepted, response.Code)
	assert.Equal(t, "partial", response.Body.String())
	assert.Len(t, logs.FilterMessage("HTTP panic recovered").All(), 1)
}
