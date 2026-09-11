package accrual

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivansaratov/gophermart-practice/internal/bonus"
	"github.com/ivansaratov/gophermart-practice/internal/order"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type workerClientFunc func(context.Context, string) (Result, error)

// Возвращает ответ фейкового сервиса для текущей попытки.
func (f workerClientFunc) GetOrder(ctx context.Context, number string) (Result, error) {
	return f(ctx, number)
}

type workerStoreStub struct {
	pending func(context.Context, string, int) ([]string, error)
	apply   func(context.Context, string, order.Status, *bonus.Amount) error
}

// Возвращает настроенную порцию очереди.
func (s workerStoreStub) PendingOrders(ctx context.Context, after string, limit int) ([]string, error) {
	return s.pending(ctx, after, limit)
}

// Проверяет результат, который обработчик передал хранилищу.
func (s workerStoreStub) ApplyAccrual(ctx context.Context, n string, status order.Status, a *bonus.Amount) error {
	return s.apply(ctx, n, status, a)
}

// Проверяет переход от ещё неизвестного заказа к завершению и сохранение точного начисления.
func TestWorkerOrderLifecycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		var statuses []order.Status
		store := workerStoreStub{
			pending: func(_ context.Context, after string, limit int) ([]string, error) {
				require.Empty(t, after)
				require.Positive(t, limit)
				return []string{"123"}, nil
			},
			apply: func(_ context.Context, n string, s order.Status, a *bonus.Amount) error {
				require.Equal(t, "123", n)
				statuses = append(statuses, s)
				if s == order.StatusProcessed {
					require.Equal(t, new(bonus.Amount(50050)), a)
					cancel()
				} else {
					require.Nil(t, a)
				}
				return nil
			},
		}
		client := workerClientFunc(func(context.Context, string) (Result, error) {
			calls++
			switch calls {
			case 1:
				return Result{}, ErrNotRegistered
			case 2:
				return Result{Number: "123", Status: StatusRegistered}, nil
			case 3:
				return Result{Number: "123", Status: StatusProcessing}, nil
			default:
				return Result{Number: "123", Status: StatusProcessed, Accrual: new(bonus.Amount(50050))}, nil
			}
		})
		require.ErrorIs(t, NewWorker(client, store, zap.NewNop()).Run(ctx), context.Canceled)
		require.Equal(t, 4, calls)
		require.Equal(t, []order.Status{order.StatusProcessing, order.StatusProcessing, order.StatusProcessed}, statuses)
	})
}

// Проверяет паузу всего опроса после ограничения сервиса и отмену ожидания.
func TestWorkerRateLimit(t *testing.T) {
	for _, delay := range []time.Duration{0, 7 * time.Second} {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			start := time.Now()
			store := workerStoreStub{pending: func(context.Context, string, int) ([]string, error) { return []string{"123", "456"}, nil }}
			client := workerClientFunc(func(context.Context, string) (Result, error) {
				calls++
				return Result{}, &HTTPError{StatusCode: http.StatusTooManyRequests, RetryAfter: delay}
			})
			done := make(chan error, 1)
			go func() { done <- NewWorker(client, store, zap.NewNop()).Run(ctx) }()
			synctest.Wait()
			require.Equal(t, 1, calls)
			if delay > 0 {
				time.Sleep(delay - time.Nanosecond)
				synctest.Wait()
				require.Equal(t, 1, calls)
			}
			cancel()
			synctest.Wait()
			require.ErrorIs(t, <-done, context.Canceled)
			require.Less(t, time.Since(start), 7*time.Second)
		})
	}
}

// Проверяет, что ограниченные повторы не мешают следующему заказу из порции.
func TestWorkerRetriesAreBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		store := workerStoreStub{
			pending: func(context.Context, string, int) ([]string, error) { return []string{"123", "456"}, nil },
			apply: func(_ context.Context, n string, s order.Status, a *bonus.Amount) error {
				require.Equal(t, "456", n)
				require.Equal(t, order.StatusInvalid, s)
				require.Nil(t, a)
				cancel()
				return nil
			},
		}
		client := workerClientFunc(func(_ context.Context, n string) (Result, error) {
			if n == "123" {
				calls++
				return Result{}, &HTTPError{StatusCode: 500}
			}
			return Result{Number: n, Status: StatusInvalid}, nil
		})
		require.ErrorIs(t, NewWorker(client, store, zap.NewNop()).Run(ctx), context.Canceled)
		require.Equal(t, 3, calls)
	})
}

