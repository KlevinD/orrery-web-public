package projection

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type projectionTestReader struct {
	refs           []contextgraph.AccessRef
	audits         []contextgraph.ProjectionAudit
	history        []contextgraph.ProjectionAuditReplayHistory
	readCalls      int
	historyCalls   int
	lastAuditQuery contextgraph.ProjectionAuditCursorQuery
	err            error
}

func (r *projectionTestReader) ListProjectionAccessRefs(ctx context.Context, query contextgraph.AccessRefQuery) (contextgraph.AccessRefPage, error) {
	r.readCalls++
	if r.err != nil {
		return contextgraph.AccessRefPage{}, r.err
	}
	if err := ctx.Err(); err != nil {
		return contextgraph.AccessRefPage{}, err
	}
	items := append([]contextgraph.AccessRef(nil), r.refs...)
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return contextgraph.AccessRefPage{Items: items, HasMore: hasMore}, nil
}

func (r *projectionTestReader) ListVisibleProjectionAudits(ctx context.Context, query contextgraph.ProjectionAuditCursorQuery) (contextgraph.ProjectionAuditCursorPage, error) {
	r.readCalls++
	r.lastAuditQuery = query
	if r.err != nil {
		return contextgraph.ProjectionAuditCursorPage{}, r.err
	}
	if err := ctx.Err(); err != nil {
		return contextgraph.ProjectionAuditCursorPage{}, err
	}
	items := make([]contextgraph.ProjectionAudit, 0, len(r.audits))
	for _, audit := range r.audits {
		if audit.TenantID != query.TenantID || !projectionTestRefAllowed(query.Visibility, audit.SourceAccessRef) {
			continue
		}
		if query.Status != "" && audit.Status != query.Status || query.ReasonCode != "" && audit.ReasonCode != query.ReasonCode {
			continue
		}
		if query.After.ID != "" && !(audit.ObservedAt.Before(query.After.ObservedAt) || audit.ObservedAt.Equal(query.After.ObservedAt) && audit.ID > query.After.ID) {
			continue
		}
		items = append(items, audit)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].ObservedAt.Equal(items[j].ObservedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].ObservedAt.After(items[j].ObservedAt)
	})
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return contextgraph.ProjectionAuditCursorPage{Items: items, HasMore: hasMore}, nil
}

func (r *projectionTestReader) GetVisibleProjectionAudit(ctx context.Context, tenantID, auditID string, visibility contextgraph.VisibilityConstraint) (contextgraph.ProjectionAudit, error) {
	r.readCalls++
	if r.err != nil {
		return contextgraph.ProjectionAudit{}, r.err
	}
	if err := ctx.Err(); err != nil {
		return contextgraph.ProjectionAudit{}, err
	}
	for _, audit := range r.audits {
		if audit.TenantID == tenantID && audit.ID == auditID && projectionTestRefAllowed(visibility, audit.SourceAccessRef) {
			return audit, nil
		}
	}
	return contextgraph.ProjectionAudit{}, contextgraph.ErrNotFound
}

func (r *projectionTestReader) ListVisibleProjectionReplayHistory(ctx context.Context, query contextgraph.ProjectionReplayHistoryCursorQuery) (contextgraph.ProjectionReplayHistoryCursorPage, error) {
	r.readCalls++
	r.historyCalls++
	if r.err != nil {
		return contextgraph.ProjectionReplayHistoryCursorPage{}, r.err
	}
	if err := ctx.Err(); err != nil {
		return contextgraph.ProjectionReplayHistoryCursorPage{}, err
	}
	items := make([]contextgraph.ProjectionAuditReplayHistory, 0, len(r.history))
	for _, item := range r.history {
		if item.TenantID != query.TenantID || item.AuditID != query.AuditID || !projectionTestRefAllowed(query.Visibility, item.SourceAccessRef) {
			continue
		}
		if query.After.ID != "" && !(item.AttemptedAt.Before(query.After.AttemptedAt) || item.AttemptedAt.Equal(query.After.AttemptedAt) && item.ID < query.After.ID) {
			continue
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].AttemptedAt.Equal(items[j].AttemptedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].AttemptedAt.After(items[j].AttemptedAt)
	})
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return contextgraph.ProjectionReplayHistoryCursorPage{Items: items, HasMore: hasMore}, nil
}

