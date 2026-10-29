package agentkernel

import (
	"context"
	"errors"
	"sync"
)

var errCallbackPanic = errors.New("agent kernel callback panicked")

// callbackExecutor bounds callbacks that may outlive a cancelled waiter. Slots
// belong to the actual callback, not the waiter, and are released only on exit.
// A shared Engine therefore cannot accumulate unbounded abandoned callbacks.
type callbackExecutor struct{ slots chan struct{} }

func invokeCallback[T any](ctx context.Context, executor *callbackExecutor, call func() (T, error), discard func(T)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	select {
	case executor.slots <- struct{}{}:
	default:
		return zero, ErrCapacity
	}
	type returned struct {
		value T
		err   error
	}
	done := make(chan returned)
	var released sync.Once
	releaseSlot := func() { released.Do(func() { <-executor.slots }) }
	release := func(value T) {
		if discard != nil {
			defer func() { _ = recover() }()
			discard(value)
		}
	}
	go func() {
		defer releaseSlot()
		result := func() (result returned) {
			defer func() {
				if recover() != nil {
					result.value, result.err = zero, errCallbackPanic
				}
			}()
			if err := ctx.Err(); err != nil {
				return returned{err: err}
			}
			result.value, result.err = call()
			return result
		}()
		select {
		case done <- result:
		case <-ctx.Done():
			release(result.value)
		}
	}()
	select {
	case result := <-done:
		releaseSlot()
		if err := ctx.Err(); err != nil {
			release(result.value)
			return zero, err
		}
		if result.err != nil {
			release(result.value)
			return zero, result.err
		}
		return result.value, nil
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}
