package aggregate

import (
	"context"
	"slices"
	"sync"
	"testing"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	contextview "orrery_v2/internal/app/ontologyview/context"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	governanceview "orrery_v2/internal/app/ontologyview/governance"
	lineageview "orrery_v2/internal/app/ontologyview/lineage"
	projectionview "orrery_v2/internal/app/ontologyview/projection"
)

type fakeExecutionReader struct{ calls []string }

func (f *fakeExecutionReader) ListTasks(_ context.Context, _ identity.Principal, query contextview.ObjectListQuery) (ontologycontract.CursorPage[contextview.ObjectItem], error) {
	f.calls = append(f.calls, query.TenantID)
	return fakePage([]contextview.ObjectItem{{ID: "same"}}, query.Limit, false, ""), nil
}
func (*fakeExecutionReader) GetTask(context.Context, identity.Principal, string, string) (contextview.ObjectDetail, error) {
	return contextview.ObjectDetail{}, nil
}
func (*fakeExecutionReader) GetRun(context.Context, identity.Principal, string, string) (contextview.ObjectDetail, error) {
	return contextview.ObjectDetail{}, nil
}
func (*fakeExecutionReader) RunLineage(context.Context, identity.Principal, contextview.NeighborhoodQuery) (contextview.Neighborhood, error) {
	return contextview.Neighborhood{}, nil
}

type fakeGovernanceReader struct{ calls []string }

func (*fakeGovernanceReader) Summary(context.Context, identity.Principal, string) (ontologycontract.Summary[governanceview.SummaryData], error) {
	return ontologycontract.Summary[governanceview.SummaryData]{Data: governanceview.SummaryData{ActionableProposals: ontologycontract.ExactCount(1)}}, nil
}
func (f *fakeGovernanceReader) ListProposals(_ context.Context, _ identity.Principal, query governanceview.ProposalListQuery) (ontologycontract.CursorPage[governanceview.ProposalItem], error) {
	f.calls = append(f.calls, query.TenantID)
	return fakePage([]governanceview.ProposalItem{{ID: "same"}}, query.Limit, false, ""), nil
}
func (*fakeGovernanceReader) GetProposal(context.Context, identity.Principal, string, string) (governanceview.ProposalDetail, error) {
	return governanceview.ProposalDetail{}, nil
}
func (*fakeGovernanceReader) AnalyzeProposal(context.Context, identity.Principal, string, string) (governanceview.AnalysisReport, error) {
	return governanceview.AnalysisReport{}, nil
}
func (*fakeGovernanceReader) ListReceipts(context.Context, identity.Principal, governanceview.ReceiptListQuery) (ontologycontract.CursorPage[governanceview.ReceiptItem], error) {
	return ontologycontract.CursorPage[governanceview.ReceiptItem]{}, nil
}
func (*fakeGovernanceReader) ListAuditEvents(context.Context, identity.Principal, governanceview.AuditEventListQuery) (ontologycontract.CursorPage[governanceview.AuditEventItem], error) {
	return ontologycontract.CursorPage[governanceview.AuditEventItem]{}, nil
}

type fakeProjectionReader struct{ calls []string }

func (*fakeProjectionReader) Summary(context.Context, identity.Principal, string) (ontologycontract.Summary[projectionview.SummaryData], error) {
	return ontologycontract.Summary[projectionview.SummaryData]{Data: projectionview.SummaryData{UnresolvedAudits: ontologycontract.ExactCount(1)}}, nil
}
func (f *fakeProjectionReader) ListAudits(_ context.Context, _ identity.Principal, query projectionview.AuditListQuery) (ontologycontract.CursorPage[projectionview.AuditItem], error) {
	f.calls = append(f.calls, query.TenantID)
	return fakePage([]projectionview.AuditItem{{ID: "same"}}, query.Limit, false, ""), nil
}
func (*fakeProjectionReader) GetAudit(context.Context, identity.Principal, string, string) (projectionview.AuditDetail, error) {
	return projectionview.AuditDetail{}, nil
}
func (*fakeProjectionReader) ListReplayHistory(context.Context, identity.Principal, projectionview.ReplayHistoryListQuery) (ontologycontract.CursorPage[projectionview.ReplayHistoryItem], error) {
	return ontologycontract.CursorPage[projectionview.ReplayHistoryItem]{}, nil
}

