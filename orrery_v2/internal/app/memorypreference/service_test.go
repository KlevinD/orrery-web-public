package memorypreference

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcapture "orrery_v2/internal/memory/capture"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/db"
)

type extractorFunc func(context.Context, ExtractionInput) ([]Fact, error)

func (f extractorFunc) Extract(ctx context.Context, in ExtractionInput) ([]Fact, error) {
	return f(ctx, in)
}

type preferenceFixture struct {
	repo    *memstore.PlatformStore
	profile memcore.MemoryProfile
	now     time.Time
}

func newPreferenceFixture(t *testing.T) *preferenceFixture {
	t.Helper()
	core, err := db.Open(context.Background(), db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	f := &preferenceFixture{repo: memstore.NewPlatformStore(core), now: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)}
	f.profile = memcore.NewScopeMemoryProfile(memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-a"}, memcore.OwnerRef{AgentID: "agent-a"})
	if err := f.repo.SaveProfile(context.Background(), f.profile); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *preferenceFixture) source(t *testing.T, id, user string, at time.Time) Request {
	t.Helper()
	writer := memcapture.NewEpisodicWriter(memcapture.EpisodicWriterOptions{Repository: f.repo, Now: func() time.Time { return f.now }})
	candidate, err := writer.CaptureCandidate(context.Background(), memcapture.CandidateDraft{
		ID: id, SourceType: memcore.SourcePersonalAssistantExchange, SourceRef: "assistant-" + id,
		ProposedScope: f.profile.DefaultScope, ProposedKind: agentcore.MemoryKindPersonal, Layer: memcore.MemoryLayerFact,
		Topic: "personal_assistant_exchange", Summary: "User: " + user + "\nAssistant: I suggest English and tables.",
		Narrative:   "User: " + user + "\nAssistant: I suggest English and tables.",
		LineageRefs: []string{"user_message:user-" + id, "run:" + id}, CreatedBy: f.profile.OwnerRef.AgentID, CreatedAt: at, UpdatedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	record, _, err := writer.MaterializeAcceptedCandidate(context.Background(), candidate)
	if err != nil || record.ID == "" {
		t.Fatalf("source materialization: %+v %v", candidate, err)
	}
	return Request{ProfileID: f.profile.ID, AgentID: f.profile.OwnerRef.AgentID, SourceRecordID: record.ID, UserMessageID: "user-" + id, UserText: user}
}

func (f *preferenceFixture) service(facts []Fact) *Service {
	return NewService(Options{Repository: f.repo, Now: func() time.Time { return f.now }, Extractor: extractorFunc(func(context.Context, ExtractionInput) ([]Fact, error) { return facts, nil })})
}

func TestPreferenceUpdatePreservesEvidenceAndIgnoresReplaysAndOlderExchanges(t *testing.T) {
	ctx := context.Background()
	f := newPreferenceFixture(t)
	first := f.source(t, "first", "以后报告必须用表格", f.now.Add(-3*time.Minute))
	one := Fact{Key: "report_format", Value: "报告必须用表格", SourceQuote: first.UserText}
	if err := f.service([]Fact{one}).Extract(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := f.source(t, "second", "以后报告不要用表格", f.now.Add(-time.Minute))
	two := Fact{Key: "report_format", Value: "报告不要用表格", SourceQuote: second.UserText}
	service := f.service([]Fact{two})
	if err := service.Extract(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := service.Extract(ctx, second); err != nil {
		t.Fatal(err)
	}
	older := f.source(t, "older", "以后报告必须用表格", f.now.Add(-2*time.Minute))
	if err := f.service([]Fact{one}).Extract(ctx, older); err != nil {
		t.Fatal(err)
	}
	current, err := f.repo.GetRecord(ctx, preferenceID(f.profile.ID, "report_format"))
	if err != nil {
		t.Fatal(err)
	}
	if current.Preferences[0] != two.Value || current.Facts[0] != second.UserText || len(current.SupersedesRefs) != 1 {
		t.Fatalf("current preference lost update/provenance: %+v", current)
	}
	prior, err := f.repo.GetRecord(ctx, strings.TrimPrefix(current.SupersedesRefs[0], "memory:"))
	if err != nil || prior.Preferences[0] != one.Value || prior.LifecycleStatus != memcore.MemoryLifecycleArchived {
		t.Fatalf("revision: %+v %v", prior, err)
	}
	all, err := f.repo.FindRecords(ctx, memstore.RecordQuery{Tags: []string{Tag}, IncludeArchived: true})
	if err != nil || len(all) != 2 {
		t.Fatalf("replays added records: %+v %v", all, err)
	}
	digest, err := Context(ctx, f.repo, f.profile, first.AgentID, f.now)
	if err != nil || !strings.Contains(digest.Summary, two.Value) || strings.Contains(digest.Summary, one.Value) || strings.Contains(digest.Summary, "I suggest") {
		t.Fatalf("default context: %+v %v", digest, err)
	}
}

func TestPreferenceRejectsWrongOwnerAndFabricatedUserEvidence(t *testing.T) {
	for _, mode := range []string{"wrong owner", "wrong source text", "assistant claim", "rejected source", "disabled profile", "model failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f := newPreferenceFixture(t)
			request := f.source(t, "source", "以后默认用中文回答", f.now.Add(-time.Minute))
			fact := Fact{Key: "response_language", Value: "默认用中文回答", SourceQuote: request.UserText}
			calls := 0
			switch mode {
			case "wrong owner":
				request.AgentID = "agent-b"
			case "wrong source text":
				request.UserText = "以后默认用英文回答"
				fact.SourceQuote = request.UserText
			case "assistant claim":
				fact.SourceQuote = "I prefer English"
				fact.Value = "默认用英文回答"
			case "rejected source":
				if err := f.repo.UpdateCandidateStatus(ctx, "source", memcore.CandidateRejected); err != nil {
					t.Fatal(err)
				}
			case "disabled profile":
				f.profile.Enabled = false
				if err := f.repo.SaveProfile(ctx, f.profile); err != nil {
					t.Fatal(err)
				}
			}
			service := NewService(Options{Repository: f.repo, Now: func() time.Time { return f.now }, Extractor: extractorFunc(func(_ context.Context, input ExtractionInput) ([]Fact, error) {
				calls++
				if strings.Contains(input.UserText, "Assistant:") {
					t.Fatal("assistant text entered extraction")
				}
				if mode == "model failure" {
					return nil, errors.New("model unavailable")
				}
				return []Fact{fact}, nil
			})})
			err := service.Extract(ctx, request)
			if mode != "rejected source" && mode != "disabled profile" && err == nil {
				t.Fatal("invalid extraction succeeded")
			}
			if (mode == "wrong owner" || mode == "wrong source text" || mode == "rejected source" || mode == "disabled profile") && calls != 0 {
				t.Fatal("unauthorized source reached model")
			}
			records, err := f.repo.FindRecords(ctx, memstore.RecordQuery{Tags: []string{Tag}, IncludeArchived: true})
			if err != nil || len(records) != 0 {
				t.Fatalf("invalid extraction wrote preferences: %+v %v", records, err)
			}
		})
	}
}

func TestPreferenceDeletionWhileModelRunsIsNotReversed(t *testing.T) {
	for _, target := range []string{"source", "preference"} {
		t.Run(target, func(t *testing.T) {
			ctx := context.Background()
			f := newPreferenceFixture(t)
			first := f.source(t, "first", "以后回答保持简洁", f.now.Add(-2*time.Minute))
			fact := Fact{Key: "response_detail", Value: "回答保持简洁", SourceQuote: first.UserText}
			if err := f.service([]Fact{fact}).Extract(ctx, first); err != nil {
				t.Fatal(err)
			}
			second := f.source(t, "second", "以后回答需要详细说明", f.now.Add(-time.Minute))
			fact.Value = "回答需要详细说明"
			fact.SourceQuote = second.UserText
			service := NewService(Options{Repository: f.repo, Now: func() time.Time { return f.now }, Extractor: extractorFunc(func(context.Context, ExtractionInput) ([]Fact, error) {
				id := second.SourceRecordID
				if target == "preference" {
					id = preferenceID(f.profile.ID, fact.Key)
				}
				if err := f.repo.UpdateRecordLifecycle(ctx, id, memcore.MemoryLifecycleDeleted); err != nil {
					return nil, err
				}
				return []Fact{fact}, nil
			})})
			if err := service.Extract(ctx, second); err != nil {
				t.Fatal(err)
			}
			record, err := f.repo.GetRecord(ctx, preferenceID(f.profile.ID, fact.Key))
			if err != nil || record.Preferences[0] == fact.Value {
				t.Fatalf("deletion reversed: %+v %v", record, err)
			}
			if target == "preference" && record.LifecycleStatus != memcore.MemoryLifecycleDeleted {
				t.Fatal("deleted preference reactivated")
			}
		})
	}
}

type failingPreferenceRepository struct{ Repository }

func (r failingPreferenceRepository) WithRecordWriteTx(ctx context.Context, key string, fn func(memstore.RecordWriteTx) error) error {
	return r.Repository.WithRecordWriteTx(ctx, key, func(tx memstore.RecordWriteTx) error { return fn(failingPreferenceTx{tx}) })
}

type failingPreferenceTx struct{ memstore.RecordWriteTx }

func (tx failingPreferenceTx) SaveRecord(ctx context.Context, record memcore.MemoryRecord) error {
	if record.LifecycleStatus == memcore.MemoryLifecycleActive {
		return errors.New("record write failed")
	}
	return tx.RecordWriteTx.SaveRecord(ctx, record)
}

func TestPreferenceUpdateRollsBackRevisionAndCandidateWhenRecordWriteFails(t *testing.T) {
	ctx := context.Background()
	f := newPreferenceFixture(t)
	first := f.source(t, "first", "以后报告用表格", f.now.Add(-2*time.Minute))
	if err := f.service([]Fact{{Key: "report_format", Value: "报告用表格", SourceQuote: first.UserText}}).Extract(ctx, first); err != nil {
		t.Fatal(err)
	}
	before, err := f.repo.GetRecord(ctx, preferenceID(f.profile.ID, "report_format"))
	if err != nil {
		t.Fatal(err)
	}
	second := f.source(t, "second", "以后报告不要用表格", f.now.Add(-time.Minute))
	service := f.service([]Fact{{Key: "report_format", Value: "报告不要用表格", SourceQuote: second.UserText}})
	service.options.Repository = failingPreferenceRepository{f.repo}
	if err := service.Extract(ctx, second); err == nil {
		t.Fatal("write failure not returned")
	}
	after, err := f.repo.GetRecord(ctx, before.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("partial preference update: %+v %v", after, err)
	}
	records, err := f.repo.FindRecords(ctx, memstore.RecordQuery{Tags: []string{Tag}, IncludeArchived: true})
	if err != nil || len(records) != 1 {
		t.Fatalf("revision escaped rollback: %+v %v", records, err)
	}
	_, err = f.repo.GetCandidate(ctx, identifier("candidate.preference", before.ID, second.SourceRecordID))
	if !errors.Is(err, memcore.ErrNotFound) {
		t.Fatalf("candidate escaped rollback: %v", err)
	}
}
