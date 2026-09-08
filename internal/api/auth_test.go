package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ivansaratov/gophermart-practice/internal/auth"
	"github.com/ivansaratov/gophermart-practice/internal/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type authenticationStub struct {
	registerSession auth.Session
	registerErr     error
	loginSession    auth.Session
	loginErr        error
	userID          int64
	authenticateErr error
}

// Возвращает настроенный результат регистрации для проверки HTTP-контракта.
func (s authenticationStub) Register(context.Context, string, string) (auth.Session, error) {
	return s.registerSession, s.registerErr
}

// Возвращает настроенный результат входа для проверки HTTP-контракта.
func (s authenticationStub) Login(context.Context, string, string) (auth.Session, error) {
	return s.loginSession, s.loginErr
}

// Возвращает настроенный результат проверки сессии для middleware.
func (s authenticationStub) Authenticate(string) (int64, error) {
	return s.userID, s.authenticateErr
}

// Проверяет успешную регистрацию и безопасные атрибуты cookie сессии.
func TestRegisterSetsSessionCookie(t *testing.T) {
	expiresAt := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	router := newTestRouter(authenticationStub{
		registerSession: auth.Session{Token: "signed-token", ExpiresAt: expiresAt},
	})

	response := performBodyRequest(router, http.MethodPost, "/api/user/register", `{"login":"alice","password":"secret"}`)

	require.Equal(t, http.StatusOK, response.Code)
	assert.Empty(t, response.Body.String())
	cookies := response.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, "gophermart_session", cookies[0].Name)
	assert.Equal(t, "signed-token", cookies[0].Value)
	assert.Equal(t, "/", cookies[0].Path)
	assert.Equal(t, expiresAt, cookies[0].Expires)
	assert.Equal(t, 24*60*60, cookies[0].MaxAge)
	assert.True(t, cookies[0].HttpOnly)
	assert.False(t, cookies[0].Secure)
	assert.Equal(t, http.SameSiteLaxMode, cookies[0].SameSite)
}

// Проверяет перевод ошибок регистрации в предусмотренные спецификацией статусы.
func TestRegisterMapsErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid credentials", err: auth.ErrInvalidInput, wantStatus: http.StatusBadRequest},
		{name: "login taken", err: auth.ErrLoginTaken, wantStatus: http.StatusConflict},
		{name: "internal", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := newTestRouter(authenticationStub{registerErr: tt.err})

			response := performBodyRequest(router, http.MethodPost, "/api/user/register", `{"login":"alice","password":"secret"}`)

			assert.Equal(t, tt.wantStatus, response.Code)
			assert.Empty(t, response.Body.String())
		})
	}
}

// Проверяет отказ от JSON, который не является единственным ожидаемым объектом.
func TestRegisterRejectsInvalidJSON(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed", body: `{`},
		{name: "trailing object", body: `{"login":"alice","password":"secret"} {}`},
		{name: "unknown field", body: `{"login":"alice","password":"secret","role":"admin"}`},
		{name: "wrong field type", body: `{"login":7,"password":"secret"}`},
		{name: "oversized", body: `{"login":"` + strings.Repeat("a", 1024*1024) + `","password":"secret"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := performBodyRequest(newTestRouter(authenticationStub{}), http.MethodPost, "/api/user/register", tt.body)

			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.Empty(t, response.Body.String())
		})
	}
}

// Проверяет перевод результатов входа в успешный ответ, 400, 401 и 500.
func TestLoginMapsResults(t *testing.T) {
	expiresAt := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name       string
		session    auth.Session
		err        error
		wantStatus int
		wantCookie bool
	}{
		{
			name:       "success",
			session:    auth.Session{Token: "signed-token", ExpiresAt: expiresAt},
			wantStatus: http.StatusOK,
			wantCookie: true,
		},
		{name: "invalid input", err: auth.ErrInvalidInput, wantStatus: http.StatusBadRequest},
		{name: "wrong credentials", err: auth.ErrInvalidCredentials, wantStatus: http.StatusUnauthorized},
		{name: "internal", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := newTestRouter(authenticationStub{loginSession: tt.session, loginErr: tt.err})

			response := performBodyRequest(router, http.MethodPost, "/api/user/login", `{"login":"alice","password":"secret"}`)

			assert.Equal(t, tt.wantStatus, response.Code)
			assert.Empty(t, response.Body.String())
			assert.Equal(t, tt.wantCookie, len(response.Result().Cookies()) == 1)
		})
	}
}

// Проверяет отказ без доверенной cookie и передачу идентификатора защищённому обработчику.
func TestRequireAuthentication(t *testing.T) {
	tests := []struct {
		name       string
		cookie     *http.Cookie
		service    authenticationStub
		wantStatus int
		wantUserID int64
	}{
		{name: "missing cookie", wantStatus: http.StatusUnauthorized},
		{
			name:       "invalid token",
			cookie:     &http.Cookie{Name: "gophermart_session", Value: "invalid"},
			service:    authenticationStub{authenticateErr: auth.ErrInvalidToken},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "authenticated",
			cookie:     &http.Cookie{Name: "gophermart_session", Value: "signed-token"},
			service:    authenticationStub{userID: 73},
			wantStatus: http.StatusNoContent,
			wantUserID: 73,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			protected := RequireAuthentication(tt.service)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				userID, ok := UserID(r.Context())
				require.True(t, ok)
				assert.Equal(t, tt.wantUserID, userID)
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			if tt.cookie != nil {
				request.AddCookie(tt.cookie)
			}
			response := httptest.NewRecorder()

			protected.ServeHTTP(response, request)

			assert.Equal(t, tt.wantStatus, response.Code)
		})
	}
}

// Собирает роутер с исправными операционными зависимостями для HTTP-тестов.
func newTestRouter(authentication Authentication) http.Handler {
	return newTestRouterWithOrders(authentication, &orderServiceStub{})
}

// Собирает роутер с заданной бизнес-логикой заказов для HTTP-тестов.
func newTestRouterWithOrders(authentication Authentication, orders Orders) http.Handler {
	return NewRouter(zap.NewNop(), observability.NewMetrics(), func(context.Context) error {
		return nil
	}, authentication, orders)
}

// Выполняет HTTP-запрос с указанным текстовым телом без открытия сокета.
func performBodyRequest(handler http.Handler, method, target, body string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	return response
}
