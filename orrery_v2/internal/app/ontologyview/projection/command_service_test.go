package projection

import (
	"context"
	"errors"
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

type projectionCommandResolver struct{ binding ontologyscope.ScopeBinding }

func (r projectionCommandResolver) Resolve(_ context.Context, tenantID string) (ontologyscope.ScopeBinding, error) {
	if tenantID != r.binding.TenantID {
		return ontologyscope.ScopeBinding{}, ontologyscope.ErrBindingNotFound
	}
	return r.binding, nil
}

type projectionCommandAuthorizer struct {
	deny         bool
	featureCalls int
	objectCalls  int
}

func (a *projectionCommandAuthorizer) AuthorizeFeature(_ context.Context, _ identity.Principal, action string) error {
	a.featureCalls++
	if a.deny || action != ontologyscope.FeatureManageAction {
		return authorization.ErrDenied
	}
	return nil
}

func (a *projectionCommandAuthorizer) AuthorizeObject(_ context.Context, _ identity.Principal, action string, _ identity.ObjectRef, _ string) error {
	a.objectCalls++
	if a.deny || action != "organization.manage" {
		return authorization.ErrDenied
	}
	return nil
}

type projectionCommandSnapshots struct{ revision string }

func (s projectionCommandSnapshots) Capture(_ context.Context, principal identity.Principal) (authorization.AuthorizationSnapshot, error) {
	return authorization.AuthorizationSnapshot{
		PrincipalKey: authorization.PrincipalCacheKey(principal), AuthorizationRevision: "authorization:" + s.revision, PolicyRevision: "policy:" + s.revision,
	}, nil
}

type projectionCommandStore struct {
	items       map[string]contextgraph.ProjectionAudit
	calls       []string
	second      *contextgraph.ProjectionAudit
	claim       contextgraph.ProjectionAuditCommandClaim
	claimErr    error
	completeErr error
	failErr     error
	afterClaim  func()
	claimCalls  int
	effectCalls int
	completions []contextgraph.ProjectionAuditCommandCompletion
	failures    []contextgraph.ProjectionAuditCommandFailure
}

func (s *projectionCommandStore) GetProjectionAudit(_ context.Context, tenantID, auditID string) (contextgraph.ProjectionAudit, error) {
	s.calls = append(s.calls, tenantID+"/"+auditID)
	item, ok := s.items[tenantID+"/"+auditID]
	if !ok {
		return contextgraph.ProjectionAudit{}, contextgraph.ErrNotFound
	}
	if len(s.calls) == 2 && s.second != nil {
		return *s.second, nil
	}
	return item, nil
}

func (s *projectionCommandStore) ClaimProjectionAuditCommand(_ context.Context, _ contextgraph.ProjectionAuditCommandClaimRequest) (contextgraph.ProjectionAuditCommandClaim, error) {
	s.claimCalls++
	if s.afterClaim != nil {
		s.afterClaim()
	}
	if s.claimErr != nil {
		return contextgraph.ProjectionAuditCommandClaim{}, s.claimErr
	}
	if s.claim.State == "" {
		return contextgraph.ProjectionAuditCommandClaim{
			Acquired: true, State: contextgraph.ProjectionAuditCommandClaimed, LeaseToken: strings.Repeat("f", 64),
		}, nil
	}
	return s.claim, nil
}

func (s *projectionCommandStore) CompleteProjectionAuditCommand(ctx context.Context, completion contextgraph.ProjectionAuditCommandCompletion) error {
	s.completions = append(s.completions, completion)
	if s.completeErr != nil {
		return s.completeErr
	}
	if completion.Effect != nil {
		s.effectCalls++
		if err := completion.Effect(ctx, contextgraph.NewMemoryStore()); err != nil {
			return errors.Join(contextgraph.ErrProjectionAuditCommandEffect, err)
		}
	}
	return nil
}

func (s *projectionCommandStore) FailProjectionAuditCommand(_ context.Context, failure contextgraph.ProjectionAuditCommandFailure) error {
	s.failures = append(s.failures, failure)
	return s.failErr
}

type projectionCommandVisibility struct {
	hidden        bool
	masked        string
	purposes      []string
	lastPrincipal identity.Principal
}

func (p *projectionCommandVisibility) Evaluate(_ context.Context, request datavisibility.EvaluateRequest) (datavisibility.Decision, error) {
	p.purposes = append(p.purposes, request.Purpose)
	p.lastPrincipal = request.Principal
	resource := request.Resources[0]
	allowed := append([]string(nil), resource.FieldPaths...)
	masked := []string(nil)
	if p.masked != "" {
		masked = []string{p.masked}
		filtered := allowed[:0]
		for _, field := range allowed {
			if field != p.masked {
				filtered = append(filtered, field)
			}
		}
		allowed = filtered
	}
	disclosure := datavisibility.ExistenceVisible
	if p.hidden {
		disclosure, allowed, masked = datavisibility.ExistenceConceal, nil, nil
	}
	return datavisibility.Decision{Resources: []datavisibility.ResourceDecision{{
		Key: resource.Key, Hidden: p.hidden, ExistenceDisclosure: disclosure,
		Fields: datavisibility.FieldDecision{Allowed: allowed, Masked: masked},
	}}}, nil
}

type projectionCommandExecutor struct {
	replayed     contextgraph.ProjectionAudit
	acknowledged contextgraph.ProjectionAudit
	audit        contextgraph.ProjectionAudit
	actor        CommandActor
	reason       string
	calls        int
	effectCalls  int
	err          error
	effectErr    error
	after        func()
}

func (e *projectionCommandExecutor) PrepareProjectionAuditReplay(_ context.Context, audit contextgraph.ProjectionAudit, actor CommandActor) (ReplayPreparation, error) {
	e.calls++
	e.audit, e.actor = audit, actor
	if e.after != nil {
		e.after()
	}
	if e.err != nil {
		return ReplayPreparation{}, e.err
	}
	result := e.replayed
	if result.ID == "" {
		result = audit
		result.Status = "replayed"
	}
	return ReplayPreparation{Result: result, Effect: func(context.Context, contextgraph.WriterStore) error {
		e.effectCalls++
		return e.effectErr
	}}, nil
}

func (e *projectionCommandExecutor) AcknowledgeProjectionAudit(_ context.Context, audit contextgraph.ProjectionAudit, actor CommandActor, reason string) (contextgraph.ProjectionAudit, error) {
	e.calls++
	e.audit, e.actor, e.reason = audit, actor, reason
	if e.after != nil {
		e.after()
	}
	if e.err != nil {
		return contextgraph.ProjectionAudit{}, e.err
	}
	result := e.acknowledged
	if result.ID == "" {
		result = audit
		result.Status = "acknowledged"
	}
	return result, nil
}

func TestProjectionCommandServiceReplaysCompositeScopedAuditWithServerActor(t *testing.T) {
	auditOne := projectionCommandAudit("tenant-1", "shared-audit", "event-1")
	auditTwo := projectionCommandAudit("tenant-2", "shared-audit", "event-2")
	store := &projectionCommandStore{items: map[string]contextgraph.ProjectionAudit{
		"tenant-1/shared-audit": auditOne, "tenant-2/shared-audit": auditTwo,
	}}
	authorizer := &projectionCommandAuthorizer{}
	visibility := &projectionCommandVisibility{}
	executor := &projectionCommandExecutor{}
	service := newProjectionCommandTestService(t, authorizer, store, visibility, executor)

	result, err := service.Replay(context.Background(), projectionCommandPrincipalFixture(identity.ViewerKindUser), ReplayRequest{TenantID: "tenant-1", AuditID: "shared-audit", IdempotencyKey: "replay-1"})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if result.AuditID != "shared-audit" || result.Status != "replayed" || executor.audit.SourceEventID != "event-1" {
		t.Fatalf("result=%+v executor=%+v", result, executor)
	}
	if executor.actor != (CommandActor{Kind: identity.ViewerKindUser, ID: "user-1"}) {
		t.Fatalf("actor=%+v", executor.actor)
	}
	if len(store.calls) != 6 {
		t.Fatalf("store calls=%v", store.calls)
	}
	for _, call := range store.calls {
		if call != "tenant-1/shared-audit" {
			t.Fatalf("store calls=%v", store.calls)
		}
	}
	if authorizer.featureCalls != 6 || authorizer.objectCalls != 6 || len(visibility.purposes) != 6 || visibility.purposes[0] != "ontology.projection.replay" {
		t.Fatalf("authorizer=%+v purposes=%v", authorizer, visibility.purposes)
	}
	if len(store.completions) != 1 || store.effectCalls != 1 || executor.effectCalls != 1 || store.completions[0].LeaseToken != strings.Repeat("f", 64) || store.completions[0].History == nil || store.completions[0].History.TenantID != "tenant-1" ||
		store.completions[0].Fence.TenantID != "tenant-1" || store.completions[0].Fence.AuthorizationRevision != "authorization:1" || store.completions[0].Fence.PolicyRevision != "policy:1" {
		t.Fatalf("atomic completion=%+v", store.completions)
	}
}

func TestProjectionCommandServiceReauthorizesClaimBeforeReplaySideEffects(t *testing.T) {
	audit := projectionCommandAudit("tenant-1", "audit-1", "event-1")
	authorizer := &projectionCommandAuthorizer{}
	store := &projectionCommandStore{
		items:      map[string]contextgraph.ProjectionAudit{"tenant-1/audit-1": audit},
		afterClaim: func() { authorizer.deny = true },
	}
	executor := &projectionCommandExecutor{}
	service := newProjectionCommandTestService(t, authorizer, store, &projectionCommandVisibility{}, executor)
	_, err := service.Replay(context.Background(), projectionCommandPrincipalFixture(identity.ViewerKindUser), ReplayRequest{
		TenantID: "tenant-1", AuditID: "audit-1", IdempotencyKey: "replay-revoked-while-claiming",
	})
	if !errors.Is(err, ontologyscope.ErrAccessDenied) || executor.calls != 0 || len(store.completions) != 0 || len(store.failures) != 1 ||
		store.failures[0].FailureCode != "access_revoked" || store.failures[0].Result != nil || store.failures[0].History != nil {
		t.Fatalf("err=%v calls=%d completions=%d failures=%+v", err, executor.calls, len(store.completions), store.failures)
	}
}

func TestProjectionCommandServiceReauthorizesAfterReplayPreparationBeforeAtomicEffect(t *testing.T) {
	audit := projectionCommandAudit("tenant-1", "audit-1", "event-1")
	store := &projectionCommandStore{items: map[string]contextgraph.ProjectionAudit{"tenant-1/audit-1": audit}}
	authorizer := &projectionCommandAuthorizer{}
	executor := &projectionCommandExecutor{after: func() { authorizer.deny = true }}
	service := newProjectionCommandTestService(t, authorizer, store, &projectionCommandVisibility{}, executor)
	_, err := service.Replay(context.Background(), projectionCommandPrincipalFixture(identity.ViewerKindUser), ReplayRequest{
		TenantID: "tenant-1", AuditID: "audit-1", IdempotencyKey: "replay-revoked-after-effect",
	})
	if !errors.Is(err, ontologyscope.ErrAccessDenied) || executor.calls != 1 || executor.effectCalls != 0 || len(store.completions) != 0 || len(store.failures) != 1 ||
		store.failures[0].FailureCode != "access_revoked" || store.failures[0].Result != nil || store.failures[0].History != nil {
		t.Fatalf("err=%v calls=%d completions=%d failures=%+v", err, executor.calls, len(store.completions), store.failures)
	}
}

func TestProjectionCommandServiceDerivesAcknowledgeActors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		principal identity.Principal
		want      CommandActor
	}{
		{name: "agent", principal: projectionCommandPrincipalFixture(identity.ViewerKindAgent), want: CommandActor{Kind: identity.ViewerKindAgent, ID: "agent-1"}},
		{name: "system", principal: projectionCommandPrincipalFixture(identity.ViewerKindSystem), want: CommandActor{Kind: identity.ViewerKindSystem, ID: "system-job"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &projectionCommandStore{items: map[string]contextgraph.ProjectionAudit{"tenant-1/audit-1": projectionCommandAudit("tenant-1", "audit-1", "event-1")}}
			executor := &projectionCommandExecutor{}
			service := newProjectionCommandTestService(t, &projectionCommandAuthorizer{}, store, &projectionCommandVisibility{}, executor)
			if _, err := service.Acknowledge(context.Background(), tc.principal, AcknowledgeRequest{TenantID: "tenant-1", AuditID: "audit-1", Reason: " reviewed ", IdempotencyKey: "ack-1"}); err != nil {
				t.Fatalf("Acknowledge: %v", err)
			}
			if executor.actor != tc.want || executor.reason != "reviewed" {
				t.Fatalf("actor=%+v reason=%q", executor.actor, executor.reason)
			}
		})
	}
}

