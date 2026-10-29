package actioncontract

import (
	"context"
	"testing"
)

func TestInterpretDefaultActionDistinguishesSubmitCodeSpeechActs(t *testing.T) {
	tests := []struct {
		name          string
		utterance     string
		wantSpeechAct SpeechAct
		wantState     ActionState
		wantExecute   bool
		wantRetrieval RetrievalMode
		wantNegated   bool
	}{
		{name: "observed assertion", utterance: "我提交代码", wantSpeechAct: SpeechActAssertion, wantState: ActionStateObserved},
		{name: "completed assertion", utterance: "我已经提交代码", wantSpeechAct: SpeechActAssertion, wantState: ActionStateCompleted},
		{name: "command", utterance: "帮我提交代码", wantSpeechAct: SpeechActCommand, wantState: ActionStateRequested, wantExecute: true},
		{name: "intention", utterance: "我要提交代码", wantSpeechAct: SpeechActIntention, wantState: ActionStatePlanned},
		{name: "knowledge question", utterance: "怎么提交代码？", wantSpeechAct: SpeechActQuestion, wantState: ActionStateUnknown, wantRetrieval: RetrievalModeExplain},
		{name: "failure report", utterance: "代码提交失败了", wantSpeechAct: SpeechActFailureReport, wantState: ActionStateFailed, wantRetrieval: RetrievalModeTroubleshoot},
		{name: "negated command", utterance: "请你不要提交代码", wantSpeechAct: SpeechActCommand, wantState: ActionStateRequested, wantNegated: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.wantRetrieval == "" {
				test.wantRetrieval = RetrievalModeNone
			}
			frame := InterpretDefaultAction("tenant-1", test.utterance)
			if !frame.Recognized || frame.ActionTypeID != "development.submit_code" {
				t.Fatalf("frame = %+v, want development.submit_code", frame)
			}
			if frame.SpeechAct != test.wantSpeechAct || frame.State != test.wantState || frame.ExecutionRequested != test.wantExecute || frame.RetrievalMode != test.wantRetrieval || frame.Negated != test.wantNegated {
				t.Fatalf("frame = %+v", frame)
			}
			if len(frame.DecomposesInto) != 3 || len(frame.KnowledgeTerms) == 0 || len(frame.MemoryTerms) == 0 {
				t.Fatalf("semantic routing fields = %+v", frame)
			}
		})
	}
}

func TestInterpretActionTypesUsesLongestVerbAndTenantVisibility(t *testing.T) {
	types := []ActionType{
		{ID: "development.submit_code", TenantID: "system", Name: "Submit code", Enabled: true, Semantics: ActionSemanticProfile{Verbs: []string{"提交代码"}}},
		{ID: "development.create_commit", TenantID: "tenant-1", Name: "Create commit", Enabled: true, Semantics: ActionSemanticProfile{Verbs: []string{"提交代码变更"}}},
	}
	frame := InterpretActionTypes(types, "tenant-1", "帮我提交代码变更")
	if frame.ActionTypeID != "development.create_commit" || !frame.ExecutionRequested {
		t.Fatalf("tenant-1 frame = %+v", frame)
	}
	frame = InterpretActionTypes(types, "tenant-2", "帮我提交代码变更")
	if frame.ActionTypeID != "development.submit_code" {
		t.Fatalf("tenant-2 frame = %+v", frame)
	}
}

func TestSemanticInterpreterReadsRegisteredActionVocabulary(t *testing.T) {
	repository := NewMemoryRepository()
	service := NewService(repository)
	if err := service.RegisterActionType(context.Background(), ActionType{
		ID: "customer.issue_refund", TenantID: "tenant-1", Name: "Issue refund", Category: CategoryAgent,
		ProviderKind: ProviderInternalService, RiskLevel: RiskMedium, Enabled: true, Version: "v1",
		Semantics: ActionSemanticProfile{Verbs: []string{"发起退款"}, Roles: []SemanticRole{{Name: "object", ObjectType: "payment", Required: true}}},
	}); err != nil {
		t.Fatalf("RegisterActionType: %v", err)
	}
	frame, err := NewSemanticInterpreter(repository).InterpretAction(context.Background(), "tenant-1", "请你发起退款")
	if err != nil {
		t.Fatalf("InterpretAction: %v", err)
	}
	if frame.ActionTypeID != "customer.issue_refund" || !frame.ExecutionRequested || len(frame.Roles) != 1 {
		t.Fatalf("frame = %+v", frame)
	}
}

func TestResolverUsesBusinessActionVerbs(t *testing.T) {
	repository := NewMemoryRepository()
	service := NewService(repository)
	actionType, ok := DefaultActionType("development.submit_code")
	if !ok {
		t.Fatal("missing development.submit_code")
	}
	if err := service.RegisterActionType(context.Background(), actionType); err != nil {
		t.Fatalf("RegisterActionType: %v", err)
	}
	resolved, err := NewResolver(repository).Resolve(context.Background(), ResolveRequest{Intent: "帮我提交代码"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.ID != "development.submit_code" {
		t.Fatalf("resolved = %+v", resolved)
	}
}