func projectionTestRefAllowed(visibility contextgraph.VisibilityConstraint, ref contextgraph.AccessRef) bool {
	if visibility.IsCandidateScan() {
		return contextgraph.NormalizeAccessRef(ref).Resolved()
	}
	ref = contextgraph.NormalizeAccessRef(ref)
	for _, allowed := range visibility.AllowedAccessRefs {
		if contextgraph.NormalizeAccessRef(allowed) == ref {
			return true
		}
	}
	return false
}

type projectionTestResolver struct{ binding ontologyscope.ScopeBinding }

func (r projectionTestResolver) Resolve(_ context.Context, tenantID string) (ontologyscope.ScopeBinding, error) {
	if tenantID != "tenant-1" && tenantID != "tenant-2" {
		return ontologyscope.ScopeBinding{}, ontologyscope.ErrBindingNotFound
	}
	binding := r.binding
	binding.TenantID = tenantID
	binding.AuthorizationTenantID = tenantID
	binding.SourceObject.ID = tenantID
	return binding, nil
}

type projectionTestAuthorizer struct{ deny bool }

func (a projectionTestAuthorizer) AuthorizeFeature(context.Context, identity.Principal, string) error {
	if a.deny {
		return ontologyscope.ErrAccessDenied
	}
	return nil
}

func (a projectionTestAuthorizer) AuthorizeObject(context.Context, identity.Principal, string, identity.ObjectRef, string) error {
	if a.deny {
		return ontologyscope.ErrAccessDenied
	}
	return nil
}

type projectionTestSnapshots struct{}

func (projectionTestSnapshots) Capture(_ context.Context, principal identity.Principal) (authorization.AuthorizationSnapshot, error) {
	return authorization.AuthorizationSnapshot{
		PrincipalKey: authorization.PrincipalCacheKey(principal), AuthorizationRevision: "auth-1", PolicyRevision: "policy-1",
	}, nil
}

type projectionTestPolicy struct {
	hidden map[string]bool
	masked map[string][]string
}

func (p *projectionTestPolicy) Evaluate(_ context.Context, req datavisibility.AdapterRequest) (datavisibility.AdapterDecision, error) {
	out := datavisibility.AdapterDecision{Coverage: datavisibility.CoverageAll, PolicyRevision: "projection-policy-1"}
	for _, resource := range req.Resources {
		hidden := p.hidden[resource.Key]
		for _, ref := range resource.AccessRefs {
			hidden = hidden || ref.ID == "denied"
		}
		masked := append([]string(nil), p.masked[resource.Key]...)
		maskedSet := make(map[string]bool, len(masked))
		for _, field := range masked {
			maskedSet[field] = true
		}
		allowed := make([]string, 0, len(resource.FieldPaths))
		for _, field := range resource.FieldPaths {
			if !maskedSet[field] {
				allowed = append(allowed, field)
			}
		}
		disclosure := datavisibility.ExistenceVisible
		if hidden {
			disclosure, allowed, masked = datavisibility.ExistenceConceal, nil, nil
		}
		out.Resources = append(out.Resources, datavisibility.AdapterResourceDecision{
			Key: resource.Key, Hidden: hidden, AllowedFieldPaths: allowed, MaskedFieldPaths: masked, ExistenceDisclosure: disclosure,
		})
	}
	return out, nil
}

func TestServiceAuthorizesBeforeProjectionStoreRead(t *testing.T) {
	reader := projectionReaderFixture()
	service := newProjectionServiceForTest(t, reader, &projectionTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}}, true)
	_, err := service.ListAudits(context.Background(), projectionPrincipal("user-1"), AuditListQuery{TenantID: "tenant-1"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("denied ListAudits error = %v", err)
	}
	if reader.readCalls != 0 {
		t.Fatalf("projection store read before scope authorization: %d", reader.readCalls)
	}
}

