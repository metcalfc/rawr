package ui

import "context"

type refreshResult[T any] struct {
	generation uint64
	value      T
	err        error
}

// Refresh runs data requests without blocking the UI. Start cancels the previous
// request; Poll discards late results from superseded requests. Start, Poll,
// Loading and Close belong to the UI goroutine. Load functions must honor ctx
// and must not mutate view state; they return immutable data instead.
type Refresh[T any] struct {
	wake       func()
	results    chan refreshResult[T]
	cancel     context.CancelFunc
	generation uint64
	loading    bool
}

// NewRefresh accepts a worker-safe, nonblocking wake function. Canvas.Wake is
// suitable; nil is useful when a caller polls without running the UI loop.
func NewRefresh[T any](wake func()) *Refresh[T] {
	return &Refresh[T]{wake: wake, results: make(chan refreshResult[T], 4)}
}
func (r *Refresh[T]) Start(ctx context.Context, load func(context.Context) (T, error)) {
	if r.cancel != nil {
		r.cancel()
	}
	ctx, cancelWork := context.WithCancel(ctx)
	// Superseding/closing suppresses delivery, whereas a caller's deadline
	// delivers its cancellation error so the latest request stops loading.
	delivery, cancelDelivery := context.WithCancel(context.Background())
	r.cancel = func() { cancelWork(); cancelDelivery() }
	r.generation++
	generation := r.generation
	r.loading = true
	go func() {
		defer cancelWork()
		value, err := load(ctx)
		if err == nil && ctx.Err() != nil {
			err = ctx.Err()
		}
		select {
		case r.results <- refreshResult[T]{generation: generation, value: value, err: err}:
			if r.wake != nil {
				r.wake()
			}
		case <-delivery.Done():
		}
	}()
}
func (r *Refresh[T]) Loading() bool { return r.loading }

// Poll returns the latest completed request, its error, and whether a result was
// available. A failed request is delivered normally so callers can keep old data.
func (r *Refresh[T]) Poll() (value T, err error, ready bool) {
	for {
		select {
		case result := <-r.results:
			if result.generation != r.generation {
				continue
			}
			r.loading = false
			value, err, ready = result.value, result.err, true
		default:
			return
		}
	}
}
func (r *Refresh[T]) Close() {
	if r.cancel != nil {
		r.cancel()
	}
	r.generation++
	r.loading = false
}
