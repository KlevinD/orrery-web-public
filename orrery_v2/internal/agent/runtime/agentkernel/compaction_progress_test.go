package agentkernel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"orrery_v2/internal/agent/llm"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/providers/profilemodel"
)

func configureChunkedProgress(t *testing.T, c *Config, req *agentcore.RuntimeRequest) {
	t.Helper()
	checkpointConfig(t, c)
	c.AutoCompact, c.CompactKeepMessages, c.CompactSummaryBytes = true, 2, 128
	req.TaskID = "durable-summary-task"
	req.RuntimeOptions.ProviderSpecific = map[string]string{"agentcore_task_mode": "long"}
	req.InputMessages = memoryHistory()
	for i := range req.InputMessages {
		if req.InputMessages[i].Role == "tool" {
			req.InputMessages[i].Content = strings.Repeat("证据引用 payload-α ", 80)
		}
	}
	messages, _ := initialMessages(*req)
	raw, _ := json.Marshal(messages)
	c.MaxContextBytes, c.CompactAtBytes = len(raw)*3/4, len(raw)/2
}

func interruptProgress(t *testing.T) (*Runner, agentcore.SendCommand) {
	t.Helper()
	steps := 0
	m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		steps++
		if steps == 2 {
			return nil, errors.New("interrupted next fragment")
		}
		return llm.NewMessageStream([]*llm.Message{reply("saved first fragment", "stop")}), nil
	}}
	r, input := openRunner(t, m, func(c *Config, req *agentcore.RuntimeRequest) { configureChunkedProgress(t, c, req) })
	if _, err := r.Run(context.Background(), input, discard); !errors.Is(err, ErrModel) {
		t.Fatalf("expected interruption: %v", err)
	}
	if p := r.checkpoint.CompactionProgress; p == nil || p.Offset == 0 || p.PendingBytes == 0 || p.Offset == p.PayloadBytes {
		t.Fatal("completed fragment was not persisted")
	}
	return r, input
}

func TestCompactionProgressReopensAndResumesWithoutRepeatingCompletedFragments(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(fmt.Sprintf("automatic=%v", automatic), func(t *testing.T) {
			testCompactionProgressReopens(t, automatic)
		})
	}
}

