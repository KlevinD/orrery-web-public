package contextgraph

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

type platformQueryFixture struct {
	graph   *PlatformStore
	allowed AccessRef
	denied  AccessRef
	tenant  string
}

func newPlatformQueryFixture(t *testing.T) platformQueryFixture {
	t.Helper()
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "context-query-" + t.Name()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := migration.NewRunner(contextGraphCompleteStoreTestMigrations()).Up(ctx, database); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	fixture := platformQueryFixture{
		graph:   NewPlatformStore(database),
		allowed: AccessRef{Kind: "mission", ID: "allowed", TenantID: "auth-a"},
		denied:  AccessRef{Kind: "mission", ID: "denied", TenantID: "auth-a"},
		tenant:  "mission:scope-a",
	}
	fixture.seed(t)
	return fixture
}

func (f platformQueryFixture) seed(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	objects := []Object{
		{ID: "root", TenantID: f.tenant, ObjectType: "mission", CanonicalKey: "mission:root", Label: "Root", Properties: map[string]any{"secret": "visible"}, EvidenceRefs: []Ref{{Kind: "document", ID: "doc-1"}}, OwnerAccessRef: f.allowed, UpdatedAt: queryTestTime(10)},
		{ID: "child", TenantID: f.tenant, ObjectType: "task", CanonicalKey: "task:child", Label: "Child", OwnerAccessRef: f.allowed, UpdatedAt: queryTestTime(9)},
		{ID: "grand", TenantID: f.tenant, ObjectType: "task", CanonicalKey: "task:grand", Label: "Grandchild", OwnerAccessRef: f.allowed, UpdatedAt: queryTestTime(8)},
		{ID: "hidden", TenantID: f.tenant, ObjectType: "task", CanonicalKey: "task:hidden", Label: "Hidden", OwnerAccessRef: f.denied, UpdatedAt: queryTestTime(7)},
		{ID: "root", TenantID: "mission:scope-b", ObjectType: "mission", CanonicalKey: "mission:root", Label: "Other tenant", OwnerAccessRef: f.denied, UpdatedAt: queryTestTime(11)},
	}
	for _, object := range objects {
		if err := f.graph.SaveObject(ctx, object); err != nil {
			t.Fatalf("save object %s/%s: %v", object.TenantID, object.ID, err)
		}
	}
	links := []Link{
		{ID: "link-root-child", TenantID: f.tenant, LinkType: "has_task", FromObjectID: "root", ToObjectID: "child", SourceAccessRef: f.allowed, UpdatedAt: queryTestTime(6)},
		{ID: "link-child-grand", TenantID: f.tenant, LinkType: "precedes", FromObjectID: "child", ToObjectID: "grand", SourceAccessRef: f.allowed, UpdatedAt: queryTestTime(5)},
		{ID: "link-hidden-endpoint", TenantID: f.tenant, LinkType: "has_task", FromObjectID: "root", ToObjectID: "hidden", SourceAccessRef: f.allowed, UpdatedAt: queryTestTime(4)},
		{ID: "link-hidden-source", TenantID: f.tenant, LinkType: "related", FromObjectID: "root", ToObjectID: "child", SourceAccessRef: f.denied, UpdatedAt: queryTestTime(3)},
	}
	for _, link := range links {
		if err := f.graph.SaveLink(ctx, link); err != nil {
			t.Fatalf("save link %s: %v", link.ID, err)
		}
	}
	facts := []Fact{
		{ID: "fact-visible", TenantID: f.tenant, Subject: Ref{Kind: "mission", ID: "root"}, Predicate: "status", Value: "active", SourceAccessRef: f.allowed, UpdatedAt: queryTestTime(2)},
		{ID: "fact-hidden-source", TenantID: f.tenant, Subject: Ref{Kind: "mission", ID: "root"}, Predicate: "secret", Value: true, SourceAccessRef: f.denied, UpdatedAt: queryTestTime(3)},
		{ID: "fact-hidden-subject", TenantID: f.tenant, Subject: Ref{Kind: "task", ID: "hidden"}, Predicate: "secret", Value: true, SourceAccessRef: f.allowed, UpdatedAt: queryTestTime(4)},
		{ID: "fact-visible", TenantID: "mission:scope-b", Subject: Ref{Kind: "mission", ID: "root"}, Predicate: "status", Value: "other", SourceAccessRef: f.denied, UpdatedAt: queryTestTime(5)},
	}
	for _, fact := range facts {
		fact.Provenance = Ref{Kind: "test_fixture", ID: fact.ID}
		if err := f.graph.SaveFact(ctx, fact); err != nil {
			t.Fatalf("save fact %s: %v", fact.ID, err)
		}
	}
	evidence := []Evidence{
		{ID: "evidence-visible", TenantID: f.tenant, SourceRef: Ref{Kind: "document", ID: "doc-1"}, Summary: "Visible evidence", SourceAccessRef: f.allowed, ObservedAt: queryTestTime(1)},
		{ID: "evidence-hidden", TenantID: f.tenant, SourceRef: Ref{Kind: "document", ID: "doc-1"}, Summary: "Hidden evidence", SourceAccessRef: f.denied, ObservedAt: queryTestTime(2)},
		{ID: "evidence-orphan", TenantID: f.tenant, SourceRef: Ref{Kind: "document", ID: "orphan"}, Summary: "Unreferenced evidence", SourceAccessRef: AccessRef{Kind: "mission", ID: "evidence-only", TenantID: "auth-a"}, ObservedAt: queryTestTime(0)},
	}
	for _, item := range evidence {
		if err := f.graph.SaveEvidence(ctx, item); err != nil {
			t.Fatalf("save evidence %s: %v", item.ID, err)
		}
	}
}

