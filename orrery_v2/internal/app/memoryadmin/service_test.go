package memoryadmin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	memaudit "orrery_v2/internal/memory/audit"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	memtestkit "orrery_v2/internal/memory/testkit"
	"orrery_v2/internal/platform/graphprotocol"
)

func TestViewerPermissionModelIncludesMemoryOperations(t *testing.T) {
	viewer := ViewerRef{ViewerID: "reviewer-1", PermissionScope: []string{PermissionMemoryRead, PermissionMemoryReview, PermissionMemoryWorldManage, PermissionMemoryAuditRead}}
	for _, permission := range []string{PermissionMemoryRead, PermissionMemoryReview, PermissionMemoryWorldManage, PermissionMemoryAuditRead} {
		if !viewer.Can(permission) {
			t.Fatalf("viewer.Can(%q) = false", permission)
		}
	}
	if viewer.Can(PermissionMemoryWrite) {
		t.Fatalf("viewer.Can(memory.write) = true, want false")
	}
}

func TestMapCandidateDTOCarriesLayerScopeSensitivityAndLineage(t *testing.T) {
	candidate := memcore.MemoryCandidate{
		ID:              "candidate-1",
		SourceType:      memcore.SourceDecision,
		SourceRef:       "decision-1",
		ProposedScope:   memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:    agentcore.MemoryKindMission,
		ProposedLayer:   memcore.MemoryLayerFact,
		Topic:           "decision",
		Summary:         "Use deterministic review.",
		Warnings:        []string{"Do not accept without source evidence."},
		LineageRefs:     []string{"discussion_message:message-1"},
		SupersedesRefs:  []string{"memory-old-world"},
		ContradictsRefs: []string{"memory-conflict-world"},
		EvidenceRefs:    []string{"evidence:evidence-1"},
		Sensitivity:     memcore.SensitivityLegal,
		DeIDLevel:       memcore.DeIDTeamSafe,
		Consent:         memcore.ConsentGranted,
		ReviewRequired:  true,
		ReviewReason:    "legal content requires review",
		Confidence:      0.82,
		CreatedBy:       agentcore.AgentID("agent-1"),
		Status:          memcore.CandidateProposed,
		CreatedAt:       time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC),
		UpdatedAt:       time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC),
	}

	dto := MapCandidateDTO(candidate)

	if dto.CandidateID != "candidate-1" || dto.ProposedLayer != "fact" {
		t.Fatalf("candidate dto identity/layer = %+v", dto)
	}
	if dto.ProposedScope.Kind != "mission" || dto.ProposedScope.ID != "mission-1" {
		t.Fatalf("candidate dto scope = %+v", dto.ProposedScope)
	}
	if dto.Sensitivity != "legal" || dto.DeIDLevel != "team_safe" || dto.Consent != "granted" {
		t.Fatalf("candidate dto sensitivity = %+v", dto)
	}
	if len(dto.EvidenceRefs) != 1 || len(dto.LineageRefs) != 1 || len(dto.SupersedesRefs) != 1 || len(dto.ContradictsRefs) != 1 {
		t.Fatalf("candidate dto refs = %+v", dto)
	}
	if len(dto.Warnings) != 1 || dto.Warnings[0] != "Do not accept without source evidence." {
		t.Fatalf("candidate dto warnings = %+v", dto.Warnings)
	}
	if dto.Confidence != 0.82 {
		t.Fatalf("candidate dto confidence = %v", dto.Confidence)
	}
}

func TestServiceListCandidatesFiltersSensitiveWithoutAuditRead(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	_ = store.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-normal",
		SourceType:    memcore.SourceSummary,
		SourceRef:     "summary-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Summary:       "Normal fact.",
		Sensitivity:   memcore.SensitivityNormal,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	})
	_ = store.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-legal",
		SourceType:    memcore.SourceDecision,
		SourceRef:     "decision-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Summary:       "Legal fact.",
		Sensitivity:   memcore.SensitivityLegal,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	})
	service := NewService(store, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now })

	page, err := service.ListCandidates(ctx, ViewerRef{ViewerID: "reader-1", PermissionScope: []string{PermissionMemoryRead}, MissionIDs: []string{"mission-1"}}, ListCandidatesQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListCandidates: %v", err)
	}
	if len(page.Candidates) != 1 || page.Candidates[0].CandidateID != "candidate-normal" {
		t.Fatalf("page = %+v, want only normal candidate", page)
	}
}

func TestServiceReadRequiresMemoryReadEvenWithAuditRead(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-audit-only",
		SourceType:    memcore.SourceSummary,
		SourceRef:     "summary-audit-only",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Summary:       "Audit-only readers must not read memory payloads.",
		Sensitivity:   memcore.SensitivityNormal,
		Status:        memcore.CandidateAccepted,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	if err := store.SaveRecord(ctx, memcore.MemoryRecord{
		ID:              "memory-audit-only",
		ProfileID:       agentcore.MemoryProfileID("profile-1"),
		CandidateID:     "candidate-audit-only",
		Scope:           memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		Layer:           memcore.MemoryLayerFact,
		SourceRefs:      []string{"summary:summary-audit-only"},
		Summary:         "Audit-only readers must not read records.",
		Facts:           []string{"Audit-only readers must not read records."},
		Sensitivity:     memcore.SensitivityNormal,
		DeIDLevel:       memcore.DeIDRaw,
		LifecycleStatus: memcore.MemoryLifecycleActive,
		Visibility:      memcore.Visibility{Mode: memcore.VisibilityScopedMembers, ScopeKind: memcore.ScopeMission, ScopeID: "mission-1"},
		Confidence:      1,
		CreatedBy:       agentcore.AgentID("agent-1"),
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("SaveRecord: %v", err)
	}
	service := NewService(store, nil, func() time.Time { return now })
	viewer := ViewerRef{ViewerID: "auditor-only", PermissionScope: []string{PermissionMemoryAuditRead}}

	if _, err := service.ListCandidates(ctx, viewer, ListCandidatesQuery{Limit: 10}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("ListCandidates audit-only err = %v, want permission denied", err)
	}
	if _, err := service.ListRecords(ctx, viewer, ListRecordsQuery{Limit: 10}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("ListRecords audit-only err = %v, want permission denied", err)
	}
	if _, err := service.GetRecord(ctx, viewer, "memory-audit-only"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("GetRecord audit-only err = %v, want permission denied", err)
	}
}

func TestServiceCandidatesRequireMatchingScopeMembership(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-visible-scope",
		SourceType:    memcore.SourceSummary,
		SourceRef:     "summary-visible",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Summary:       "Visible mission candidate.",
		Sensitivity:   memcore.SensitivityNormal,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("SaveCandidate visible: %v", err)
	}
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-private-scope",
		SourceType:    memcore.SourceSummary,
		SourceRef:     "summary-private",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-owner"},
		ProposedKind:  agentcore.MemoryKindPersonal,
		ProposedLayer: memcore.MemoryLayerFact,
		Summary:       "Private candidate.",
		Sensitivity:   memcore.SensitivityNormal,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("agent-owner"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("SaveCandidate private: %v", err)
	}
	service := NewService(store, nil, func() time.Time { return now })
	missionReader := ViewerRef{ViewerID: "reader-1", PermissionScope: []string{PermissionMemoryRead}, MissionIDs: []string{"mission-1"}}

	page, err := service.ListCandidates(ctx, missionReader, ListCandidatesQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListCandidates: %v", err)
	}
	if len(page.Candidates) != 1 || page.Candidates[0].CandidateID != "candidate-visible-scope" {
		t.Fatalf("page = %+v, want only mission-visible candidate", page)
	}
	auditPage, err := service.ListCandidates(ctx, ViewerRef{ViewerID: "auditor-1", PermissionScope: []string{PermissionMemoryRead, PermissionMemoryAuditRead}}, ListCandidatesQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListCandidates auditor: %v", err)
	}
	if len(auditPage.Candidates) != 2 {
		t.Fatalf("audit page = %+v, want candidates across scopes", auditPage)
	}
	_, err = service.AcceptCandidate(ctx, ViewerRef{ViewerID: "reviewer-1", PermissionScope: []string{PermissionMemoryReview}}, "candidate-private-scope", AcceptCandidateRequest{ProfileID: "profile-1"})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("AcceptCandidate private err = %v, want permission denied", err)
	}
	owner, err := service.AcceptCandidate(ctx, ViewerRef{ViewerID: "agent-owner", AgentID: "agent-owner", PermissionScope: []string{PermissionMemoryReview}}, "candidate-private-scope", AcceptCandidateRequest{ProfileID: "profile-1", MemoryID: "memory-private-candidate"})
	if err != nil {
		t.Fatalf("AcceptCandidate owner: %v", err)
	}
	if owner.CandidateID != "candidate-private-scope" {
		t.Fatalf("owner accepted record = %+v", owner)
	}
}