func testCompactionProgressReopens(t *testing.T, automatic bool) {
	path := filepath.Join(t.TempDir(), "progress.db")
	journal, backing := openTestJournal(t, path)
	var fragments []string
	steps := 0
	first := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, opts ...llm.Option) (*llm.MessageStream, error) {
		steps++
		if steps == 2 {
			return nil, errors.New("process interrupted")
		}
		if len(llm.Apply(opts...).Tools) != 0 {
			t.Fatal("summary received tools")
		}
		var data struct {
			Fragment string `json:"historical_fragment"`
		}
		if json.Unmarshal([]byte(messages[1].Content), &data) != nil || !utf8.ValidString(data.Fragment) {
			t.Fatal("invalid source fragment")
		}
		fragments = append(fragments, data.Fragment)
		message := reply("first completed fragment", "stop")
		message.ResponseMeta.Usage = &llm.TokenUsage{PromptTokens: 100, CompletionTokens: 20}
		return llm.NewMessageStream([]*llm.Message{message}), nil
	}}
	r, input := openRunner(t, first, func(c *Config, req *agentcore.RuntimeRequest) {
		c.ToolJournal = journal
		configureChunkedProgress(t, c, req)
		if automatic {
			req.MissionID = "durable-summary-mission"
			req.RuntimeOptions.ProviderSpecific["agentcore_task_recovery"] = "automatic"
		}
		c.AuthorizeTool = func(context.Context, runtimecore.RuntimeToolCallRequest) error {
			t.Fatal("historical tool replayed")
			return nil
		}
	})
	before, _ := json.Marshal(r.messages)
	if _, err := r.Run(context.Background(), input, discard); !errors.Is(err, ErrModel) {
		t.Fatal(err)
	}
	if automatic && !r.CanResumeTask() {
		t.Fatal("interrupted summary was not automatically recoverable")
	}
	cp := *r.checkpoint
	p := cp.CompactionProgress
	if p == nil || p.Offset != len(fragments[0]) || p.Summary != "first completed fragment" || cp.ModelCalls != 2 || cp.Deadline.IsZero() {
		t.Fatalf("incomplete durable progress: %+v", p)
	}
	after, _ := json.Marshal(cp.Messages)
	if string(before) != string(after) || cp.Compactions != 0 || cp.Usage.InputTokens != 100 || cp.Usage.OutputTokens != 20 {
		t.Fatal("partial summary replaced live history or lost accounting")
	}
	_ = r.Close(context.Background())
	if err := backing.Close(); err != nil {
		t.Fatal(err)
	}
	newJournal, reopened := openTestJournal(t, path)
	r.config.ToolJournal = newJournal
	r.config.Checkpoints, _ = NewSQLCheckpointStore(reopened)
	previous := p.Summary
	second := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, opts ...llm.Option) (*llm.MessageStream, error) {
		if !strings.HasPrefix(messages[0].Content, compactionInstruction) {
			return llm.NewMessageStream([]*llm.Message{reply("continued answer", "stop")}), nil
		}
		if len(llm.Apply(opts...).Tools) != 0 {
			t.Fatal("summary received executable tools")
		}
		var data struct {
			Previous string `json:"previous_working_memory"`
			Fragment string `json:"historical_fragment"`
		}
		if json.Unmarshal([]byte(messages[1].Content), &data) != nil || data.Previous != previous || !utf8.ValidString(data.Fragment) {
			t.Fatal("resumed summary lost prior memory or UTF-8 boundary")
		}
		fragments = append(fragments, data.Fragment)
		previous = fmt.Sprintf("preserved %d fragments", len(fragments))
		return llm.NewMessageStream([]*llm.Message{reply(previous, "stop")}), nil
	}}
	resume := resumeTestRunner
	if automatic {
		resume = reopenTaskRunner
	}
	resumed, err := resume(t, r, r.request, second)
	if err != nil {
		t.Fatal(err)
	}
	if !resumed.deadline.Equal(cp.Deadline) || resumed.modelCalls != 2 {
		t.Fatal("resume expanded time or call budgets")
	}
	if _, err := r.config.Checkpoints.Save(context.Background(), cp); !errors.Is(err, ErrCheckpoint) {
		t.Fatal("old process retained write ownership")
	}
	result, err := resumed.Run(context.Background(), input, discard)
	if err != nil || result.ResultText != "continued answer" || resumed.compactions != 1 || resumed.checkpoint.CompactionProgress != nil {
		t.Fatalf("resume failed: %+v %v", result, err)
	}
	prefix := 0
	for cp.Messages[prefix].Role == llm.System {
		prefix++
	}
	expected, _ := json.Marshal(cp.Messages[prefix : prefix+p.ArchiveMessages])
	if strings.Join(fragments, "") != string(expected) || resumed.modelCalls != len(fragments)+2 {
		t.Fatal("completed source fragment repeated, omitted, or uncharged")
	}
	archives, _ := reopened.ListRecordTable(context.Background(), compactionArchiveTable)
	if len(archives) != 1 {
		t.Fatal("resumed history was not archived exactly once")
	}
}

func TestCompletedSummaryResumesAfterArchiveFailureBelowCompactionThreshold(t *testing.T) {
	first := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
		if !strings.HasPrefix(messages[0].Content, compactionInstruction) {
			return nil, profilemodel.ErrContextWindowExceeded
		}
		return llm.NewMessageStream([]*llm.Message{reply("durable completed memory", "stop")}), nil
	}}
	var durable CheckpointStore
	r, input := openRunner(t, first, func(c *Config, req *agentcore.RuntimeRequest) {
		checkpointConfig(t, c)
		durable = c.Checkpoints
		c.Checkpoints = failingCheckpointStore{CheckpointStore: durable, fail: func(c Checkpoint) bool { return c.Compactions > 0 }}
		c.AutoCompact, c.CompactKeepMessages, c.CompactSummaryBytes = true, 2, 128
		req.InputMessages = memoryHistory()
	})
	if _, err := r.Run(context.Background(), input, discard); !errors.Is(err, ErrCheckpoint) {
		t.Fatal(err)
	}
	p := r.checkpoint.CompactionProgress
	if p == nil || p.Offset != p.PayloadBytes || r.checkpoint.Compactions != 0 || r.checkpoint.WindowRecoveries != 1 {
		t.Fatal("final summary was not saved before archive failure")
	}
	r.config.Checkpoints = durable
	second := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
		if strings.HasPrefix(messages[0].Content, compactionInstruction) {
			t.Fatal("completed summary regenerated")
		}
		body, _ := json.Marshal(messages)
		if !strings.Contains(string(body), "durable completed memory") {
			t.Fatal("completed memory omitted")
		}
		return llm.NewMessageStream([]*llm.Message{reply("recovered", "stop")}), nil
	}}
	resumed, err := resumeTestRunner(t, r, r.request, second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := resumed.Run(context.Background(), input, discard)
	if err != nil || result.ResultText != "recovered" || second.calls.Load() != 1 || resumed.modelCalls != 3 || resumed.windowRecoveries != 1 {
		t.Fatalf("completed summary recovery failed: %v", err)
	}
}

