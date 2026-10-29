package authorization

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/permissionscope"
	"orrery_v2/internal/app/rebac"
)

const (
	DecisionSourceScope = "coarse_scope"
	DecisionSourceReBAC = "rebac_relation"
)

var ErrDenied = errors.New("authorization denied")
var ErrInvalidRequest = errors.New("authorization request is invalid")
var ErrServiceDependency = errors.New("authorization service dependency is not configured")
var ErrUnsupported = errors.New("authorization operation is not supported by adapter")

type CheckRequest struct {
	Principal identity.Principal
	Action    string
	Object    identity.ObjectRef
	TenantID  string
	At        time.Time
	// CoarseScopeAlternatives can satisfy the coarse precheck; relation adapters still evaluate the object.
	CoarseScopeAlternatives []string
}

type Decision struct {
	Allowed bool
	Source  string
	Reason  string
}

type SubjectRef struct {
	Kind string
	ID   string
}

type RelationTuple struct {
	ID        string
	TenantID  string
	Subject   SubjectRef
	Relation  string
	Object    identity.ObjectRef
	Source    SubjectRef
	ValidFrom *time.Time
	ValidTo   *time.Time
	CreatedAt time.Time
}

type Adapter interface {
	Check(ctx context.Context, req CheckRequest) (Decision, error)
	BatchCheck(ctx context.Context, reqs []CheckRequest) ([]Decision, error)
	ListObjects(ctx context.Context, principal identity.Principal, relation string, objectType string, tenantID string) ([]identity.ObjectRef, error)
	WriteRelations(ctx context.Context, tuples []RelationTuple) error
	DeleteRelations(ctx context.Context, tuples []RelationTuple) error
}

type RelationChecker interface {
	CheckRelation(ctx context.Context, tuple RelationTuple) (bool, error)
}

type RelationLister interface {
	ListRelations(ctx context.Context) ([]RelationTuple, error)
}

type Service struct {
	adapter Adapter
}

func NewService(adapter Adapter) *Service {
	return &Service{adapter: adapter}
}

func (s *Service) Can(ctx context.Context, principal identity.Principal, action string, object identity.ObjectRef) error {
	decision, err := s.Check(ctx, CheckRequest{Principal: principal, Action: action, Object: object})
	if err != nil {
		return err
	}
	if !decision.Allowed {
		return fmt.Errorf("%w: %s", ErrDenied, decision.Reason)
	}
	return nil
}

func (s *Service) Check(ctx context.Context, req CheckRequest) (Decision, error) {
	if s == nil || s.adapter == nil {
		return Decision{}, ErrServiceDependency
	}
	return s.adapter.Check(ctx, req)
}

func (s *Service) BatchCan(ctx context.Context, reqs []CheckRequest) ([]Decision, error) {
	if s == nil || s.adapter == nil {
		return nil, ErrServiceDependency
	}
	return s.adapter.BatchCheck(ctx, reqs)
}

func (s *Service) ListObjects(ctx context.Context, principal identity.Principal, relation string, objectType string, tenantID string) ([]identity.ObjectRef, error) {
	if s == nil || s.adapter == nil {
		return nil, ErrServiceDependency
	}
	return s.adapter.ListObjects(ctx, principal, relation, objectType, tenantID)
}

func (s *Service) WriteRelations(ctx context.Context, tuples []RelationTuple) error {
	if s == nil || s.adapter == nil {
		return ErrServiceDependency
	}
	return s.adapter.WriteRelations(ctx, tuples)
}

func (s *Service) DeleteRelations(ctx context.Context, tuples []RelationTuple) error {
	if s == nil || s.adapter == nil {
		return ErrServiceDependency
	}
	return s.adapter.DeleteRelations(ctx, tuples)
}

func (s *Service) CheckRelation(ctx context.Context, tuple RelationTuple) (bool, error) {
	if s == nil || s.adapter == nil {
		return false, ErrServiceDependency
	}
	checker, ok := s.adapter.(RelationChecker)
	if !ok {
		return false, ErrUnsupported
	}
	return checker.CheckRelation(ctx, tuple)
}

