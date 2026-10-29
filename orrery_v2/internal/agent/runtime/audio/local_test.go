package audio

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalSpeechRejectsInvalidIdentityAndUnboundedWorkBeforeExecution(t *testing.T) {
	p := NewLocal(LocalConfig{})
	if _, err := p.Synthesize(context.Background(), SpeechRequest{Text: "hello", Format: "mp3"}); err == nil || errors.Is(err, ErrNotConfigured) {
		t.Fatal("identity validation missing")
	}
	scope := Scope{TenantID: "tenant", OwnerID: "owner", SourceID: "stored-message"}
	for _, input := range []SpeechRequest{{Scope: scope, Text: strings.Repeat("字", 1501), Format: "mp3"}, {Scope: scope, Text: "hello", Voice: "../../other-model", Format: "mp3"}, {Scope: scope, Text: "hello", Format: "wav"}} {
		if _, err := p.Synthesize(context.Background(), input); !errors.Is(err, ErrInvalidAudio) {
			t.Fatalf("invalid work accepted: %v", err)
		}
	}
	if _, err := p.Synthesize(context.Background(), SpeechRequest{Scope: scope, Text: "hello", Format: "mp3"}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("relative deployment paths accepted: %v", err)
	}
}

func TestAllLocalSpeechAdaptersShareOneCancellableSlot(t *testing.T) {
	localSpeechSlot <- struct{}{}
	defer func() { <-localSpeechSlot }()
	dir := t.TempDir()
	cfg := LocalConfig{PythonPath: filepath.Join(dir, "python"), ASRDir: dir, TTSDir: dir, WorkDir: dir}
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		_, err := NewLocal(cfg).Synthesize(ctx, SpeechRequest{Scope: Scope{TenantID: "tenant", OwnerID: "owner", SourceID: "stored-message"}, Text: "hello", Format: "mp3"})
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("instance bypassed global limit or ignored cancellation: %v", err)
		}
	}
}
