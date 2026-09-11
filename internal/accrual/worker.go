package accrual

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/ivansaratov/gophermart-practice/internal/bonus"
	"github.com/ivansaratov/gophermart-practice/internal/order"
	"go.uber.org/zap"
)

// Думаю для учебного проекта легче захардкодить
const (
	workerBatchSize    = 100
	workerPollInterval = time.Second
	workerRetryDelay   = time.Second
	workerMaxAttempts  = 3
)

// Запрос к системе начислений.
type OrderClient interface {
	GetOrder(context.Context, string) (Result, error)
}

// Чтение очереди и атомарное сохранение результата.
type WorkerStore interface {
	PendingOrders(context.Context, string, int) ([]string, error)
	ApplyAccrual(context.Context, string, order.Status, *bonus.Amount) error
}

type Worker struct {
	client OrderClient
	store  WorkerStore
	logger *zap.Logger
}

func NewWorker(client OrderClient, store WorkerStore, logger *zap.Logger) *Worker {
	return &Worker{client: client, store: store, logger: logger}
}

func (w *Worker) Run(ctx context.Context) error {
	after := ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		numbers, err := w.store.PendingOrders(ctx, after, workerBatchSize)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			w.logger.Warn("Read accrual queue failed", zap.Error(err))
		} else {
			for _, number := range numbers {
				if err := ctx.Err(); err != nil {
					return err
				}
				w.processOrder(ctx, number)
				after = number
			}
			// После последней порции начинаем заново.
			if len(numbers) < workerBatchSize {
				after = ""
			}
		}
		// Добавляем некоторую паузу что бы вечно не гонять, у нас все такие не супер нагруженная система.
		if err := waitWorker(ctx, workerPollInterval); err != nil {
			return err
		}
	}
}

// Делает ограниченное число попыток для заказа.
func (w *Worker) processOrder(ctx context.Context, number string) {
	for attempt := 0; attempt < workerMaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return
		}
		result, err := w.client.GetOrder(ctx, number)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			var status order.Status
			switch result.Status {
			case StatusRegistered, StatusProcessing:
				status = order.StatusProcessing
			case StatusInvalid:
				status = order.StatusInvalid
			case StatusProcessed:
				status = order.StatusProcessed
			default:
				w.logger.Warn("Unknown accrual status", zap.String("order", number))
				return
			}
			// Выполняем только для полностью законченного расчета.
			var amount *bonus.Amount
			if status == order.StatusProcessed {
				amount = result.Accrual
			}
			if err := w.store.ApplyAccrual(ctx, number, status, amount); err != nil && ctx.Err() == nil {
				w.logger.Warn("Save accrual result failed", zap.String("order", number), zap.Error(err))
			}
			return
		}
		if errors.Is(err, ErrNotRegistered) {
			return
		}
		w.logger.Warn("Request accrual failed", zap.String("order", number), zap.Error(err))
		var httpErr *HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusTooManyRequests {
			delay := httpErr.RetryAfter
			if delay <= 0 {
				delay = workerRetryDelay
			}
			// Некоторая имплементация retry.
			if waitWorker(ctx, delay) != nil {
				return
			}
			continue
		}
		var networkErr net.Error
		retryable := errors.As(err, &networkErr) || (httpErr != nil && httpErr.StatusCode >= 500 && httpErr.StatusCode <= 599)
		if !retryable || attempt == workerMaxAttempts-1 {
			return
		}
		if waitWorker(ctx, workerRetryDelay) != nil {
			return
		}
	}
}

// Ждёт заданное время, но сразу прекращает ожидание при отмене.
func waitWorker(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}
