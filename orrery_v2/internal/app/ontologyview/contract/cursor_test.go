package contract

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCursorBindsPrincipalScopeQueryAndRevisions(t *testing.T) {
	now := time.Date(2026, 7, 11, 8, 0, 0, 0, time.UTC)
	codec, err := NewCursorCodec(CursorCodecOptions{
		PrimaryID: "v2",
		Keys:      []CursorKey{{ID: "v2", Key: bytes.Repeat([]byte{2}, 32)}, {ID: "v1", Key: bytes.Repeat([]byte{1}, 32)}},
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewCursorCodec: %v", err)
	}
	binding := testCursorBinding()
	token, err := codec.Encode(CursorPosition{Binding: binding, Seek: []string{"2026-07-11T08:00:00Z", "object-9"}})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if strings.Contains(token, binding.TenantID) || strings.Contains(token, binding.PrincipalKey) || strings.Contains(token, "object-9") {
		t.Fatalf("cursor exposes plaintext binding: %s", token)
	}
	position, err := codec.Decode(token, binding)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(position.Seek) != 2 || position.Seek[1] != "object-9" {
		t.Fatalf("seek = %+v", position.Seek)
	}

	changed := []CursorBinding{
		withCursorBinding(binding, func(value *CursorBinding) { value.PrincipalKey = "principal-2" }),
		withCursorBinding(binding, func(value *CursorBinding) { value.TenantID = "mission:2" }),
		withCursorBinding(binding, func(value *CursorBinding) { value.BindingVersion++ }),
		withCursorBinding(binding, func(value *CursorBinding) { value.QueryHash = strings.Repeat("b", 64) }),
		withCursorBinding(binding, func(value *CursorBinding) { value.AuthorizationRevision = "authorization:8" }),
		withCursorBinding(binding, func(value *CursorBinding) { value.PolicyRevision = "policy:4" }),
	}
	for _, expected := range changed {
		if _, err := codec.Decode(token, expected); !errors.Is(err, ErrCursorStale) {
			t.Fatalf("changed binding error = %v", err)
		}
	}
}

func TestCursorRejectsTamperingExpiryAndMissingTrustedRevision(t *testing.T) {
	now := time.Date(2026, 7, 11, 8, 0, 0, 0, time.UTC)
	codec, err := NewCursorCodec(CursorCodecOptions{
		PrimaryID: "v1", Keys: []CursorKey{{ID: "v1", Key: bytes.Repeat([]byte{1}, 32)}}, TTL: time.Minute, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewCursorCodec: %v", err)
	}
	binding := testCursorBinding()
	token, err := codec.Encode(CursorPosition{Binding: binding, Seek: []string{"object-1"}})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	tamperAt := strings.LastIndexByte(token, '.') + 8
	tampered := token[:tamperAt] + alternateCursorCharacter(token[tamperAt]) + token[tamperAt+1:]
	if _, err := codec.Decode(tampered, binding); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("tampered cursor error = %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := codec.Decode(token, binding); !errors.Is(err, ErrCursorStale) {
		t.Fatalf("expired cursor error = %v", err)
	}
	binding.AuthorizationRevision = ""
	if _, err := codec.Encode(CursorPosition{Binding: binding, Seek: []string{"object-1"}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing revision error = %v", err)
	}
}

func TestCursorSupportsKeyRotation(t *testing.T) {
	now := time.Date(2026, 7, 11, 8, 0, 0, 0, time.UTC)
	oldCodec, err := NewCursorCodec(CursorCodecOptions{PrimaryID: "old", Keys: []CursorKey{{ID: "old", Key: bytes.Repeat([]byte{1}, 32)}}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("old codec: %v", err)
	}
	token, err := oldCodec.Encode(CursorPosition{Binding: testCursorBinding(), Seek: []string{"object-1"}})
	if err != nil {
		t.Fatalf("old encode: %v", err)
	}
	rotated, err := NewCursorCodec(CursorCodecOptions{
		PrimaryID: "new", Keys: []CursorKey{{ID: "new", Key: bytes.Repeat([]byte{2}, 32)}, {ID: "old", Key: bytes.Repeat([]byte{1}, 32)}}, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("rotated codec: %v", err)
	}
	if _, err := rotated.Decode(token, testCursorBinding()); err != nil {
		t.Fatalf("decode old key after rotation: %v", err)
	}
}

func TestCursorCanonicalQueryHashIsStable(t *testing.T) {
	left, err := CanonicalQueryHash(map[string]any{"status": "active", "sort": []string{"updated_at", "id"}})
	if err != nil {
		t.Fatalf("left hash: %v", err)
	}
	right, err := CanonicalQueryHash(map[string]any{"sort": []string{"updated_at", "id"}, "status": "active"})
	if err != nil {
		t.Fatalf("right hash: %v", err)
	}
	if left == "" || left != right {
		t.Fatalf("query hashes = %q/%q", left, right)
	}
}

func TestCursorRejectsNonCanonicalQueryHashAndOversizedSeek(t *testing.T) {
	codec, err := NewCursorCodec(CursorCodecOptions{PrimaryID: "v1", Keys: []CursorKey{{ID: "v1", Key: bytes.Repeat([]byte{1}, 32)}}})
	if err != nil {
		t.Fatalf("NewCursorCodec: %v", err)
	}
	binding := testCursorBinding()
	binding.QueryHash = strings.Repeat("A", 64)
	if _, err := codec.Encode(CursorPosition{Binding: binding, Seek: []string{"object-1"}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("uppercase hash error = %v", err)
	}
	binding = testCursorBinding()
	if _, err := codec.Encode(CursorPosition{Binding: binding, Seek: []string{strings.Repeat("a", 400), strings.Repeat("b", 400), strings.Repeat("c", 400)}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("oversized seek error = %v", err)
	}
	if _, err := NewCursorCodec(CursorCodecOptions{PrimaryID: "v1", Keys: []CursorKey{{ID: "v1", Key: bytes.Repeat([]byte{1}, 32)}}, TTL: time.Millisecond}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("sub-second ttl error = %v", err)
	}
}

func testCursorBinding() CursorBinding {
	return CursorBinding{
		PrincipalKey:          "principal-1",
		TenantID:              "mission:1",
		BindingVersion:        3,
		QueryHash:             strings.Repeat("a", 64),
		AuthorizationRevision: "authorization:7",
		PolicyRevision:        "policy:3",
	}
}

func withCursorBinding(value CursorBinding, change func(*CursorBinding)) CursorBinding {
	change(&value)
	return value
}

func alternateCursorCharacter(value byte) string {
	if value == 'A' {
		return "B"
	}
	return "A"
}
