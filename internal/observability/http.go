package observability

import (
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

const unknownRoute = "unknown"

// Metrics хранит изолированный registry и HTTP-метрики одного экземпля приложения.
type Metrics struct {
	registry        *prometheus.Registry
	requestsTotal   *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
}

// Создаёт независимый registry с метриками.
func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()
	requestsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gophermart_http_requests_total",
		Help: "Total number of HTTP requests handled by Gophermart.",
	}, []string{"method", "route", "status"})
	requestDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "gophermart_http_request_duration_seconds",
		Help:    "Duration of HTTP requests handled by Gophermart.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})

	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		requestsTotal,
		requestDuration,
	)

	return &Metrics{
		registry:        registry,
		requestsTotal:   requestsTotal,
		requestDuration: requestDuration,
	}
}

// Возвращает HTTP-обработчик для выгрузки Prometheus-метрик.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// Добавляет к запросу структурный access log.
func (m *Metrics) Middleware(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			startedAt := time.Now()
			wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(wrapped, r)

			status := wrapped.Status()
			if status == 0 {
				status = http.StatusOK
			}
			route := chi.RouteContext(r.Context()).RoutePattern()
			if route == "" {
				// Фиксированная метка не позволяет неизвестным URL раздувать cardinality метрик.
				route = unknownRoute
			}
			elapsed := time.Since(startedAt)

			m.requestsTotal.WithLabelValues(r.Method, route, strconv.Itoa(status)).Inc()
			m.requestDuration.WithLabelValues(r.Method, route).Observe(elapsed.Seconds())
			logger.Info("HTTP request",
				zap.String("request_id", middleware.GetReqID(r.Context())),
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.String("route", route),
				zap.Int("status", status),
				zap.Duration("duration", elapsed),
			)
		})
	}
}

// Предотвращает завершение процесса из-за panic в HTTP-обработчике.
func Recoverer(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					if recovered == http.ErrAbortHandler {
						panic(recovered)
					}

					logger.Error("HTTP panic recovered",
						zap.Any("panic", recovered),
						zap.ByteString("stack", debug.Stack()),
					)
					if wrapped, ok := w.(interface{ Status() int }); ok && wrapped.Status() != 0 {
						// После отправки заголовков статус изменить нельзя: обрываем поток, не дописывая ложный HTTP 500.
						panic(http.ErrAbortHandler)
					}
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