func TestCompactionProgressCannotResetBudgetOrChangePolicyAndAuthority(t *testing.T) {
	r, input := interruptProgress(t)
	never := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		t.Fatal("rejected resume invoked model")
		return nil, nil
	}}
	original := r.config
	for _, mutate := range []func(*Config){func(c *Config) { c.AutoCompact = false }, func(c *Config) { c.CompactSummaryBytes++ }, func(c *Config) { c.CompactKeepMessages++ }} {
		r.config = original
		mutate(&r.config)
		if _, err := resumeTestRunner(t, r, r.request, never); !errors.Is(err, ErrCheckpoint) && !errors.Is(err, ErrBudget) {
			t.Fatalf("incompatible progress policy accepted: %v", err)
		}
	}
	r.config = original
	for _, mutate := range []func(*agentcore.RuntimeRequest){func(q *agentcore.RuntimeRequest) { q.TenantID = "other" }, func(q *agentcore.RuntimeRequest) { q.ExecutionPrincipal.ID = "other" }} {
		req := r.request
		mutate(&req)
		if _, err := resumeTestRunner(t, r, req, never); !errors.Is(err, ErrCheckpoint) {
			t.Fatalf("progress crossed authority boundary: %v", err)
		}
	}
	req := r.request
	req.RuntimeOptions.MaxTurns = 3 // Two spent calls leave no room for another summary plus an answer.
	resumed, err := resumeTestRunner(t, r, req, never)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resumed.Run(context.Background(), input, discard); !errors.Is(err, ErrBudget) || resumed.modelCalls != 2 {
		t.Fatalf("resume replenished budget: %v", err)
	}
	if never.calls.Load() != 0 {
		t.Fatal("rejection disclosed conversation")
	}
}

func TestCheckpointRejectsCompactionProgressRewindAndPrematureCommit(t *testing.T) {
	r, _ := interruptProgress(t)
	old := *r.checkpoint
	cases := map[string]func(*Checkpoint){
		"clear":               func(c *Checkpoint) { c.CompactionProgress = nil },
		"summary replacement": func(c *Checkpoint) { c.CompactionProgress.Summary = "different memory" },
		"policy replacement":  func(c *Checkpoint) { c.CompactionProgress.PolicyHash = strings.Repeat("a", 64) },
		"rewind":              func(c *Checkpoint) { c.CompactionProgress.Offset = 0; c.CompactionProgress.Summary = "" },
		"skip source": func(c *Checkpoint) {
			c.CompactionProgress.Offset += c.CompactionProgress.PendingBytes - 1
			c.CompactionProgress.PendingBytes = 0
		},
		"early archive": func(c *Checkpoint) {
			c.Compactions++
			c.CompactionProgress = nil
			c.Messages = []*llm.Message{llm.UserMessage("lost original history")}
		},
		"tool boundary": func(c *Checkpoint) { c.Phase = CheckpointToolsPending },
		"turn advance":  func(c *Checkpoint) { c.Turn++ },
		"source replacement": func(c *Checkpoint) {
			c.Messages = append([]*llm.Message(nil), c.Messages...)
			m := *c.Messages[1]
			m.Content += " changed"
			c.Messages[1] = &m
			c.CompactionProgress.SourceHash, _ = digest(c.Messages)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			candidate := old
			candidate.CompactionProgress = cloneCompactionProgress(old.CompactionProgress)
			mutate(&candidate)
			if _, err := r.config.Checkpoints.Save(context.Background(), candidate); !errors.Is(err, ErrCheckpoint) {
				t.Fatalf("unsafe transition accepted: %v", err)
			}
		})
	}
	for _, mutate := range []func(*CompactionProgress){
		func(p *CompactionProgress) { p.Version++ }, func(p *CompactionProgress) { p.Generation++ },
		func(p *CompactionProgress) { p.ArchiveMessages = 2 }, func(p *CompactionProgress) { p.Offset = p.PayloadBytes + 1 },
		func(p *CompactionProgress) { p.SummaryLimit = 1 }, func(p *CompactionProgress) { p.PendingBytes = -1 },
	} {
		candidate := old
		candidate.CompactionProgress = cloneCompactionProgress(old.CompactionProgress)
		mutate(candidate.CompactionProgress)
		if _, err := checkpointRecord(candidate); !errors.Is(err, ErrCheckpoint) {
			t.Fatalf("malformed persisted progress accepted: %v", err)
		}
	}
}

