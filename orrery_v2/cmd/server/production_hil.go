package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
)

type hilContextGraph interface {
	BuildContextBundle(context.Context, contextgraph.BuildContextBundleRequest) (contextgraph.ContextBundle, error)
}

// productionHILResolutionObservers lets the composition root attach domain
// consumers after the shared HIL service has been constructed. HIL commits
// remain authoritative; observers are notification-only and each consumer
// validates the payload it owns before applying a business transition.
type productionHILResolutionObservers struct {
	mu        sync.RWMutex
	observers []hil.ResolutionObserver
}

type productionAsyncHILResolutionObserver struct {
	observer hil.ResolutionObserver
	report   func(any)
}

func newProductionAsyncHILResolutionObserver(observer hil.ResolutionObserver, report func(any)) *productionAsyncHILResolutionObserver {
	return &productionAsyncHILResolutionObserver{observer: observer, report: report}
}

func (o *productionAsyncHILResolutionObserver) DecisionResolved(ctx context.Context, resolution hil.DecisionResolution) {
	if o == nil || o.observer == nil {
		return
	}
	detached := context.WithoutCancel(ctx)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil && o.report != nil {
				o.report(recovered)
			}
		}()
		o.observer.DecisionResolved(detached, resolution)
	}()
}

func newProductionHILResolutionObservers(observers ...hil.ResolutionObserver) *productionHILResolutionObservers {
	result := &productionHILResolutionObservers{}
	result.Add(observers...)
	return result
}