func TestServiceSummaryAggregatesOnlyVisibleRows(t *testing.T) {
	reader := projectionReaderFixture()
	reader.audits = append(reader.audits,
		projectionAudit("hidden", "tenant-1", "skipped", 40, reader.refs[0]),
		projectionAudit("denied", "tenant-1", "skipped", 50, reader.refs[1]),
		projectionAudit("masked-status", "tenant-1", "skipped", 30, reader.refs[0]),
	)
	policy := &projectionTestPolicy{
		hidden: map[string]bool{"projection-audit:hidden": true},
		masked: map[string][]string{"projection-audit:masked-status": {"status"}},
	}
	service := newProjectionServiceForTest(t, reader, policy, false)
	summary, err := service.Summary(context.Background(), projectionPrincipal("user-1"), "tenant-1")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if summary.Data.UnresolvedAudits.Status != ontologycontract.MetricUnavailable || summary.Data.UnresolvedAudits.Value != nil {
		t.Fatalf("unresolved metric = %+v", summary.Data.UnresolvedAudits)
	}
	if summary.Data.LatestActivity.Status != ontologycontract.MetricExact || summary.Data.LatestActivity.ObservedAt == nil || !summary.Data.LatestActivity.ObservedAt.Equal(time.Unix(30, 0).UTC()) {
		t.Fatalf("latest activity metric = %+v", summary.Data.LatestActivity)
	}
	if summary.Meta.DataRevision == "" || summary.Meta.AuthorizationRevision == nil {
		t.Fatalf("summary meta = %+v", summary.Meta)
	}
}

func TestServiceSummaryScansMoreThanLegacyAccessRefLimitInBatches(t *testing.T) {
	reader := projectionReaderFixture()
	reader.refs = make([]contextgraph.AccessRef, contextgraph.MaxVisibilityAccessRefs+1)
	reader.audits = nil
	for index := range reader.refs {
		reader.refs[index] = contextgraph.AccessRef{Kind: "mission", ID: "ref-" + strconv.Itoa(index), TenantID: "tenant-1"}
		reader.audits = append(reader.audits, projectionAudit("audit-"+strconv.Itoa(index), "tenant-1", "skipped", int64(index+1), reader.refs[index]))
	}
	service := newProjectionServiceForTest(t, reader, &projectionTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}}, false)
	summary, err := service.Summary(context.Background(), projectionPrincipal("user-1"), "tenant-1")
	if err != nil {
		t.Fatalf("Summary cardinality: %v", err)
	}
	if summary.Data.UnresolvedAudits.Status != ontologycontract.MetricExact || summary.Data.UnresolvedAudits.Value == nil || *summary.Data.UnresolvedAudits.Value != int64(len(reader.refs)) || summary.Data.LatestActivity.Status != ontologycontract.MetricExact {
		t.Fatalf("cardinality summary = %+v", summary.Data)
	}
}

func TestServiceListAuditsFiltersBeforePaginationAndBindsCursor(t *testing.T) {
	reader := projectionReaderFixture()
	reader.audits = []contextgraph.ProjectionAudit{
		projectionAudit("hidden", "tenant-1", "skipped", 30, reader.refs[0]),
		projectionAudit("visible-a", "tenant-1", "skipped", 20, reader.refs[0]),
		projectionAudit("visible-b", "tenant-1", "skipped", 10, reader.refs[0]),
	}
	policy := &projectionTestPolicy{
		hidden: map[string]bool{"projection-audit:hidden": true},
		masked: map[string][]string{"projection-audit:visible-a": {"reason_code"}},
	}
	service := newProjectionServiceForTest(t, reader, policy, false)
	first, err := service.ListAudits(context.Background(), projectionPrincipal("user-1"), AuditListQuery{TenantID: "tenant-1", Limit: 1})
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != "visible-a" || first.Items[0].ReasonCode != "" || !first.Page.HasMore {
		t.Fatalf("first audit page = %+v, err=%v", first, err)
	}
	second, err := service.ListAudits(context.Background(), projectionPrincipal("user-1"), AuditListQuery{
		TenantID: "tenant-1", Limit: 1, Cursor: first.Page.NextCursor,
	})
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != "visible-b" || second.Page.HasMore {
		t.Fatalf("second audit page = %+v, err=%v", second, err)
	}
	if _, err := service.ListAudits(context.Background(), projectionPrincipal("user-2"), AuditListQuery{
		TenantID: "tenant-1", Limit: 1, Cursor: first.Page.NextCursor,
	}); !errors.Is(err, ontologycontract.ErrCursorStale) {
		t.Fatalf("cross-principal cursor error = %v", err)
	}
	if !strings.HasPrefix(first.Page.NextCursor, "ocs1.") {
		t.Fatalf("candidate continuation is not stateful: %q", first.Page.NextCursor)
	}
}