func TestCompactionProgressKeepsReducedFragmentCapAcrossResume(t *testing.T) {
	var failedFragment string
	steps := 0
	first := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
		steps++
		if steps == 1 {
			return nil, profilemodel.ErrContextWindowExceeded
		}
		var data struct {
			Fragment string `json:"historical_fragment"`
		}
		_ = json.Unmarshal([]byte(messages[1].Content), &data)
		failedFragment = data.Fragment
		return nil, errors.New("interrupted after reduced fragment")
	}}
	r, input := openRunner(t, first, func(c *Config, req *agentcore.RuntimeRequest) { configureChunkedProgress(t, c, req) })
	if _, err := r.Run(context.Background(), input, discard); !errors.Is(err, ErrModel) {
		t.Fatal(err)
	}
	p := r.checkpoint.CompactionProgress
	if p == nil || p.Offset != 0 || p.FragmentCap >= p.PayloadBytes || len(failedFragment) > p.FragmentCap {
		t.Fatal("rejected fragment cap not durable")
	}
	seen := false
	second := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
		if !strings.HasPrefix(messages[0].Content, compactionInstruction) {
			return llm.NewMessageStream([]*llm.Message{reply("answer", "stop")}), nil
		}
		var data struct {
			Fragment string `json:"historical_fragment"`
		}
		_ = json.Unmarshal([]byte(messages[1].Content), &data)
		if !seen && data.Fragment != failedFragment {
			t.Fatal("resume repeated the rejected oversized fragment")
		}
		seen = true
		if len(data.Fragment) > p.FragmentCap {
			t.Fatal("fragment cap increased on resume")
		}
		return llm.NewMessageStream([]*llm.Message{reply("retained memory", "stop")}), nil
	}}
	resumed, err := resumeTestRunner(t, r, r.request, second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resumed.Run(context.Background(), input, discard); err != nil || !seen {
		t.Fatalf("reduced fragment resume failed: %v", err)
	}
}

func TestCompactionProgressWriteFailureCannotPublishUnsavedMemory(t *testing.T) {
	calls := 0
	first := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		calls++
		return llm.NewMessageStream([]*llm.Message{reply(fmt.Sprintf("summary %d", calls), "stop")}), nil
	}}
	var durable CheckpointStore
	r, input := openRunner(t, first, func(c *Config, req *agentcore.RuntimeRequest) {
		configureChunkedProgress(t, c, req)
		durable = c.Checkpoints
		c.Checkpoints = failingCheckpointStore{CheckpointStore: durable, fail: func(c Checkpoint) bool {
			return c.CompactionProgress != nil && c.CompactionProgress.Offset > 0
		}}
	})
	if _, err := r.Run(context.Background(), input, discard); !errors.Is(err, ErrCheckpoint) || calls != 1 {
		t.Fatalf("progress write failure ignored: %v", err)
	}
	if p := r.checkpoint.CompactionProgress; p == nil || p.Offset != 0 || p.Summary != "" || r.checkpoint.ModelCalls != 1 {
		t.Fatal("uncommitted memory leaked into checkpoint")
	}
	r.config.Checkpoints = durable
	resumed, err := resumeTestRunner(t, r, r.request, first)
	if err != nil {
		t.Fatal(err)
	}
	if p := resumed.compactionProgress; p == nil || p.Offset != 0 || p.Summary != "" || resumed.modelCalls != 1 {
		t.Fatal("resume accepted memory that was not durably saved")
	}
	archives, _ := durable.(*SQLCheckpointStore).store.ListRecordTable(context.Background(), compactionArchiveTable)
	if len(archives) != 0 {
		t.Fatal("unsaved memory created archive")
	}
}
