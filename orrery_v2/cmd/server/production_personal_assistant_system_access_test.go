package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/projectadmin"
	"orrery_v2/internal/app/rebac"
	"orrery_v2/internal/app/reliability"
	gwoutbound "orrery_v2/internal/gateway/outbound"
	knowledgestore "orrery_v2/internal/knowledge/store"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/graphprotocol"
	"orrery_v2/internal/platform/testkit"
	toolres "orrery_v2/internal/resource/tool"
)

func systemReadAccessFixture(t *testing.T) (productionContextAccessPolicy, identity.Principal, *rebac.MemoryStore) {
	t.Helper()
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store("core")
	if err != nil {
		t.Fatal(err)
	}
	organizations := operations.NewOrganizationCatalogStore(runtime.DBRouter)
	for _, id := range []string{"tenant-a", "tenant-b"} {
		if err := organizations.SaveOrganization(ctx, operations.OrganizationDTO{OrganizationID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	relations := rebac.NewMemoryStore()
	for _, tuple := range []rebac.RelationTuple{
		{ID: "platform-member", TenantID: "global", Subject: rebac.Ref{Kind: "user", ID: "human-a"}, Relation: "member", Object: rebac.Ref{Kind: "platform", ID: "global"}},
		{ID: "organization-member", TenantID: "tenant-a", Subject: rebac.Ref{Kind: "user", ID: "human-a"}, Relation: "member", Object: rebac.Ref{Kind: "organization", ID: "tenant-a"}},
	} {
		if err := relations.SaveRelationTuple(ctx, tuple); err != nil {
			t.Fatal(err)
		}
	}
	authorizer := authorization.NewService(authorization.NewReBACAdapter(relations, nil))
	scopes, err := newProductionOntologyScopeState(core, organizations, projectadmin.NewMemoryRepository(), missioncontrol.NewMemoryProjectionRepository(), toolres.NewMemoryRegistry(), authorizer, authorization.NewSnapshotService(nil, nil), &fixedProductionRoleAuthorizer{allowed: true})
	if err != nil {
		t.Fatal(err)
	}
	policy := newProductionContextAccessPolicy(core, runtime.DBRouter, scopes, authorizer, knowledgestore.NewPlatformStore(core), memstore.NewPlatformStore(core))
	principal := identity.NormalizePrincipal(identity.Principal{SubjectProvider: identity.ProviderLocal, Subject: "human-a", UserID: "owner-a", Username: "owner-a", AgentProfileID: "human-a", ViewerKind: identity.ViewerKindUser, TenantID: "tenant-a", CoarseScopes: []string{"operations.read", "operations.manage", "ontology.read", "ontology.manage", "org.read", "org.manage", "enterprise_graph.read"}})
	return policy, principal, relations
}

func TestSystemAccessMemberReadsStatusWithoutOrganizationManage(t *testing.T) {
	policy, principal, _ := systemReadAccessFixture(t)
	for _, test := range []struct {
		name, tenant    string
		manage, allowed bool
	}{
		{"organization_read", "tenant-a", false, true},
		{"organization_manage_denied", "tenant-a", true, false},
		{"foreign_organization_denied", "tenant-b", false, false},
		{"system_read", "system", false, true},
		{"system_manage", "system", true, true},
		{"global_read", "global", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			allowed, err := policy.personalAssistantSystemAccess(context.Background(), principal, test.tenant, graphprotocol.AccessRef{}, test.manage)
			if err != nil || allowed != test.allowed {
				t.Fatalf("allowed=%v err=%v", allowed, err)
			}
		})
	}
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		allowed, err := policy.personalAssistantSystemAccess(context.Background(), principal, "tenant-a", graphprotocol.AccessRef{Kind: "organization", ID: tenant, TenantID: tenant}, false)
		if err != nil || allowed != (tenant == "tenant-a") {
			t.Fatalf("original RSI source %s: allowed=%v err=%v", tenant, allowed, err)
		}
	}
	principal.CoarseScopes = []string{"operations.read"}
	if allowed, err := policy.personalAssistantSystemAccess(context.Background(), principal, "tenant-a", graphprotocol.AccessRef{}, false); err != nil || allowed {
		t.Fatal("missing ontology scope granted read access", err)
	}
}

func systemReadNotificationFixture(t *testing.T) (*productionPersonalAssistantSystemUpdates, *systemRepairReaderStub, *rebac.MemoryStore) {
	t.Helper()
	policy, _, relations := systemReadAccessFixture(t)
	s, _, _ := systemUpdatesFixture(t)
	s.access = policy.personalAssistantSystemAccess
	source := systemSourceFixture(t)
	reader := &systemRepairReaderStub{}
	source.repairs = reader
	s.source = source
	now := s.now().Add(time.Second)
	service := reliability.NewService(reliability.NewPlatformRepository(source.core), reliability.WithClock(func() time.Time { return now }))
	i, err := service.ObserveFailure(context.Background(), reliability.FailureObservation{ID: "event", TenantID: "tenant-a", Source: "server_log", Component: "scheduler", ErrorCode: "timeout", Message: "public fault summary", OccurredAt: now, Severity: reliability.FailureSeverityError})
	if err != nil {
		t.Fatal(err)
	}
	reader.receipt = &reliability.AutomaticRepairReceipt{TenantID: i.TenantID, IncidentID: i.ID, OccurrenceCount: i.OccurrenceCount, LastSeenNS: i.LastSeenAt.UnixNano(), Status: "running", Summary: "protected host diagnostics", UpdatedAt: now.Add(time.Minute)}
	// Effective scopes still come from the real account resolver in recipient().
	s.attention.scopes.(*recordingGatewayViewerAuthorization).resolved = []string{"operations.read", "ontology.read", "org.read", "enterprise_graph.read"}
	return s, reader, relations
}

func systemReadTestDelivery(key, body string) gwoutbound.Delivery {
	return gwoutbound.Delivery{TenantID: "tenant-a", HumanProfileID: "human-a", AccountID: "bot-a", ChannelKey: "weixin", IdempotencyKey: key, Body: body}
}

func TestSystemUpdatesReadOnlyOrganizationUsesPublicMaterial(t *testing.T) {
	s, reader, _ := systemReadNotificationFixture(t)
	sweepSystem(t, s, 3)
	writer := s.replies.(*agentNoticeWriterStub)
	if writer.calls != 1 || systemDeliveryCount(t, s) != 1 || reader.calls != 0 {
		t.Fatalf("read-only organization blocked or host receipt read: model=%d deliveries=%d host=%d", writer.calls, systemDeliveryCount(t, s), reader.calls)
	}
	if strings.Contains(writer.material, "protected host diagnostics") {
		t.Fatal("protected material entered model input")
	}
	var key, body string
	if err := s.jobs.QueryRow(context.Background(), "SELECT idempotency_key,body FROM gateway_message_deliveries").Scan(&key, &body); err != nil {
		t.Fatal(err)
	}
	d := systemReadTestDelivery(key, body)
	if allowed, err := s.Validate(context.Background(), d); err != nil || !allowed {
		t.Fatal("read-only delivery rejected", err)
	}
	s.attention.scopes.(*recordingGatewayViewerAuthorization).resolved = nil
	if allowed, _ := s.Validate(context.Background(), d); allowed {
		t.Fatal("read permission revocation did not block queued delivery")
	}
	if _, allowed, _ := s.ContextNotice(context.Background(), d); allowed {
		t.Fatal("read permission revocation did not hide assistant context")
	}
}

func TestSystemUpdatesManageRevocationHidesReceiptsAndKeepsReadStatus(t *testing.T) {
	s, reader, relations := systemReadNotificationFixture(t)
	admin := rebac.RelationTuple{ID: "organization-admin", TenantID: "tenant-a", Subject: rebac.Ref{Kind: "user", ID: "human-a"}, Relation: "admin", Object: rebac.Ref{Kind: "organization", ID: "tenant-a"}}
	ctx := context.Background()
	if err := relations.SaveRelationTuple(ctx, admin); err != nil {
		t.Fatal(err)
	}
	auth := s.attention.scopes.(*recordingGatewayViewerAuthorization)
	auth.resolved = append(auth.resolved, "ontology.manage", "org.manage")
	sweepSystem(t, s, 1)
	writer := s.replies.(*agentNoticeWriterStub)
	if reader.calls == 0 || !strings.Contains(writer.material, "protected host diagnostics") || systemDeliveryCount(t, s) != 1 {
		t.Fatal("authorized manager lost host receipt", writer.material)
	}
	var key, body string
	if err := s.jobs.QueryRow(ctx, "SELECT idempotency_key,body FROM gateway_message_deliveries").Scan(&key, &body); err != nil {
		t.Fatal(err)
	}
	d := systemReadTestDelivery(key, body)
	if allowed, err := s.Validate(ctx, d); err != nil || !allowed {
		t.Fatal("authorized managed notice rejected", err)
	}
	if err := relations.DeleteRelationTuple(ctx, admin); err != nil {
		t.Fatal(err)
	}
	before := reader.calls
	if allowed, _ := s.Validate(ctx, d); allowed {
		t.Fatal("queued host receipt survived management revocation")
	}
	if _, allowed, _ := s.ContextNotice(ctx, d); allowed {
		t.Fatal("private assistant context survived management revocation")
	}
	sweepSystem(t, s, 2)
	if reader.calls != before || strings.Contains(writer.material, "protected host diagnostics") || systemDeliveryCount(t, s) != 2 {
		t.Fatal("lost basic read status or loaded receipt after management revocation", writer.material)
	}
}