func (f platformQueryFixture) visibility() VisibilityConstraint {
	return VisibilityConstraint{AllowedAccessRefs: []AccessRef{f.allowed}}
}

func queryTestTime(second int64) time.Time {
	return time.Unix(second, 0).UTC()
}

func TestPlatformQueryCountsVisibleContextWithoutEvidenceSideChannels(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	counts, err := fixture.graph.CountVisibleContext(context.Background(), fixture.tenant, fixture.visibility())
	if err != nil {
		t.Fatalf("CountVisibleContext: %v", err)
	}
	if counts.Objects != 3 || counts.Links != 2 || counts.Facts != 1 {
		t.Fatalf("visible counts = %+v", counts)
	}
	if counts.OldestAt == nil || !counts.OldestAt.Equal(queryTestTime(2)) || counts.NewestAt == nil || !counts.NewestAt.Equal(queryTestTime(10)) {
		t.Fatalf("visible time bounds = %+v", counts)
	}
}

func TestPlatformQueryReadsContextOverviewGroups(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	groups, err := fixture.graph.ReadContextOverviewGroups(context.Background(), fixture.tenant)
	if err != nil {
		t.Fatalf("ReadContextOverviewGroups: %v", err)
	}
	if len(groups) != 12 {
		t.Fatalf("overview group count = %d, groups=%+v", len(groups), groups)
	}
	assertContextOverviewGroup(t, groups, ContextOverviewObject, "mission", 1, fixture.allowed)
	assertContextOverviewGroup(t, groups, ContextOverviewObject, "task", 2, fixture.allowed)
	assertContextOverviewGroup(t, groups, ContextOverviewFact, "", 1, fixture.allowed)
	assertContextOverviewGroup(t, groups, ContextOverviewFact, "", 1, fixture.allowed, fixture.denied)
	assertContextOverviewGroup(t, groups, ContextOverviewLink, "", 2, fixture.allowed)
	assertContextOverviewGroup(t, groups, ContextOverviewLink, "", 1, fixture.allowed, fixture.denied)
	assertContextOverviewGroup(t, groups, ContextOverviewEvidence, "", 1, fixture.allowed)
	assertContextOverviewGroup(t, groups, ContextOverviewEvidence, "", 1, AccessRef{Kind: "mission", ID: "evidence-only", TenantID: "auth-a"})
	for _, group := range groups {
		if group.NewestAt != nil && group.NewestAt.After(queryTestTime(10)) {
			t.Fatalf("overview leaked another tenant: %+v", group)
		}
	}
}

func TestPlatformStoreSnapshotsOnlyCurrentSeededSubgraph(t *testing.T) {
	fixture := newPlatformQueryFixture(t)

	objects, links, facts, evidence, err := fixture.graph.SnapshotCurrentSubgraph(
		context.Background(), fixture.tenant, []Ref{{Kind: "mission", ID: "root"}}, 1, DefaultCurrentSubgraphLimits(),
	)
	if err != nil {
		t.Fatalf("SnapshotCurrentSubgraph: %v", err)
	}
	if got := objectIDs(objects); !slices.Equal(got, []string{"child", "hidden", "root"}) {
		t.Fatalf("object ids = %v", got)
	}
	if len(links) != 3 || len(facts) != 3 || len(evidence) != 0 {
		t.Fatalf("subgraph objects=%d links=%d facts=%d evidence=%d", len(objects), len(links), len(facts), len(evidence))
	}
}