func TestServiceProjectionListsFailClosedAtCandidateScanBoundary(t *testing.T) {
	reader := projectionReaderFixture()
	reader.audits = nil
	hidden := make(map[string]bool, projectionCandidateScanBatches*projectionScanBatch*2)
	for index := 0; index < projectionCandidateScanBatches*projectionScanBatch; index++ {
		id := "hidden-audit-" + strconv.Itoa(index)
		reader.audits = append(reader.audits, projectionAudit(id, "tenant-1", "skipped", int64(10_000-index), reader.refs[0]))
		hidden["projection-audit:"+id] = true
	}
	reader.audits = append(reader.audits, projectionAudit("visible-after-boundary", "tenant-1", "skipped", 1, reader.refs[0]))
	service := newProjectionServiceForTest(t, reader, &projectionTestPolicy{hidden: hidden, masked: map[string][]string{}}, false)

	audits, err := service.ListAudits(context.Background(), projectionPrincipal("user-1"), AuditListQuery{TenantID: "tenant-1", Limit: 1})
	if !errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) || len(audits.Items) != 0 || audits.Page.HasMore || audits.Page.NextCursor != "" {
		t.Fatalf("boundary audit page = %+v err=%v", audits, err)
	}

	reader.audits = []contextgraph.ProjectionAudit{projectionAudit("audit-1", "tenant-1", "skipped", 20_000, reader.refs[0])}
	reader.history = nil
	for index := 0; index < projectionCandidateScanBatches*projectionScanBatch; index++ {
		id := "hidden-history-" + strconv.Itoa(index)
		reader.history = append(reader.history, projectionHistory(id, "audit-1", int64(10_000-index), reader.refs[0]))
		hidden["projection-replay-history:"+id] = true
	}
	reader.history = append(reader.history, projectionHistory("visible-history-after-boundary", "audit-1", 1, reader.refs[0]))
	service = newProjectionServiceForTest(t, reader, &projectionTestPolicy{hidden: hidden, masked: map[string][]string{}}, false)
	history, err := service.ListReplayHistory(context.Background(), projectionPrincipal("user-1"), ReplayHistoryListQuery{
		TenantID: "tenant-1", AuditID: "audit-1", Limit: 1,
	})
	if !errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) || len(history.Items) != 0 || history.Page.HasMore || history.Page.NextCursor != "" {
		t.Fatalf("boundary replay history page = %+v err=%v", history, err)
	}
}

