package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ivansaratov/gophermart-practice/internal/bonus"
	"github.com/ivansaratov/gophermart-practice/internal/order"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type orderServiceStub struct {
	result order.UploadResult
	err    error
	called bool
	userID int64
	number string

	listedOrders []order.Order
	listErr      error
	listCalled   bool
	listUserID   int64
}

// Запоминает переданный заказ и возвращает настроенный доменный результат.
func (s *orderServiceStub) Upload(_ context.Context, userID int64, number string) (order.UploadResult, error) {
	s.called = true
	s.userID = userID
	s.number = number
	return s.result, s.err
}

// Запоминает пользователя и возвращает настроенный список заказов.
func (s *orderServiceStub) List(_ context.Context, userID int64) ([]order.Order, error) {
	s.listCalled = true
	s.listUserID = userID
	return s.listedOrders, s.listErr
}

// Проверяет полное отображение доменных результатов загрузки в HTTP-статусы.
func TestOrderUploadMapsResults(t *testing.T) {
	tests := []struct {
		name       string
		result     order.UploadResult
		err        error
		wantStatus int
	}{
		{name: "created", result: order.UploadCreated, wantStatus: http.StatusAccepted},
		{name: "same owner", result: order.UploadAlreadyOwned, wantStatus: http.StatusOK},
		{name: "malformed", err: order.ErrInvalidFormat, wantStatus: http.StatusBadRequest},
		{name: "another owner", err: order.ErrOwnedByAnotherUser, wantStatus: http.StatusConflict},
		{name: "invalid checksum", err: order.ErrInvalidNumber, wantStatus: http.StatusUnprocessableEntity},
		{name: "internal", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upload := &orderServiceStub{result: tt.result, err: tt.err}
			router := newTestRouterWithOrders(authenticationStub{userID: 42}, upload)
			request := httptest.NewRequest(http.MethodPost, "/api/user/orders", strings.NewReader("12345678903"))
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "signed-token"})
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			assert.Equal(t, tt.wantStatus, response.Code)
			assert.Empty(t, response.Body.String())
			require.True(t, upload.called)
			assert.Equal(t, int64(42), upload.userID)
			assert.Equal(t, "12345678903", upload.number)
		})
	}
}

// Проверяет, что маршрут не вызывает бизнес-логику без действующей сессии.
func TestOrderUploadRequiresAuthentication(t *testing.T) {
	upload := &orderServiceStub{}
	router := newTestRouterWithOrders(authenticationStub{}, upload)

	response := performBodyRequest(router, http.MethodPost, "/api/user/orders", "12345678903")

	assert.Equal(t, http.StatusUnauthorized, response.Code)
	assert.Empty(t, response.Body.String())
	assert.False(t, upload.called)
}

// Проверяет ограничение памяти до передачи тела бизнес-логике.
func TestOrderUploadRejectsOversizedBody(t *testing.T) {
	upload := &orderServiceStub{}
	router := newTestRouterWithOrders(authenticationStub{userID: 42}, upload)
	request := httptest.NewRequest(http.MethodPost, "/api/user/orders", strings.NewReader(strings.Repeat("1", (64<<10)+1)))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "signed-token"})
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Empty(t, response.Body.String())
	assert.False(t, upload.called)
}

// Проверяет JSON списка, пустой результат и безопасное отображение ошибки.
func TestOrderListMapsResults(t *testing.T) {
	newer := time.Date(2026, time.September, 6, 12, 1, 0, 0, time.UTC)
	older := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		orders      []order.Order
		err         error
		wantStatus  int
		wantJSON    string
		contentType string
	}{
		{
			name: "orders",
			orders: []order.Order{
				{Number: "12345678903", Status: order.StatusProcessing, UploadedAt: newer},
				{Number: "9278923470", Status: order.StatusNew, UploadedAt: older},
			},
			wantStatus: http.StatusOK,
			wantJSON: `[
				{"number":"12345678903","status":"PROCESSING","uploaded_at":"2026-09-06T12:01:00Z"},
				{"number":"9278923470","status":"NEW","uploaded_at":"2026-09-06T12:00:00Z"}
			]`,
			contentType: "application/json",
		},
		{name: "empty", orders: []order.Order{}, wantStatus: http.StatusNoContent},
		{name: "internal", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orders := &orderServiceStub{listedOrders: tt.orders, listErr: tt.err}
			router := newTestRouterWithOrders(authenticationStub{userID: 42}, orders)
			request := httptest.NewRequest(http.MethodGet, "/api/user/orders", nil)
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "signed-token"})
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			assert.Equal(t, tt.wantStatus, response.Code)
			assert.Equal(t, tt.contentType, response.Header().Get("Content-Type"))
			if tt.wantJSON == "" {
				assert.Empty(t, response.Body.String())
			} else {
				assert.JSONEq(t, tt.wantJSON, response.Body.String())
				assert.NotContains(t, response.Body.String(), `"accrual"`)
			}
			require.True(t, orders.listCalled)
			assert.Equal(t, int64(42), orders.listUserID)
		})
	}
}

// Проверяет защиту нового маршрута до обращения к сервису заказов.
func TestOrderListRequiresAuthentication(t *testing.T) {
	orders := &orderServiceStub{}
	router := newTestRouterWithOrders(authenticationStub{}, orders)

	response := performRequest(router, http.MethodGet, "/api/user/orders")

	assert.Equal(t, http.StatusUnauthorized, response.Code)
	assert.Empty(t, response.Body.String())
	assert.False(t, orders.listCalled)
}

// Проверяет точное число баллов в JSON и отличие отсутствующего начисления от нуля.
func TestOrderListAccrual(t *testing.T) {
	tests := []struct {
		name   string
		status order.Status
		amount *bonus.Amount
		want   string
	}{
		{name: "new", status: order.StatusNew},
		{name: "processing", status: order.StatusProcessing},
		{name: "invalid", status: order.StatusInvalid},
		{name: "processed without accrual", status: order.StatusProcessed},
		{name: "zero", status: order.StatusProcessed, amount: new(bonus.Amount(0)), want: "0"},
		{name: "fraction", status: order.StatusProcessed, amount: new(bonus.Amount(50050)), want: "500.5"},
		{name: "hundredth", status: order.StatusProcessed, amount: new(bonus.Amount(1)), want: "0.01"},
		{name: "whole", status: order.StatusProcessed, amount: new(bonus.Amount(4200)), want: "42"},
		{name: "large exact value", status: order.StatusProcessed, amount: new(bonus.Amount(9223372036854775807)), want: "92233720368547758.07"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orders := &orderServiceStub{listedOrders: []order.Order{{Number: "12345678903", Status: tt.status, Accrual: tt.amount}}}
			router := newTestRouterWithOrders(authenticationStub{userID: 42}, orders)
			request := httptest.NewRequest(http.MethodGet, "/api/user/orders", nil)
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "signed-token"})
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			require.Equal(t, http.StatusOK, response.Code)
			// Читаем исходное JSON-число: float64 мог бы скрыть потерю точности у больших сумм.
			var body []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			require.Len(t, body, 1)
			assert.Equal(t, tt.want, string(body[0]["accrual"]))
			assert.Equal(t, `"`+string(tt.status)+`"`, string(body[0]["status"]))
		})
	}
}