func TestProjectionCommandServiceFailsClosedForWrongTenantPermissionAndPolicy(t *testing.T) {
	t.Run("wrong tenant", func(t *testing.T) {
		store := &projectionCommandStore{items: map[string]contextgraph.ProjectionAudit{"tenant-2/shared": projectionCommandAudit("tenant-2", "shared", "event-2")}}
		executor := &projectionCommandExecutor{}
		service := newProjectionCommandTestService(t, &projectionCommandAuthorizer{}, store, &projectionCommandVisibility{}, executor)
		_, err := service.Replay(context.Background(), projectionCommandPrincipalFixture(identity.ViewerKindUser), ReplayRequest{TenantID: "tenant-1", AuditID: "shared", IdempotencyKey: "replay-wrong-tenant"})
		if !errors.Is(err, ontologycontract.ErrUnavailable) || executor.calls != 0 {
			t.Fatalf("err=%v calls=%d", err, executor.calls)
		}
	})
	t.Run("scope denied", func(t *testing.T) {
		store := &projectionCommandStore{items: map[string]contextgraph.ProjectionAudit{"tenant-1/audit-1": projectionCommandAudit("tenant-1", "audit-1", "event-1")}}
		executor := &projectionCommandExecutor{}
		service := newProjectionCommandTestService(t, &projectionCommandAuthorizer{deny: true}, store, &projectionCommandVisibility{}, executor)
		_, err := service.Replay(context.Background(), projectionCommandPrincipalFixture(identity.ViewerKindUser), ReplayRequest{TenantID: "tenant-1", AuditID: "audit-1", IdempotencyKey: "replay-denied"})
		if !errors.Is(err, ontologyscope.ErrAccessDenied) || len(store.calls) != 0 || executor.calls != 0 {
			t.Fatalf("err=%v store=%v calls=%d", err, store.calls, executor.calls)
		}
	})
	for _, tc := range []struct {
		name       string
		visibility *projectionCommandVisibility
	}{
		{name: "hidden", visibility: &projectionCommandVisibility{hidden: true}},
		{name: "masked", visibility: &projectionCommandVisibility{masked: "source_event_id"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &projectionCommandStore{items: map[string]contextgraph.ProjectionAudit{"tenant-1/audit-1": projectionCommandAudit("tenant-1", "audit-1", "event-1")}}
			executor := &projectionCommandExecutor{}
			service := newProjectionCommandTestService(t, &projectionCommandAuthorizer{}, store, tc.visibility, executor)
			_, err := service.Replay(context.Background(), projectionCommandPrincipalFixture(identity.ViewerKindUser), ReplayRequest{TenantID: "tenant-1", AuditID: "audit-1", IdempotencyKey: "replay-policy"})
			if !errors.Is(err, ontologycontract.ErrUnavailable) || executor.calls != 0 {
				t.Fatalf("err=%v calls=%d", err, executor.calls)
			}
		})
	}
}

