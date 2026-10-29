package contract

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

type StateCursorOptions struct {
	TTL        time.Duration
	MaxEntries int
	Now        func() time.Time
	Random     io.Reader
}

type stateCursorEntry struct {
	position  CursorPosition
	expiresAt time.Time
}

type StateCursorStore struct {
	mu         sync.Mutex
	ttl        time.Duration
	maxEntries int
	now        func() time.Time
	random     io.Reader
	entries    map[string]stateCursorEntry
}

func NewStateCursorStore(opts StateCursorOptions) (*StateCursorStore, error) {
	ttl := opts.TTL
	if ttl == 0 {
		ttl = defaultCursorTTL
	}
	if ttl <= 0 || ttl > defaultCursorTTL || opts.MaxEntries <= 0 || opts.MaxEntries > 10000 {
		return nil, fmt.Errorf("%w: state cursor TTL or capacity is invalid", ErrInvalidRequest)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	random := opts.Random
	if random == nil {
		random = rand.Reader
	}
	return &StateCursorStore{ttl: ttl, maxEntries: opts.MaxEntries, now: now, random: random, entries: map[string]stateCursorEntry{}}, nil
}

func (s *StateCursorStore) Issue(position CursorPosition) (string, error) {
	if s == nil {
		return "", fmt.Errorf("%w: state cursor store is required", ErrInvalidRequest)
	}
	binding, err := normalizeCursorBinding(position.Binding, false)
	if err != nil {
		return "", err
	}
	seek, err := normalizeSeek(position.Seek)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	s.purgeExpiredLocked(now)
	if len(s.entries) >= s.maxEntries {
		return "", RateLimitError{RetryAfter: s.nextExpiryDelayLocked(now)}
	}
	for attempts := 0; attempts < 4; attempts++ {
		value := make([]byte, 32)
		if _, err := io.ReadFull(s.random, value); err != nil {
			return "", fmt.Errorf("generate state cursor: %w", err)
		}
		token := "ocs1." + base64.RawURLEncoding.EncodeToString(value)
		if _, exists := s.entries[token]; exists {
			continue
		}
		s.entries[token] = stateCursorEntry{position: CursorPosition{Binding: binding, Seek: seek}, expiresAt: now.Add(s.ttl)}
		return token, nil
	}
	return "", RateLimitError{RetryAfter: time.Second}
}

func (s *StateCursorStore) Resolve(token string, expected CursorBinding) (CursorPosition, error) {
	if s == nil {
		return CursorPosition{}, ErrInvalidCursor
	}
	token = strings.TrimSpace(token)
	if !validStateCursorToken(token) {
		return CursorPosition{}, ErrInvalidCursor
	}
	expected, err := normalizeCursorBinding(expected, false)
	if err != nil {
		return CursorPosition{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	entry, ok := s.entries[token]
	if !ok {
		return CursorPosition{}, ErrCursorStale
	}
	if !now.Before(entry.expiresAt) {
		delete(s.entries, token)
		return CursorPosition{}, ErrCursorStale
	}
	if entry.position.Binding != expected {
		return CursorPosition{}, ErrCursorStale
	}
	return CursorPosition{Binding: entry.position.Binding, Seek: append([]string(nil), entry.position.Seek...)}, nil
}

func (s *StateCursorStore) Len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredLocked(s.now().UTC())
	return len(s.entries)
}

func (s *StateCursorStore) InvalidateAll() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.entries = map[string]stateCursorEntry{}
	s.mu.Unlock()
}

func (s *StateCursorStore) purgeExpiredLocked(now time.Time) {
	for token, entry := range s.entries {
		if !now.Before(entry.expiresAt) {
			delete(s.entries, token)
		}
	}
}

func (s *StateCursorStore) nextExpiryDelayLocked(now time.Time) time.Duration {
	delay := s.ttl
	for _, entry := range s.entries {
		candidate := entry.expiresAt.Sub(now)
		if candidate > 0 && candidate < delay {
			delay = candidate
		}
	}
	if delay < time.Second {
		return time.Second
	}
	return (delay + time.Second - 1).Truncate(time.Second)
}

func validStateCursorToken(token string) bool {
	if !strings.HasPrefix(token, "ocs1.") || len(token) > 128 {
		return false
	}
	encoded := strings.TrimPrefix(token, "ocs1.")
	value, err := base64.RawURLEncoding.DecodeString(encoded)
	return err == nil && len(value) == 32 && base64.RawURLEncoding.EncodeToString(value) == encoded
}