func TestServiceListAuditsDoesNotLeakMaskedFilterFields(t *testing.T) {
	reader := projectionReaderFixture()
	reader.audits = []contextgraph.ProjectionAudit{
		projectionAudit("masked", "tenant-1", "skipped", 20, reader.refs[0]),
		projectionAudit("visible", "tenant-1", "skipped", 10, reader.refs[0]),
	}
	policy := &projectionTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{"projection-audit:masked": {"status"}}}
	service := newProjectionServiceForTest(t, reader, policy, false)
	page, err := service.ListAudits(context.Background(), projectionPrincipal("user-1"), AuditListQuery{TenantID: "tenant-1", Status: "skipped"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "visible" {
		t.Fatalf("status-filtered audit page = %+v, err=%v", page, err)
	}
	if reader.lastAuditQuery.Status != "" || reader.lastAuditQuery.ReasonCode != "" {
		t.Fatalf("sensitive audit filters reached candidate store: %+v", reader.lastAuditQuery)
	}
}

func TestServiceGetAuditUsesTenantAndMasksSensitiveFields(t *testing.T) {
	reader := projectionReaderFixture()
	reader.audits = []contextgraph.ProjectionAudit{
		projectionAudit("shared", "tenant-1", "skipped", 10, reader.refs[0]),
		projectionAudit("shared", "tenant-2", "replayed", 20, reader.refs[0]),
		projectionAudit("tenant-two-only", "tenant-2", "skipped", 30, reader.refs[0]),
		projectionAudit("invisible", "tenant-1", "skipped", 40, reader.refs[1]),
	}
	reader.audits[0].Reason, reader.audits[0].Details = "sensitive reason", map[string]any{"project_id": "secret"}
	reader.audits[1].Reason = "tenant two"
	policy := &projectionTestPolicy{
		hidden: map[string]bool{},
		masked: map[string][]string{"projection-audit:shared": {"details", "reason", "source_event_id"}},
	}
	service := newProjectionServiceForTest(t, reader, policy, false)
	detail, err := service.GetAudit(context.Background(), projectionPrincipal("user-1"), "tenant-1", "shared")
	if err != nil {
		t.Fatalf("GetAudit: %v", err)
	}
	if detail.ID != "shared" || detail.Reason != "" || detail.SourceEventID != "" || detail.Details != nil {
		t.Fatalf("masked audit detail = %+v", detail)
	}
	if _, err := service.GetAudit(context.Background(), projectionPrincipal("user-1"), "tenant-1", "tenant-two-only"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("wrong-tenant exact audit error = %v", err)
	}
	if _, err := service.GetAudit(context.Background(), projectionPrincipal("user-1"), "tenant-1", "invisible"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("invisible exact audit error = %v", err)
	}
}

func TestMapAuditDetailRemovesLegacyRawReplayErrors(t *testing.T) {
	audit := contextgraph.ProjectionAudit{ID: "audit-1", Details: map[string]any{
		"last_replay_error":      "database password appeared in an old error",
		"last_replay_error_code": "unexpected backend detail",
		"missing_target":         "project-1",
	}}
	detail := mapAuditDetail(audit, datavisibility.ResourceDecision{Fields: datavisibility.FieldDecision{Allowed: []string{"details"}}})
	if _, exists := detail.Details["last_replay_error"]; exists || detail.Details["last_replay_error_code"] != "replay_failed" || detail.Details["missing_target"] != "project-1" {
		t.Fatalf("sanitized audit details = %+v", detail.Details)
	}
}

func TestServiceReplayHistoryRequiresVisibleParentAndPaginates(t *testing.T) {
	reader := projectionReaderFixture()
	reader.audits = []contextgraph.ProjectionAudit{projectionAudit("audit-1", "tenant-1", "skipped", 10, reader.refs[0])}
	reader.history = []contextgraph.ProjectionAuditReplayHistory{
		projectionHistory("history-hidden", "audit-1", 30, reader.refs[0]),
		projectionHistory("history-b", "audit-1", 20, reader.refs[0]),
		projectionHistory("history-a", "audit-1", 20, reader.refs[0]),
	}
	policy := &projectionTestPolicy{
		hidden: map[string]bool{"projection-replay-history:history-hidden": true},
		masked: map[string][]string{"projection-replay-history:history-b": {"error"}},
	}
	service := newProjectionServiceForTest(t, reader, policy, false)
	first, err := service.ListReplayHistory(context.Background(), projectionPrincipal("user-1"), ReplayHistoryListQuery{TenantID: "tenant-1", AuditID: "audit-1", Limit: 1})
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != "history-b" || first.Items[0].Error != "" || !first.Page.HasMore {
		t.Fatalf("first replay history page = %+v, err=%v", first, err)
	}
	second, err := service.ListReplayHistory(context.Background(), projectionPrincipal("user-1"), ReplayHistoryListQuery{
		TenantID: "tenant-1", AuditID: "audit-1", Limit: 1, Cursor: first.Page.NextCursor,
	})
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != "history-a" || second.Page.HasMore {
		t.Fatalf("second replay history page = %+v, err=%v", second, err)
	}

	hiddenParentPolicy := &projectionTestPolicy{hidden: map[string]bool{"projection-audit:audit-1": true}, masked: map[string][]string{}}
	hiddenParentReader := projectionReaderFixture()
	hiddenParentReader.audits = reader.audits
	hiddenParentReader.history = reader.history
	hiddenParentService := newProjectionServiceForTest(t, hiddenParentReader, hiddenParentPolicy, false)
	_, err = hiddenParentService.ListReplayHistory(context.Background(), projectionPrincipal("user-1"), ReplayHistoryListQuery{TenantID: "tenant-1", AuditID: "audit-1"})
	if !errors.Is(err, ErrUnavailable) || hiddenParentReader.historyCalls != 0 {
		t.Fatalf("hidden parent replay history error=%v history_calls=%d", err, hiddenParentReader.historyCalls)
	}
}

func newProjectionServiceForTest(t *testing.T, reader *projectionTestReader, policy *projectionTestPolicy, deny bool) *Service {
	t.Helper()
	binding := ontologyscope.ScopeBinding{
		TenantID: "tenant-1", Kind: ontologyscope.KindCustom, SourceObject: identity.ObjectRef{Type: "workspace", ID: "tenant-1"},
		AuthorizationTenantID: "tenant-1", ReadAction: "workspace.view", ManageAction: "workspace.manage", BindingVersion: 1, State: ontologyscope.BindingStateActive,
	}
	gate := ontologyscope.NewScopeAccessGate(projectionTestResolver{binding: binding}, projectionTestAuthorizer{deny: deny}, projectionTestSnapshots{})
	codec, err := ontologycontract.NewCursorCodec(ontologycontract.CursorCodecOptions{
		PrimaryID: "test", Keys: []ontologycontract.CursorKey{{ID: "test", Key: bytes.Repeat([]byte{17}, 32)}},
	})
	if err != nil {
		t.Fatalf("NewCursorCodec: %v", err)
	}
	stateful, err := ontologycontract.NewStateCursorStore(ontologycontract.StateCursorOptions{MaxEntries: 100})
	if err != nil {
		t.Fatalf("NewStateCursorStore: %v", err)
	}
	visibility := datavisibility.NewService(datavisibility.Register(datavisibility.ResourceAudit, policy))
	service, err := NewService(gate, reader, visibility, CursorSet{Encrypted: codec, Stateful: stateful}, nil, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	service.now = func() time.Time { return time.Unix(100, 0).UTC() }
	return service
}

func projectionReaderFixture() *projectionTestReader {
	allowed := contextgraph.AccessRef{Kind: "mission", ID: "allowed", TenantID: "tenant-1"}
	denied := contextgraph.AccessRef{Kind: "mission", ID: "denied", TenantID: "tenant-1"}
	return &projectionTestReader{
		refs: []contextgraph.AccessRef{allowed, denied},
		audits: []contextgraph.ProjectionAudit{
			projectionAudit("audit-skipped", "tenant-1", "skipped", 10, allowed),
			projectionAudit("audit-replayed", "tenant-1", "replayed", 20, allowed),
		},
	}
}

func projectionAudit(id, tenantID, status string, unix int64, ref contextgraph.AccessRef) contextgraph.ProjectionAudit {
	return contextgraph.ProjectionAudit{
		ID: id, TenantID: tenantID, SourceEventID: "event-" + id, SourceEventType: "mission.updated",
		Status: status, ReasonCode: "missing_target", Reason: "target unavailable", SourceAccessRef: ref,
		AccessState: contextgraph.AccessStateResolved, ObservedAt: time.Unix(unix, 0).UTC(),
	}
}

func projectionHistory(id, auditID string, unix int64, ref contextgraph.AccessRef) contextgraph.ProjectionAuditReplayHistory {
	return contextgraph.ProjectionAuditReplayHistory{
		ID: id, AuditID: auditID, TenantID: "tenant-1", SourceEventID: "event-" + auditID, SourceEventType: "mission.updated",
		Status: "failed", Error: "target unavailable", SourceAccessRef: ref,
		AccessState: contextgraph.AccessStateResolved, AttemptedAt: time.Unix(unix, 0).UTC(),
	}
}

func projectionPrincipal(id string) identity.Principal {
	return identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "oidc", Subject: id, UserID: id, ViewerKind: identity.ViewerKindUser, CoarseScopes: []string{"operations.read"},
	})
}