type fakeLineageReader struct {
	mu           sync.Mutex
	summaryCalls []string
}

func (f *fakeLineageReader) Summary(_ context.Context, _ identity.Principal, tenantID string) (ontologycontract.Summary[lineageview.SummaryData], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.summaryCalls = append(f.summaryCalls, tenantID)
	return ontologycontract.Summary[lineageview.SummaryData]{}, nil
}

func (f *fakeLineageReader) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := slices.Clone(f.summaryCalls)
	slices.Sort(calls)
	return calls
}
func (*fakeLineageReader) GetProvenance(context.Context, identity.Principal, string, string) (lineageview.ProvenanceAnchor, error) {
	return lineageview.ProvenanceAnchor{}, nil
}
func (*fakeLineageReader) ListAssertions(context.Context, identity.Principal, lineageview.FactPageQuery) (ontologycontract.CursorPage[lineageview.AssertionItem], error) {
	return ontologycontract.CursorPage[lineageview.AssertionItem]{}, nil
}
func (*fakeLineageReader) ListEvidence(context.Context, identity.Principal, lineageview.FactPageQuery) (ontologycontract.CursorPage[lineageview.EvidenceItem], error) {
	return ontologycontract.CursorPage[lineageview.EvidenceItem]{}, nil
}
func (*fakeLineageReader) ListProjectionTrace(context.Context, identity.Principal, lineageview.FactPageQuery) (ontologycontract.CursorPage[lineageview.ProjectionTraceItem], error) {
	return ontologycontract.CursorPage[lineageview.ProjectionTraceItem]{}, nil
}

func TestDomainServicesAggregateTopLevelCollectionsAndSummaries(t *testing.T) {
	catalog := &fakeScopeCatalog{pages: map[string]ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem]{"": scopePage([]string{"org-1", "system"}, false, "")}}
	principal := testPrincipal("user-1")

	executionReader := &fakeExecutionReader{}
	executions, err := NewExecutionService(newTestScopeEnumerator(t, catalog), executionReader)
	if err != nil {
		t.Fatalf("NewExecutionService: %v", err)
	}
	tasks, err := executions.ListTasks(context.Background(), principal, contextview.ObjectListQuery{Limit: 10})
	if err != nil || len(tasks.Items) != 2 || tasks.Items[0].TenantID != "org-1" || tasks.Items[1].TenantID != "system" {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}

	governanceReader := &fakeGovernanceReader{}
	governance, err := NewGovernanceService(newTestScopeEnumerator(t, catalog), governanceReader)
	if err != nil {
		t.Fatalf("NewGovernanceService: %v", err)
	}
	proposals, err := governance.ListProposals(context.Background(), principal, governanceview.ProposalListQuery{Queue: governanceview.QueueAll, Limit: 10})
	if err != nil || len(proposals.Items) != 2 || proposals.Items[0].TenantID != "org-1" || proposals.Items[1].TenantID != "system" {
		t.Fatalf("proposals=%+v err=%v", proposals, err)
	}

	projectionReader := &fakeProjectionReader{}
	projection, err := NewProjectionService(newTestScopeEnumerator(t, catalog), projectionReader)
	if err != nil {
		t.Fatalf("NewProjectionService: %v", err)
	}
	audits, err := projection.ListAudits(context.Background(), principal, projectionview.AuditListQuery{Limit: 10})
	if err != nil || len(audits.Items) != 2 || audits.Items[0].TenantID != "org-1" || audits.Items[1].TenantID != "system" {
		t.Fatalf("audits=%+v err=%v", audits, err)
	}

	lineageReader := &fakeLineageReader{}
	lineage, err := NewLineageService(newTestScopeEnumerator(t, catalog), lineageReader)
	if err != nil {
		t.Fatalf("NewLineageService: %v", err)
	}
	if _, err := lineage.Summary(context.Background(), principal, ""); err != nil {
		t.Fatalf("lineage summary: %v", err)
	}
	if calls := lineageReader.calls(); !slices.Equal(calls, []string{"org-1", "system"}) {
		t.Fatalf("lineage summary calls=%v", calls)
	}
}