func TestProjectionCommandServiceRejectsConcurrentAuditChange(t *testing.T) {
	first := projectionCommandAudit("tenant-1", "audit-1", "event-1")
	second := first
	second.Status = "acknowledged"
	store := &projectionCommandStore{items: map[string]contextgraph.ProjectionAudit{"tenant-1/audit-1": first}, second: &second}
	executor := &projectionCommandExecutor{}
	service := newProjectionCommandTestService(t, &projectionCommandAuthorizer{}, store, &projectionCommandVisibility{}, executor)
	_, err := service.Replay(context.Background(), projectionCommandPrincipalFixture(identity.ViewerKindUser), ReplayRequest{TenantID: "tenant-1", AuditID: "audit-1", IdempotencyKey: "replay-concurrent"})
	if !errors.Is(err, ontologycontract.ErrStateConflict) || executor.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, executor.calls)
	}
}

func TestProjectionCommandServiceReturnsPersistedIdempotentResultWithoutExecuting(t *testing.T) {
	audit := projectionCommandAudit("tenant-1", "audit-1", "event-1")
	at := time.Unix(20, 0).UTC()
	store := &projectionCommandStore{
		items: map[string]contextgraph.ProjectionAudit{"tenant-1/audit-1": audit},
		claim: contextgraph.ProjectionAuditCommandClaim{
			State: contextgraph.ProjectionAuditCommandSucceeded, ResultStatus: "replayed", ReasonCode: "missing_target", ObservedAt: &at,
		},
	}
	executor := &projectionCommandExecutor{}
	service := newProjectionCommandTestService(t, &projectionCommandAuthorizer{}, store, &projectionCommandVisibility{}, executor)
	result, err := service.Replay(context.Background(), projectionCommandPrincipalFixture(identity.ViewerKindUser), ReplayRequest{
		TenantID: "tenant-1", AuditID: "audit-1", IdempotencyKey: "replay-idempotent",
	})
	if err != nil || result.Status != "replayed" || result.ObservedAt == nil || !result.ObservedAt.Equal(at) || executor.calls != 0 || len(store.completions) != 0 {
		t.Fatalf("result=%+v err=%v executor=%d completions=%d", result, err, executor.calls, len(store.completions))
	}
}