func (s *Service) ListRelations(ctx context.Context) ([]RelationTuple, error) {
	if s == nil || s.adapter == nil {
		return nil, ErrServiceDependency
	}
	lister, ok := s.adapter.(RelationLister)
	if !ok {
		return nil, ErrUnsupported
	}
	return lister.ListRelations(ctx)
}

type ScopeAdapter struct {
	actionScopes map[string]string
}

func NewScopeAdapter(actionScopes map[string]string) *ScopeAdapter {
	return &ScopeAdapter{actionScopes: cloneActionScopes(actionScopes)}
}

func (a *ScopeAdapter) Check(ctx context.Context, req CheckRequest) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	req = normalizeCheckRequest(req)
	if err := validateScopeCheck(req); err != nil {
		return Decision{}, err
	}
	required := a.requiredScope(req.Action)
	if required == "" {
		return Decision{Allowed: true, Source: DecisionSourceScope, Reason: "action does not require a coarse scope"}, nil
	}
	if principalHasScope(req.Principal, required) {
		return Decision{Allowed: true, Source: DecisionSourceScope, Reason: "principal has scope " + required}, nil
	}
	for _, alternative := range req.CoarseScopeAlternatives {
		if principalHasScope(req.Principal, alternative) {
			return Decision{Allowed: true, Source: DecisionSourceScope, Reason: "principal has alternative scope " + alternative}, nil
		}
	}
	return Decision{Allowed: false, Source: DecisionSourceScope, Reason: "missing scope " + required}, nil
}

func (a *ScopeAdapter) BatchCheck(ctx context.Context, reqs []CheckRequest) ([]Decision, error) {
	out := make([]Decision, 0, len(reqs))
	for _, req := range reqs {
		decision, err := a.Check(ctx, req)
		if err != nil {
			return nil, err
		}
		out = append(out, decision)
	}
	return out, nil
}

func (a *ScopeAdapter) ListObjects(ctx context.Context, principal identity.Principal, relation string, objectType string, tenantID string) ([]identity.ObjectRef, error) {
	_ = ctx
	_ = principal
	_ = relation
	_ = objectType
	_ = tenantID
	return nil, ErrUnsupported
}

func (a *ScopeAdapter) WriteRelations(ctx context.Context, tuples []RelationTuple) error {
	_ = ctx
	_ = tuples
	return ErrUnsupported
}

func (a *ScopeAdapter) DeleteRelations(ctx context.Context, tuples []RelationTuple) error {
	_ = ctx
	_ = tuples
	return ErrUnsupported
}

func (a *ScopeAdapter) ListRelations(ctx context.Context) ([]RelationTuple, error) {
	_ = ctx
	return nil, ErrUnsupported
}

type ReBACAdapter struct {
	store  rebac.Store
	scopes *ScopeAdapter
}

type relationTupleLister interface {
	ListRelationTuples(context.Context) ([]rebac.RelationTuple, error)
}

type tenantRelationTupleLister interface {
	ListRelationTuplesByTenant(context.Context, string) ([]rebac.RelationTuple, error)
}

type tenantRelationTupleBatchLister interface {
	ListRelationTuplesByTenants(context.Context, []string) ([]rebac.RelationTuple, error)
}

type relationTupleCache struct {
	byTenant map[string][]rebac.RelationTuple
	all      []rebac.RelationTuple
	allRead  bool
}

func NewReBACAdapter(store rebac.Store, scopeAdapter *ScopeAdapter) *ReBACAdapter {
	if scopeAdapter == nil {
		scopeAdapter = NewScopeAdapter(nil)
	}
	return &ReBACAdapter{store: store, scopes: scopeAdapter}
}

func (a *ReBACAdapter) Check(ctx context.Context, req CheckRequest) (Decision, error) {
	return a.check(ctx, req, &relationTupleCache{})
}

