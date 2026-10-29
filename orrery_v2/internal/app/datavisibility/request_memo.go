package datavisibility

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

type requestMemoContextKey struct{}

// RequestMemo shares adapter work only within one bounded visibility request.
type RequestMemo struct {
	mu         sync.Mutex
	values     map[string]any
	validators map[string]func(context.Context) error
}

func WithRequestMemo(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	if RequestMemoFromContext(ctx) != nil {
		return ctx
	}
	return WithFreshRequestMemo(ctx)
}

// WithFreshRequestMemo starts a separate consistency baseline for a complete
// retry of a visibility request.
func WithFreshRequestMemo(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, requestMemoContextKey{}, &RequestMemo{
		values:     map[string]any{},
		validators: map[string]func(context.Context) error{},
	})
}

func RequestMemoFromContext(ctx context.Context) *RequestMemo {
	if ctx == nil {
		return nil
	}
	memo, _ := ctx.Value(requestMemoContextKey{}).(*RequestMemo)
	return memo
}

func (m *RequestMemo) Load(key string) (any, bool) {
	if m == nil || key == "" {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.values[key]
	return value, ok
}

func (m *RequestMemo) Store(key string, value any) {
	if m == nil || key == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = value
}

// RegisterValidator adds one final consistency check for shared request work.
// Re-registering the same key preserves the first baseline and validator.
func (m *RequestMemo) RegisterValidator(key string, validator func(context.Context) error) {
	if m == nil || key == "" || validator == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.validators[key]; !exists {
		m.validators[key] = validator
	}
}

// Validate verifies that memoized authorization and policy baselines remained
// stable across the complete request.
func (m *RequestMemo) Validate(ctx context.Context) error {
	if m == nil || ctx == nil {
		return nil
	}
	m.mu.Lock()
	keys := make([]string, 0, len(m.validators))
	for key := range m.validators {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	validators := make([]func(context.Context) error, 0, len(keys))
	for _, key := range keys {
		validators = append(validators, m.validators[key])
	}
	m.mu.Unlock()
	for index, validator := range validators {
		if err := validator(ctx); err != nil {
			return fmt.Errorf("request consistency validator %d: %w", index, err)
		}
	}
	return nil
}
