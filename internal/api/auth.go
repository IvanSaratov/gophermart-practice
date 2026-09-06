package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/ivansaratov/gophermart-practice/internal/auth"
	"go.uber.org/zap"
)

const (
	sessionCookieName      = "gophermart_session"
	maxCredentialsBodySize = 64 << 10
)

// Ограничивает HTTP-слой операциями, необходимыми для учётных данных и сессий.
type Authentication interface {
	Register(context.Context, string, string) (auth.Session, error)
	Login(context.Context, string, string) (auth.Session, error)
	Authenticate(string) (int64, error)
}

type credentialsRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

type authenticationHandlers struct {
	logger         *zap.Logger
	authentication Authentication
}

// Регистрирует пользователя, открывает сессию и переводит доменные ошибки в HTTP-статусы.
func (h authenticationHandlers) register(w http.ResponseWriter, r *http.Request) {
	credentials, err := decodeCredentials(w, r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	session, err := h.authentication.Register(r.Context(), credentials.Login, credentials.Password)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidInput):
			w.WriteHeader(http.StatusBadRequest)
		case errors.Is(err, auth.ErrLoginTaken):
			w.WriteHeader(http.StatusConflict)
		default:
			h.logger.Error("User registration failed", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}

	setSessionCookie(w, session)
	w.WriteHeader(http.StatusOK)
}

// Проверяет учётные данные, открывает сессию и скрывает причину ошибки авторизации.
func (h authenticationHandlers) login(w http.ResponseWriter, r *http.Request) {
	credentials, err := decodeCredentials(w, r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	session, err := h.authentication.Login(r.Context(), credentials.Login, credentials.Password)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidInput):
			w.WriteHeader(http.StatusBadRequest)
		case errors.Is(err, auth.ErrInvalidCredentials):
			w.WriteHeader(http.StatusUnauthorized)
		default:
			h.logger.Error("User login failed", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}

	setSessionCookie(w, session)
	w.WriteHeader(http.StatusOK)
}

// Декодирует ровно один JSON-объект и отклоняет неизвестные поля и хвостовые данные.
func decodeCredentials(w http.ResponseWriter, r *http.Request) (credentialsRequest, error) {
	var credentials credentialsRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxCredentialsBodySize)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&credentials); err != nil {
		return credentialsRequest{}, err
	}

	// Второе значение, включая `null`, означает, что тело не является одним объектом.
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return credentialsRequest{}, errors.New("request body must contain one JSON object")
	}

	return credentials, nil
}

// Устанавливает недоступную JavaScript cookie на тот же срок, что и JWT.
func setSessionCookie(w http.ResponseWriter, session auth.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    session.Token,
		Path:     "/",
		Expires:  session.ExpiresAt,
		MaxAge:   int(auth.SessionTTL / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

type userIDContextKey struct{}

// Защищает обработчик cookie и передаёт ему идентификатор пользователя.
func RequireAuthentication(authentication Authentication) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(sessionCookieName)
			if err != nil || cookie.Value == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			userID, err := authentication.Authenticate(cookie.Value)
			if err != nil {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), userIDContextKey{}, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Извлекает идентификатор, добавленный middleware после успешной проверки сессии.
func UserID(ctx context.Context) (int64, bool) {
	userID, ok := ctx.Value(userIDContextKey{}).(int64)
	return userID, ok
}