func (a *ReBACAdapter) check(ctx context.Context, req CheckRequest, cache *relationTupleCache) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	if a == nil || a.store == nil {
		return Decision{}, ErrServiceDependency
	}
	req = normalizeCheckRequest(req)
	if err := validateScopeCheck(req); err != nil {
		return Decision{}, err
	}
	scopeAdapter := a.scopes
	if scopeAdapter == nil {
		scopeAdapter = NewScopeAdapter(nil)
	}
	scopeDecision, err := scopeAdapter.Check(ctx, req)
	if err != nil {
		return Decision{}, err
	}
	if !scopeDecision.Allowed {
		return scopeDecision, nil
	}
	mapping, ok := relationMappingForCheck(req.Action, req.Object.Type)
	if !ok {
		return Decision{Allowed: false, Source: DecisionSourceReBAC, Reason: "relation mapping is missing for action " + req.Action}, nil
	}
	if req.TenantID == "" || req.Object.Type == "" || req.Object.ID == "" {
		return Decision{}, fmt.Errorf("%w: tenant_id, object type and object id are required for relation check", ErrInvalidRequest)
	}
	subject := subjectRefFromPrincipalForObject(req.Principal, req.Object.Type)
	if subject.Kind == "" || subject.ID == "" {
		return Decision{}, fmt.Errorf("%w: principal subject is required for relation check", ErrInvalidRequest)
	}
	if tuples, supported, err := a.relationTuples(ctx, req.TenantID, cache); err != nil {
		return Decision{}, err
	} else if supported {
		allowed := newReBACRelationEvaluator(tuples, req.At).check(
			req.TenantID,
			rebac.Ref{Kind: subject.Kind, ID: subject.ID},
			mapping.Relation,
			rebac.Ref{Kind: req.Object.Type, ID: req.Object.ID},
		)
		if allowed {
			return Decision{Allowed: true, Source: DecisionSourceReBAC, Reason: "relation " + mapping.Relation + " allows action " + req.Action}, nil
		}
		return Decision{Allowed: false, Source: DecisionSourceReBAC, Reason: "missing relation " + mapping.Relation + " for action " + req.Action}, nil
	}
	for _, relation := range directRelationCandidates(req.Action, mapping.Relation) {
		allowed, _, err := a.store.CheckRelation(ctx, rebac.RelationCheck{
			TenantID: req.TenantID,
			Subject:  rebac.Ref{Kind: subject.Kind, ID: subject.ID},
			Relation: relation,
			Object:   rebac.Ref{Kind: req.Object.Type, ID: req.Object.ID},
			At:       req.At,
		})
		if err != nil {
			return Decision{}, err
		}
		if allowed {
			return Decision{Allowed: true, Source: DecisionSourceReBAC, Reason: "relation " + relation + " allows action " + req.Action}, nil
		}
	}
	return Decision{Allowed: false, Source: DecisionSourceReBAC, Reason: "missing relation " + mapping.Relation + " for action " + req.Action}, nil
}

func (a *ReBACAdapter) BatchCheck(ctx context.Context, reqs []CheckRequest) ([]Decision, error) {
	out := make([]Decision, 0, len(reqs))
	cache := &relationTupleCache{}
	if lister, ok := a.store.(tenantRelationTupleBatchLister); ok {
		tenantIDs := make([]string, 0, len(reqs))
		seen := make(map[string]struct{}, len(reqs))
		for _, raw := range reqs {
			req := normalizeCheckRequest(raw)
			if req.TenantID == "" {
				continue
			}
			if _, exists := seen[req.TenantID]; exists {
				continue
			}
			seen[req.TenantID] = struct{}{}
			tenantIDs = append(tenantIDs, req.TenantID)
		}
		tuples, err := lister.ListRelationTuplesByTenants(ctx, tenantIDs)
		if err != nil {
			return nil, err
		}
		cache.byTenant = make(map[string][]rebac.RelationTuple, len(tenantIDs))
		for _, tenantID := range tenantIDs {
			cache.byTenant[tenantID] = []rebac.RelationTuple{}
		}
		for _, tuple := range tuples {
			if _, requested := cache.byTenant[tuple.TenantID]; !requested {
				return nil, ErrServiceDependency
			}
			cache.byTenant[tuple.TenantID] = append(cache.byTenant[tuple.TenantID], tuple)
		}
	}
	for _, req := range reqs {
		decision, err := a.check(ctx, req, cache)
		if err != nil {
			return nil, err
		}
		out = append(out, decision)
	}
	return out, nil
}

