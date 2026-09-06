package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivansaratov/gophermart-practice/internal/order"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type orderUploadStub struct {
	result order.UploadResult
	err    error
	called bool
	userID int64
	number string
}

// Запоминает переданный заказ и возвращает настроенный доменный результат.
func (s *orderUploadStub) Upload(_ context.Context, userID int64, number string) (order.UploadResult, error) {
	s.called = true
	s.userID = userID
	s.number = number
	return s.result, s.err
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
			upload := &orderUploadStub{result: tt.result, err: tt.err}
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
	upload := &orderUploadStub{}
	router := newTestRouterWithOrders(authenticationStub{}, upload)

	response := performBodyRequest(router, http.MethodPost, "/api/user/orders", "12345678903")

	assert.Equal(t, http.StatusUnauthorized, response.Code)
	assert.Empty(t, response.Body.String())
	assert.False(t, upload.called)
}

// Проверяет ограничение памяти до передачи тела бизнес-логике.
func TestOrderUploadRejectsOversizedBody(t *testing.T) {
	upload := &orderUploadStub{}
	router := newTestRouterWithOrders(authenticationStub{userID: 42}, upload)
	request := httptest.NewRequest(http.MethodPost, "/api/user/orders", strings.NewReader(strings.Repeat("1", (64<<10)+1)))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "signed-token"})
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Empty(t, response.Body.String())
	assert.False(t, upload.called)
}