func TestServiceAuditReviewerCanReviewCandidateAcrossScopes(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-audit-review",
		SourceType:    memcore.SourceSummary,
		SourceRef:     "summary-audit-review",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-owner"},
		ProposedKind:  agentcore.MemoryKindPersonal,
		ProposedLayer: memcore.MemoryLayerFact,
		Summary:       "Cross-scope review candidate.",
		Sensitivity:   memcore.SensitivityNormal,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("agent-owner"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	service := NewService(store, nil, func() time.Time { return now })
	viewer := ViewerRef{ViewerID: "auditor-1", PermissionScope: []string{PermissionMemoryReview, PermissionMemoryAuditRead}}

	receipt, err := service.RejectCandidate(ctx, viewer, "candidate-audit-review", RejectCandidateRequest{Reason: "duplicate"})
	if err != nil {
		t.Fatalf("RejectCandidate auditor: %v", err)
	}
	if receipt.Status != string(memcore.CandidateRejected) {
		t.Fatalf("receipt = %+v", receipt)
	}
}

func TestServiceCreateCandidateRequiresWriteAndStoresProposedCandidate(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	audit := &memtestkit.MemoryAuditRecorder{}
	service := NewService(store, audit, func() time.Time { return now })

	dto, err := service.CreateCandidate(ctx, ViewerRef{ViewerID: "writer-1", PermissionScope: []string{PermissionMemoryWrite}, MissionIDs: []string{"mission-1"}}, CreateCandidateRequest{
		CandidateID:     "manual-1",
		SourceType:      "summary",
		SourceRef:       "manual-source-1",
		ProposedKind:    "mission",
		ProposedLayer:   "fact",
		ProposedScope:   MemoryScopeDTO{Kind: "mission", ID: "mission-1"},
		Summary:         "Manual memory summary.",
		EvidenceRefs:    []string{"evidence:evidence-1"},
		LineageRefs:     []string{"memory:source-1"},
		SupersedesRefs:  []string{"memory:old-world"},
		ContradictsRefs: []string{"memory:conflicting-world"},
		Confidence:      0.66,
	})
	if err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}
	if dto.CandidateID != "manual-1" || dto.Status != "proposed" || dto.CreatedBy != "writer-1" {
		t.Fatalf("dto = %+v", dto)
	}
	if dto.Confidence != 0.66 {
		t.Fatalf("candidate dto confidence = %v", dto.Confidence)
	}
	if len(dto.LineageRefs) != 1 || dto.LineageRefs[0] != "memory:source-1" || len(dto.SupersedesRefs) != 1 || dto.SupersedesRefs[0] != "memory:old-world" || len(dto.ContradictsRefs) != 1 || dto.ContradictsRefs[0] != "memory:conflicting-world" {
		t.Fatalf("candidate dto relation refs = %+v", dto)
	}
	events := audit.Snapshot()
	if len(events) != 1 || events[0].Type != memaudit.EventMemoryCandidateCreated || events[0].SubjectID != "manual-1" {
		t.Fatalf("events = %+v", events)
	}
}

func TestServiceCreateCandidateRequiresMatchingScopeMembership(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	service := NewService(store, nil, func() time.Time { return now })
	req := CreateCandidateRequest{
		CandidateID:   "manual-scope-1",
		SourceType:    "summary",
		SourceRef:     "manual-source-scope",
		ProposedKind:  "mission",
		ProposedLayer: "fact",
		ProposedScope: MemoryScopeDTO{Kind: "mission", ID: "mission-1"},
		Summary:       "Scoped manual memory.",
	}

	_, err := service.CreateCandidate(ctx, ViewerRef{ViewerID: "writer-1", PermissionScope: []string{PermissionMemoryWrite, PermissionMemoryAuditRead}}, req)
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("CreateCandidate unscoped audit writer err = %v, want permission denied", err)
	}
	dto, err := service.CreateCandidate(ctx, ViewerRef{ViewerID: "writer-1", PermissionScope: []string{PermissionMemoryWrite}, MissionIDs: []string{"mission-1"}}, req)
	if err != nil {
		t.Fatalf("CreateCandidate scoped writer: %v", err)
	}
	if dto.CandidateID != "manual-scope-1" || dto.ProposedScope.ID != "mission-1" {
		t.Fatalf("dto = %+v", dto)
	}
}

func TestServiceAcceptCandidateCreatesLinkedRecordAndAudits(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	audit := &memtestkit.MemoryAuditRecorder{}
	candidate := memcore.MemoryCandidate{
		ID:              "candidate-accept-1",
		MissionID:       "mission-1",
		SourceType:      memcore.SourceEvidence,
		SourceRef:       "evidence-1",
		ProposedScope:   memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:    agentcore.MemoryKindMission,
		ProposedLayer:   memcore.MemoryLayerFact,
		Topic:           "qa",
		Summary:         "QA passed.",
		EvidenceRefs:    []string{"evidence:evidence-1"},
		LineageRefs:     []string{"qa_result:qa-1"},
		SupersedesRefs:  []string{"memory-old-world"},
		ContradictsRefs: []string{"memory-conflict-world"},
		Sensitivity:     memcore.SensitivityLegal,
		DeIDLevel:       memcore.DeIDTeamSafe,
		Confidence:      0.78,
		Status:          memcore.CandidateProposed,
		CreatedBy:       agentcore.AgentID("agent-1"),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := store.SaveCandidate(ctx, candidate); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	service := NewService(store, audit, func() time.Time { return now })

	record, err := service.AcceptCandidate(ctx, ViewerRef{ViewerID: "reviewer-1", PermissionScope: []string{PermissionMemoryReview, PermissionMemoryAuditRead}, MissionIDs: []string{"mission-1"}}, "candidate-accept-1", AcceptCandidateRequest{
		ProfileID: "memory-profile-1",
		MemoryID:  "memory-record-1",
		Reason:    "approved evidence",
	})
	if err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}
	if record.MemoryID != "memory-record-1" || record.CandidateID != "candidate-accept-1" {
		t.Fatalf("record identity = %+v", record)
	}
	if record.Sensitivity != "legal" || len(record.SourceMemoryRefs) != 2 || len(record.SourceEvidenceRefs) != 1 {
		t.Fatalf("record refs/sensitivity = %+v", record)
	}
	if len(record.SupersedesRefs) != 1 || record.SupersedesRefs[0] != "memory-old-world" || len(record.ContradictsRefs) != 1 || record.ContradictsRefs[0] != "memory-conflict-world" {
		t.Fatalf("record world relation refs = %+v/%+v", record.SupersedesRefs, record.ContradictsRefs)
	}
	if record.Confidence != 0.78 {
		t.Fatalf("record confidence = %v", record.Confidence)
	}
	saved, err := store.GetCandidate(ctx, "candidate-accept-1")
	if err != nil {
		t.Fatalf("GetCandidate: %v", err)
	}
	if saved.Status != memcore.CandidateAccepted {
		t.Fatalf("candidate status = %q", saved.Status)
	}
	events := audit.Snapshot()
	if len(events) != 2 || events[0].Type != memaudit.EventMemoryCandidateAccepted || events[1].Type != memaudit.EventMemoryRecordCreated {
		t.Fatalf("events = %+v", events)
	}
}

func TestServiceAcceptCandidateInfersAndCreatesCollectiveProfile(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	candidate := memcore.MemoryCandidate{
		ID:            "candidate-discussion-1",
		SourceType:    memcore.SourceDiscussionMessage,
		SourceRef:     "message-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeDiscussion, ID: "discussion-1"},
		ProposedKind:  agentcore.MemoryKindDiscussion,
		ProposedLayer: memcore.MemoryLayerFact,
		Summary:       "团队决定采用分阶段发布。",
		Status:        memcore.CandidateProposed,
		CreatedBy:     "agent-1",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.SaveCandidate(ctx, candidate); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	service := NewService(store, nil, func() time.Time { return now })
	record, err := service.AcceptCandidate(ctx, ViewerRef{
		ViewerID:        "reviewer-1",
		DiscussionIDs:   []string{"discussion-1"},
		PermissionScope: []string{PermissionMemoryReview},
	}, candidate.ID, AcceptCandidateRequest{})
	if err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}
	wantProfileID := memcore.ScopeMemoryProfileID(candidate.ProposedScope)
	if record.ProfileID != string(wantProfileID) {
		t.Fatalf("record profile = %q, want %q", record.ProfileID, wantProfileID)
	}
	profile, err := store.GetProfile(ctx, wantProfileID)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if profile.MemoryKind != agentcore.MemoryKindDiscussion || profile.DefaultScope.ID != "discussion-1" {
		t.Fatalf("discussion profile = %+v", profile)
	}
}