func (a *ReBACAdapter) relationTuples(ctx context.Context, tenantID string, cache *relationTupleCache) ([]rebac.RelationTuple, bool, error) {
	if lister, ok := a.store.(tenantRelationTupleLister); ok {
		if cache != nil && cache.byTenant != nil {
			if tuples, found := cache.byTenant[tenantID]; found {
				return tuples, true, nil
			}
		}
		tuples, err := lister.ListRelationTuplesByTenant(ctx, tenantID)
		if err != nil {
			return nil, true, err
		}
		if cache != nil {
			if cache.byTenant == nil {
				cache.byTenant = make(map[string][]rebac.RelationTuple)
			}
			cache.byTenant[tenantID] = tuples
		}
		return tuples, true, nil
	}
	lister, ok := a.store.(relationTupleLister)
	if !ok {
		return nil, false, nil
	}
	if cache != nil && cache.allRead {
		return cache.all, true, nil
	}
	tuples, err := lister.ListRelationTuples(ctx)
	if err != nil {
		return nil, true, err
	}
	if cache != nil {
		cache.all = tuples
		cache.allRead = true
	}
	return tuples, true, nil
}

func (a *ReBACAdapter) ListObjects(ctx context.Context, principal identity.Principal, relation string, objectType string, tenantID string) ([]identity.ObjectRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a == nil || a.store == nil {
		return nil, ErrServiceDependency
	}
	principal = identity.NormalizePrincipal(principal)
	if !principal.Authenticated() {
		return nil, fmt.Errorf("%w: authenticated principal is required", ErrInvalidRequest)
	}
	relation = normalizeRelation(relation)
	objectType = normalizeKind(objectType)
	tenantID = strings.TrimSpace(tenantID)
	subject := subjectRefFromPrincipalForObject(principal, objectType)
	if subject.Kind == "" || subject.ID == "" || relation == "" || objectType == "" || tenantID == "" {
		return nil, fmt.Errorf("%w: subject, relation, object type and tenant_id are required for rebac list objects", ErrInvalidRequest)
	}
	tuples, supported, err := a.relationTuples(ctx, tenantID, &relationTupleCache{})
	if err != nil {
		return nil, err
	}
	if !supported {
		return nil, ErrUnsupported
	}

	candidates := map[string]struct{}{}
	for _, tuple := range tuples {
		if strings.TrimSpace(tuple.TenantID) != tenantID {
			continue
		}
		object := normalizeReBACRef(tuple.Object)
		if object.Kind == objectType && object.ID != "" {
			candidates[object.ID] = struct{}{}
		}
	}
	ids := make([]string, 0, len(candidates))
	for id := range candidates {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	evaluator := newReBACRelationEvaluator(tuples, time.Time{})
	objects := make([]identity.ObjectRef, 0, len(ids))
	for _, id := range ids {
		if evaluator.check(
			tenantID,
			rebac.Ref{Kind: subject.Kind, ID: subject.ID},
			relation,
			rebac.Ref{Kind: objectType, ID: id},
		) {
			objects = append(objects, identity.ObjectRef{Type: objectType, ID: id, TenantID: tenantID})
		}
	}
	return objects, nil
}

func (a *ReBACAdapter) WriteRelations(ctx context.Context, tuples []RelationTuple) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a == nil || a.store == nil {
		return ErrServiceDependency
	}
	now := time.Now().UTC()
	for _, tuple := range tuples {
		rebacTuple, err := relationTupleToReBAC(tuple, now)
		if err != nil {
			return err
		}
		if err := a.store.SaveRelationTuple(ctx, rebacTuple); err != nil {
			return err
		}
	}
	return nil
}

