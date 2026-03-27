package crawler

import "context"

func Generate[T, R any](ctx context.Context, data []T, f func(index int, e T) R, size int) <-chan R {
	result := make(chan R, size)

	go func() {
		defer close(result)

		for i, v := range data {
			select {
			case <-ctx.Done():
				return
			case result <- f(i, v):
			}
		}
	}()

	return result
}