func TestServiceAcceptCandidatePublishesMemoryRecordAcceptedEvent(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	candidate := memcore.MemoryCandidate{
		ID:            "candidate-accept-1",
		MissionID:     "mission-1",
		SourceType:    memcore.SourceEvidence,
		SourceRef:     "evidence-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         "qa",
		Summary:       "QA passed.",
		EvidenceRefs:  []string{"evidence:evidence-1"},
		LineageRefs:   []string{"qa_result:qa-1"},
		Confidence:    0.78,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.SaveCandidate(ctx, candidate); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	sink := &recordingMemoryRecordMetadataSink{}
	service := NewService(store, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now }).
		WithMemoryRecordMetadataSink(sink)

	record, err := service.AcceptCandidate(ctx, ViewerRef{ViewerID: "reviewer-1", PermissionScope: []string{PermissionMemoryReview}, MissionIDs: []string{"mission-1"}}, "candidate-accept-1", AcceptCandidateRequest{
		ProfileID: "memory-profile-1",
		MemoryID:  "memory-record-1",
	})
	if err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}

	events := sink.Snapshot()
	if len(events) != 2 {
		t.Fatalf("events = %+v, want accepted and scope link events", events)
	}
	event := events[0]
	if event.Type != "memory.record.accepted" || event.StreamID != record.MemoryID || event.TenantID != "mission:mission-1" {
		t.Fatalf("event identity = %+v", event)
	}
	if event.PublishedBy.Kind != "service" || event.PublishedBy.ID != "memory" {
		t.Fatalf("published by = %+v", event.PublishedBy)
	}
	if event.Payload["id"] != "memory-record-1" ||
		event.Payload["summary"] != "QA passed." ||
		event.Payload["layer"] != "fact" ||
		event.Payload["topic"] != "qa" ||
		event.Payload["scope_kind"] != "mission" ||
		event.Payload["scope_id"] != "mission-1" ||
		event.Payload["lifecycle_status"] != "active" ||
		event.Payload["confidence"] != 0.78 {
		t.Fatalf("payload = %+v", event.Payload)
	}
	if got := strings.Join(event.Payload["source_refs"].([]string), ","); got != "evidence:evidence-1,qa_result:qa-1" {
		t.Fatalf("source refs = %q", got)
	}
	if got := strings.Join(event.Payload["evidence_refs"].([]string), ","); got != "evidence:evidence-1" {
		t.Fatalf("evidence refs = %q", got)
	}
	scopeEvent := events[1]
	if scopeEvent.Type != "memory.scope.linked" ||
		scopeEvent.StreamID != record.MemoryID ||
		scopeEvent.Payload["memory_record_id"] != record.MemoryID ||
		scopeEvent.Payload["mission_id"] != "mission-1" {
		t.Fatalf("scope event = %+v", scopeEvent)
	}
}

func TestServiceAcceptCandidatePublishesMemoryScopeLinkedEvent(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	candidate := memcore.MemoryCandidate{
		ID:            "candidate-scope-link-1",
		MissionID:     "mission-1",
		SourceType:    memcore.SourceEvidence,
		SourceRef:     "evidence-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         "qa",
		Summary:       "QA scope should be graph traversable.",
		EvidenceRefs:  []string{"evidence:evidence-1"},
		LineageRefs:   []string{"task_contract:task-1", "qa_result:qa-1"},
		Confidence:    0.78,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.SaveCandidate(ctx, candidate); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	sink := &recordingMemoryRecordMetadataSink{}
	service := NewService(store, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now }).
		WithMemoryRecordMetadataSink(sink)

	if _, err := service.AcceptCandidate(ctx, ViewerRef{ViewerID: "reviewer-1", PermissionScope: []string{PermissionMemoryReview}, MissionIDs: []string{"mission-1"}}, "candidate-scope-link-1", AcceptCandidateRequest{
		ProfileID: "memory-profile-1",
		MemoryID:  "memory-scope-link-1",
	}); err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}

	events := sink.Snapshot()
	if len(events) != 2 {
		t.Fatalf("events = %+v, want accepted and scope link events", events)
	}
	event := events[1]
	if event.Type != "memory.scope.linked" ||
		event.ID != "memory-scope-linked:memory-scope-link-1" ||
		event.TenantID != "mission:mission-1" ||
		event.StreamID != "memory-scope-link-1" ||
		event.Payload["memory_record_id"] != "memory-scope-link-1" ||
		event.Payload["mission_id"] != "mission-1" ||
		event.Payload["task_id"] != "task-1" {
		t.Fatalf("scope event = %+v", event)
	}
	if _, ok := event.Payload["project_id"]; ok {
		t.Fatalf("scope event should omit unrelated project id: %+v", event.Payload)
	}
}

func TestServiceAcceptCandidatePublishesKnowledgeDocumentIDFromExplicitEvidenceRef(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	candidate := memcore.MemoryCandidate{
		ID:            "candidate-knowledge-ref-1",
		MissionID:     "mission-1",
		SourceType:    memcore.SourceEvidence,
		SourceRef:     "evidence-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         "release",
		Summary:       "Release review follows the handbook.",
		EvidenceRefs:  []string{"knowledge_document:kdoc-1"},
		Confidence:    0.82,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.SaveCandidate(ctx, candidate); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	sink := &recordingMemoryRecordMetadataSink{}
	service := NewService(store, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now }).
		WithMemoryRecordMetadataSink(sink)

	if _, err := service.AcceptCandidate(ctx, ViewerRef{ViewerID: "reviewer-1", PermissionScope: []string{PermissionMemoryReview}, MissionIDs: []string{"mission-1"}}, "candidate-knowledge-ref-1", AcceptCandidateRequest{
		ProfileID: "memory-profile-1",
		MemoryID:  "memory-record-knowledge-1",
	}); err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}

	events := sink.Snapshot()
	if len(events) != 3 {
		t.Fatalf("events = %+v", events)
	}
	if events[0].Payload["knowledge_document_id"] != "kdoc-1" {
		t.Fatalf("payload = %+v", events[0].Payload)
	}
	if events[1].Type != "memory.scope.linked" || events[1].Payload["mission_id"] != "mission-1" {
		t.Fatalf("scope event = %+v", events[1])
	}
	if events[2].Type != "memory.knowledge.citation.added" ||
		events[2].TenantID != "mission:mission-1" ||
		events[2].StreamID != "memory-record-knowledge-1" ||
		events[2].Payload["memory_record_id"] != "memory-record-knowledge-1" ||
		events[2].Payload["knowledge_document_id"] != "kdoc-1" {
		t.Fatalf("citation event = %+v", events[2])
	}
}

func TestServiceAcceptCandidateOmitsKnowledgeDocumentIDWhenAmbiguous(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	candidate := memcore.MemoryCandidate{
		ID:            "candidate-knowledge-ref-ambiguous",
		MissionID:     "mission-1",
		SourceType:    memcore.SourceEvidence,
		SourceRef:     "evidence-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         "release",
		Summary:       "Release review cites multiple documents.",
		EvidenceRefs:  []string{"knowledge_document:kdoc-1", "knowledge_document:kdoc-2"},
		Confidence:    0.82,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.SaveCandidate(ctx, candidate); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	sink := &recordingMemoryRecordMetadataSink{}
	service := NewService(store, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now }).
		WithMemoryRecordMetadataSink(sink)

	if _, err := service.AcceptCandidate(ctx, ViewerRef{ViewerID: "reviewer-1", PermissionScope: []string{PermissionMemoryReview}, MissionIDs: []string{"mission-1"}}, "candidate-knowledge-ref-ambiguous", AcceptCandidateRequest{
		ProfileID: "memory-profile-1",
		MemoryID:  "memory-record-knowledge-ambiguous",
	}); err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}

	events := sink.Snapshot()
	if len(events) != 2 {
		t.Fatalf("ambiguous knowledge references should only publish accepted and scope events: %+v", events)
	}
	if _, ok := events[0].Payload["knowledge_document_id"]; ok {
		t.Fatalf("payload should omit ambiguous knowledge_document_id: %+v", events[0].Payload)
	}
	if events[1].Type != "memory.scope.linked" || events[1].Payload["mission_id"] != "mission-1" {
		t.Fatalf("scope event = %+v", events[1])
	}
}

func TestServiceAcceptCandidatePublishesKnowledgeChunkCitationFromExplicitEvidenceRef(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 20, 14, 0, 0, 0, time.UTC)
	candidate := memcore.MemoryCandidate{
		ID:            "candidate-knowledge-chunk-ref-1",
		MissionID:     "mission-1",
		SourceType:    memcore.SourceEvidence,
		SourceRef:     "evidence-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         "release",
		Summary:       "Release rollout cites a source chunk.",
		EvidenceRefs:  []string{"knowledge_chunk:kdoc-1:0"},
		Confidence:    0.84,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.SaveCandidate(ctx, candidate); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	sink := &recordingMemoryRecordMetadataSink{}
	service := NewService(store, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now }).
		WithMemoryRecordMetadataSink(sink)

	if _, err := service.AcceptCandidate(ctx, ViewerRef{ViewerID: "reviewer-1", PermissionScope: []string{PermissionMemoryReview}, MissionIDs: []string{"mission-1"}}, "candidate-knowledge-chunk-ref-1", AcceptCandidateRequest{
		ProfileID: "memory-profile-1",
		MemoryID:  "memory-record-knowledge-chunk-1",
	}); err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}

	events := sink.Snapshot()
	if len(events) != 3 {
		t.Fatalf("events = %+v", events)
	}
	if events[0].Payload["knowledge_chunk_id"] != "kdoc-1:0" {
		t.Fatalf("payload = %+v", events[0].Payload)
	}
	if events[1].Type != "memory.scope.linked" || events[1].Payload["mission_id"] != "mission-1" {
		t.Fatalf("scope event = %+v", events[1])
	}
	if events[2].Type != "memory.knowledge_chunk.citation.added" ||
		events[2].TenantID != "mission:mission-1" ||
		events[2].StreamID != "memory-record-knowledge-chunk-1" ||
		events[2].Payload["memory_record_id"] != "memory-record-knowledge-chunk-1" ||
		events[2].Payload["knowledge_chunk_id"] != "kdoc-1:0" {
		t.Fatalf("chunk citation event = %+v", events[2])
	}
}

