package logging

import (
	"context"
	"fmt"
	"os"
	"sync"
)

// Observation is independent of output sinks: one event is emitted even when
// the logger writes both text and JSON. With loggers share the observer.
type Observation struct {
	Level   Level
	Message string
	Fields  []Field
}

type observationState struct {
	mu      sync.RWMutex
	observe func(context.Context, Observation)
}
type observedLogger struct {
	next   Logger
	state  *observationState
	fields []Field
}
type suppressObservationKey struct{}

func WithoutObservation(ctx context.Context) context.Context {
	return context.WithValue(ctx, suppressObservationKey{}, true)
}

func SetObserver(logger Logger, observer func(context.Context, Observation)) bool {
	l, ok := logger.(*observedLogger)
	if !ok {
		return false
	}
	l.state.mu.Lock()
	l.state.observe = observer
	l.state.mu.Unlock()
	return true
}

// CarryObserver retains the shared capture hook when runtime logging is reloaded.
// The destination must be a newly constructed logger, before it is published.
func CarryObserver(from, to Logger) {
	previous, ok := from.(*observedLogger)
	next, nextOK := to.(*observedLogger)
	if ok && nextOK {
		next.state = previous.state
	}
}

func observable(next Logger) Logger { return &observedLogger{next: next, state: &observationState{}} }
func (l *observedLogger) With(fields ...Field) Logger {
	all := append(append([]Field(nil), l.fields...), fields...)
	return &observedLogger{next: l.next.With(fields...), state: l.state, fields: all}
}
func (l *observedLogger) Debug(ctx context.Context, msg string, f ...Field) {
	l.next.Debug(ctx, msg, f...)
}
func (l *observedLogger) Info(ctx context.Context, msg string, f ...Field) {
	l.next.Info(ctx, msg, f...)
}
func (l *observedLogger) Warn(ctx context.Context, msg string, f ...Field) {
	l.next.Warn(ctx, msg, f...)
	l.observe(ctx, LevelWarn, msg, f)
}
func (l *observedLogger) Error(ctx context.Context, msg string, f ...Field) {
	l.next.Error(ctx, msg, f...)
	l.observe(ctx, LevelError, msg, f)
}
func (l *observedLogger) observe(ctx context.Context, level Level, msg string, fields []Field) {
	if ctx == nil {
		ctx = context.Background()
	}
	if suppressed, _ := ctx.Value(suppressObservationKey{}).(bool); suppressed {
		return
	}
	l.state.mu.RLock()
	observe := l.state.observe
	l.state.mu.RUnlock()
	if observe != nil {
		defer func() {
			if recover() != nil {
				fmt.Fprintln(os.Stderr, "failure_capture: log observer panicked; original log was written")
			}
		}()
		observe(WithoutObservation(ctx), Observation{Level: level, Message: msg, Fields: append(append([]Field(nil), l.fields...), fields...)})
	}
}
