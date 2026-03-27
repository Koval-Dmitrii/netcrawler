package crawler

import "golang.org/x/sync/singleflight"

type singleFlight[T any] interface {
	Do(key string, fn func() (T, error)) (v T, shared bool, err error)
}

var _ singleFlight[any] = (*singleFlightImpl[any])(nil)

type singleFlightImpl[T any] struct {
	sf singleflight.Group
}

func newSingleFlight[T any]() *singleFlightImpl[T] {
	return &singleFlightImpl[T]{}
}

func (s *singleFlightImpl[T]) Do(key string, fn func() (T, error)) (T, bool, error) {
	v, err, shared := s.sf.Do(key, func() (any, error) {
		return fn()
	})

	return v.(T), shared, err
}