func (a *ReBACAdapter) DeleteRelations(ctx context.Context, tuples []RelationTuple) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deleter, ok := a.store.(interface {
		DeleteRelationTuple(context.Context, rebac.RelationTuple) error
	})
	if !ok {
		return ErrUnsupported
	}
	for _, tuple := range tuples {
		rebacTuple, err := relationTupleToReBAC(tuple, time.Now().UTC())
		if err != nil {
			return err
		}
		if err := deleter.DeleteRelationTuple(ctx, rebacTuple); err != nil {
			return err
		}
	}
	return nil
}

func (a *ReBACAdapter) ListRelations(ctx context.Context) ([]RelationTuple, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lister, ok := a.store.(interface {
		ListRelationTuples(context.Context) ([]rebac.RelationTuple, error)
	})
	if !ok {
		return nil, ErrUnsupported
	}
	tuples, err := lister.ListRelationTuples(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]RelationTuple, 0, len(tuples))
	for _, tuple := range tuples {
		out = append(out, normalizeRelationTuple(RelationTuple{
			ID: tuple.ID, TenantID: tuple.TenantID,
			Subject:   SubjectRef{Kind: tuple.Subject.Kind, ID: tuple.Subject.ID},
			Relation:  tuple.Relation,
			Object:    identity.ObjectRef{Type: tuple.Object.Kind, ID: tuple.Object.ID, TenantID: tuple.TenantID},
			Source:    SubjectRef{Kind: tuple.Source.Kind, ID: tuple.Source.ID},
			ValidFrom: tuple.ValidFrom, ValidTo: tuple.ValidTo, CreatedAt: tuple.CreatedAt,
		}))
	}
	return out, nil
}

func directRelationCandidates(action string, mapped string) []string {
	candidates := []string{normalizeRelation(mapped)}
	switch normalizeAction(action) {
	case "mission.view":
		candidates = append(candidates, "owner", "participant")
	case "mission.monitor":
		candidates = append(candidates, "development_monitor")
	case "mission.edit", "mission.manage":
		candidates = append(candidates, "owner")
	case "mission.qa_approve":
		candidates = append(candidates, "owner")
	case "project.view":
		candidates = append(candidates, "owner", "participant")
	case "project.edit", "project.manage":
		candidates = append(candidates, "owner")
	case "development_project.view":
		candidates = append(candidates, "viewer", "contributor", "maintainer", "owner")
	case "development_project.monitor":
		candidates = append(candidates, "maintainer", "owner")
	case "development_project.edit":
		candidates = append(candidates, "contributor", "maintainer", "owner")
	case "development_project.configure":
		candidates = append(candidates, "maintainer", "owner")
	case "development_project.run", "development_project.submit":
		candidates = append(candidates, "contributor", "maintainer", "owner", "executor")
	case "development_project.approve":
		candidates = append(candidates, "maintainer", "owner")
	case "development_project.manage":
		candidates = append(candidates, "owner")
	case "development_project.merge":
		candidates = append(candidates, "merger", "owner")
	case "scm_connection.view", "scm_connection.use":
		candidates = append(candidates, "owner")
	case "scm_connection.manage":
		candidates = append(candidates, "owner")
	case "todo.view":
		candidates = append(candidates, "proposer", "assignee", "reviewer", "collaborator")
	case "todo.edit":
		candidates = append(candidates, "assignee", "reviewer")
	}
	out := candidates[:0]
	seen := map[string]struct{}{}
	for _, relation := range candidates {
		relation = normalizeRelation(relation)
		if relation == "" {
			continue
		}
		if _, ok := seen[relation]; ok {
			continue
		}
		seen[relation] = struct{}{}
		out = append(out, relation)
	}
	return out
}

type relationMapping struct {
	Scope    string
	Relation string
}

