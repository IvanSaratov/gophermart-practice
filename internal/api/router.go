package api

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/ivansaratov/gophermart-practice/internal/observability"
	"go.uber.org/zap"
)

const readinessTimeout = 2 * time.Second

// Проверяет готовность обязательных зависимостей обслуживать запросы.
type ReadinessCheck func(context.Context) error

// Собирает операционные и пользовательские HTTP-маршруты с общими middleware.
func NewRouter(logger *zap.Logger, metrics *observability.Metrics, readiness ReadinessCheck, authentication Authentication, orders Orders) http.Handler {
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(metrics.Middleware(logger))
	router.Use(observability.Recoverer(logger))
	authenticationAPI := authenticationHandlers{logger: logger, authentication: authentication}
	orderAPI := orderHandlers{logger: logger, orders: orders}

	router.Get("/health", health)
	router.Get("/ready", ready(logger, readiness))
	router.Get("/metrics", metrics.Handler().ServeHTTP)
	router.Post("/api/user/register", authenticationAPI.register)
	router.Post("/api/user/login", authenticationAPI.login)
	router.Group(func(router chi.Router) {
		router.Use(RequireAuthentication(authentication))
		router.Get("/api/user/orders", orderAPI.listOrders)
		router.Post("/api/user/orders", orderAPI.uploadOrder)
	})

	return router
}

// Сообщает, что процесс запущен и обрабатывает HTTP-запросы.
func health(w http.ResponseWriter, _ *http.Request) {
	writePlainText(w, http.StatusOK, "ok\n")
}

// Возвращает HTTP-обработчик, отражающий готовность внешних зависимостей.
func ready(logger *zap.Logger, readiness ReadinessCheck) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Ограничиваем контекст, чтобы зависимость могла прервать ожидание по deadline.
		readinessCtx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
		defer cancel()

		if err := readiness(readinessCtx); err != nil {
			logger.Warn("Readiness check failed", zap.Error(err))
			writePlainText(w, http.StatusServiceUnavailable, "not ready\n")
			return
		}

		writePlainText(w, http.StatusOK, "ok\n")
	}
}

// Записывает текстовый HTTP-ответ с явным Content-Type и статусом.
func writePlainText(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
