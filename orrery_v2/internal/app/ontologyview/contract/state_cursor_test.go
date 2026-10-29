package contract

import (
	"errors"
	"testing"
	"time"
)

func TestCursorStateStoreSupportsUnrevisionedAuthorizationAndInvalidation(t *testing.T) {
	now := time.Date(2026, 7, 11, 8, 0, 0, 0, time.UTC)
	store, err := NewStateCursorStore(StateCursorOptions{TTL: time.Minute, MaxEntries: 2, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewStateCursorStore: %v", err)
	}
	binding := testCursorBinding()
	binding.AuthorizationRevision = ""
	binding.PolicyRevision = ""
	token, err := store.Issue(CursorPosition{Binding: binding, Seek: []string{"object-1"}})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := store.Resolve(token, binding); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	store.InvalidateAll()
	if _, err := store.Resolve(token, binding); !errors.Is(err, ErrCursorStale) {
		t.Fatalf("invalidated cursor error = %v", err)
	}
}

func TestCursorStateStoreIsBoundedAndExpiresEntries(t *testing.T) {
	now := time.Date(2026, 7, 11, 8, 0, 0, 0, time.UTC)
	store, err := NewStateCursorStore(StateCursorOptions{TTL: time.Minute, MaxEntries: 1, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewStateCursorStore: %v", err)
	}
	position := CursorPosition{Binding: testCursorBinding(), Seek: []string{"object-1"}}
	if _, err := store.Issue(position); err != nil {
		t.Fatalf("first issue: %v", err)
	}
	if _, err := store.Issue(position); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("capacity error = %v", err)
	} else {
		var rateLimit RateLimitError
		if !errors.As(err, &rateLimit) || rateLimit.RetryAfter != time.Minute {
			t.Fatalf("capacity retry = %+v", rateLimit)
		}
	}
	now = now.Add(time.Minute)
	if _, err := store.Issue(position); err != nil {
		t.Fatalf("issue after expiry: %v", err)
	}
}

func TestCursorStateStoreDistinguishesMalformedAndMissingTokens(t *testing.T) {
	store, err := NewStateCursorStore(StateCursorOptions{TTL: time.Minute, MaxEntries: 2})
	if err != nil {
		t.Fatalf("NewStateCursorStore: %v", err)
	}
	binding := testCursorBinding()
	if _, err := store.Resolve("ocs1.not-base64!", binding); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("malformed cursor error = %v", err)
	}
	if _, err := store.Resolve("ocs1.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", binding); !errors.Is(err, ErrCursorStale) {
		t.Fatalf("missing cursor error = %v", err)
	}
}

func TestCursorStateStoreInvalidationPurgesAllEntries(t *testing.T) {
	store, err := NewStateCursorStore(StateCursorOptions{TTL: time.Minute, MaxEntries: 2})
	if err != nil {
		t.Fatalf("NewStateCursorStore: %v", err)
	}
	for _, seek := range []string{"object-1", "object-2"} {
		if _, err := store.Issue(CursorPosition{Binding: testCursorBinding(), Seek: []string{seek}}); err != nil {
			t.Fatalf("Issue %s: %v", seek, err)
		}
	}
	if store.Len() != 2 {
		t.Fatalf("len before invalidation = %d", store.Len())
	}
	store.InvalidateAll()
	if store.Len() != 0 {
		t.Fatalf("len after invalidation = %d", store.Len())
	}
}
