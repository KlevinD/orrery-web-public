package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/memorypreference"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/testkit"
)

type productionPreferenceExtractorFunc func(context.Context, memorypreference.ExtractionInput) ([]memorypreference.Fact, error)

func (f productionPreferenceExtractorFunc) Extract(ctx context.Context, input memorypreference.ExtractionInput) ([]memorypreference.Fact, error) {
	return f(ctx, input)
}

func TestProductionPreferenceQueueCapturesUserEvidenceAndLoadsOwnerContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := testkit.NewInitializedTestRuntime(t)
	repository := memstore.NewPlatformStore(testkit.NewTestDB(t))
	var calls atomic.Int32
	const userText = "以后默认用中文回答"
	service := memorypreference.NewService(memorypreference.Options{Repository: repository, Now: rt.Clock.Now,
		Extractor: productionPreferenceExtractorFunc(func(_ context.Context, input memorypreference.ExtractionInput) ([]memorypreference.Fact, error) {
			calls.Add(1)
			if input.UserText != userText {
				return nil, fmt.Errorf("unexpected user evidence")
			}
			return []memorypreference.Fact{{Key: "response_language", Value: "默认用中文回答", SourceQuote: userText}}, nil
		}),
	})
	if err := registerProductionPreferenceWorker(rt, service); err != nil {
		t.Fatal(err)
	}
	provider := newProductionDiscussionMemoryProvider(nil, repository, productionDiscussionMemoryProviderOptions{Now: rt.Clock.Now, PreferenceQueue: rt.ExecutionQueue})
	owner := discussionsession.PersonalAssistantPreferenceOwner{TenantID: "tenant-a", OwnerID: "account-1", AgentID: discussionsession.PersonalAssistantAgentID("tenant-a", "account-1")}
	exchange := discussionsession.PersonalAssistantMemoryExchange{
		PreferenceExtractionAllowed: true, TenantID: owner.TenantID, OwnerID: owner.OwnerID, AgentID: owner.AgentID,
		ConversationID: "private", RunID: "run-1", UserMessageID: "user-1", AssistantMessageID: "assistant-1",
		UserContent: userText, AssistantContent: "I suggest answering in English.", OccurredAt: rt.Clock.Now(),
	}
	if err := provider.CapturePersonalAssistantMemoryExchange(ctx, exchange); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("capture waited for a model call")
	}
	jobID := productionPreferenceTopic + ":" + personalAssistantMemoryCandidateID(owner.AgentID, exchange.RunID)
	job, ok, err := rt.ExecutionQueue.Get(ctx, jobID)
	if err != nil || !ok || strings.Contains(string(job.Payload), "English") {
		t.Fatalf("queued source: %+v %v", job, err)
	}
	if err := provider.CapturePersonalAssistantMemoryExchange(ctx, exchange); err != nil {
		t.Fatal(err)
	}
	if err := rt.StartExecutionRunner(ctx, platform.ExecutionRunnerRuntimeConfig{PollInterval: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
waitForJob:
	for {
		select {
		case <-ticker.C:
			status, ok, err := rt.ExecutionQueue.GetStatus(ctx, jobID)
			if err != nil {
				t.Fatal(err)
			}
			if ok && status.Status == execution.StatusCompleted {
				break waitForJob
			}
			if ok && (status.Status == execution.StatusFailed || status.Status == execution.StatusDeadLetter) {
				t.Fatalf("preference job failed: %+v", status)
			}
		case <-deadline.C:
			t.Fatal("registered preference worker did not complete queued extraction")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("duplicate capture extracted %d times", calls.Load())
	}
	digest, err := provider.PersonalAssistantPreferences(ctx, owner)
	if err != nil || !strings.Contains(digest.Summary, "默认用中文回答") || strings.Contains(digest.Summary, "English") || digest.RetrievalMode != "stable_preferences" || len(digest.SourceEvidence) != 1 {
		t.Fatalf("owner context: %+v %v", digest, err)
	}
	for _, other := range []discussionsession.PersonalAssistantPreferenceOwner{
		{TenantID: "tenant-b", OwnerID: owner.OwnerID, AgentID: discussionsession.PersonalAssistantAgentID("tenant-b", owner.OwnerID)},
		{TenantID: owner.TenantID, OwnerID: "account-2", AgentID: discussionsession.PersonalAssistantAgentID(owner.TenantID, "account-2")},
	} {
		digest, err := provider.PersonalAssistantPreferences(ctx, other)
		if err != nil || digest.Summary != "" {
			t.Fatalf("private preference leaked: %+v %v", digest, err)
		}
	}
	owner.TenantID = "tenant-b"
	if _, err := provider.PersonalAssistantPreferences(ctx, owner); err == nil {
		t.Fatal("forged owner/agent tuple accepted")
	}
}

type recordingPreferenceQueue struct {
	jobs []execution.Job
	err  error
}

func (q *recordingPreferenceQueue) Enqueue(_ context.Context, job execution.Job) error {
	q.jobs = append(q.jobs, job)
	return q.err
}

func TestProductionPreferenceCaptureSkipsSharedSensitiveAndTruncatedSources(t *testing.T) {
	for _, mode := range []string{"shared", "sensitive", "truncated", "no durable signal"} {
		t.Run(mode, func(t *testing.T) {
			queue := &recordingPreferenceQueue{}
			provider := newProductionDiscussionMemoryProvider(nil, memstore.NewPlatformStore(testkit.NewTestDB(t)), productionDiscussionMemoryProviderOptions{PreferenceQueue: queue})
			exchange := discussionsession.PersonalAssistantMemoryExchange{PreferenceExtractionAllowed: true, TenantID: "tenant", OwnerID: "owner", AgentID: discussionsession.PersonalAssistantAgentID("tenant", "owner"), ConversationID: "conversation", RunID: "run", UserMessageID: "user", AssistantMessageID: "assistant", UserContent: "以后默认用中文回答", AssistantContent: "ok", OccurredAt: time.Now().UTC()}
			switch mode {
			case "shared":
				exchange.PreferenceExtractionAllowed = false
			case "sensitive":
				exchange.UserContent = "请记住我的 password is 12345"
			case "truncated":
				exchange.UserContent += strings.Repeat("中", 2001)
			case "no durable signal":
				exchange.UserContent = "分析一下这张图"
			}
			if err := provider.CapturePersonalAssistantMemoryExchange(context.Background(), exchange); err != nil {
				t.Fatal(err)
			}
			if len(queue.jobs) != 0 {
				t.Fatalf("unsafe source queued: %+v", queue.jobs)
			}
		})
	}
}

func TestProductionPreferenceWorkerRejectsForgedTenantBeforeModel(t *testing.T) {
	job := productionPreferenceJob{Owner: discussionsession.PersonalAssistantPreferenceOwner{TenantID: "tenant-b", OwnerID: "owner", AgentID: discussionsession.PersonalAssistantAgentID("tenant-a", "owner")}}
	payload, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	err = productionPreferenceHandler(nil)(context.Background(), execution.Job{Payload: payload})
	var permanent execution.PermanentError
	if !errors.As(err, &permanent) {
		t.Fatalf("bad owner should be permanently rejected: %v", err)
	}
}
