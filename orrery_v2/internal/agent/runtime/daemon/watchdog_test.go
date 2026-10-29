package daemon

import (
	"context"
	"testing"
	"time"
)

func TestWithHardTimeoutCancelsContext(t *testing.T) {
	ctx, cancel := WithHardTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	if ctx.Err() == nil {
		t.Fatalf("expected timeout")
	}
}