func (o *productionHILResolutionObservers) Add(observers ...hil.ResolutionObserver) {
	if o == nil || len(observers) == 0 {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, observer := range observers {
		if observer != nil {
			o.observers = append(o.observers, observer)
		}
	}
}

func (o *productionHILResolutionObservers) DecisionResolved(ctx context.Context, resolution hil.DecisionResolution) {
	if o == nil {
		return
	}
	o.mu.RLock()
	observers := append([]hil.ResolutionObserver(nil), o.observers...)
	o.mu.RUnlock()
	for _, observer := range observers {
		if observer != nil {
			observer.DecisionResolved(ctx, resolution)
		}
	}
}

type productionHILContextProvider struct {
	graph   hilContextGraph
	timeout time.Duration
}

const (
	hilContextCapacityFallbackBundleID   = "context-unavailable:capacity"
	hilContextManualGateFallbackBundleID = "context-unavailable:manual-gate"
	marketingWechatDraftApprovalTool     = "mcp_marketing_marketing_wechat_draft_create"
)

const productionHILContextTimeout = 5 * time.Second

func (p productionHILContextProvider) BuildDecisionContext(ctx context.Context, request hil.DecisionContextRequest) (hil.OntologyContextSnapshot, error) {
	if isForcedManualMarketingWechatDraft(request) {
		// This action is always HIL-gated. Avoid an expensive global graph scan
		// before creating the durable approval the user must resolve.
		return hil.OntologyContextSnapshot{BundleID: hilContextManualGateFallbackBundleID}, nil
	}
	if p.graph == nil {
		return hil.OntologyContextSnapshot{}, fmt.Errorf("context graph is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := p.timeout
	if timeout <= 0 {
		timeout = productionHILContextTimeout
	}
	buildCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	subjects := make([]contextgraph.Ref, 0, len(request.Subjects))
	for _, subject := range request.Subjects {
		subjects = append(subjects, contextgraph.Ref{Kind: subject.Kind, ID: subject.ID})
	}
	bundle, err := p.graph.BuildContextBundle(buildCtx, contextgraph.BuildContextBundleRequest{
		TenantID:    request.TenantID,
		Consumer:    "orrery-hil",
		Purpose:     "decision-control",
		ActorRef:    contextgraph.Ref{Kind: string(request.Actor.Kind), ID: request.Actor.ID},
		SubjectRefs: subjects,
		Query:       request.Query,
		MaxDepth:    2,
		TokenBudget: 4096,
	})
	if err != nil {
		if isHILContextCapacityError(err) {
			// A missing graph snapshot must remain a manual decision, not prevent
			// the runtime from surfacing the approval that protects the action.
			return hil.OntologyContextSnapshot{BundleID: hilContextCapacityFallbackBundleID}, nil
		}
		return hil.OntologyContextSnapshot{}, err
	}
	return hil.OntologyContextSnapshot{
		BundleID:                bundle.BundleID,
		SchemaHash:              bundle.Schema.VersionHash,
		Confidence:              bundle.Confidence,
		Complete:                contextBundleHasSubjects(bundle, subjects) && !bundle.Partial,
		EvidenceRefs:            hilEvidenceRefs(bundle),
		Conflict:                hilBundleHasConflict(bundle),
		RecommendedParticipants: hilParticipants(bundle.Objects),
	}, nil
}

func isForcedManualMarketingWechatDraft(request hil.DecisionContextRequest) bool {
	action := strings.TrimSpace(request.Action)
	actionTypeID := strings.TrimSpace(request.ActionTypeID)
	if strings.EqualFold(action, hil.MarketingWechatDraftApprovalActionType) &&
		strings.EqualFold(actionTypeID, hil.MarketingWechatDraftApprovalActionType) {
		return true
	}
	toolName, _ := request.PayloadPreview["approval_tool_name"].(string)
	if !strings.EqualFold(strings.TrimSpace(toolName), marketingWechatDraftApprovalTool) {
		return false
	}
	canonicalAction := string(actioncontract.ActionCommunicationWechatDraftCreate)
	return strings.EqualFold(action, "runtime.approval") && strings.EqualFold(actionTypeID, "tool_use") ||
		strings.EqualFold(action, canonicalAction) && strings.EqualFold(actionTypeID, canonicalAction)
}

func isHILContextCapacityError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no space left on device") || strings.Contains(message, "sqlstate 53100")
}

func newProductionHILService(ctx context.Context, core *db.Store, rt *platform.Runtime, profiles productionHILProfileDirectory, installDefaults bool, observer hil.ResolutionObserver) (*hil.Service, *hil.WorkflowService, *hil.PlatformRepository, error) {
	if core == nil || rt == nil || profiles == nil {
		return nil, nil, nil, fmt.Errorf("production HIL dependencies are required")
	}
	repository := hil.NewPlatformRepository(core)
	workflow := hil.NewWorkflowService(hil.WorkflowServiceOptions{
		Repository:          repository,
		Assignees:           productionHILAssigneeResolver{profiles: profiles},
		IDs:                 rt.IDGenerator,
		Clock:               rt.Clock.Now,
		NotificationChannel: "feishu",
	})
	if installDefaults {
		if err := ensureProductionApprovalWorkflows(ctx, workflow); err != nil {
			return nil, nil, nil, err
		}
	}
	service := hil.NewService(
		repository,
		productionHILContextProvider{graph: newProductionContextGraphService(core)},
		rt.IDGenerator,
		hil.WithClock(rt.Clock.Now),
		hil.WithResolutionObserver(observer),
		hil.WithWorkflowService(workflow),
	)
	return service, workflow, repository, nil
}

func contextBundleHasSubjects(bundle contextgraph.ContextBundle, subjects []contextgraph.Ref) bool {
	if len(subjects) == 0 {
		return true
	}
	visible := make(map[string]struct{}, len(bundle.Objects))
	for _, object := range bundle.Objects {
		visible[strings.ToLower(strings.TrimSpace(object.ObjectType))+":"+strings.TrimSpace(object.ID)] = struct{}{}
	}
	for _, subject := range subjects {
		key := strings.ToLower(strings.TrimSpace(subject.Kind)) + ":" + strings.TrimSpace(subject.ID)
		if _, ok := visible[key]; !ok {
			return false
		}
	}
	return true
}

func hilParticipants(objects []contextgraph.Object) []hil.CouncilParticipant {
	participants := make([]hil.CouncilParticipant, 0)
	seen := map[string]struct{}{}
	for _, object := range objects {
		kind := strings.ToLower(strings.TrimSpace(object.ObjectType))
		if kind != "person" && kind != "agent" || !hilObjectActive(object) {
			continue
		}
		actorKind := hil.ActorAgent
		if kind == "person" {
			actorKind = hil.ActorHuman
		}
		actorID := trimOntologyActorPrefix(object.ID, kind)
		key := string(actorKind) + ":" + actorID
		if actorID == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		participants = append(participants, hil.CouncilParticipant{
			Actor:       hil.ActorRef{Kind: actorKind, ID: actorID},
			DisplayName: strings.TrimSpace(object.Label),
			Role:        hilObjectRole(object),
		})
	}
	sort.Slice(participants, func(i, j int) bool {
		left := string(participants[i].Actor.Kind) + ":" + participants[i].Actor.ID
		right := string(participants[j].Actor.Kind) + ":" + participants[j].Actor.ID
		return left < right
	})
	for index := range participants {
		if participants[index].Actor.Kind == hil.ActorHuman {
			participants[index].Required = true
			break
		}
	}
	return participants
}

func hilObjectActive(object contextgraph.Object) bool {
	status := strings.ToLower(strings.TrimSpace(object.Status))
	if status == "" && object.Properties != nil {
		status = strings.ToLower(strings.TrimSpace(fmt.Sprint(object.Properties["status"])))
	}
	return status != "inactive" && status != "disabled" && status != "archived"
}

func hilObjectRole(object contextgraph.Object) string {
	if object.Properties == nil {
		return ""
	}
	for _, key := range []string{"role", "role_id", "runtime_kind"} {
		if value := strings.TrimSpace(fmt.Sprint(object.Properties[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func trimOntologyActorPrefix(id, kind string) string {
	id = strings.TrimSpace(id)
	prefix := strings.ToLower(strings.TrimSpace(kind)) + ":"
	if strings.HasPrefix(strings.ToLower(id), prefix) {
		return strings.TrimSpace(id[len(prefix):])
	}
	return id
}

func hilBundleHasConflict(bundle contextgraph.ContextBundle) bool {
	for _, fact := range bundle.Facts {
		if hilConflictSignal(fact.Predicate, fact.Value) {
			return true
		}
	}
	for _, object := range bundle.Objects {
		for _, key := range []string{"decision_conflict", "requires_council", "council_required"} {
			if hilTruthy(object.Properties[key]) {
				return true
			}
		}
	}
	return false
}

func hilConflictSignal(predicate string, value any) bool {
	switch strings.ToLower(strings.TrimSpace(predicate)) {
	case "decision_conflict", "requires_council", "council_required":
		return hilTruthy(value)
	default:
		return false
	}
}

func hilTruthy(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "yes", "1", "required", "conflict":
			return true
		}
	}
	return false
}

func hilEvidenceRefs(bundle contextgraph.ContextBundle) []string {
	seen := map[string]struct{}{}
	for _, evidence := range bundle.Evidence {
		if id := strings.TrimSpace(evidence.ID); id != "" {
			seen[id] = struct{}{}
		}
	}
	addRefs := func(refs []contextgraph.Ref) {
		for _, ref := range refs {
			if id := strings.TrimSpace(ref.ID); id != "" {
				seen[id] = struct{}{}
			}
		}
	}
	for _, object := range bundle.Objects {
		addRefs(object.EvidenceRefs)
	}
	for _, link := range bundle.Links {
		addRefs(link.EvidenceRefs)
	}
	for _, fact := range bundle.Facts {
		addRefs(fact.EvidenceRefs)
	}
	refs := make([]string, 0, len(seen))
	for ref := range seen {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}
