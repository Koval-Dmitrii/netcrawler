package crawler

import (
	"context"
	"sync"
)

func Run[T any](
	ctx context.Context,
	workersCount int,
	input <-chan T,
	f func(e T),
) {
	wg := new(sync.WaitGroup)

	for i := 0; i < workersCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for {
				select {
				case <-ctx.Done():
					return
				case v, ok := <-input:
					if !ok {
						return
					}

					select {
					case <-ctx.Done():
						return
					default:
						f(v)
					}
				}
			}
		}()
	}

	wg.Wait()
}
