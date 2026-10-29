package ontologyreasoning

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
)

func TestCreateReasoningRunStoresSafeTrace(t *testing.T) {
	now := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	service := NewService(ServiceOptions{
		Store: store,
		Clock: func() time.Time { return now },
	})

	run, err := service.CreateRun(context.Background(), CreateRunRequest{
		TenantID:        "tenant-1",
		Engine:          "test",
		Model:           "structured-candidates",
		ModelVersion:    "v1",
		IntentHash:      "sha256:intent",
		InputHash:       "sha256:input",
		PromptHash:      "sha256:prompt",
		ContextPackHash: "sha256:context",
		Candidates: []Candidate{{
			Kind:       CandidateLink,
			Operation:  OperationUpsert,
			LinkType:   "person_member_of_team",
			From:       &ObjectRef{Type: "person", ID: "person-1"},
			To:         &ObjectRef{Type: "team", ID: "team-1"},
			Confidence: 0.92,
		}},
	})
	if err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}
	if run.ID == "" {
		t.Fatalf("expected generated run id")
	}
	if run.CandidateCount != 1 {
		t.Fatalf("expected candidate count 1, got %d", run.CandidateCount)
	}
	if run.Status != RunStatusDraft {
		t.Fatalf("expected draft status, got %q", run.Status)
	}
	if run.RawPrompt != "" || run.RawOutput != "" || run.RawInput != "" {
		t.Fatalf("run leaked raw prompt/input/output: %+v", run)
	}
	if !run.CreatedAt.Equal(now) || !run.UpdatedAt.Equal(now) {
		t.Fatalf("expected deterministic timestamps, got created=%s updated=%s", run.CreatedAt, run.UpdatedAt)
	}
}

func TestGetReasoningRunRequiresTenantMatch(t *testing.T) {
	service := NewService(ServiceOptions{
		Store: NewMemoryStore(),
		Clock: func() time.Time { return time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC) },
	})
	run, err := service.CreateRun(context.Background(), CreateRunRequest{
		TenantID:        "tenant-1",
		Engine:          "test",
		Model:           "structured-candidates",
		IntentHash:      "sha256:intent",
		InputHash:       "sha256:input",
		PromptHash:      "sha256:prompt",
		ContextPackHash: "sha256:context",
	})
	if err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := service.GetRun(context.Background(), "tenant-2", run.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for cross-tenant get, got %v", err)
	}
}

func TestListReasoningRunsRequiresTenantAndFilters(t *testing.T) {
	now := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	ticks := 0
	service := NewService(ServiceOptions{
		Store: NewMemoryStore(),
		Clock: func() time.Time {
			ticks++
			return now.Add(time.Duration(ticks) * time.Nanosecond)
		},
	})
	for _, req := range []CreateRunRequest{
		{TenantID: "tenant-1", Engine: "test", Model: "structured-candidates"},
		{TenantID: "tenant-2", Engine: "test", Model: "structured-candidates"},
	} {
		if _, err := service.CreateRun(context.Background(), req); err != nil {
			t.Fatalf("CreateRun(%s) returned error: %v", req.TenantID, err)
		}
	}

	if _, err := service.ListRuns(context.Background(), " "); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest for empty tenant, got %v", err)
	}

	runs, err := service.ListRuns(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != 1 || runs[0].TenantID != "tenant-1" {
		t.Fatalf("expected tenant-1 run only, got %+v", runs)
	}
	runs[0].TenantID = "mutated"
	again, err := service.ListRuns(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("ListRuns returned error on second call: %v", err)
	}
	if len(again) != 1 || again[0].TenantID != "tenant-1" {
		t.Fatalf("store returned mutable run slice: %+v", again)
	}
}