func TestProjectionCommandServicePersistsFailureAndRequiresIdempotencyKey(t *testing.T) {
	audit := projectionCommandAudit("tenant-1", "audit-1", "event-1")
	store := &projectionCommandStore{items: map[string]contextgraph.ProjectionAudit{"tenant-1/audit-1": audit}}
	want := errors.New("replay dependency failed")
	executor := &projectionCommandExecutor{err: want}
	authorizer := &projectionCommandAuthorizer{}
	service := newProjectionCommandTestService(t, authorizer, store, &projectionCommandVisibility{}, executor)

	_, err := service.Replay(context.Background(), projectionCommandPrincipalFixture(identity.ViewerKindUser), ReplayRequest{
		TenantID: "tenant-1", AuditID: "audit-1", IdempotencyKey: "replay-failed",
	})
	if err == nil || executor.calls != 1 || len(store.failures) != 1 || store.failures[0].FailureCode != "dependency_failure" ||
		store.failures[0].LeaseToken != strings.Repeat("f", 64) || store.failures[0].Result == nil || store.failures[0].History == nil || store.failures[0].Fence == nil {
		t.Fatalf("err=%v calls=%d failures=%+v", err, executor.calls, store.failures)
	}
	featureCalls := authorizer.featureCalls
	_, err = service.Replay(context.Background(), projectionCommandPrincipalFixture(identity.ViewerKindUser), ReplayRequest{TenantID: "tenant-1", AuditID: "audit-1"})
	if !errors.Is(err, ontologycontract.ErrInvalidRequest) || authorizer.featureCalls != featureCalls || store.claimCalls != 1 {
		t.Fatalf("missing key err=%v feature calls=%d claim calls=%d", err, authorizer.featureCalls, store.claimCalls)
	}
}