func TestSnapshotCurrentSubgraphRejectsUnresolvedRows(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	ctx := context.Background()
	for _, statement := range []string{
		`UPDATE context_graph_links SET access_state = 'unresolved' WHERE tenant_id = ? AND from_object_id = 'root'`,
		`UPDATE context_graph_facts SET access_state = 'unresolved' WHERE tenant_id = ? AND subject_id = 'root'`,
	} {
		if _, err := fixture.graph.exec(ctx, statement, fixture.tenant); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.graph.exec(ctx, `
INSERT INTO context_graph_evidence (
	id, tenant_id, source_kind, source_id, summary, confidence,
	source_access_kind, source_access_id, source_access_tenant_id, access_state, observed_at
) VALUES ('doc-1', ?, 'document', 'doc-1', 'unresolved', 1, '', '', '', 'unresolved', 1)`, fixture.tenant); err != nil {
		t.Fatal(err)
	}

	objects, links, facts, evidence, err := fixture.graph.SnapshotCurrentSubgraph(
		ctx, fixture.tenant, []Ref{{Kind: "mission", ID: "root"}}, 1, DefaultCurrentSubgraphLimits(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := objectIDs(objects); !slices.Equal(got, []string{"root"}) || len(links) != 0 || len(facts) != 0 || len(evidence) != 0 {
		t.Fatalf("unresolved rows leaked: objects=%v links=%+v facts=%+v evidence=%+v", got, links, facts, evidence)
	}
}

func TestSnapshotCurrentSubgraphStopsAtNodeLimit(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	ctx := context.Background()
	for index := range 201 {
		id := fmt.Sprintf("wide-%03d", index)
		if err := fixture.graph.SaveObject(ctx, Object{
			ID: id, TenantID: fixture.tenant, ObjectType: "task", CanonicalKey: id,
			OwnerAccessRef: fixture.allowed, UpdatedAt: queryTestTime(int64(100 + index)),
		}); err != nil {
			t.Fatal(err)
		}
		if err := fixture.graph.SaveLink(ctx, Link{
			ID: "wide-link-" + id, TenantID: fixture.tenant, LinkType: "has_task",
			FromObjectID: "root", ToObjectID: id, SourceAccessRef: fixture.allowed,
		}); err != nil {
			t.Fatal(err)
		}
	}
	_, _, _, _, err := fixture.graph.SnapshotCurrentSubgraph(
		ctx, fixture.tenant, []Ref{{Kind: "mission", ID: "root"}}, 1, DefaultCurrentSubgraphLimits(),
	)
	if !errors.Is(err, ErrQueryBudgetExceeded) {
		t.Fatalf("SnapshotCurrentSubgraph error = %v, want ErrQueryBudgetExceeded", err)
	}
}

func TestSnapshotCurrentSubgraphStopsAtEdgeLimit(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	ctx := context.Background()
	for index := range 401 {
		if err := fixture.graph.SaveLink(ctx, Link{
			ID: fmt.Sprintf("dense-%03d", index), TenantID: fixture.tenant, LinkType: "related",
			FromObjectID: "root", ToObjectID: "child", SourceAccessRef: fixture.allowed,
		}); err != nil {
			t.Fatal(err)
		}
	}
	_, _, _, _, err := fixture.graph.SnapshotCurrentSubgraph(
		ctx, fixture.tenant, []Ref{{Kind: "mission", ID: "root"}}, 1, DefaultCurrentSubgraphLimits(),
	)
	if !errors.Is(err, ErrQueryBudgetExceeded) {
		t.Fatalf("SnapshotCurrentSubgraph error = %v, want ErrQueryBudgetExceeded", err)
	}
}

func objectIDs(objects []Object) []string {
	ids := make([]string, 0, len(objects))
	for _, object := range objects {
		ids = append(ids, object.ID)
	}
	return ids
}

func TestPlatformQueryReadsMoreThanLegacyOverviewGroupLimitWithOneAccessRef(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	const groupCount = 5001
	tenantID := "mission:many-overview-groups"
	err := fixture.graph.store.WithTx(context.Background(), func(tx db.Tx) error {
		for index := 0; index < groupCount; index++ {
			id := fmt.Sprintf("object-%04d", index)
			if _, err := tx.ExecContext(context.Background(), `
INSERT INTO context_graph_objects (
	id, tenant_id, object_type, canonical_key, label, properties_json,
	sensitivity, confidence, status, provenance_json, evidence_refs_json,
	owner_access_kind, owner_access_id, owner_access_tenant_id, access_state,
	source_stream_id, source_version, temporal_json, updated_at
) VALUES (?, ?, ?, ?, ?, '{}', '', 0, '', '{}', '[]', ?, ?, ?, 'resolved', '', 0, '{}', ?)`,
				id, tenantID, fmt.Sprintf("type-%04d", index), id, id,
				fixture.allowed.Kind, fixture.allowed.ID, fixture.allowed.TenantID, int64(index+1),
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed overview groups: %v", err)
	}

	groups, err := fixture.graph.ReadContextOverviewGroups(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("ReadContextOverviewGroups: %v", err)
	}
	if len(groups) != groupCount {
		t.Fatalf("overview group count = %d, want %d", len(groups), groupCount)
	}
}

func TestPlatformQueryListsDistinctContextTenants(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	page, err := fixture.graph.ListContextTenantIDs(context.Background(), 2)
	if err != nil {
		t.Fatalf("ListContextTenantIDs: %v", err)
	}
	if page.HasMore || len(page.TenantIDs) != 2 || page.TenantIDs[0] != "mission:scope-a" || page.TenantIDs[1] != "mission:scope-b" {
		t.Fatalf("context tenants = %+v", page)
	}
	limited, err := fixture.graph.ListContextTenantIDs(context.Background(), 1)
	if err != nil || !limited.HasMore || len(limited.TenantIDs) != 1 || limited.TenantIDs[0] != "mission:scope-a" {
		t.Fatalf("limited context tenants = %+v, err=%v", limited, err)
	}
}

func TestPlatformQueryListsOnlyContextTenantsWithCanonicalScopeSources(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	ctx := context.Background()
	if _, err := fixture.graph.exec(ctx, `CREATE TABLE ontology_scope_bindings (
		tenant_id TEXT PRIMARY KEY,
		state TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create scope binding fixture: %v", err)
	}
	for _, object := range []Object{
		{ID: "organization", TenantID: "org-a", ObjectType: "organization", OwnerAccessRef: fixture.allowed, UpdatedAt: queryTestTime(20)},
		{ID: "system", TenantID: "system", ObjectType: "system", OwnerAccessRef: fixture.allowed, UpdatedAt: queryTestTime(21)},
		{ID: "project", TenantID: "project:org-a/project-a", ObjectType: "project", OwnerAccessRef: fixture.allowed, UpdatedAt: queryTestTime(22)},
		{ID: "mission", TenantID: "mission:global/mission-a", ObjectType: "mission", OwnerAccessRef: fixture.allowed, UpdatedAt: queryTestTime(23)},
		{ID: "resource", TenantID: "resource:resource-a", ObjectType: "resource", OwnerAccessRef: fixture.allowed, UpdatedAt: queryTestTime(24)},
		{ID: "custom", TenantID: "custom-a", ObjectType: "custom", OwnerAccessRef: fixture.allowed, UpdatedAt: queryTestTime(25)},
	} {
		if err := fixture.graph.SaveObject(ctx, object); err != nil {
			t.Fatalf("save scope object %s: %v", object.TenantID, err)
		}
	}
	for table, keys := range map[string][]string{
		"operations_organizations":   {"org-a"},
		"projects":                   {"org-a/project-a"},
		"mission_control_projection": {"mission-a"},
		"resource_tool_definitions":  {"resource-a"},
	} {
		for _, key := range keys {
			if _, err := fixture.graph.exec(ctx, `INSERT INTO platform_records (table_name, record_key, payload_json) VALUES (?, ?, '{}')`, table, key); err != nil {
				t.Fatalf("save scope source %s/%s: %v", table, key, err)
			}
		}
	}
	if _, err := fixture.graph.exec(ctx, `INSERT INTO ontology_scope_bindings (tenant_id, state) VALUES ('custom-a', 'active')`); err != nil {
		t.Fatalf("save custom scope binding: %v", err)
	}

	page, err := fixture.graph.ListContextScopeTenantIDs(ctx, 10)
	if err != nil {
		t.Fatalf("ListContextScopeTenantIDs: %v", err)
	}
	want := []string{"custom-a", "mission:global/mission-a", "org-a", "project:org-a/project-a", "resource:resource-a", "system"}
	if page.HasMore || !slices.Equal(page.TenantIDs, want) {
		t.Fatalf("context scope tenants = %+v, want %v", page, want)
	}
}

func assertContextOverviewGroup(t *testing.T, groups []ContextOverviewGroup, kind ContextOverviewGroupKind, objectType string, count int64, refs ...AccessRef) {
	t.Helper()
	wantRefs := make(map[AccessRef]bool, len(refs))
	for _, ref := range refs {
		wantRefs[NormalizeAccessRef(ref)] = true
	}
	for _, group := range groups {
		if group.Kind != kind || group.ObjectType != objectType || group.Count != count || len(group.AccessRefs) != len(wantRefs) {
			continue
		}
		matched := true
		for _, ref := range group.AccessRefs {
			matched = matched && wantRefs[NormalizeAccessRef(ref)]
		}
		if matched {
			return
		}
	}
	t.Fatalf("missing overview group kind=%q object_type=%q count=%d refs=%+v in %+v", kind, objectType, count, refs, groups)
}

func TestPlatformQueryListsVisibleObjects(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	page, err := fixture.graph.ListVisibleObjects(context.Background(), ObjectQuery{TenantID: fixture.tenant, Visibility: fixture.visibility(), Limit: 2})
	if err != nil {
		t.Fatalf("ListVisibleObjects first page: %v", err)
	}
	if len(page.Items) != 2 || page.Items[0].ID != "root" || page.Items[1].ID != "child" || !page.HasMore {
		t.Fatalf("first object page = %+v", page)
	}
	next, err := fixture.graph.ListVisibleObjects(context.Background(), ObjectQuery{
		TenantID: fixture.tenant, Visibility: fixture.visibility(), Limit: 2,
		After: ObjectSeek{UpdatedAt: page.Items[1].UpdatedAt, ID: page.Items[1].ID},
	})
	if err != nil {
		t.Fatalf("ListVisibleObjects second page: %v", err)
	}
	if len(next.Items) != 1 || next.Items[0].ID != "grand" || next.HasMore {
		t.Fatalf("second object page = %+v", next)
	}
	refs, err := fixture.graph.ListContextEntityAccessRefs(context.Background(), AccessRefQuery{TenantID: fixture.tenant, Limit: 10})
	if err != nil || len(refs.Items) != 2 {
		t.Fatalf("tenant access refs = %+v, err=%v", refs, err)
	}
}

func TestPlatformQueryPaginatesDistinctContextAccessRefsAcrossSources(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	shared := AccessRef{Kind: "project", ID: "shared", TenantID: "auth-b"}
	factOnly := AccessRef{Kind: "resource", ID: "fact-only", TenantID: "auth-c"}
	if err := fixture.graph.SaveLink(context.Background(), Link{
		ID: "link-shared-access", TenantID: fixture.tenant, LinkType: "related",
		FromObjectID: "root", ToObjectID: "child", SourceAccessRef: shared, UpdatedAt: queryTestTime(12),
	}); err != nil {
		t.Fatalf("save shared link: %v", err)
	}
	for _, fact := range []Fact{
		{ID: "fact-shared-access", TenantID: fixture.tenant, Subject: Ref{Kind: "mission", ID: "root"}, Predicate: "shared", Value: true, Provenance: Ref{Kind: "test_fixture", ID: "fact-shared-access"}, SourceAccessRef: shared, UpdatedAt: queryTestTime(13)},
		{ID: "fact-only-access", TenantID: fixture.tenant, Subject: Ref{Kind: "mission", ID: "root"}, Predicate: "fact-only", Value: true, Provenance: Ref{Kind: "test_fixture", ID: "fact-only-access"}, SourceAccessRef: factOnly, UpdatedAt: queryTestTime(14)},
	} {
		if err := fixture.graph.SaveFact(context.Background(), fact); err != nil {
			t.Fatalf("save fact %s: %v", fact.ID, err)
		}
	}

	first, err := fixture.graph.ListContextEntityAccessRefs(context.Background(), AccessRefQuery{TenantID: fixture.tenant, Limit: 2})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if !first.HasMore || len(first.Items) != 2 || first.Items[0] != fixture.allowed || first.Items[1] != fixture.denied {
		t.Fatalf("first page = %+v", first)
	}
	last := first.Items[len(first.Items)-1]
	second, err := fixture.graph.ListContextEntityAccessRefs(context.Background(), AccessRefQuery{
		TenantID: fixture.tenant, Limit: 2, After: AccessRefSeek{Kind: last.Kind, ID: last.ID, TenantID: last.TenantID},
	})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if second.HasMore || len(second.Items) != 2 || second.Items[0] != shared || second.Items[1] != factOnly {
		t.Fatalf("second page = %+v", second)
	}
}

func TestPlatformQueryPaginatesDistinctAccessRefsAcrossSources(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	ctx := context.Background()
	tenantID := "mission:access-ref-pagination"
	authTenantID := "auth-pagination"
	alpha := AccessRef{Kind: "alpha", ID: "one", TenantID: authTenantID}
	beta := AccessRef{Kind: "beta", ID: "one", TenantID: authTenantID}
	charlie := AccessRef{Kind: "charlie", ID: "one", TenantID: authTenantID}
	delta := AccessRef{Kind: "delta", ID: "one", TenantID: authTenantID}

	for _, object := range []Object{
		{ID: "root", TenantID: tenantID, ObjectType: "mission", OwnerAccessRef: alpha, UpdatedAt: queryTestTime(30)},
		{ID: "child", TenantID: tenantID, ObjectType: "task", OwnerAccessRef: delta, UpdatedAt: queryTestTime(31)},
	} {
		if err := fixture.graph.SaveObject(ctx, object); err != nil {
			t.Fatalf("save pagination object %s: %v", object.ID, err)
		}
	}
	for _, link := range []Link{
		{ID: "link-beta", TenantID: tenantID, LinkType: "has_task", FromObjectID: "root", ToObjectID: "child", SourceAccessRef: beta, UpdatedAt: queryTestTime(32)},
		{ID: "link-alpha-duplicate", TenantID: tenantID, LinkType: "related", FromObjectID: "root", ToObjectID: "child", SourceAccessRef: alpha, UpdatedAt: queryTestTime(33)},
	} {
		if err := fixture.graph.SaveLink(ctx, link); err != nil {
			t.Fatalf("save pagination link %s: %v", link.ID, err)
		}
	}
	for _, fact := range []Fact{
		{ID: "fact-charlie", TenantID: tenantID, Subject: Ref{Kind: "mission", ID: "root"}, Predicate: "status", Value: "active", Provenance: Ref{Kind: "test_fixture", ID: "fact-charlie"}, SourceAccessRef: charlie, UpdatedAt: queryTestTime(34)},
		{ID: "fact-alpha-duplicate", TenantID: tenantID, Subject: Ref{Kind: "mission", ID: "root"}, Predicate: "owner", Value: "alpha", Provenance: Ref{Kind: "test_fixture", ID: "fact-alpha-duplicate"}, SourceAccessRef: alpha, UpdatedAt: queryTestTime(35)},
	} {
		if err := fixture.graph.SaveFact(ctx, fact); err != nil {
			t.Fatalf("save pagination fact %s: %v", fact.ID, err)
		}
	}

	first, err := fixture.graph.ListContextEntityAccessRefs(ctx, AccessRefQuery{TenantID: tenantID, Limit: 2})
	if err != nil {
		t.Fatalf("list first access-ref page: %v", err)
	}
	if !first.HasMore || !slices.Equal(first.Items, []AccessRef{alpha, beta}) {
		t.Fatalf("first access-ref page = %+v", first)
	}
	second, err := fixture.graph.ListContextEntityAccessRefs(ctx, AccessRefQuery{
		TenantID: tenantID,
		After:    AccessRefSeek{Kind: beta.Kind, ID: beta.ID, TenantID: beta.TenantID},
		Limit:    2,
	})
	if err != nil {
		t.Fatalf("list second access-ref page: %v", err)
	}
	if second.HasMore || !slices.Equal(second.Items, []AccessRef{charlie, delta}) {
		t.Fatalf("second access-ref page = %+v", second)
	}
}

func TestPlatformQueryCandidateScanIsBoundedAndCannotBackAggregates(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	page, err := fixture.graph.ListVisibleObjects(context.Background(), ObjectQuery{
		TenantID: fixture.tenant, Visibility: CandidateVisibilityConstraint(), Limit: 1,
	})
	if err != nil || len(page.Items) != 1 || !page.HasMore {
		t.Fatalf("candidate page = %+v err=%v", page, err)
	}
	if _, err := fixture.graph.CountVisibleContext(context.Background(), fixture.tenant, CandidateVisibilityConstraint()); !errors.Is(err, ErrQueryInvalid) {
		t.Fatalf("candidate aggregate error = %v", err)
	}
}

func TestPlatformQueryGetsTenantObject(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	object, err := fixture.graph.GetVisibleObject(context.Background(), fixture.tenant, "root", fixture.visibility())
	if err != nil || object.Label != "Root" {
		t.Fatalf("GetVisibleObject = %+v, err=%v", object, err)
	}
	_, err = fixture.graph.GetVisibleObject(context.Background(), fixture.tenant, "hidden", fixture.visibility())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("hidden object error = %v", err)
	}
}

func TestPlatformQueryListsObjectChildren(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	ctx := context.Background()
	facts, err := fixture.graph.ListVisibleFacts(ctx, FactQuery{TenantID: fixture.tenant, Visibility: fixture.visibility(), ObjectType: "mission", ObjectID: "root", Limit: 10})
	if err != nil || len(facts.Items) != 1 || facts.Items[0].ID != "fact-visible" {
		t.Fatalf("visible facts = %+v, err=%v", facts, err)
	}
	relations, err := fixture.graph.ListVisibleRelations(ctx, RelationQuery{TenantID: fixture.tenant, Visibility: fixture.visibility(), ObjectID: "root", Direction: RelationBoth, Limit: 10})
	if err != nil || len(relations.Items) != 1 || relations.Items[0].Link.ID != "link-root-child" || relations.Items[0].OtherObject.ID != "child" || relations.Items[0].Direction != RelationOutgoing {
		t.Fatalf("visible relations = %+v, err=%v", relations, err)
	}
	evidence, err := fixture.graph.ListVisibleEvidence(ctx, EvidenceQuery{TenantID: fixture.tenant, Visibility: fixture.visibility(), SourceRefs: []Ref{{Kind: "document", ID: "doc-1"}}, Limit: 10})
	if err != nil || len(evidence.Items) != 1 || evidence.Items[0].ID != "evidence-visible" {
		t.Fatalf("visible evidence = %+v, err=%v", evidence, err)
	}
}

func TestPlatformQueryGetsVisibleFactAndListsLineageFacts(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	ctx := context.Background()
	detail, err := fixture.graph.GetVisibleFact(ctx, fixture.tenant, "fact-visible", fixture.visibility())
	if err != nil {
		t.Fatalf("GetVisibleFact: %v", err)
	}
	if detail.Fact.ID != "fact-visible" || detail.Subject.ID != "root" || detail.Subject.Label != "Root" {
		t.Fatalf("visible fact detail = %+v", detail)
	}
	other, err := fixture.graph.GetVisibleFact(ctx, "mission:scope-b", "fact-visible", VisibilityConstraint{AllowedAccessRefs: []AccessRef{fixture.denied}})
	if err != nil || other.Fact.Value != "other" || other.Subject.Label != "Other tenant" {
		t.Fatalf("other tenant fact detail = %+v, err=%v", other, err)
	}
	for _, factID := range []string{"fact-hidden-source", "fact-hidden-subject"} {
		if _, err := fixture.graph.GetVisibleFact(ctx, fixture.tenant, factID, fixture.visibility()); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetVisibleFact(%s) error = %v", factID, err)
		}
	}
	page, err := fixture.graph.ListVisibleLineageFacts(ctx, VisibleFactQuery{TenantID: fixture.tenant, Visibility: fixture.visibility(), Limit: 1})
	if err != nil {
		t.Fatalf("ListVisibleLineageFacts: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "fact-visible" || !page.Items[0].SubjectAccessRef.Resolved() || page.HasMore {
		t.Fatalf("visible lineage facts = %+v", page)
	}
}

func TestPlatformQueryListsFactAssertions(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	ctx := context.Background()
	observed := queryTestTime(12)
	for _, provenanceID := range []string{"event-a", "event-b", "event-c"} {
		if err := fixture.graph.SaveFact(ctx, Fact{
			ID: "fact-visible", TenantID: fixture.tenant, Subject: Ref{Kind: "mission", ID: "root"},
			Predicate: "status", Value: provenanceID, Provenance: Ref{Kind: "event", ID: provenanceID},
			SourceAccessRef: fixture.allowed, SourceStreamID: "stream", SourceVersion: int64(len(provenanceID)), UpdatedAt: observed,
			Temporal: TemporalMetadata{Mode: "event", SourceRecordedAt: observed, OrreryIngestedAt: observed},
		}); err != nil {
			t.Fatalf("save assertion %s: %v", provenanceID, err)
		}
	}
	page, err := fixture.graph.ListVisibleFactAssertions(ctx, FactAssertionQuery{
		TenantID: fixture.tenant, FactID: "fact-visible", Visibility: fixture.visibility(), Limit: 2,
	})
	if err != nil {
		t.Fatalf("ListVisibleFactAssertions first page: %v", err)
	}
	if len(page.Items) != 2 || !page.HasMore || page.Items[0].AssertionID <= page.Items[1].AssertionID {
		t.Fatalf("first assertion page = %+v", page)
	}
	next, err := fixture.graph.ListVisibleFactAssertions(ctx, FactAssertionQuery{
		TenantID: fixture.tenant, FactID: "fact-visible", Visibility: fixture.visibility(), Limit: 2,
		After: FactAssertionSeek{ObservedAt: page.Items[1].ObservedAt, ID: page.Items[1].AssertionID},
	})
	if err != nil {
		t.Fatalf("ListVisibleFactAssertions second page: %v", err)
	}
	if len(next.Items) == 0 || next.HasMore {
		t.Fatalf("second assertion page = %+v", next)
	}
	seen := map[string]struct{}{}
	for _, item := range append(append([]FactAssertion{}, page.Items...), next.Items...) {
		if !item.Fact.SubjectAccessRef.Resolved() {
			t.Fatalf("assertion subject access ref is unresolved: %+v", item)
		}
		if _, duplicate := seen[item.AssertionID]; duplicate {
			t.Fatalf("duplicate assertion across pages: %s", item.AssertionID)
		}
		seen[item.AssertionID] = struct{}{}
	}
	if _, err := fixture.graph.ListVisibleFactAssertions(ctx, FactAssertionQuery{
		TenantID: fixture.tenant, FactID: "fact-hidden-source", Visibility: fixture.visibility(), Limit: 10,
	}); err != nil {
		t.Fatalf("hidden fact assertion query: %v", err)
	}
	hidden, _ := fixture.graph.ListVisibleFactAssertions(ctx, FactAssertionQuery{
		TenantID: fixture.tenant, FactID: "fact-hidden-source", Visibility: fixture.visibility(), Limit: 10,
	})
	if len(hidden.Items) != 0 {
		t.Fatalf("hidden fact assertions leaked = %+v", hidden)
	}
}

func TestPlatformQueryListsVisibleProjectionTrace(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	ctx := context.Background()
	for _, audit := range []ProjectionAudit{
		{ID: "audit-b", TenantID: fixture.tenant, SourceEventID: "event-1", SourceEventType: "mission.updated", Status: "applied", SourceAccessRef: fixture.allowed, ObservedAt: queryTestTime(14)},
		{ID: "audit-a", TenantID: fixture.tenant, SourceEventID: "event-1", SourceEventType: "mission.updated", Status: "skipped", SourceAccessRef: fixture.allowed, ObservedAt: queryTestTime(14)},
		{ID: "audit-hidden", TenantID: fixture.tenant, SourceEventID: "event-1", SourceEventType: "mission.updated", Status: "skipped", SourceAccessRef: fixture.denied, ObservedAt: queryTestTime(15)},
		{ID: "audit-other-event", TenantID: fixture.tenant, SourceEventID: "event-2", SourceEventType: "mission.updated", Status: "applied", SourceAccessRef: fixture.allowed, ObservedAt: queryTestTime(16)},
	} {
		if err := fixture.graph.SaveProjectionAudit(ctx, audit); err != nil {
			t.Fatalf("save projection audit %s: %v", audit.ID, err)
		}
	}
	page, err := fixture.graph.ListVisibleProjectionTrace(ctx, ProjectionTraceQuery{
		TenantID: fixture.tenant, SourceEventID: "event-1", SourceEventType: "mission.updated", Visibility: fixture.visibility(), Limit: 1,
	})
	if err != nil {
		t.Fatalf("ListVisibleProjectionTrace first page: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "audit-b" || !page.HasMore {
		t.Fatalf("first projection trace page = %+v", page)
	}
	next, err := fixture.graph.ListVisibleProjectionTrace(ctx, ProjectionTraceQuery{
		TenantID: fixture.tenant, SourceEventID: "event-1", SourceEventType: "mission.updated", Visibility: fixture.visibility(), Limit: 1,
		After: ProjectionAuditSeek{ObservedAt: page.Items[0].ObservedAt, ID: page.Items[0].ID},
	})
	if err != nil {
		t.Fatalf("ListVisibleProjectionTrace second page: %v", err)
	}
	if len(next.Items) != 1 || next.Items[0].ID != "audit-a" || next.HasMore {
		t.Fatalf("second projection trace page = %+v", next)
	}
}

func TestPlatformQueryBuildsBoundedNeighborhood(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	ctx := context.Background()
	graph, err := fixture.graph.BuildVisibleNeighborhood(ctx, NeighborhoodQuery{TenantID: fixture.tenant, Visibility: fixture.visibility(), RootID: "root", Depth: 2, MaxNodes: 10, MaxEdges: 10})
	if err != nil {
		t.Fatalf("BuildVisibleNeighborhood: %v", err)
	}
	if len(graph.Objects) != 3 || len(graph.Links) != 2 || graph.Truncated || graph.Objects[0].ID != "root" {
		t.Fatalf("visible neighborhood = %+v", graph)
	}
	bounded, err := fixture.graph.BuildVisibleNeighborhood(ctx, NeighborhoodQuery{TenantID: fixture.tenant, Visibility: fixture.visibility(), RootID: "root", Depth: 2, MaxNodes: 2, MaxEdges: 10})
	if err != nil {
		t.Fatalf("BuildVisibleNeighborhood bounded: %v", err)
	}
	if len(bounded.Objects) != 2 || len(bounded.Links) != 1 || !bounded.Truncated {
		t.Fatalf("bounded neighborhood = %+v", bounded)
	}
	filtered, err := fixture.graph.BuildVisibleNeighborhood(ctx, NeighborhoodQuery{TenantID: fixture.tenant, Visibility: fixture.visibility(), RootID: "root", LinkType: "has_task", Depth: 2, MaxNodes: 10, MaxEdges: 10})
	if err != nil {
		t.Fatalf("BuildVisibleNeighborhood filtered: %v", err)
	}
	if len(filtered.Links) != 1 || filtered.Links[0].LinkType != "has_task" {
		t.Fatalf("filtered neighborhood = %+v", filtered)
	}
}

func TestPlatformQueryAppliesNeighborhoodLinkAllowlistBeforeBudget(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	ctx := context.Background()
	for index := int64(0); index < 5; index++ {
		if err := fixture.graph.SaveLink(ctx, Link{
			ID: "history-" + time.Unix(index, 0).Format("150405"), TenantID: fixture.tenant,
			LinkType: "scheduled_task_has_run", FromObjectID: "root", ToObjectID: "child",
			SourceAccessRef: fixture.allowed, UpdatedAt: queryTestTime(100 + index),
		}); err != nil {
			t.Fatal(err)
		}
	}

	graph, err := fixture.graph.BuildVisibleNeighborhood(ctx, NeighborhoodQuery{
		TenantID: fixture.tenant, Visibility: fixture.visibility(), RootID: "root",
		LinkTypes: []string{"has_task"}, Depth: 1, MaxNodes: 2, MaxEdges: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Objects) != 2 || len(graph.Links) != 1 || graph.Links[0].LinkType != "has_task" || graph.Truncated {
		t.Fatalf("allowlisted neighborhood = %+v", graph)
	}

	multiple, err := fixture.graph.BuildVisibleNeighborhood(ctx, NeighborhoodQuery{
		TenantID: fixture.tenant, Visibility: fixture.visibility(), RootID: "root",
		LinkTypes: []string{"precedes", "has_task", "has_task"}, Depth: 2, MaxNodes: 3, MaxEdges: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(multiple.Links) != 2 || multiple.Links[0].LinkType == "scheduled_task_has_run" || multiple.Links[1].LinkType == "scheduled_task_has_run" {
		t.Fatalf("multi-type neighborhood = %+v", multiple)
	}
}

func TestPlatformQueryRejectsMissingTenant(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	ctx := context.Background()
	if _, err := fixture.graph.ListContextEntityAccessRefs(ctx, AccessRefQuery{Limit: 10}); !errors.Is(err, ErrQueryInvalid) {
		t.Fatalf("ListContextEntityAccessRefs missing tenant error = %v", err)
	}
	if _, err := fixture.graph.ListVisibleObjects(ctx, ObjectQuery{Visibility: fixture.visibility(), Limit: 10}); !errors.Is(err, ErrQueryInvalid) {
		t.Fatalf("ListVisibleObjects missing tenant error = %v", err)
	}
	if _, err := fixture.graph.BuildVisibleNeighborhood(ctx, NeighborhoodQuery{Visibility: fixture.visibility(), RootID: "root", Depth: 1, MaxNodes: 10, MaxEdges: 10}); !errors.Is(err, ErrQueryInvalid) {
		t.Fatalf("BuildVisibleNeighborhood missing tenant error = %v", err)
	}
	if _, err := fixture.graph.GetVisibleFact(ctx, "", "fact-visible", fixture.visibility()); !errors.Is(err, ErrQueryInvalid) {
		t.Fatalf("GetVisibleFact missing tenant error = %v", err)
	}
	if _, err := fixture.graph.ListVisibleFactAssertions(ctx, FactAssertionQuery{FactID: "fact-visible", Visibility: fixture.visibility(), Limit: 10}); !errors.Is(err, ErrQueryInvalid) {
		t.Fatalf("ListVisibleFactAssertions missing tenant error = %v", err)
	}
}

func TestPlatformQueryIsolatesDuplicateIDs(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	otherVisibility := VisibilityConstraint{AllowedAccessRefs: []AccessRef{fixture.denied}}
	other, err := fixture.graph.GetVisibleObject(context.Background(), "mission:scope-b", "root", otherVisibility)
	if err != nil || other.Label != "Other tenant" {
		t.Fatalf("other tenant duplicate = %+v, err=%v", other, err)
	}
	_, err = fixture.graph.GetVisibleObject(context.Background(), fixture.tenant, "root", otherVisibility)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong visibility duplicate error = %v", err)
	}
}
