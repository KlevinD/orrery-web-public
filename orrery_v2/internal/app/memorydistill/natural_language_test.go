package memorydistill

import (
	"context"
	"errors"
	"fmt"
	"orrery_v2/internal/agent/llm"
	memcore "orrery_v2/internal/memory/core"
	mempageindex "orrery_v2/internal/memory/pageindex"
	memstore "orrery_v2/internal/memory/store"
	memtestkit "orrery_v2/internal/memory/testkit"
	"strings"
	"testing"
	"time"
)

func TestServiceUsesPageIndexRecallAndNaturalLanguageSynthesis(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	seedAcceptedFact(t, ctx, store, "memory-result-1", "candidate-result-1", now)
	seedAcceptedFact(t, ctx, store, "memory-result-2", "candidate-result-2", now)
	seedAcceptedFact(t, ctx, store, "memory-result-trace", "candidate-result-trace", now)
	all, err := store.FindRecords(ctx, memstore.RecordQuery{IncludeArchived: true})
	if err != nil {
		t.Fatalf("FindRecords: %v", err)
	}
	for _, record := range all {
		record.Topic = "agent_result"
		record.Tags = []string{"agent_result"}
		if record.ID == "memory-result-trace" {
			record.Summary = "Capability gap observed after runtime_timeout; workarounds attempted returned fetch failed."
			record.Content = record.Summary
			record.Facts = []string{record.Summary}
		}
		if err := store.SaveRecord(ctx, record); err != nil {
			t.Fatalf("SaveRecord: %v", err)
		}
	}
	all, err = store.FindRecords(ctx, memstore.RecordQuery{IncludeArchived: true})
	if err != nil {
		t.Fatalf("FindRecords updated: %v", err)
	}
	recaller := &recordingSemanticRecaller{records: all[:2]}
	synthesizer := &recordingNaturalLanguageSynthesizer{result: NaturalLanguageSynthesis{
		Summary:   "缺少验收证据时应暂停发布。",
		Narrative: "团队在多次任务中发现，先补齐可核验的验收证据，再进入发布流程，可以减少返工并提高交付可信度。",
		Lessons:   []string{"发布前确认每项验收标准都有可核验的证据。"},
		Warnings:  []string{"证据不足时不要把任务完成状态当作验收结论。"},
	}}
	audit := &memtestkit.MemoryAuditRecorder{}
	service := NewService(store, audit, func() time.Time { return now }).
		WithSemanticRecall(recaller).
		WithNaturalLanguageSynthesizer(synthesizer)

	result, err := service.DistillExperience(ctx, ViewerRef{
		ViewerID:        "distiller-1",
		Language:        "zh-CN",
		PermissionScope: []string{PermissionMemoryDistill, permissionMemoryAuditRead},
		OrganizationIDs: []string{"org-1"},
	}, DistillExperienceRequest{
		TargetScope: MemoryScopeDTO{Kind: "organization", ID: "org-1"},
		Topic:       "agent_result",
	})
	if err != nil {
		t.Fatalf("DistillExperience: %v", err)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].Summary != synthesizer.result.Summary {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.Candidates[0].CandidateID, ".natural.v1.") {
		t.Fatalf("candidate id = %q", result.Candidates[0].CandidateID)
	}
	if len(result.SourceMemoryIDs) != 2 || len(synthesizer.request.Sources) != 2 {
		t.Fatalf("sources = %+v synthesized=%d", result.SourceMemoryIDs, len(synthesizer.request.Sources))
	}
	if synthesizer.request.Language != "zh-CN" {
		t.Fatalf("synthesis language = %q", synthesizer.request.Language)
	}
	if recaller.request.Query == "" || len(recaller.request.ProfileRefs) == 0 {
		t.Fatalf("PageIndex request = %+v", recaller.request)
	}
	records, err := store.FindRecords(ctx, memstore.RecordQuery{ScopeKinds: []memcore.ScopeKind{memcore.ScopeOrganization}, ScopeIDs: []string{"org-1"}})
	if err != nil {
		t.Fatalf("FindRecords organization: %v", err)
	}
	if len(records) != 1 || records[0].Narrative != synthesizer.result.Narrative || len(records[0].Lessons) != 1 || naturalLanguageTag(records[0].Tags) != "zh-CN" {
		t.Fatalf("organization records = %+v", records)
	}
	originalID := records[0].ID
	synthesizer.result = NaturalLanguageSynthesis{
		Summary:   "Pause releases when acceptance evidence is missing.",
		Narrative: "Repeated delivery outcomes show that teams reduce rework by collecting verifiable acceptance evidence before releasing.",
		Lessons:   []string{"Verify every acceptance criterion before release."},
	}
	_, err = service.DistillExperience(ctx, ViewerRef{
		ViewerID:        "distiller-1",
		Language:        "en-US",
		PermissionScope: []string{PermissionMemoryDistill, permissionMemoryAuditRead},
		OrganizationIDs: []string{"org-1"},
	}, DistillExperienceRequest{
		TargetScope: MemoryScopeDTO{Kind: "organization", ID: "org-1"},
		Topic:       "agent_result",
	})
	if err != nil {
		t.Fatalf("DistillExperience in English: %v", err)
	}
	records, err = store.FindRecords(ctx, memstore.RecordQuery{ScopeKinds: []memcore.ScopeKind{memcore.ScopeOrganization}, ScopeIDs: []string{"org-1"}})
	if err != nil {
		t.Fatalf("FindRecords organization after language change: %v", err)
	}
	if len(records) != 1 || records[0].ID != originalID || records[0].Summary != synthesizer.result.Summary || naturalLanguageTag(records[0].Tags) != "en-US" {
		t.Fatalf("localized organization records = %+v", records)
	}
	if len(audit.Snapshot()) != 6 {
		t.Fatalf("audit events = %+v", audit.Snapshot())
	}
}

