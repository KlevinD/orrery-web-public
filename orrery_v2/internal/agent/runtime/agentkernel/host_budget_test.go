package agentkernel

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/llm"
)

func TestHostRequestBudgetSelection(t *testing.T) {
	for _, mode := range []string{"ordinary", "seconds", "invalid", "negative", "long-task", "long-without-task", "deadline", "expired"} {
		t.Run(mode, func(t *testing.T) {
			r := &Runner{config: Config{LongTaskTimeout: 15 * time.Minute}, request: agentcore.RuntimeRequest{RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{}}}}
			want := time.Duration(0)
			switch mode {
			case "seconds":
				r.request.RuntimeOptions.ProviderSpecific["timeout_seconds"] = "30"
				want = 30 * time.Second
			case "invalid":
				r.request.RuntimeOptions.ProviderSpecific["timeout_seconds"] = "invalid"
			case "negative":
				r.request.RuntimeOptions.ProviderSpecific["timeout_seconds"] = "-1"
			case "long-task":
				r.request.TaskID = "host-task"
				r.request.RuntimeOptions.ProviderSpecific["agentcore_task_mode"] = "long"
				want = 15 * time.Minute
			case "long-without-task":
				r.request.RuntimeOptions.ProviderSpecific["agentcore_task_mode"] = "long"
			case "deadline":
				r.request.Deadline = time.Now().Add(time.Minute)
				r.request.RuntimeOptions.ProviderSpecific["timeout_seconds"] = "3600"
				got := r.TurnTimeout()
				if got <= 59*time.Second || got > time.Minute {
					t.Fatalf("deadline lost: %s", got)
				}
				return
			case "expired":
				r.request.Deadline = time.Now().Add(-time.Minute)
				want = time.Millisecond
			}
			if got := r.TurnTimeout(); got != want {
				t.Fatalf("budget = %s, want %s", got, want)
			}
		})
	}
}

func TestExpiredHostDeadlineStopsBeforeModelOrTool(t *testing.T) {
	m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		t.Error("expired request reached model")
		return nil, ErrModel
	}}
	runner, input := openRunner(t, m, func(_ *Config, req *agentcore.RuntimeRequest) { req.Deadline = time.Now().Add(-time.Minute) })
	if _, err := runner.Run(context.Background(), input, discard); !errors.Is(err, context.DeadlineExceeded) || m.calls.Load() != 0 {
		t.Fatalf("expired budget ignored: err=%v calls=%d", err, m.calls.Load())
	}
}