func TestServiceAcceptCandidatePublishesKnowledgeAnchorCitationFromExplicitEvidenceRef(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 20, 16, 0, 0, 0, time.UTC)
	anchorID := "kdoc-1:page:1:anchor:0007"
	candidate := memcore.MemoryCandidate{
		ID:            "candidate-knowledge-anchor-ref-1",
		MissionID:     "mission-1",
		SourceType:    memcore.SourceEvidence,
		SourceRef:     "evidence-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         "release",
		Summary:       "Release rollout cites a PageIndex anchor.",
		EvidenceRefs:  []string{"knowledge_anchor:" + anchorID},
		Confidence:    0.84,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.SaveCandidate(ctx, candidate); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	sink := &recordingMemoryRecordMetadataSink{}
	service := NewService(store, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now }).
		WithMemoryRecordMetadataSink(sink)

	if _, err := service.AcceptCandidate(ctx, ViewerRef{ViewerID: "reviewer-1", PermissionScope: []string{PermissionMemoryReview}, MissionIDs: []string{"mission-1"}}, "candidate-knowledge-anchor-ref-1", AcceptCandidateRequest{
		ProfileID: "memory-profile-1",
		MemoryID:  "memory-record-knowledge-anchor-1",
	}); err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}

	events := sink.Snapshot()
	if len(events) != 3 {
		t.Fatalf("events = %+v", events)
	}
	if events[0].Payload["knowledge_anchor_id"] != anchorID {
		t.Fatalf("payload = %+v", events[0].Payload)
	}
	if events[1].Type != "memory.scope.linked" || events[1].Payload["mission_id"] != "mission-1" {
		t.Fatalf("scope event = %+v", events[1])
	}
	if events[2].Type != "memory.knowledge_anchor.citation.added" ||
		events[2].TenantID != "mission:mission-1" ||
		events[2].StreamID != "memory-record-knowledge-anchor-1" ||
		events[2].Payload["memory_record_id"] != "memory-record-knowledge-anchor-1" ||
		events[2].Payload["knowledge_anchor_id"] != anchorID {
		t.Fatalf("anchor citation event = %+v", events[2])
	}
}

func TestServiceAcceptCandidatePublishesMemoryRecordAcceptedToOntologyRunner(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	candidate := memcore.MemoryCandidate{
		ID:            "candidate-accept-1",
		MissionID:     "mission-1",
		SourceType:    memcore.SourceEvidence,
		SourceRef:     "evidence-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         "qa",
		Summary:       "QA passed.",
		EvidenceRefs:  []string{"evidence:evidence-1"},
		LineageRefs:   []string{"qa_result:qa-1"},
		Confidence:    0.78,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.SaveCandidate(ctx, candidate); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	ontologyRegistry := ontology.NewMemoryRegistry()
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := ontology.RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := ontology.NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)
	service := NewService(store, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now }).
		WithMemoryRecordMetadataSink(OntologyMemoryRecordMetadataSink{Runner: runner})

	record, err := service.AcceptCandidate(ctx, ViewerRef{ViewerID: "reviewer-1", PermissionScope: []string{PermissionMemoryReview}, MissionIDs: []string{"mission-1"}}, "candidate-accept-1", AcceptCandidateRequest{
		ProfileID: "memory-profile-1",
		MemoryID:  "memory-record-1",
	})
	if err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}

	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "mission:mission-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 2 {
		t.Fatalf("objects = %+v", objects)
	}
	memoryObject := findContextGraphObject(t, objects, record.MemoryID)
	if memoryObject.ObjectType != "memory_record" {
		t.Fatalf("memory object = %+v", memoryObject)
	}
	missionReference := findContextGraphObject(t, objects, "mission-1")
	if missionReference.ObjectType != "mission" || missionReference.Status != "reference_pending" || missionReference.Properties["_orrery_reference"] != "pending" {
		t.Fatalf("mission reference = %+v", missionReference)
	}
	if len(facts) < 8 {
		t.Fatalf("facts = %+v", facts)
	}
	if len(links) != 1 || links[0].LinkType != "memory_about_mission" || links[0].FromObjectID != record.MemoryID || links[0].ToObjectID != "mission-1" {
		t.Fatalf("links = %+v", links)
	}
	if len(evidence) != 2 {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestServiceAcceptExperienceCandidateMarksSourceFactsDistilled(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	seedAcceptedSourceFactForExperience(t, ctx, store, "memory-source-1", "candidate-source-1", now)
	seedAcceptedSourceFactForExperience(t, ctx, store, "memory-source-2", "candidate-source-2", now)
	experience := memcore.MemoryCandidate{
		ID:            "candidate-experience-1",
		SourceType:    memcore.SourceSummary,
		SourceRef:     "experience:qa",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeProject, ID: "project-1"},
		ProposedKind:  agentcore.MemoryKindProject,
		ProposedLayer: memcore.MemoryLayerExperience,
		Topic:         "qa",
		Summary:       "QA needs regression evidence earlier.",
		Lessons:       []string{"Add regression evidence before QA review."},
		Warnings:      []string{"Do not treat as policy before review."},
		LineageRefs:   []string{"memory:memory-source-1", "memory:memory-source-2"},
		Sensitivity:   memcore.SensitivityNormal,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("distiller-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.SaveCandidate(ctx, experience); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	service := NewService(store, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now })

	record, err := service.AcceptCandidate(ctx, ViewerRef{ViewerID: "reviewer-1", PermissionScope: []string{PermissionMemoryReview}, ProjectIDs: []string{"project-1"}}, "candidate-experience-1", AcceptCandidateRequest{ProfileID: "profile-1", MemoryID: "memory-experience-1"})
	if err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}
	if record.Layer != "experience" || len(record.LineageRefs) != 2 || len(record.Warnings) != 1 {
		t.Fatalf("accepted experience record = %+v", record)
	}
	records, err := store.FindRecords(ctx, memstore.RecordQuery{LifecycleStatuses: []memcore.MemoryLifecycleStatus{memcore.MemoryLifecycleDistilled}, IncludeArchived: true})
	if err != nil {
		t.Fatalf("FindRecords: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("distilled source records = %+v", records)
	}
}

func TestServiceAcceptExperienceCandidatePublishesLineageLinkedEvents(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	seedAcceptedSourceFactForExperience(t, ctx, store, "memory-lineage-source-1", "candidate-lineage-source-1", now)
	seedAcceptedSourceFactForExperience(t, ctx, store, "memory-lineage-source-2", "candidate-lineage-source-2", now)
	experience := memcore.MemoryCandidate{
		ID:            "candidate-lineage-experience",
		SourceType:    memcore.SourceSummary,
		SourceRef:     "experience:qa",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeProject, ID: "project-1"},
		ProposedKind:  agentcore.MemoryKindProject,
		ProposedLayer: memcore.MemoryLayerExperience,
		Topic:         "qa",
		Summary:       "QA needs regression evidence earlier.",
		Lessons:       []string{"Add regression evidence before QA review."},
		LineageRefs:   []string{"memory:memory-lineage-source-1", "memory:memory-lineage-source-2", "qa_result:qa-1"},
		Sensitivity:   memcore.SensitivityNormal,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("distiller-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.SaveCandidate(ctx, experience); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	sink := &recordingMemoryRecordMetadataSink{}
	service := NewService(store, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now }).
		WithMemoryRecordMetadataSink(sink)

	if _, err := service.AcceptCandidate(ctx, ViewerRef{ViewerID: "reviewer-1", PermissionScope: []string{PermissionMemoryReview}, ProjectIDs: []string{"project-1"}}, "candidate-lineage-experience", AcceptCandidateRequest{ProfileID: "profile-1", MemoryID: "memory-lineage-experience"}); err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}

	events := sink.Snapshot()
	if len(events) != 6 {
		t.Fatalf("events = %+v, want accepted, scope, two lifecycle updates, and two lineage links", events)
	}
	if events[0].Type != "memory.record.accepted" || events[0].StreamID != "memory-lineage-experience" {
		t.Fatalf("accepted event = %+v", events[0])
	}
	if events[1].Type != "memory.scope.linked" || events[1].Payload["project_id"] != "project-1" {
		t.Fatalf("scope event = %+v", events[1])
	}
	for i, sourceID := range []string{"memory-lineage-source-1", "memory-lineage-source-2"} {
		event := events[i+2]
		if event.Type != "memory.record.lifecycle.updated" ||
			event.StreamID != sourceID ||
			event.Payload["lifecycle_status"] != string(memcore.MemoryLifecycleDistilled) {
			t.Fatalf("lifecycle event %d = %+v", i, event)
		}
	}
	for i, sourceID := range []string{"memory-lineage-source-1", "memory-lineage-source-2"} {
		event := events[i+4]
		if event.Type != "memory.lineage.linked" ||
			event.TenantID != "project:project-1" ||
			event.StreamID != "memory-lineage-experience" ||
			event.Payload["memory_record_id"] != "memory-lineage-experience" ||
			event.Payload["source_memory_record_id"] != sourceID {
			t.Fatalf("lineage event %d = %+v", i, event)
		}
	}
}

func TestServiceAcceptOrganizationWorldRequiresWorldManagePermission(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	candidate := memcore.MemoryCandidate{
		ID:              "candidate-world-1",
		SourceType:      memcore.SourceSummary,
		SourceRef:       "world:organization:org-1:quality",
		ProposedScope:   memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
		ProposedKind:    agentcore.MemoryKindOrganization,
		ProposedLayer:   memcore.MemoryLayerWorld,
		Topic:           "quality",
		Summary:         "Organization world quality memory.",
		Narrative:       "Organization quality belief requires review.",
		Lessons:         []string{"Use accepted quality source memory before updating organization guidance."},
		LineageRefs:     []string{"memory:memory-exp-1", "memory:memory-exp-2"},
		SupersedesRefs:  []string{"memory:memory-world-old"},
		ContradictsRefs: []string{"memory:memory-world-conflict"},
		Sensitivity:     memcore.SensitivityNormal,
		DeIDLevel:       memcore.DeIDTeamSafe,
		Confidence:      0.79,
		Status:          memcore.CandidateProposed,
		CreatedBy:       agentcore.AgentID("distiller-1"),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := store.SaveCandidate(ctx, candidate); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	service := NewService(store, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now })

	_, err := service.AcceptCandidate(ctx, ViewerRef{ViewerID: "reviewer-1", PermissionScope: []string{PermissionMemoryReview}, OrganizationIDs: []string{"org-1"}}, "candidate-world-1", AcceptCandidateRequest{ProfileID: "profile-world", MemoryID: "memory-world-1"})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("AcceptCandidate world with review-only err = %v, want permission denied", err)
	}

	record, err := service.AcceptCandidate(ctx, ViewerRef{ViewerID: "world-manager-1", PermissionScope: []string{PermissionMemoryWorldManage}, OrganizationIDs: []string{"org-1"}}, "candidate-world-1", AcceptCandidateRequest{ProfileID: "profile-world", MemoryID: "memory-world-1"})
	if err != nil {
		t.Fatalf("AcceptCandidate world manager: %v", err)
	}
	if record.Layer != "world" || len(record.SupersedesRefs) != 1 || len(record.ContradictsRefs) != 1 {
		t.Fatalf("accepted world record = %+v", record)
	}
}