func TestServiceFiltersOperationalHistoryBeforeApplyingRequestedSourceLimit(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 8, 19, 4, 0, 0, 0, time.UTC)
	for index := 0; index < 60; index++ {
		id := fmt.Sprintf("memory-%03d", index)
		seedAcceptedFact(t, ctx, store, id, "candidate-"+id, now)
		record, err := store.GetRecord(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		record.Topic = "agent_result"
		record.Tags = []string{"agent_result"}
		record.Summary = "runtime_timeout after fetch failed; capability gap and workarounds attempted"
		record.Facts = []string{record.Summary}
		if index >= 58 {
			record.Summary = fmt.Sprintf("Reusable outcome %d was accepted by the delivery owner.", index)
			record.Facts = []string{record.Summary}
		}
		if err := store.SaveRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	recaller := &recordingSemanticRecaller{}
	recaller.records, _ = store.FindRecords(ctx, memstore.RecordQuery{IncludeArchived: true})
	synthesizer := &recordingNaturalLanguageSynthesizer{result: NaturalLanguageSynthesis{
		Summary:   "Two accepted outcomes establish a reusable delivery practice.",
		Narrative: "The delivery owner accepted the same practice in two completed outcomes, so the organization can reuse it when the same conditions apply.",
		Lessons:   []string{"Confirm owner acceptance before reusing the practice."},
	}}
	service := NewService(store, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now }).
		WithSemanticRecall(recaller).
		WithNaturalLanguageSynthesizer(synthesizer)

	result, err := service.DistillExperience(ctx, ViewerRef{
		ViewerID:        "distiller-1",
		PermissionScope: []string{PermissionMemoryDistill, permissionMemoryAuditRead},
		OrganizationIDs: []string{"org-1"},
	}, DistillExperienceRequest{
		TargetScope: MemoryScopeDTO{Kind: "organization", ID: "org-1"},
		Topic:       "agent_result",
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("DistillExperience: %v", err)
	}
	if len(result.SourceMemoryIDs) != 2 || result.SourceMemoryIDs[0] != "memory-058" || result.SourceMemoryIDs[1] != "memory-059" {
		t.Fatalf("source memories = %+v", result.SourceMemoryIDs)
	}
	if len(recaller.request.ProfileRefs) == 0 {
		t.Fatal("PageIndex recall was not invoked")
	}
}

func TestNaturalLanguageSynthesisRejectsOperationalTrace(t *testing.T) {
	message := llm.AssistantMessage(`{"summary":"Runtime report","narrative":"Read C:\\workspace\\trace.log and inspect the command output.","lessons":["Retry the command"],"warnings":[]}`, nil)
	_, err := parseNaturalLanguageSynthesis(message)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("parse error = %v, want invalid request", err)
	}
}