func TestProjectionCommandServiceDoesNotPersistFailureAuditAfterAccessRevocation(t *testing.T) {
	audit := projectionCommandAudit("tenant-1", "audit-1", "event-1")
	store := &projectionCommandStore{items: map[string]contextgraph.ProjectionAudit{"tenant-1/audit-1": audit}}
	authorizer := &projectionCommandAuthorizer{}
	executor := &projectionCommandExecutor{err: errors.New("replay dependency failed"), after: func() { authorizer.deny = true }}
	service := newProjectionCommandTestService(t, authorizer, store, &projectionCommandVisibility{}, executor)

	_, err := service.Replay(context.Background(), projectionCommandPrincipalFixture(identity.ViewerKindUser), ReplayRequest{
		TenantID: "tenant-1", AuditID: "audit-1", IdempotencyKey: "replay-revoked-failure",
	})
	if err == nil || len(store.failures) != 1 || store.failures[0].FailureCode != "access_revoked" ||
		store.failures[0].Result != nil || store.failures[0].History != nil || store.failures[0].Fence != nil {
		t.Fatalf("err=%v failures=%+v", err, store.failures)
	}
}

func TestProjectionCommandServicePersistsRolledBackEffectFailure(t *testing.T) {
	audit := projectionCommandAudit("tenant-1", "audit-1", "event-1")
	store := &projectionCommandStore{items: map[string]contextgraph.ProjectionAudit{"tenant-1/audit-1": audit}}
	executor := &projectionCommandExecutor{effectErr: errors.New("effect failed")}
	service := newProjectionCommandTestService(t, &projectionCommandAuthorizer{}, store, &projectionCommandVisibility{}, executor)

	_, err := service.Replay(context.Background(), projectionCommandPrincipalFixture(identity.ViewerKindUser), ReplayRequest{
		TenantID: "tenant-1", AuditID: "audit-1", IdempotencyKey: "replay-effect-failed",
	})
	if !errors.Is(err, ontologyscope.ErrDependencyUnavailable) || executor.effectCalls != 1 || store.effectCalls != 1 ||
		len(store.completions) != 1 || len(store.failures) != 1 || store.failures[0].FailureCode != "dependency_failure" {
		t.Fatalf("err=%v executor effects=%d store effects=%d completions=%d failures=%+v", err, executor.effectCalls, store.effectCalls, len(store.completions), store.failures)
	}
}

