package discussionsession

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

type stageCaptureLogger struct {
	mu   sync.Mutex
	rows []map[string]any
}

func (l *stageCaptureLogger) Debug(context.Context, string, ...any) {}
func (l *stageCaptureLogger) Warn(context.Context, string, ...any)  {}
func (l *stageCaptureLogger) Info(_ context.Context, _ string, fields ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	row := map[string]any{}
	for i := 0; i+1 < len(fields); i += 2 {
		row[fields[i].(string)] = fields[i+1]
	}
	l.rows = append(l.rows, row)
}

func TestStreamStagesCorrelateWithPersistedRunWithoutLoggingContent(t *testing.T) {
	logger := &stageCaptureLogger{}
	const privateContent = "private-notification-and-prompt"
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamTokenDelta, Delta: privateContent},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{AssistantMessage: privateContent, Status: "completed"}},
	}}
	service := newTestService(ServiceOptions{Repository: newMemoryRepository(t), DiscussionAgent: runner,
		Clock: fixedClock{now: testTime}, IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"), Logger: logger})
	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: privateContent, IdempotencyKey: "stage-test"})
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	logger.mu.Lock()
	defer logger.mu.Unlock()
	seen := map[string]bool{}
	for _, row := range logger.rows {
		if row["operation"] != "discussion.stage" {
			continue
		}
		stage := row["stage"].(string)
		seen[stage] = true
		if row["status"] != "completed" || row["duration_ms"].(float64) < 0 {
			t.Fatalf("invalid stage: %+v", row)
		}
		if stage == "runtime_execute" || stage == "runtime_first_text" || stage == "run_finalize" {
			if row["conversation_id"] != "conv-1" || row["run_id"] != "run-1" {
				t.Fatalf("uncorrelated stage: %+v", row)
			}
		}
	}
	for _, stage := range []string{"stream_prepare", "turn_start", "run_context_load", "run_create", "context_prepare", "runtime_first_text", "runtime_execute", "assistant_message_save", "memory_capture", "run_finalize", "post_policy", "targets_execute", "turn_finalize", "turn_finalize_load", "turn_finalize_commit", "turn_finalize_snapshot"} {
		if !seen[stage] {
			t.Errorf("missing stage %s", stage)
		}
	}
	raw, _ := json.Marshal(logger.rows)
	if strings.Contains(string(raw), privateContent) {
		t.Fatal("stage logs contain private prompt or reply")
	}
}

func TestStreamPreparationFailureIsTimed(t *testing.T) {
	logger := &stageCaptureLogger{}
	service := newTestService(ServiceOptions{Repository: newMemoryRepository(t), Logger: logger})
	_, err := service.SendMessageStream(context.Background(), ViewerRef{}, SendMessageCommand{})
	if err == nil {
		t.Fatal("invalid request succeeded")
	}
	if len(logger.rows) != 1 || logger.rows[0]["stage"] != "stream_prepare" || logger.rows[0]["status"] != "failed" {
		t.Fatalf("failed preparation not timed: %+v", logger.rows)
	}
}