func TestSemanticRecallFailureFallsBackToAuthorizedCandidates(t *testing.T) {
	candidates := []memcore.MemoryRecord{
		{ID: "memory-1", ProfileID: "profile-1", Scope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"}},
		{ID: "memory-2", ProfileID: "profile-2", Scope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-2"}},
	}
	service := &Service{recall: &recordingSemanticRecaller{err: errors.New("pageindex timeout")}}

	result, err := service.recallSourceRecords(context.Background(), ViewerRef{
		ViewerID: "distiller-1",
	}, candidates, experienceRecallQuery("agent_result"), 50)
	if err != nil {
		t.Fatalf("recallSourceRecords: %v", err)
	}
	if len(result) != len(candidates) || result[0].ID != candidates[0].ID || result[1].ID != candidates[1].ID {
		t.Fatalf("fallback records = %+v", result)
	}
	result[0].Summary = "mutated"
	if candidates[0].Summary == "mutated" {
		t.Fatal("fallback must clone candidate records")
	}
	if query := experienceRecallQuery("agent_result"); !strings.Contains(query, "reusable work outcomes") {
		t.Fatalf("experience recall query = %q", query)
	}
	if query := worldRecallQuery("agent_result"); !strings.Contains(query, "durable organization facts") {
		t.Fatalf("world recall query = %q", query)
	}
}

func TestNaturalLanguageSynthesisRemovesThinkingBeforeParsingJSON(t *testing.T) {
	message := llm.AssistantMessage("<think>This internal reasoning must never be stored.</think>\n\n```json\n"+
		`{"summary":"Verify evidence first","narrative":"Repeated tasks show that confirming acceptance evidence before declaring completion reduces rework and improves delivery confidence.","lessons":["Confirm evidence for every acceptance criterion before release."],"warnings":[]}`+
		"\n```", nil)
	result, err := parseNaturalLanguageSynthesis(message)
	if err != nil {
		t.Fatalf("parseNaturalLanguageSynthesis: %v", err)
	}
	if result.Summary != "Verify evidence first" || strings.Contains(result.Narrative, "think") {
		t.Fatalf("result = %+v", result)
	}
}

func TestChatNaturalLanguageSynthesizerSendsOnlyCleanSourceContent(t *testing.T) {
	var input []*llm.Message
	chat := &memoryDistillTestChatModel{generate: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.Message, error) {
		input = messages
		return llm.AssistantMessage(`{"summary":"A durable conclusion","narrative":"The team should verify evidence before treating work as complete.","lessons":["Verify acceptance evidence first."],"warnings":[]}`, nil), nil
	}}
	synthesizer := NewChatNaturalLanguageSynthesizer(ChatModelProviderFunc(func(context.Context) (llm.ChatModel, error) { return chat, nil }))
	result, err := synthesizer.Synthesize(context.Background(), NaturalLanguageSynthesisRequest{
		Layer:       memcore.MemoryLayerExperience,
		Language:    "en-US",
		TargetScope: memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
		Topic:       "quality",
		Sources: []memcore.MemoryRecord{{
			ID: "memory-1", Layer: memcore.MemoryLayerFact, Summary: "Acceptance evidence was missing.", Narrative: "The release was paused until evidence was supplied.",
		}},
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if result.Summary != "A durable conclusion" || result.Language != "en-US" || len(input) != 2 {
		t.Fatalf("result/input = %+v/%+v", result, input)
	}
	if strings.Contains(input[1].Content, "memory-1") || strings.Contains(input[1].Content, "org-1") {
		t.Fatalf("prompt leaked internal IDs: %s", input[1].Content)
	}
	if !strings.Contains(input[0].Content, "Write all output in clear English") || strings.Contains(input[0].Content, "predominant language") {
		t.Fatalf("system prompt does not require English output: %s", input[0].Content)
	}
}

func TestNaturalLanguageMemoryPromptUsesRequestedLanguage(t *testing.T) {
	tests := []struct {
		language string
		want     string
		reject   string
	}{
		{language: "zh-CN", want: "clear Simplified Chinese", reject: "clear English"},
		{language: "en-US", want: "clear English", reject: "clear Simplified Chinese"},
	}
	for _, test := range tests {
		t.Run(test.language, func(t *testing.T) {
			prompt := naturalLanguageMemoryPrompt(memcore.MemoryLayerExperience, test.language)
			if !strings.Contains(prompt, test.want) || strings.Contains(prompt, test.reject) {
				t.Fatalf("prompt = %q", prompt)
			}
		})
	}
}

type recordingSemanticRecaller struct {
	request mempageindex.RecallRequest
	records []memcore.MemoryRecord
	err     error
}

func (r *recordingSemanticRecaller) Recall(_ context.Context, request mempageindex.RecallRequest) ([]memcore.MemoryRecord, error) {
	r.request = request
	return append([]memcore.MemoryRecord(nil), r.records...), r.err
}

type recordingNaturalLanguageSynthesizer struct {
	request NaturalLanguageSynthesisRequest
	result  NaturalLanguageSynthesis
}

func (s *recordingNaturalLanguageSynthesizer) Synthesize(_ context.Context, request NaturalLanguageSynthesisRequest) (NaturalLanguageSynthesis, error) {
	s.request = request
	return s.result, nil
}

type memoryDistillTestChatModel struct {
	generate func(context.Context, []*llm.Message, ...llm.Option) (*llm.Message, error)
}

func (m *memoryDistillTestChatModel) Generate(ctx context.Context, input []*llm.Message, options ...llm.Option) (*llm.Message, error) {
	return m.generate(ctx, input, options...)
}

func (m *memoryDistillTestChatModel) Stream(ctx context.Context, input []*llm.Message, options ...llm.Option) (*llm.MessageStream, error) {
	message, err := m.Generate(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	return llm.NewMessageStream([]*llm.Message{message}), nil
}