func TestServiceRejectAndMergeRequireReviewPermission(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	candidate := memcore.MemoryCandidate{
		ID:            "candidate-review-1",
		SourceType:    memcore.SourceSummary,
		SourceRef:     "summary-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		Summary:       "Review summary.",
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	_ = store.SaveCandidate(ctx, candidate)
	service := NewService(store, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now })

	_, err := service.RejectCandidate(ctx, ViewerRef{ViewerID: "reader-1", PermissionScope: []string{PermissionMemoryRead}}, "candidate-review-1", RejectCandidateRequest{Reason: "duplicate"})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("RejectCandidate err = %v, want permission denied", err)
	}
	receipt, err := service.MergeCandidate(ctx, ViewerRef{ViewerID: "reviewer-1", PermissionScope: []string{PermissionMemoryReview}, MissionIDs: []string{"mission-1"}}, "candidate-review-1", MergeCandidateRequest{TargetMemoryID: "memory-existing", Reason: "same fact"})
	if err != nil {
		t.Fatalf("MergeCandidate: %v", err)
	}
	if receipt.Status != "merged" || receipt.TargetID != "memory-existing" {
		t.Fatalf("receipt = %+v", receipt)
	}
}

func TestServiceReviewCommandsRejectAlreadyFinalCandidates(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-final-1",
		SourceType:    memcore.SourceSummary,
		SourceRef:     "summary-final-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Summary:       "Already final.",
		Status:        memcore.CandidateRejected,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	service := NewService(store, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now })
	reviewer := ViewerRef{ViewerID: "reviewer-1", PermissionScope: []string{PermissionMemoryReview}, MissionIDs: []string{"mission-1"}}

	if _, err := service.AcceptCandidate(ctx, reviewer, "candidate-final-1", AcceptCandidateRequest{ProfileID: "profile-1"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("AcceptCandidate final err = %v, want conflict", err)
	}
	if _, err := service.RejectCandidate(ctx, reviewer, "candidate-final-1", RejectCandidateRequest{Reason: "duplicate"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("RejectCandidate final err = %v, want conflict", err)
	}
	if _, err := service.MergeCandidate(ctx, reviewer, "candidate-final-1", MergeCandidateRequest{TargetMemoryID: "memory-existing", Reason: "same fact"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("MergeCandidate final err = %v, want conflict", err)
	}
}

func TestServiceListAndGetRecordsFilterSensitiveWithoutAuditRead(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	for _, candidateID := range []string{"candidate-normal-record", "candidate-secret-record"} {
		_ = store.SaveCandidate(ctx, memcore.MemoryCandidate{
			ID:            candidateID,
			SourceType:    memcore.SourceSummary,
			SourceRef:     candidateID + "-source",
			ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
			ProposedKind:  agentcore.MemoryKindMission,
			Summary:       candidateID,
			Status:        memcore.CandidateAccepted,
			CreatedBy:     agentcore.AgentID("agent-1"),
			CreatedAt:     now,
			UpdatedAt:     now,
		})
	}
	_ = store.SaveRecord(ctx, memcore.MemoryRecord{
		ID:              "memory-normal",
		ProfileID:       agentcore.MemoryProfileID("profile-1"),
		CandidateID:     "candidate-normal-record",
		Scope:           memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		Layer:           memcore.MemoryLayerFact,
		SourceRefs:      []string{"summary:candidate-normal-record-source"},
		Summary:         "Normal record.",
		Facts:           []string{"Normal record."},
		Sensitivity:     memcore.SensitivityNormal,
		DeIDLevel:       memcore.DeIDRaw,
		LifecycleStatus: memcore.MemoryLifecycleActive,
		Visibility:      memcore.Visibility{Mode: memcore.VisibilityScopedMembers, ScopeKind: memcore.ScopeMission, ScopeID: "mission-1"},
		Confidence:      1,
		CreatedBy:       agentcore.AgentID("agent-1"),
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	_ = store.SaveRecord(ctx, memcore.MemoryRecord{
		ID:              "memory-secret",
		ProfileID:       agentcore.MemoryProfileID("profile-1"),
		CandidateID:     "candidate-secret-record",
		Scope:           memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		Layer:           memcore.MemoryLayerFact,
		SourceRefs:      []string{"summary:candidate-secret-record-source"},
		Summary:         "Secret record.",
		Facts:           []string{"Secret record."},
		Sensitivity:     memcore.SensitivitySecret,
		DeIDLevel:       memcore.DeIDRaw,
		LifecycleStatus: memcore.MemoryLifecycleActive,
		Visibility:      memcore.Visibility{Mode: memcore.VisibilityScopedMembers, ScopeKind: memcore.ScopeMission, ScopeID: "mission-1"},
		Confidence:      1,
		CreatedBy:       agentcore.AgentID("agent-1"),
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	service := NewService(store, nil, func() time.Time { return now })

	page, err := service.ListRecords(ctx, ViewerRef{ViewerID: "reader-1", PermissionScope: []string{PermissionMemoryRead}, MissionIDs: []string{"mission-1"}}, ListRecordsQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(page.Records) != 1 || page.Records[0].MemoryID != "memory-normal" {
		t.Fatalf("page = %+v", page)
	}
	_, err = service.GetRecord(ctx, ViewerRef{ViewerID: "reader-1", PermissionScope: []string{PermissionMemoryRead}, MissionIDs: []string{"mission-1"}}, "memory-secret")
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("GetRecord sensitive err = %v, want permission denied", err)
	}
	secret, err := service.GetRecord(ctx, ViewerRef{ViewerID: "auditor-1", PermissionScope: []string{PermissionMemoryRead, PermissionMemoryAuditRead}, MissionIDs: []string{"mission-1"}}, "memory-secret")
	if err != nil {
		t.Fatalf("GetRecord auditor: %v", err)
	}
	if secret.MemoryID != "memory-secret" || secret.Sensitivity != "secret" {
		t.Fatalf("secret dto = %+v", secret)
	}
}

func TestServiceListAndGetRecordsIncludeCitationEvidenceWhenResolverConfigured(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 20, 17, 0, 0, 0, time.UTC)
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-citation-evidence",
		SourceType:    memcore.SourceEvidence,
		SourceRef:     "evidence-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Summary:       "Release follows cited evidence.",
		Status:        memcore.CandidateAccepted,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	record := memcore.MemoryRecord{
		ID:                 "memory-citation-evidence",
		ProfileID:          agentcore.MemoryProfileID("profile-1"),
		CandidateID:        "candidate-citation-evidence",
		Scope:              memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		Layer:              memcore.MemoryLayerFact,
		SourceRefs:         []string{"knowledge_document:kdoc-1"},
		SourceEvidenceRefs: []string{"knowledge_chunk:kdoc-1:0", "knowledge_anchor:kdoc-1:page:1:anchor:0007"},
		Summary:            "Release follows cited evidence.",
		Facts:              []string{"Release follows cited evidence."},
		Sensitivity:        memcore.SensitivityNormal,
		DeIDLevel:          memcore.DeIDRaw,
		LifecycleStatus:    memcore.MemoryLifecycleActive,
		Visibility:         memcore.Visibility{Mode: memcore.VisibilityScopedMembers, ScopeKind: memcore.ScopeMission, ScopeID: "mission-1"},
		Confidence:         1,
		CreatedBy:          agentcore.AgentID("agent-1"),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := store.SaveRecord(ctx, record); err != nil {
		t.Fatalf("SaveRecord: %v", err)
	}
	resolver := &fakeCitationEvidenceResolver{
		evidence: []CitationEvidenceDTO{
			{Kind: "knowledge_document", ID: "kdoc-1", Title: "release-handbook.md", KnowledgeBaseID: "kb-1", SourceURI: "pageindex://folder-123/pi-doc-1"},
			{Kind: "knowledge_chunk", ID: "kdoc-1:0", KnowledgeBaseID: "kb-1", DocumentID: "kdoc-1", ContentHash: "sha256:chunk"},
			{Kind: "knowledge_anchor", ID: "kdoc-1:page:1:anchor:0007", KnowledgeBaseID: "kb-1", DocumentID: "kdoc-1", PageID: "kdoc-1:page:1", PageNumber: 1, ProviderNodeID: "0007", SnippetHash: "sha256:anchor", SourceURI: "pageindex://folder-123/pi-doc-1#page=1&anchor=0007"},
		},
	}
	service := NewService(store, nil, func() time.Time { return now }).
		WithCitationEvidenceResolver(resolver)
	viewer := ViewerRef{ViewerID: "reader-1", PermissionScope: []string{PermissionMemoryRead}, MissionIDs: []string{"mission-1"}}

	page, err := service.ListRecords(ctx, viewer, ListRecordsQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(page.Records) != 1 || len(page.Records[0].CitationEvidence) != 3 {
		t.Fatalf("page = %+v", page)
	}
	got, err := service.GetRecord(ctx, viewer, "memory-citation-evidence")
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if len(got.CitationEvidence) != 3 {
		t.Fatalf("citation evidence = %+v", got.CitationEvidence)
	}
	if got.CitationEvidence[2].Kind != "knowledge_anchor" ||
		got.CitationEvidence[2].KnowledgeBaseID != "kb-1" ||
		got.CitationEvidence[2].PageNumber != 1 ||
		got.CitationEvidence[2].ProviderNodeID != "0007" ||
		got.CitationEvidence[2].SnippetHash != "sha256:anchor" {
		t.Fatalf("anchor evidence = %+v", got.CitationEvidence[2])
	}
	if len(resolver.requests) != 2 ||
		resolver.requests[0].MemoryID != "memory-citation-evidence" ||
		resolver.requests[0].TenantID != "mission:mission-1" ||
		resolver.requests[0].Refs[0] != "knowledge_document:kdoc-1" {
		t.Fatalf("resolver requests = %+v", resolver.requests)
	}
}

func TestServiceRecordsRequireMatchingScopeMembership(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"candidate-visible-scope", "candidate-private-scope"} {
		if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{
			ID:            id,
			SourceType:    memcore.SourceSummary,
			SourceRef:     id + "-source",
			ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
			ProposedKind:  agentcore.MemoryKindMission,
			ProposedLayer: memcore.MemoryLayerFact,
			Summary:       id,
			Status:        memcore.CandidateAccepted,
			CreatedBy:     agentcore.AgentID("agent-owner"),
			CreatedAt:     now,
			UpdatedAt:     now,
		}); err != nil {
			t.Fatalf("SaveCandidate(%s): %v", id, err)
		}
	}
	if err := store.SaveRecord(ctx, memcore.MemoryRecord{
		ID:              "memory-visible-scope",
		ProfileID:       agentcore.MemoryProfileID("profile-1"),
		CandidateID:     "candidate-visible-scope",
		Scope:           memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		Layer:           memcore.MemoryLayerFact,
		SourceRefs:      []string{"summary:candidate-visible-scope-source"},
		Summary:         "Visible mission record.",
		Facts:           []string{"Visible mission record."},
		Sensitivity:     memcore.SensitivityNormal,
		LifecycleStatus: memcore.MemoryLifecycleActive,
		Visibility:      memcore.Visibility{Mode: memcore.VisibilityScopedMembers, ScopeKind: memcore.ScopeMission, ScopeID: "mission-1"},
		Confidence:      1,
		CreatedBy:       agentcore.AgentID("agent-owner"),
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("SaveRecord visible: %v", err)
	}
	if err := store.SaveRecord(ctx, memcore.MemoryRecord{
		ID:              "memory-private-scope",
		ProfileID:       agentcore.MemoryProfileID("profile-1"),
		CandidateID:     "candidate-private-scope",
		Scope:           memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-owner"},
		Layer:           memcore.MemoryLayerFact,
		SourceRefs:      []string{"summary:candidate-private-scope-source"},
		Summary:         "Owner-only record.",
		Facts:           []string{"Owner-only record."},
		Sensitivity:     memcore.SensitivityNormal,
		LifecycleStatus: memcore.MemoryLifecycleActive,
		Visibility:      memcore.Visibility{Mode: memcore.VisibilityOwnerOnly},
		Confidence:      1,
		CreatedBy:       agentcore.AgentID("agent-owner"),
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("SaveRecord private: %v", err)
	}
	service := NewService(store, nil, func() time.Time { return now })
	missionReader := ViewerRef{ViewerID: "reader-1", PermissionScope: []string{PermissionMemoryRead}, MissionIDs: []string{"mission-1"}}

	page, err := service.ListRecords(ctx, missionReader, ListRecordsQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(page.Records) != 1 || page.Records[0].MemoryID != "memory-visible-scope" {
		t.Fatalf("page = %+v, want only mission-visible record", page)
	}
	if _, err := service.GetRecord(ctx, missionReader, "memory-private-scope"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("GetRecord private err = %v, want permission denied", err)
	}
	auditor := ViewerRef{ViewerID: "auditor-1", PermissionScope: []string{PermissionMemoryRead, PermissionMemoryAuditRead}}
	auditPage, err := service.ListRecords(ctx, auditor, ListRecordsQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListRecords auditor: %v", err)
	}
	if len(auditPage.Records) != 2 {
		t.Fatalf("audit page = %+v, want records across scopes", auditPage)
	}
	auditRecord, err := service.GetRecord(ctx, auditor, "memory-private-scope")
	if err != nil {
		t.Fatalf("GetRecord auditor: %v", err)
	}
	if auditRecord.MemoryID != "memory-private-scope" {
		t.Fatalf("auditor record = %+v", auditRecord)
	}
	owner, err := service.GetRecord(ctx, ViewerRef{ViewerID: "agent-owner", AgentID: "agent-owner", PermissionScope: []string{PermissionMemoryRead}}, "memory-private-scope")
	if err != nil {
		t.Fatalf("GetRecord owner: %v", err)
	}
	if owner.MemoryID != "memory-private-scope" {
		t.Fatalf("owner record = %+v", owner)
	}
}

func TestServiceLifecycleArchiveAnonymizeDeleteAndReferenceRecords(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	audit := &memtestkit.MemoryAuditRecorder{}
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-life",
		SourceType:    memcore.SourceSummary,
		SourceRef:     "summary-life",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
		ProposedKind:  agentcore.MemoryKindOrganization,
		ProposedLayer: memcore.MemoryLayerWorld,
		Summary:       "Use qualitative ranges for compensation discussions.",
		Status:        memcore.CandidateAccepted,
		CreatedBy:     agentcore.AgentID("reviewer-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	if err := store.SaveRecord(ctx, memcore.MemoryRecord{
		ID:                 "memory-life",
		ProfileID:          agentcore.MemoryProfileID("profile-org"),
		CandidateID:        "candidate-life",
		Scope:              memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
		Layer:              memcore.MemoryLayerWorld,
		Topic:              "policy",
		SourceRefs:         []string{"discussion:comp-plan-employee-zhang"},
		SourceEvidenceRefs: []string{"evidence:salary-zhang-2026"},
		Content:            "Zhang San salary is 100000.",
		Summary:            "Use qualitative ranges for compensation discussions.",
		Narrative:          "Zhang San was discussed directly.",
		Lessons:            []string{"Use ranges, not exact compensation numbers."},
		Entities:           map[string]string{"employee": "Zhang San"},
		LineageRefs:        []string{"memory:source-fact"},
		Sensitivity:        memcore.SensitivityCompensation,
		DeIDLevel:          memcore.DeIDRaw,
		LifecycleStatus:    memcore.MemoryLifecycleActive,
		Visibility:         memcore.Visibility{Mode: memcore.VisibilityScopedMembers, ScopeKind: memcore.ScopeOrganization, ScopeID: "org-1"},
		Confidence:         0.9,
		CreatedBy:          agentcore.AgentID("reviewer-1"),
		CreatedAt:          now,
		UpdatedAt:          now,
	}); err != nil {
		t.Fatalf("SaveRecord: %v", err)
	}
	service := NewService(store, audit, func() time.Time { return now.Add(time.Hour) })
	viewer := ViewerRef{ViewerID: "lifecycle-manager", PermissionScope: []string{PermissionMemoryReview, PermissionMemoryWorldManage, PermissionMemoryRead, PermissionMemoryAuditRead}, OrganizationIDs: []string{"org-1"}}

	touched, err := service.TouchRecords(ctx, viewer, TouchMemoryRecordsRequest{MemoryIDs: []string{"memory-life"}})
	if err != nil {
		t.Fatalf("TouchRecords: %v", err)
	}
	if touched.Status != "referenced" || touched.UpdatedAt != now.Add(time.Hour) {
		t.Fatalf("touch receipt = %+v", touched)
	}
	record, err := service.GetRecord(ctx, viewer, "memory-life")
	if err != nil {
		t.Fatalf("GetRecord after touch: %v", err)
	}
	if record.ReferenceCount != 1 || !record.LastReferencedAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("reference fields = count %d last %v", record.ReferenceCount, record.LastReferencedAt)
	}

	anonymized, err := service.AnonymizeRecord(ctx, viewer, "memory-life", MemoryLifecycleRequest{Reason: "remove employee identity"})
	if err != nil {
		t.Fatalf("AnonymizeRecord: %v", err)
	}
	if anonymized.LifecycleStatus != "anonymized" || anonymized.DeIDLevel != "organization_anonymous" || anonymized.Sensitivity != "normal" {
		t.Fatalf("anonymized dto = %+v", anonymized)
	}
	if strings.Contains(strings.Join(anonymized.SourceMemoryRefs, ","), "Zhang") ||
		strings.Contains(anonymized.Content, "Zhang") ||
		strings.Contains(anonymized.Narrative, "Zhang") ||
		strings.Contains(anonymized.Summary, "Zhang") {
		t.Fatalf("anonymized dto leaked identity: %+v", anonymized)
	}
	if !containsString(anonymized.Lessons, "Use ranges, not exact compensation numbers.") {
		t.Fatalf("anonymized lesson lost value: %+v", anonymized.Lessons)
	}

	archived, err := service.ArchiveRecord(ctx, viewer, "memory-life", MemoryLifecycleRequest{Reason: "superseded"})
	if err != nil {
		t.Fatalf("ArchiveRecord: %v", err)
	}
	if archived.Status != "archived" {
		t.Fatalf("archive receipt = %+v", archived)
	}
	page, err := service.ListRecords(ctx, viewer, ListRecordsQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListRecords after archive: %v", err)
	}
	if len(page.Records) != 0 {
		t.Fatalf("archived records should leave main retrieval page: %+v", page)
	}

	deleted, err := service.DeleteRecord(ctx, viewer, "memory-life", MemoryLifecycleRequest{Reason: "retention expired"})
	if err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}
	if deleted.Status != "deleted" {
		t.Fatalf("delete receipt = %+v", deleted)
	}
	events := audit.Snapshot()
	if len(events) != 4 ||
		events[0].Type != memaudit.EventMemoryRecordReferenced ||
		events[1].Type != memaudit.EventMemoryRecordAnonymized ||
		events[2].Type != memaudit.EventMemoryRecordArchived ||
		events[3].Type != memaudit.EventMemoryRecordDeleted {
		t.Fatalf("audit events = %+v", events)
	}
}

func TestServicePublishesMemoryRecordLifecycleUpdatedEvents(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-lifecycle-event",
		SourceType:    memcore.SourceSummary,
		SourceRef:     "summary-lifecycle-event",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Summary:       "Lifecycle event fact.",
		Status:        memcore.CandidateAccepted,
		CreatedBy:     agentcore.AgentID("reviewer-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	if err := store.SaveRecord(ctx, memcore.MemoryRecord{
		ID:                 "memory-lifecycle-event",
		ProfileID:          agentcore.MemoryProfileID("profile-1"),
		CandidateID:        "candidate-lifecycle-event",
		Scope:              memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		Layer:              memcore.MemoryLayerFact,
		Topic:              "release",
		SourceRefs:         []string{"summary:summary-lifecycle-event"},
		SourceEvidenceRefs: []string{"evidence:evidence-lifecycle-event"},
		Summary:            "Lifecycle event fact.",
		LifecycleStatus:    memcore.MemoryLifecycleActive,
		Visibility:         memcore.Visibility{Mode: memcore.VisibilityScopedMembers, ScopeKind: memcore.ScopeMission, ScopeID: "mission-1"},
		Confidence:         0.9,
		CreatedBy:          agentcore.AgentID("reviewer-1"),
		CreatedAt:          now,
		UpdatedAt:          now,
	}); err != nil {
		t.Fatalf("SaveRecord: %v", err)
	}
	sink := &recordingMemoryRecordMetadataSink{}
	service := NewService(store, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now.Add(time.Hour) }).
		WithMemoryRecordMetadataSink(sink)
	viewer := ViewerRef{ViewerID: "reviewer-1", PermissionScope: []string{PermissionMemoryRead, PermissionMemoryReview}, MissionIDs: []string{"mission-1"}}

	if _, err := service.TouchRecords(ctx, viewer, TouchMemoryRecordsRequest{MemoryIDs: []string{"memory-lifecycle-event"}, Reason: "used in prompt"}); err != nil {
		t.Fatalf("TouchRecords: %v", err)
	}
	if _, err := service.ArchiveRecord(ctx, viewer, "memory-lifecycle-event", MemoryLifecycleRequest{Reason: "superseded"}); err != nil {
		t.Fatalf("ArchiveRecord: %v", err)
	}

	events := sink.Snapshot()
	if len(events) != 2 {
		t.Fatalf("events = %+v, want touch and archive lifecycle events", events)
	}
	for _, event := range events {
		if event.Type != "memory.record.lifecycle.updated" || event.StreamID != "memory-lifecycle-event" || event.TenantID != "mission:mission-1" {
			t.Fatalf("event identity = %+v", event)
		}
		if event.Payload["id"] != "memory-lifecycle-event" || event.Payload["summary"] != "Lifecycle event fact." {
			t.Fatalf("payload identity = %+v", event.Payload)
		}
	}
	wantReferencedAt := now.Add(time.Hour).UTC().Format(time.RFC3339Nano)
	if events[0].Payload["lifecycle_status"] != "active" || events[0].Payload["reference_count"] != 1 || events[0].Payload["last_referenced_at"] != wantReferencedAt {
		t.Fatalf("touch payload = %+v", events[0].Payload)
	}
	if events[1].Payload["lifecycle_status"] != "archived" || events[1].Payload["reference_count"] != 1 || events[1].Payload["last_referenced_at"] != wantReferencedAt {
		t.Fatalf("archive payload = %+v", events[1].Payload)
	}
}

func TestServiceSweepRetentionArchivesExpiredUnusedRecords(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	audit := &memtestkit.MemoryAuditRecorder{}
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{ID: "candidate-retention", Status: memcore.CandidateAccepted}); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	records := []memcore.MemoryRecord{
		{
			ID:              "memory-expired-unused",
			ProfileID:       agentcore.MemoryProfileID("profile-1"),
			CandidateID:     "candidate-retention",
			Scope:           memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
			Layer:           memcore.MemoryLayerFact,
			Summary:         "Expired unused fact.",
			LifecycleStatus: memcore.MemoryLifecycleActive,
			ExpiresAt:       now.Add(-time.Hour),
			Visibility:      memcore.Visibility{Mode: memcore.VisibilityScopedMembers, ScopeKind: memcore.ScopeMission, ScopeID: "mission-1"},
			CreatedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:       now.Add(-48 * time.Hour),
		},
		{
			ID:              "memory-expired-referenced",
			ProfileID:       agentcore.MemoryProfileID("profile-1"),
			CandidateID:     "candidate-retention",
			Scope:           memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
			Layer:           memcore.MemoryLayerFact,
			Summary:         "Expired but recently referenced fact.",
			LifecycleStatus: memcore.MemoryLifecycleActive,
			ReferenceCount:  2,
			ExpiresAt:       now.Add(-time.Hour),
			Visibility:      memcore.Visibility{Mode: memcore.VisibilityScopedMembers, ScopeKind: memcore.ScopeMission, ScopeID: "mission-1"},
			CreatedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:       now.Add(-48 * time.Hour),
		},
		{
			ID:              "memory-sensitive-expired",
			ProfileID:       agentcore.MemoryProfileID("profile-1"),
			CandidateID:     "candidate-retention",
			Scope:           memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
			Layer:           memcore.MemoryLayerFact,
			Summary:         "Sensitive expired fact.",
			Sensitivity:     memcore.SensitivityCompensation,
			LifecycleStatus: memcore.MemoryLifecycleActive,
			ExpiresAt:       now.Add(-time.Hour),
			Visibility:      memcore.Visibility{Mode: memcore.VisibilityScopedMembers, ScopeKind: memcore.ScopeMission, ScopeID: "mission-1"},
			CreatedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:       now.Add(-48 * time.Hour),
		},
	}
	for _, record := range records {
		if err := store.SaveRecord(ctx, record); err != nil {
			t.Fatalf("SaveRecord(%s): %v", record.ID, err)
		}
	}
	service := NewService(store, audit, func() time.Time { return now })
	viewer := ViewerRef{ViewerID: "lifecycle-sweeper", PermissionScope: []string{PermissionMemoryReview, PermissionMemoryRead, PermissionMemoryAuditRead}, MissionIDs: []string{"mission-1"}}

	result, err := service.SweepRetention(ctx, viewer, MemoryRetentionSweepRequest{Reason: "nightly lifecycle sweep"})
	if err != nil {
		t.Fatalf("SweepRetention: %v", err)
	}
	if result.Status != "swept" || result.CheckedCount != 3 || result.ArchivedCount != 1 || !containsString(result.ArchivedMemoryIDs, "memory-expired-unused") {
		t.Fatalf("retention sweep result = %+v", result)
	}
	page, err := service.ListRecords(ctx, viewer, ListRecordsQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListRecords after sweep: %v", err)
	}
	if containsRecordID(page.Records, "memory-expired-unused") {
		t.Fatalf("expired unused record should leave main retrieval page: %+v", page)
	}
	if !containsRecordID(page.Records, "memory-expired-referenced") || !containsRecordID(page.Records, "memory-sensitive-expired") {
		t.Fatalf("referenced and sensitive expired records should remain visible: %+v", page)
	}
	archived, err := store.FindRecords(ctx, memstore.RecordQuery{LifecycleStatuses: []memcore.MemoryLifecycleStatus{memcore.MemoryLifecycleArchived}, IncludeArchived: true})
	if err != nil {
		t.Fatalf("FindRecords archived: %v", err)
	}
	if len(archived) != 1 || archived[0].ID != "memory-expired-unused" {
		t.Fatalf("archived records = %+v", archived)
	}
	events := audit.Snapshot()
	if len(events) != 1 || events[0].Type != memaudit.EventMemoryRecordArchived || events[0].SubjectID != "memory-expired-unused" {
		t.Fatalf("audit events = %+v", events)
	}
}

func TestServiceSweepRetentionRequiresWorldManageForOrganizationWorldRecords(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	audit := &memtestkit.MemoryAuditRecorder{}
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{ID: "candidate-world-retention", Status: memcore.CandidateAccepted}); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	if err := store.SaveRecord(ctx, memcore.MemoryRecord{
		ID:              "memory-world-expired",
		ProfileID:       agentcore.MemoryProfileID("profile-org"),
		CandidateID:     "candidate-world-retention",
		Scope:           memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
		Layer:           memcore.MemoryLayerWorld,
		Topic:           "policy",
		Summary:         "Expired organization world rule.",
		LineageRefs:     []string{"memory:source"},
		Sensitivity:     memcore.SensitivityNormal,
		LifecycleStatus: memcore.MemoryLifecycleActive,
		ExpiresAt:       now.Add(-time.Hour),
		Visibility:      memcore.Visibility{Mode: memcore.VisibilityScopedMembers, ScopeKind: memcore.ScopeOrganization, ScopeID: "org-1"},
		CreatedAt:       now.Add(-48 * time.Hour),
		UpdatedAt:       now.Add(-48 * time.Hour),
	}); err != nil {
		t.Fatalf("SaveRecord: %v", err)
	}
	service := NewService(store, audit, func() time.Time { return now })

	withoutWorldManage, err := service.SweepRetention(ctx, ViewerRef{ViewerID: "reviewer", PermissionScope: []string{PermissionMemoryReview, PermissionMemoryAuditRead}, OrganizationIDs: []string{"org-1"}}, MemoryRetentionSweepRequest{})
	if err != nil {
		t.Fatalf("SweepRetention without world manage: %v", err)
	}
	if withoutWorldManage.ArchivedCount != 0 {
		t.Fatalf("sweep without world manage archived world memory: %+v", withoutWorldManage)
	}

	withWorldManage, err := service.SweepRetention(ctx, ViewerRef{ViewerID: "world-manager", PermissionScope: []string{PermissionMemoryReview, PermissionMemoryWorldManage, PermissionMemoryAuditRead}, OrganizationIDs: []string{"org-1"}}, MemoryRetentionSweepRequest{})
	if err != nil {
		t.Fatalf("SweepRetention with world manage: %v", err)
	}
	if withWorldManage.ArchivedCount != 1 || !containsString(withWorldManage.ArchivedMemoryIDs, "memory-world-expired") {
		t.Fatalf("sweep with world manage result = %+v", withWorldManage)
	}
}

func seedAcceptedSourceFactForExperience(t *testing.T, ctx context.Context, store *memtestkit.FakeStore, memoryID, candidateID string, now time.Time) {
	t.Helper()
	candidate := memcore.MemoryCandidate{
		ID:            candidateID,
		SourceType:    memcore.SourceQAResult,
		SourceRef:     "qa_result:" + candidateID,
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         "qa",
		Summary:       "Accepted QA fact.",
		EvidenceRefs:  []string{"evidence:" + candidateID},
		CreatedBy:     agentcore.AgentID("agent-1"),
		Status:        memcore.CandidateAccepted,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.SaveCandidate(ctx, candidate); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	record := memcore.MemoryRecord{
		ID:                 memoryID,
		ProfileID:          agentcore.MemoryProfileID("profile-1"),
		CandidateID:        candidateID,
		Layer:              memcore.MemoryLayerFact,
		Scope:              memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		Topic:              "qa",
		Summary:            "Accepted QA fact.",
		Facts:              []string{"Accepted QA fact."},
		SourceRefs:         []string{"qa_result:" + candidateID},
		SourceEvidenceRefs: []string{"evidence:" + candidateID},
		Sensitivity:        memcore.SensitivityNormal,
		DeIDLevel:          memcore.DeIDRaw,
		LifecycleStatus:    memcore.MemoryLifecycleActive,
		Confidence:         1,
		CreatedBy:          agentcore.AgentID("agent-1"),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := store.SaveRecord(ctx, record); err != nil {
		t.Fatalf("SaveRecord: %v", err)
	}
}

func containsRecordID(records []MemoryRecordDTO, target string) bool {
	for _, record := range records {
		if record.MemoryID == target {
			return true
		}
	}
	return false
}

func findContextGraphObject(t *testing.T, objects []contextgraph.Object, target string) contextgraph.Object {
	t.Helper()
	for _, object := range objects {
		if object.ID == target {
			return object
		}
	}
	t.Fatalf("context graph object %q not found in %+v", target, objects)
	return contextgraph.Object{}
}

type recordingMemoryRecordMetadataSink struct {
	events []graphprotocol.DomainEvent
}

type fakeCitationEvidenceResolver struct {
	evidence []CitationEvidenceDTO
	requests []CitationEvidenceQuery
}

func (r *fakeCitationEvidenceResolver) ResolveCitationEvidence(ctx context.Context, query CitationEvidenceQuery) ([]CitationEvidenceDTO, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.requests = append(r.requests, query)
	return append([]CitationEvidenceDTO(nil), r.evidence...), nil
}

func (s *recordingMemoryRecordMetadataSink) PublishMemoryRecordMetadata(_ context.Context, event graphprotocol.DomainEvent) error {
	s.events = append(s.events, cloneDomainEvent(event))
	return nil
}

func (s *recordingMemoryRecordMetadataSink) Snapshot() []graphprotocol.DomainEvent {
	events := make([]graphprotocol.DomainEvent, 0, len(s.events))
	for _, event := range s.events {
		events = append(events, cloneDomainEvent(event))
	}
	return events
}

func cloneDomainEvent(event graphprotocol.DomainEvent) graphprotocol.DomainEvent {
	if event.Payload == nil {
		return event
	}
	payload := make(map[string]any, len(event.Payload))
	for key, value := range event.Payload {
		payload[key] = value
	}
	event.Payload = payload
	return event
}
