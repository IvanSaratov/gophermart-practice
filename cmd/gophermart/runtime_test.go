package main

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"
)

// Проверяет отмену обоих компонентов и ожидание воркера.
func TestRunRuntimeCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		workerStopped := make(chan struct{})
		releaseWorker := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- runRuntime(ctx,
				func(ctx context.Context) error { <-ctx.Done(); <-releaseWorker; close(workerStopped); return ctx.Err() },
				func(ctx context.Context) error { <-ctx.Done(); return nil },
			)
		}()
		synctest.Wait()
		cancel()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("runtime returned before worker stopped")
		default:
		}
		close(releaseWorker)
		synctest.Wait()
		require.NoError(t, <-done)
		select {
		case <-workerStopped:
		default:
			t.Fatal("worker still running")
		}
	})
}

// Проверяет, что ошибка любого компонента останавливает второй.
func TestRunRuntimeErrors(t *testing.T) {
	failure := errors.New("component failed")
	for _, workerFails := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			stopped := false
			wait := func(ctx context.Context) error { <-ctx.Done(); stopped = true; return ctx.Err() }
			fail := func(context.Context) error { return failure }
			worker, server := wait, fail
			if workerFails {
				worker, server = fail, wait
			}
			require.ErrorIs(t, runRuntime(context.Background(), worker, server), failure)
			require.True(t, stopped)
		})
	}
}

// Проверяет, что отменённый запуск не начинает работу.
func TestRunRuntimeAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	run := func(context.Context) error { called = true; return nil }
	require.NoError(t, runRuntime(ctx, run, run))
	require.False(t, called)
}