func TestBuildContextPackIncludesRegistryGraphAndActions(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	registry := testReasoningRegistry(t, ctx)
	graph := &fakeGraphReader{
		objects: []contextgraph.Object{
			{ID: "person-1", TenantID: "tenant-1", ObjectType: "person", CanonicalKey: "person:1", Label: "Ada", EvidenceRefs: []contextgraph.Ref{{Kind: "message", ID: "msg-1"}}, UpdatedAt: now},
			{ID: "team-1", TenantID: "tenant-1", ObjectType: "team", CanonicalKey: "team:1", Label: "Growth", UpdatedAt: now},
			{ID: "person-2", TenantID: "tenant-2", ObjectType: "person", CanonicalKey: "person:2", Label: "Other", UpdatedAt: now},
		},
		links: []contextgraph.Link{
			{ID: "link-1", TenantID: "tenant-1", LinkType: "person_member_of_team", FromObjectID: "person-1", ToObjectID: "team-1", UpdatedAt: now},
		},
		facts: []contextgraph.Fact{
			{ID: "fact-1", TenantID: "tenant-1", Subject: contextgraph.Ref{Kind: "person", ID: "person-1"}, Predicate: "title", Value: "Engineer", UpdatedAt: now},
		},
		evidence: []contextgraph.Evidence{
			{ID: "msg-1", TenantID: "tenant-1", SourceRef: contextgraph.Ref{Kind: "message", ID: "msg-1"}, Summary: "request", ObservedAt: now},
		},
	}
	service := NewService(ServiceOptions{
		Store:    NewMemoryStore(),
		Registry: registry,
		Graph:    graph,
		Actions: fakeActionTypeReader{types: []actioncontract.ActionType{{
			ID:             "people.assign_member",
			TenantID:       "system",
			Name:           "Assign member",
			ObjectTypeRefs: []string{"person", "team"},
			RiskLevel:      actioncontract.RiskLow,
			Enabled:        true,
		}}},
		Clock: func() time.Time { return now },
	})

	pack, err := service.BuildContextPack(ctx, BuildContextPackRequest{
		TenantID:     "tenant-1",
		Intent:       "add Ada to Growth",
		SubjectRefs:  []contextgraph.Ref{{Kind: "person", ID: "person-1"}},
		EvidenceRefs: []contextgraph.Ref{{Kind: "message", ID: "msg-1"}},
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("BuildContextPack returned error: %v", err)
	}
	if pack.TenantID != "tenant-1" {
		t.Fatalf("expected tenant-1, got %q", pack.TenantID)
	}
	if pack.Hash == "" {
		t.Fatalf("expected non-empty context hash")
	}
	if len(pack.CandidateObjects) != 1 || pack.CandidateObjects[0].ID != "person-1" {
		t.Fatalf("expected only requested tenant object, got %+v", pack.CandidateObjects)
	}
	if len(pack.NeighborLinks) != 1 || pack.NeighborLinks[0].ID != "link-1" {
		t.Fatalf("expected connected tenant link, got %+v", pack.NeighborLinks)
	}
	if len(pack.Facts) != 1 || pack.Facts[0].ID != "fact-1" {
		t.Fatalf("expected attached fact, got %+v", pack.Facts)
	}
	if len(pack.AllowedObjectTypes) != 2 {
		t.Fatalf("expected registry object types, got %+v", pack.AllowedObjectTypes)
	}
	if len(pack.AllowedLinkTypes) != 1 || pack.AllowedLinkTypes[0].ID != "person_member_of_team" {
		t.Fatalf("expected allowed link type, got %+v", pack.AllowedLinkTypes)
	}
	if len(pack.AllowedPropertyTypes) != 1 || pack.AllowedPropertyTypes[0].PropertyID != "title" {
		t.Fatalf("expected allowed property type, got %+v", pack.AllowedPropertyTypes)
	}
	if len(pack.AllowedActions) != 1 || string(pack.AllowedActions[0].ID) != "people.assign_member" {
		t.Fatalf("expected visible action, got %+v", pack.AllowedActions)
	}
}

func TestBuildContextPackCapsResultSize(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	service := NewService(ServiceOptions{
		Store:    NewMemoryStore(),
		Registry: testReasoningRegistry(t, ctx),
		Graph: &fakeGraphReader{objects: []contextgraph.Object{
			{ID: "person-1", TenantID: "tenant-1", ObjectType: "person", CanonicalKey: "person:1", Label: "Ada", UpdatedAt: now},
			{ID: "person-2", TenantID: "tenant-1", ObjectType: "person", CanonicalKey: "person:2", Label: "Grace", UpdatedAt: now},
		}},
		Clock: func() time.Time { return now },
	})

	pack, err := service.BuildContextPack(ctx, BuildContextPackRequest{
		TenantID: "tenant-1",
		Limit:    1,
	})
	if err != nil {
		t.Fatalf("BuildContextPack returned error: %v", err)
	}
	if len(pack.CandidateObjects) != 1 {
		t.Fatalf("expected one object due to limit, got %+v", pack.CandidateObjects)
	}
}

func TestValidateRunAcceptsValidLinkCandidate(t *testing.T) {
	ctx := context.Background()
	service := testValidationService(t, ctx, actioncontract.RiskLow)
	run := createReasoningRunForValidation(t, ctx, service, Candidate{
		ID:        "candidate-1",
		Kind:      CandidateLink,
		Operation: OperationUpsert,
		LinkType:  "person_member_of_team",
		From:      &ObjectRef{Type: "person", ID: "person-1"},
		To:        &ObjectRef{Type: "team", ID: "team-1"},
	})

	validated, err := service.ValidateRun(ctx, "tenant-1", run.ID)
	if err != nil {
		t.Fatalf("ValidateRun returned error: %v", err)
	}
	if validated.ValidCandidateCount != 1 || validated.RejectedCandidateCount != 0 {
		t.Fatalf("unexpected validation counts: %+v", validated)
	}
	if len(validated.Validation) != 1 || validated.Validation[0].Status != ValidationAccepted {
		t.Fatalf("expected accepted validation, got %+v", validated.Validation)
	}
}

func TestValidateRunRejectsUnknownLinkType(t *testing.T) {
	ctx := context.Background()
	service := testValidationService(t, ctx, actioncontract.RiskLow)
	run := createReasoningRunForValidation(t, ctx, service, Candidate{
		ID:        "candidate-1",
		Kind:      CandidateLink,
		Operation: OperationUpsert,
		LinkType:  "unknown_link",
		From:      &ObjectRef{Type: "person", ID: "person-1"},
		To:        &ObjectRef{Type: "team", ID: "team-1"},
	})

	validated, err := service.ValidateRun(ctx, "tenant-1", run.ID)
	if err != nil {
		t.Fatalf("ValidateRun returned error: %v", err)
	}
	assertValidationIssue(t, validated, "unknown_link_type")
}

func TestValidateRunRejectsCrossTenantObjectRef(t *testing.T) {
	ctx := context.Background()
	service := testValidationService(t, ctx, actioncontract.RiskLow)
	run := createReasoningRunForValidation(t, ctx, service, Candidate{
		ID:        "candidate-1",
		Kind:      CandidateLink,
		Operation: OperationUpsert,
		LinkType:  "person_member_of_team",
		From:      &ObjectRef{Type: "person", ID: "person-2"},
		To:        &ObjectRef{Type: "team", ID: "team-1"},
	})

	validated, err := service.ValidateRun(ctx, "tenant-1", run.ID)
	if err != nil {
		t.Fatalf("ValidateRun returned error: %v", err)
	}
	assertValidationIssue(t, validated, "cross_tenant_reference")
}

func TestValidateRunRejectsFactValueTypeMismatch(t *testing.T) {
	ctx := context.Background()
	service := testValidationService(t, ctx, actioncontract.RiskLow)
	run := createReasoningRunForValidation(t, ctx, service, Candidate{
		ID:        "candidate-1",
		Kind:      CandidateFact,
		Operation: OperationUpsert,
		Subject:   &ObjectRef{Type: "person", ID: "person-1"},
		Predicate: "title",
		Value:     42,
	})

	validated, err := service.ValidateRun(ctx, "tenant-1", run.ID)
	if err != nil {
		t.Fatalf("ValidateRun returned error: %v", err)
	}
	assertValidationIssue(t, validated, "fact_value_type_mismatch")
}

func TestValidateRunMarksHighRiskActionReviewRequired(t *testing.T) {
	ctx := context.Background()
	service := testValidationService(t, ctx, actioncontract.RiskHigh)
	run := createReasoningRunForValidation(t, ctx, service, Candidate{
		ID:           "candidate-1",
		Kind:         CandidateAction,
		Operation:    OperationSuggest,
		ActionTypeID: "people.assign_member",
		ObjectRefs: []actioncontract.ObjectRef{
			{Kind: "person", ID: "person-1"},
			{Kind: "team", ID: "team-1"},
		},
	})

	validated, err := service.ValidateRun(ctx, "tenant-1", run.ID)
	if err != nil {
		t.Fatalf("ValidateRun returned error: %v", err)
	}
	if validated.ReviewRequiredCount != 1 || validated.RejectedCandidateCount != 0 {
		t.Fatalf("unexpected validation counts: %+v", validated)
	}
	if len(validated.Validation) != 1 || validated.Validation[0].Status != ValidationReviewRequired {
		t.Fatalf("expected review required validation, got %+v", validated.Validation)
	}
	assertValidationIssue(t, validated, "action_review_required")
}

func TestCreateProposalsCreatesReviewItemsForAcceptedCandidates(t *testing.T) {
	ctx := context.Background()
	service := testValidationService(t, ctx, actioncontract.RiskHigh)
	run, err := service.CreateRun(ctx, CreateRunRequest{
		TenantID:        "tenant-1",
		Engine:          "test",
		Model:           "structured-candidates",
		IntentHash:      "sha256:intent",
		InputHash:       "sha256:input",
		PromptHash:      "sha256:prompt",
		ContextPackHash: "sha256:context",
		Candidates: []Candidate{
			{
				ID:        "link-candidate",
				Kind:      CandidateLink,
				Operation: OperationUpsert,
				LinkType:  "person_member_of_team",
				From:      &ObjectRef{Type: "person", ID: "person-1"},
				To:        &ObjectRef{Type: "team", ID: "team-1"},
			},
			{
				ID:           "action-candidate",
				Kind:         CandidateAction,
				Operation:    OperationSuggest,
				ActionTypeID: "people.assign_member",
				ObjectRefs: []actioncontract.ObjectRef{
					{Kind: "person", ID: "person-1"},
					{Kind: "team", ID: "team-1"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	proposed, err := service.CreateProposals(ctx, "tenant-1", run.ID)
	if err != nil {
		t.Fatalf("CreateProposals returned error: %v", err)
	}
	if proposed.Status != RunStatusProposed {
		t.Fatalf("expected proposed status, got %q", proposed.Status)
	}
	if len(proposed.Proposals) != 2 {
		t.Fatalf("expected two proposals, got %+v", proposed.Proposals)
	}
	if proposed.Proposals[0].Status != ProposalStatusProposed || proposed.Proposals[1].Status != ProposalStatusProposed {
		t.Fatalf("expected proposed review items, got %+v", proposed.Proposals)
	}
}

func TestCreateProposalsDoesNotCreateItemsForRejectedCandidates(t *testing.T) {
	ctx := context.Background()
	service := testValidationService(t, ctx, actioncontract.RiskLow)
	run := createReasoningRunForValidation(t, ctx, service, Candidate{
		ID:        "candidate-1",
		Kind:      CandidateLink,
		Operation: OperationUpsert,
		LinkType:  "unknown_link",
		From:      &ObjectRef{Type: "person", ID: "person-1"},
		To:        &ObjectRef{Type: "team", ID: "team-1"},
	})

	proposed, err := service.CreateProposals(ctx, "tenant-1", run.ID)
	if err != nil {
		t.Fatalf("CreateProposals returned error: %v", err)
	}
	if len(proposed.Proposals) != 0 {
		t.Fatalf("expected no proposals for rejected candidate, got %+v", proposed.Proposals)
	}
}

func TestCreateProposalsIsIdempotentForRun(t *testing.T) {
	ctx := context.Background()
	service := testValidationService(t, ctx, actioncontract.RiskLow)
	run := createReasoningRunForValidation(t, ctx, service, Candidate{
		ID:        "candidate-1",
		Kind:      CandidateLink,
		Operation: OperationUpsert,
		LinkType:  "person_member_of_team",
		From:      &ObjectRef{Type: "person", ID: "person-1"},
		To:        &ObjectRef{Type: "team", ID: "team-1"},
	})

	first, err := service.CreateProposals(ctx, "tenant-1", run.ID)
	if err != nil {
		t.Fatalf("first CreateProposals returned error: %v", err)
	}
	second, err := service.CreateProposals(ctx, "tenant-1", run.ID)
	if err != nil {
		t.Fatalf("second CreateProposals returned error: %v", err)
	}
	if len(first.Proposals) != 1 || len(second.Proposals) != 1 {
		t.Fatalf("expected one idempotent proposal, got first=%+v second=%+v", first.Proposals, second.Proposals)
	}
	if first.Proposals[0].ID != second.Proposals[0].ID {
		t.Fatalf("expected stable proposal id, got %q and %q", first.Proposals[0].ID, second.Proposals[0].ID)
	}
}

func TestCreateProposalsPublishesAcceptedMetadataToGovernance(t *testing.T) {
	ctx := context.Background()
	service := testValidationService(t, ctx, actioncontract.RiskLow)
	governance := ontology.NewMemoryMetadataProposalStore()
	service.metadata = governance
	run := createReasoningRunForValidation(t, ctx, service, Candidate{
		ID:             "metadata-candidate",
		Kind:           CandidateMetadata,
		Operation:      OperationSuggest,
		MetadataTarget: &MetadataTarget{Kind: "object_type", ID: "person"},
		MetadataKey:    "allowed_actions",
		MetadataValue:  []any{"people.assign_member"},
		Confidence:     0.91,
		Reason:         "Observed in validated workflows",
	})

	proposed, err := service.CreateProposals(ctx, "tenant-1", run.ID)
	if err != nil {
		t.Fatalf("CreateProposals returned error: %v", err)
	}
	if len(proposed.Proposals) != 1 || proposed.Proposals[0].GovernanceRef == nil {
		t.Fatalf("reasoning proposal is not linked to governance: %+v", proposed.Proposals)
	}
	page, err := governance.ListMetadataProposalPage(ctx, ontology.MetadataProposalQuery{TenantID: "tenant-1", Limit: 10})
	if err != nil {
		t.Fatalf("ListMetadataProposalPage returned error: %v", err)
	}
	if len(page.Proposals) != 1 {
		t.Fatalf("governance proposals = %+v", page.Proposals)
	}
	got := page.Proposals[0]
	if got.ID != proposed.Proposals[0].GovernanceRef.ID || got.TargetKind != "object_type" || got.TargetID != "person" || got.ProposedValue != `["people.assign_member"]` || got.SourceRef != run.ID+":metadata-candidate" {
		t.Fatalf("governance proposal = %+v", got)
	}
}

func testReasoningRegistry(t *testing.T, ctx context.Context) *ontology.MemoryRegistry {
	t.Helper()
	registry := ontology.NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ontology.ObjectType{
		ID: "person", TenantScope: ontology.TenantScopeGlobal, DisplayName: "Person", CanonicalKeyRule: "person:{id}",
	}); err != nil {
		t.Fatalf("SaveObjectType(person): %v", err)
	}
	if err := registry.SaveObjectType(ctx, ontology.ObjectType{
		ID: "team", TenantScope: ontology.TenantScopeGlobal, DisplayName: "Team", CanonicalKeyRule: "team:{id}",
	}); err != nil {
		t.Fatalf("SaveObjectType(team): %v", err)
	}
	if err := registry.SaveLinkType(ctx, ontology.LinkType{
		ID: "person_member_of_team", TenantScope: ontology.TenantScopeGlobal, FromObjectType: "person", ToObjectType: "team", Cardinality: ontology.CardinalityManyToMany,
	}); err != nil {
		t.Fatalf("SaveLinkType: %v", err)
	}
	if err := registry.SavePropertyType(ctx, ontology.PropertyType{
		ID: "person.title", ObjectTypeID: "person", PropertyID: "title", ValueType: ontology.ValueTypeString,
	}); err != nil {
		t.Fatalf("SavePropertyType: %v", err)
	}
	return registry
}

type fakeGraphReader struct {
	objects  []contextgraph.Object
	links    []contextgraph.Link
	facts    []contextgraph.Fact
	evidence []contextgraph.Evidence
}

func (f *fakeGraphReader) Snapshot(ctx context.Context, tenantID string) ([]contextgraph.Object, []contextgraph.Link, []contextgraph.Fact, []contextgraph.Evidence, error) {
	return append([]contextgraph.Object(nil), f.objects...), append([]contextgraph.Link(nil), f.links...), append([]contextgraph.Fact(nil), f.facts...), append([]contextgraph.Evidence(nil), f.evidence...), ctx.Err()
}

type fakeActionTypeReader struct {
	types []actioncontract.ActionType
}

func (f fakeActionTypeReader) ListActionTypes(ctx context.Context) ([]actioncontract.ActionType, error) {
	return append([]actioncontract.ActionType(nil), f.types...), ctx.Err()
}

func testValidationService(t *testing.T, ctx context.Context, risk actioncontract.RiskLevel) *Service {
	t.Helper()
	now := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	return NewService(ServiceOptions{
		Store:    NewMemoryStore(),
		Registry: testReasoningRegistry(t, ctx),
		Graph: &fakeGraphReader{
			objects: []contextgraph.Object{
				{ID: "person-1", TenantID: "tenant-1", ObjectType: "person", CanonicalKey: "person:1", Label: "Ada", UpdatedAt: now},
				{ID: "team-1", TenantID: "tenant-1", ObjectType: "team", CanonicalKey: "team:1", Label: "Growth", UpdatedAt: now},
				{ID: "person-2", TenantID: "tenant-2", ObjectType: "person", CanonicalKey: "person:2", Label: "Other", UpdatedAt: now},
			},
		},
		Actions: fakeActionTypeReader{types: []actioncontract.ActionType{{
			ID:             "people.assign_member",
			TenantID:       "system",
			Name:           "Assign member",
			ObjectTypeRefs: []string{"person", "team"},
			RiskLevel:      risk,
			Enabled:        true,
		}}},
		Clock: func() time.Time { return now },
	})
}

func createReasoningRunForValidation(t *testing.T, ctx context.Context, service *Service, candidate Candidate) ReasoningRun {
	t.Helper()
	run, err := service.CreateRun(ctx, CreateRunRequest{
		TenantID:        "tenant-1",
		Engine:          "test",
		Model:           "structured-candidates",
		IntentHash:      "sha256:intent",
		InputHash:       "sha256:input",
		PromptHash:      "sha256:prompt",
		ContextPackHash: "sha256:context",
		Candidates:      []Candidate{candidate},
	})
	if err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}
	return run
}

func assertValidationIssue(t *testing.T, run ReasoningRun, code string) {
	t.Helper()
	if run.RejectedCandidateCount == 0 && run.ReviewRequiredCount == 0 {
		t.Fatalf("expected validation issue %q, got counts %+v", code, run)
	}
	for _, result := range run.Validation {
		for _, issue := range result.Issues {
			if issue.Code == code {
				return
			}
		}
	}
	t.Fatalf("expected issue %q, got %+v", code, run.Validation)
}