var defaultActionScopes = map[string]string{
	"mission.view":                  "mission.read",
	"mission.monitor":               "development.monitor",
	"mission.edit":                  "mission.write",
	"mission.manage":                "mission.write",
	"mission.qa_approve":            "approvals.decide",
	"project.view":                  "project.read",
	"project.edit":                  "project.write",
	"project.manage":                "project.manage",
	"project.create":                "project.create",
	"development_project.view":      "development.read",
	"development_project.monitor":   "development.monitor",
	"development_project.create":    "development.create",
	"development_project.edit":      "development.write",
	"development_project.configure": "development.manage",
	"development_project.run":       "development.execute",
	"development_project.submit":    "development.submit",
	"development_project.approve":   "development.approve",
	"development_project.manage":    "development.manage",
	"development_project.merge":     "development.merge",
	"scm_connection.view":           "connection.read",
	"scm_connection.use":            "connection.use",
	"scm_connection.manage":         "connection.manage",
	"scm_connection.create":         "connection.manage",
	"todo.view":                     "todo.read",
	"todo.edit":                     "todo.write",
	"discussion.read":               "discussion.read",
	"discussion.write":              "discussion.write",
	"discussion.manage":             "discussion.write",
	"resource.view":                 "resource.read",
	"resource.use":                  "tool.call",
	"resource.manage":               "resource.manage",
	"organization.read":             "org.read",
	"organization.manage":           "org.manage",
	"department.read":               "org.read",
	"department.manage":             "org.manage",
	"workforce.member.read":         "workforce.read",
	"workforce.member.manage":       "workforce.manage",
	"gateway.delivery.send":         "gateway.delivery.send",
	"role.read":                     "roles.read",
	"role.manage":                   "roles.manage",
	"role.assignment.read":          "roles.read",
}

var defaultRelationMappings = map[string]relationMapping{
	"mission.view":                  {Scope: "mission.read", Relation: "viewer"},
	"mission.monitor":               {Scope: "development.monitor", Relation: "development_monitor"},
	"mission.edit":                  {Scope: "mission.write", Relation: "editor"},
	"mission.manage":                {Scope: "mission.write", Relation: "manager"},
	"mission.qa_approve":            {Scope: "approvals.decide", Relation: "qa_approver"},
	"project.view":                  {Scope: "project.read", Relation: "viewer"},
	"project.edit":                  {Scope: "project.write", Relation: "editor"},
	"project.manage":                {Scope: "project.manage", Relation: "editor"},
	"project.create":                {Scope: "project.create", Relation: "admin"},
	"development_project.view":      {Scope: "development.read", Relation: "reader"},
	"development_project.monitor":   {Scope: "development.monitor", Relation: "monitor"},
	"development_project.create":    {Scope: "development.create", Relation: "viewer"},
	"development_project.edit":      {Scope: "development.write", Relation: "writer"},
	"development_project.configure": {Scope: "development.manage", Relation: "configurer"},
	"development_project.run":       {Scope: "development.execute", Relation: "runner"},
	"development_project.submit":    {Scope: "development.submit", Relation: "submitter"},
	"development_project.approve":   {Scope: "development.approve", Relation: "approver"},
	"development_project.manage":    {Scope: "development.manage", Relation: "manager"},
	"development_project.merge":     {Scope: "development.merge", Relation: "merge_authorizer"},
	"scm_connection.view":           {Scope: "connection.read", Relation: "user"},
	"scm_connection.use":            {Scope: "connection.use", Relation: "user"},
	"scm_connection.manage":         {Scope: "connection.manage", Relation: "manager"},
	"scm_connection.create":         {Scope: "connection.manage", Relation: "admin"},
	"todo.view":                     {Scope: "todo.read", Relation: "reader"},
	"todo.edit":                     {Scope: "todo.write", Relation: "editor"},
	"discussion.read":               {Scope: "discussion.read", Relation: "viewer"},
	"discussion.write":              {Scope: "discussion.write", Relation: "poster"},
	"discussion.manage":             {Scope: "discussion.write", Relation: "manager"},
	"resource.view":                 {Scope: "resource.read", Relation: "viewer"},
	"resource.use":                  {Scope: "tool.call", Relation: "operator"},
	"resource.manage":               {Scope: "resource.manage", Relation: "operator"},
	"organization.read":             {Scope: "org.read", Relation: "viewer"},
	"organization.manage":           {Scope: "org.manage", Relation: "admin"},
	"department.read":               {Scope: "org.read", Relation: "viewer"},
	"department.manage":             {Scope: "org.manage", Relation: "editor"},
	"workforce.member.read":         {Scope: "workforce.read", Relation: "viewer"},
	"workforce.member.manage":       {Scope: "workforce.manage", Relation: "editor"},
	"gateway.delivery.send":         {Scope: "gateway.delivery.send", Relation: "viewer"},
	"role.read":                     {Scope: "roles.read", Relation: "viewer"},
	"role.manage":                   {Scope: "roles.manage", Relation: "admin"},
	"role.assignment.read":          {Scope: "roles.read", Relation: "viewer"},
}

