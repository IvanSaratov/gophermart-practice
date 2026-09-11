package main

import (
	"context"
	"errors"
)

// Просто выносим отдельную функция запуска HTTP и worker слоя.
// Ожидаем так же завершение обоих.
func runRuntime(ctx context.Context, worker, server func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if ctx.Err() != nil {
		return nil
	}
	results := make(chan error, 2)
	for _, run := range []func(context.Context) error{worker, server} {
		go func() {
			err := run(ctx)
			// Штатная остановка проекта.
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				err = nil
			}
			cancel()
			results <- err
		}()
	}
	// Ждем обоих результатов, там ещё под капотом останавливается БД.
	first, second := <-results, <-results
	return errors.Join(first, second)
}