func TestProjectionCommandServiceAbortsBeforeEffectWhenCommitFenceChanges(t *testing.T) {
	audit := projectionCommandAudit("tenant-1", "audit-1", "event-1")
	store := &projectionCommandStore{
		items:       map[string]contextgraph.ProjectionAudit{"tenant-1/audit-1": audit},
		completeErr: errors.Join(contextgraph.ErrProjectionAuditCommandConflict, contextgraph.ErrProjectionAuditCommandFence),
	}
	executor := &projectionCommandExecutor{}
	service := newProjectionCommandTestService(t, &projectionCommandAuthorizer{}, store, &projectionCommandVisibility{}, executor)

	_, err := service.Replay(context.Background(), projectionCommandPrincipalFixture(identity.ViewerKindUser), ReplayRequest{
		TenantID: "tenant-1", AuditID: "audit-1", IdempotencyKey: "replay-fence-changed",
	})
	if !errors.Is(err, ontologycontract.ErrStateConflict) || executor.effectCalls != 0 || store.effectCalls != 0 ||
		len(store.failures) != 1 || store.failures[0].FailureCode != "state_conflict" || store.failures[0].Result != nil || store.failures[0].History != nil {
		t.Fatalf("err=%v executor effects=%d store effects=%d failures=%+v", err, executor.effectCalls, store.effectCalls, store.failures)
	}
}