func normalizeCheckRequest(req CheckRequest) CheckRequest {
	req.Principal = identity.NormalizePrincipal(req.Principal)
	req.Action = normalizeAction(req.Action)
	req.CoarseScopeAlternatives = normalizeScopes(req.CoarseScopeAlternatives)
	req.Object.Type = normalizeKind(req.Object.Type)
	req.Object.ID = strings.TrimSpace(req.Object.ID)
	req.Object.TenantID = strings.TrimSpace(req.Object.TenantID)
	req.TenantID = firstNonEmpty(req.TenantID, req.Object.TenantID, req.Principal.TenantID)
	req.Object.TenantID = firstNonEmpty(req.Object.TenantID, req.TenantID)
	if !req.At.IsZero() {
		req.At = req.At.UTC()
	}
	return req
}

func normalizeScopes(scopes []string) []string {
	if len(scopes) == 0 {
		return nil
	}
	out := make([]string, 0, len(scopes))
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		scope = normalizeScope(scope)
		if scope == "" {
			continue
		}
		if _, exists := seen[scope]; exists {
			continue
		}
		seen[scope] = struct{}{}
		out = append(out, scope)
	}
	return out
}

func validateScopeCheck(req CheckRequest) error {
	if !req.Principal.Authenticated() {
		return fmt.Errorf("%w: authenticated principal is required", ErrInvalidRequest)
	}
	if req.Action == "" {
		return fmt.Errorf("%w: action is required", ErrInvalidRequest)
	}
	return nil
}

func (a *ScopeAdapter) requiredScope(action string) string {
	action = normalizeAction(action)
	if a != nil {
		if scope := normalizeScope(a.actionScopes[action]); scope != "" {
			return scope
		}
	}
	if scope := normalizeScope(defaultActionScopes[action]); scope != "" {
		return scope
	}
	return normalizeScope(action)
}

func principalHasScope(principal identity.Principal, required string) bool {
	required = normalizeScope(required)
	for _, scope := range principal.CoarseScopes {
		if scopeAllows(normalizeScope(scope), required) {
			return true
		}
	}
	return false
}

func scopeAllows(granted, required string) bool {
	return permissionscope.Allows(granted, required)
}

func relationMappingForAction(action string) (relationMapping, bool) {
	action = normalizeAction(action)
	mapping, ok := defaultRelationMappings[action]
	if !ok {
		return relationMapping{}, false
	}
	mapping.Scope = normalizeScope(mapping.Scope)
	mapping.Relation = normalizeRelation(mapping.Relation)
	return mapping, mapping.Relation != ""
}

func relationMappingForCheck(action string, objectType string) (relationMapping, bool) {
	action = normalizeAction(action)
	objectType = normalizeKind(objectType)
	if objectType == "platform" {
		switch action {
		case "project.create", "development_project.create", "scm_connection.create":
			return relationMapping{Scope: defaultActionScopes[action], Relation: "member"}, true
		}
	}
	if mapping, ok := relationMappingForAction(action); ok {
		return mapping, true
	}
	if objectType != "platform" {
		return relationMapping{}, false
	}
	if action == "" {
		return relationMapping{}, false
	}
	return relationMapping{Scope: normalizeScope(action), Relation: "member"}, true
}