// Проверяет продолжение обхода курсором и повторный обход после конца очереди.
func TestWorkerCursorAndRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for range 2 {
			ctx, cancel := context.WithCancel(context.Background())
			pages := 0
			store := workerStoreStub{pending: func(_ context.Context, after string, limit int) ([]string, error) {
				pages++
				switch pages {
				case 1:
					require.Empty(t, after)
					numbers := make([]string, limit)
					for i := range numbers {
						numbers[i] = strconv.Itoa(i + 1)
					}
					return numbers, nil
				case 2:
					require.Equal(t, "100", after)
					return []string{"456"}, nil
				default:
					require.Empty(t, after)
					cancel()
					return nil, nil
				}
			}}
			client := workerClientFunc(func(context.Context, string) (Result, error) { return Result{}, ErrNotRegistered })
			require.ErrorIs(t, NewWorker(client, store, zap.NewNop()).Run(ctx), context.Canceled)
			require.Equal(t, 3, pages)
			cancel()
		}
	})
}

// Проверяет восстановление после ошибок чтения очереди и сохранения результата.
func TestWorkerDatabaseErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		reads, writes := 0, 0
		store := workerStoreStub{
			pending: func(context.Context, string, int) ([]string, error) {
				reads++
				if reads == 1 {
					return nil, errors.New("read failed")
				}
				return []string{"123"}, nil
			},
			apply: func(context.Context, string, order.Status, *bonus.Amount) error {
				writes++
				if writes == 2 {
					cancel()
				}
				return errors.New("write failed")
			},
		}
		client := workerClientFunc(func(context.Context, string) (Result, error) {
			return Result{Number: "123", Status: StatusProcessed}, nil
		})
		require.ErrorIs(t, NewWorker(client, store, zap.NewNop()).Run(ctx), context.Canceled)
		require.Equal(t, 3, reads)
		require.Equal(t, 2, writes)
	})
}

// Проверяет паузу после каждой 429, включая переход к другому заказу после последней попытки.
func TestWorkerRateLimitDelaysNextOrder(t *testing.T) {
	for _, delay := range []time.Duration{0, 7 * time.Second} {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			start := time.Now()
			var times []time.Duration
			var numbers []string
			store := workerStoreStub{pending: func(context.Context, string, int) ([]string, error) { return []string{"123", "456"}, nil }}
			client := workerClientFunc(func(_ context.Context, n string) (Result, error) {
				times = append(times, time.Since(start))
				numbers = append(numbers, n)
				if n == "456" {
					cancel()
					return Result{}, ErrNotRegistered
				}
				return Result{}, &HTTPError{StatusCode: 429, RetryAfter: delay}
			})
			require.ErrorIs(t, NewWorker(client, store, zap.NewNop()).Run(ctx), context.Canceled)
			step := delay
			if step == 0 {
				step = time.Second
			}
			require.Equal(t, []time.Duration{0, step, 2 * step, 3 * step}, times)
			require.Equal(t, []string{"123", "123", "123", "456"}, numbers)
		})
	}
}

// Проверяет повтор сетевой ошибки.
func TestWorkerClientErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want int
	}{
		{name: "network timeout", err: context.DeadlineExceeded, want: 3},
		{name: "invalid response", err: ErrInvalidResponse, want: 1},
		{name: "not found", err: &HTTPError{StatusCode: 404}, want: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				calls := 0
				store := workerStoreStub{pending: func(context.Context, string, int) ([]string, error) { return []string{"123", "456"}, nil }}
				client := workerClientFunc(func(_ context.Context, n string) (Result, error) {
					if n == "456" {
						cancel()
						return Result{}, ErrNotRegistered
					}
					calls++
					return Result{}, tt.err
				})
				require.ErrorIs(t, NewWorker(client, store, zap.NewNop()).Run(ctx), context.Canceled)
				require.Equal(t, tt.want, calls)
			})
		})
	}
}

// Проверяет передачу отмены текущему запросу и отсутствие новых запросов после остановки.
func TestWorkerCancelsRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		store := workerStoreStub{pending: func(context.Context, string, int) ([]string, error) { return []string{"123", "456"}, nil }}
		client := workerClientFunc(func(ctx context.Context, _ string) (Result, error) { calls++; <-ctx.Done(); return Result{}, ctx.Err() })
		done := make(chan error, 1)
		go func() { done <- NewWorker(client, store, zap.NewNop()).Run(ctx) }()
		synctest.Wait()
		require.Equal(t, 1, calls)
		cancel()
		synctest.Wait()
		require.ErrorIs(t, <-done, context.Canceled)
		require.Equal(t, 1, calls)
	})
}

// Проверяет, что пустая очередь не создаёт непрерывные запросы к базе.
func TestWorkerEmptyQueueWaits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls atomic.Int64
		store := workerStoreStub{pending: func(context.Context, string, int) ([]string, error) { calls.Add(1); return nil, nil }}
		done := make(chan error, 1)
		go func() { done <- NewWorker(nil, store, zap.NewNop()).Run(ctx) }()
		synctest.Wait()
		require.Equal(t, int64(1), calls.Load())
		time.Sleep(time.Second)
		synctest.Wait()
		require.Equal(t, int64(2), calls.Load())
		cancel()
		synctest.Wait()
		require.ErrorIs(t, <-done, context.Canceled)
	})
}