func TestProjectionCommandServiceFailsClosedWithoutTrustedAuthorizationRevision(t *testing.T) {
	audit := projectionCommandAudit("tenant-1", "audit-1", "event-1")
	store := &projectionCommandStore{items: map[string]contextgraph.ProjectionAudit{"tenant-1/audit-1": audit}}
	executor := &projectionCommandExecutor{}
	service := newProjectionCommandTestServiceWithSnapshots(t, &projectionCommandAuthorizer{}, store, &projectionCommandVisibility{}, executor, projectionCommandSnapshots{})

	_, err := service.Replay(context.Background(), projectionCommandPrincipalFixture(identity.ViewerKindUser), ReplayRequest{
		TenantID: "tenant-1", AuditID: "audit-1", IdempotencyKey: "replay-no-revision",
	})
	if !errors.Is(err, ontologyscope.ErrDependencyUnavailable) || store.claimCalls != 0 || executor.calls != 0 {
		t.Fatalf("err=%v claim calls=%d executor calls=%d", err, store.claimCalls, executor.calls)
	}
}

func newProjectionCommandTestService(t *testing.T, authorizer *projectionCommandAuthorizer, store *projectionCommandStore, visibility *projectionCommandVisibility, executor *projectionCommandExecutor) *CommandService {
	return newProjectionCommandTestServiceWithSnapshots(t, authorizer, store, visibility, executor, projectionCommandSnapshots{revision: "1"})
}

func newProjectionCommandTestServiceWithSnapshots(t *testing.T, authorizer *projectionCommandAuthorizer, store *projectionCommandStore, visibility *projectionCommandVisibility, executor *projectionCommandExecutor, snapshots projectionCommandSnapshots) *CommandService {
	t.Helper()
	binding := ontologyscope.ScopeBinding{
		TenantID: "tenant-1", Kind: ontologyscope.KindOrganization,
		SourceObject: identity.ObjectRef{Type: "organization", ID: "tenant-1"}, AuthorizationTenantID: "tenant-1",
		ReadAction: "organization.read", ManageAction: "organization.manage", BindingVersion: 1, State: ontologyscope.BindingStateActive,
	}
	gate := ontologyscope.NewScopeAccessGate(projectionCommandResolver{binding: binding}, authorizer, snapshots)
	service, err := NewCommandService(gate, store, visibility, executor)
	if err != nil {
		t.Fatalf("NewCommandService: %v", err)
	}
	return service
}

func projectionCommandAudit(tenantID, auditID, eventID string) contextgraph.ProjectionAudit {
	return contextgraph.ProjectionAudit{
		ID: auditID, TenantID: tenantID, SourceEventID: eventID, SourceEventType: "project.organization.linked",
		Status: "skipped", ReasonCode: "missing_target", SourceAccessRef: contextgraph.AccessRef{Kind: "organization", ID: tenantID, TenantID: tenantID},
		AccessState: contextgraph.AccessStateResolved, ObservedAt: time.Unix(10, 0).UTC(),
	}
}

func projectionCommandPrincipalFixture(kind string) identity.Principal {
	principal := identity.Principal{SubjectProvider: "test", ViewerKind: kind, CoarseScopes: []string{"operations.manage"}}
	switch kind {
	case identity.ViewerKindAgent:
		principal.Subject, principal.AgentProfileID = "agent-subject", "agent-1"
	case identity.ViewerKindSystem:
		principal.Subject = "system-job"
	default:
		principal.Subject, principal.UserID, principal.Username = "user-subject", "user-1", "user-one"
	}
	return identity.NormalizePrincipal(principal)
}