func relationTupleToReBAC(tuple RelationTuple, now time.Time) (rebac.RelationTuple, error) {
	tuple = normalizeRelationTuple(tuple)
	if tuple.TenantID == "" || tuple.Subject.Kind == "" || tuple.Subject.ID == "" || tuple.Relation == "" || tuple.Object.Type == "" || tuple.Object.ID == "" {
		return rebac.RelationTuple{}, fmt.Errorf("%w: tenant_id, subject, relation and object are required", ErrInvalidRequest)
	}
	if tuple.ID == "" {
		tuple.ID = tuple.TenantID + ":" + tuple.Subject.Kind + ":" + tuple.Subject.ID + ":" + tuple.Relation + ":" + tuple.Object.Type + ":" + tuple.Object.ID
	}
	if tuple.CreatedAt.IsZero() {
		tuple.CreatedAt = now
	}
	return rebac.RelationTuple{
		ID:        tuple.ID,
		TenantID:  tuple.TenantID,
		Subject:   rebac.Ref{Kind: tuple.Subject.Kind, ID: tuple.Subject.ID},
		Relation:  tuple.Relation,
		Object:    rebac.Ref{Kind: tuple.Object.Type, ID: tuple.Object.ID},
		Source:    rebac.Ref{Kind: tuple.Source.Kind, ID: tuple.Source.ID},
		ValidFrom: tuple.ValidFrom,
		ValidTo:   tuple.ValidTo,
		CreatedAt: tuple.CreatedAt,
	}, nil
}

func normalizeRelationTuple(tuple RelationTuple) RelationTuple {
	tuple.ID = strings.TrimSpace(tuple.ID)
	tuple.TenantID = strings.TrimSpace(tuple.TenantID)
	tuple.Subject.Kind = normalizeSubjectKind(tuple.Subject.Kind)
	tuple.Subject.ID = strings.TrimSpace(tuple.Subject.ID)
	tuple.Relation = normalizeRelation(tuple.Relation)
	tuple.Object.Type = normalizeKind(tuple.Object.Type)
	tuple.Object.ID = strings.TrimSpace(tuple.Object.ID)
	tuple.Object.TenantID = firstNonEmpty(tuple.Object.TenantID, tuple.TenantID)
	tuple.Source.Kind = normalizeKind(tuple.Source.Kind)
	tuple.Source.ID = strings.TrimSpace(tuple.Source.ID)
	if tuple.ValidFrom != nil {
		validFrom := tuple.ValidFrom.UTC()
		tuple.ValidFrom = &validFrom
	}
	if tuple.ValidTo != nil {
		validTo := tuple.ValidTo.UTC()
		tuple.ValidTo = &validTo
	}
	if !tuple.CreatedAt.IsZero() {
		tuple.CreatedAt = tuple.CreatedAt.UTC()
	}
	return tuple
}

func subjectRefFromPrincipal(principal identity.Principal) SubjectRef {
	principal = identity.NormalizePrincipal(principal)
	kind := normalizeSubjectKind(principal.ViewerKind)
	if kind == "" {
		kind = identity.ViewerKindUser
	}
	return SubjectRef{
		Kind: kind,
		ID:   firstNonEmpty(principal.UserID, principal.Username, principal.Subject),
	}
}

func subjectRefFromPrincipalForObject(principal identity.Principal, objectType string) SubjectRef {
	principal = identity.NormalizePrincipal(principal)
	if principal.AgentProfileID != "" && normalizeSubjectKind(principal.ViewerKind) == identity.ViewerKindUser {
		return SubjectRef{Kind: identity.ViewerKindUser, ID: principal.AgentProfileID}
	}
	return subjectRefFromPrincipal(principal)
}

func cloneActionScopes(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for action, scope := range in {
		action = normalizeAction(action)
		scope = normalizeScope(scope)
		if action == "" || scope == "" {
			continue
		}
		out[action] = scope
	}
	return out
}

func normalizeAction(value string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "orrery:")
}

func normalizeScope(value string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "orrery:")
}

func normalizeKind(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeSubjectKind(value string) string {
	switch normalizeKind(value) {
	case "human", identity.ViewerKindUser:
		return identity.ViewerKindUser
	case "ai", identity.ViewerKindAgent:
		return identity.ViewerKindAgent
	default:
		return normalizeKind(value)
	}
}

func normalizeRelation(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

var _ Adapter = (*ScopeAdapter)(nil)
var _ Adapter = (*ReBACAdapter)(nil)
