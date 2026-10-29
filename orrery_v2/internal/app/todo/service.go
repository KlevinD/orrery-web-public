package todo

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
)

type ServiceOptions struct {
	AttachmentStore        discussionsession.AttachmentStore
	SourceDiscussion       TodoSourceDiscussionPort
	Clock                  clock.Clock
	IDGenerator            idgen.ErrorGenerator
	IdeaIDGenerator        idgen.ErrorGenerator
	Repository             *Repository
	DraftSuggester         DraftSuggester
	AutomationDiscussion   TodoAutomationDiscussionPort
	AutomationAudit        TodoAutomationAuditPort
	MissionExecution       TodoMissionExecutionPort
	MissionPause           TodoMissionPausePort
	DevelopmentExecution   TodoDevelopmentExecutionGuard
	ExecutionProgress      TodoExecutionProgressPort
	WorkspaceExecution     TodoWorkspaceExecutionPort
	DecisionPrincipal      TodoDecisionPrincipalResolver
	HILDecisionService     TodoHILDecisionService
	AutomationAgentIDs     []string
	ExecutionSupportIDs    []string
	AutomationBatchSize    int
	AllowOwnerSelfApproval bool
	HumanReviewEnabled     func(context.Context) (bool, error)
}

// Service is the application facade used by HTTP, MCP, and ontology adapters.
// Task, idea, and conversation behavior lives in focused files in this package.
type Service struct {
	attachments            discussionsession.AttachmentStore
	sourceDiscussion       TodoSourceDiscussionPort
	clock                  clock.Clock
	ids                    idgen.ErrorGenerator
	ideaIDs                idgen.ErrorGenerator
	repo                   *Repository
	draftSuggester         DraftSuggester
	automationDiscussion   TodoAutomationDiscussionPort
	automationAudit        TodoAutomationAuditPort
	missionExecution       TodoMissionExecutionPort
	missionPause           TodoMissionPausePort
	developmentExecution   TodoDevelopmentExecutionGuard
	executionProgress      TodoExecutionProgressPort
	workspaceExecution     TodoWorkspaceExecutionPort
	decisionPrincipal      TodoDecisionPrincipalResolver
	hilDecisionService     TodoHILDecisionService
	automationAgentIDs     []string
	executionSupportIDs    []string
	automationBatchSize    int
	allowOwnerSelfApproval bool
	humanReviewEnabled     func(context.Context) (bool, error)
	access                 *authorization.Service
	relations              *authorization.RelationProjector
	notificationWake       atomic.Pointer[func()]
}

// SetNotificationWake installs a nonblocking wakeup after durable Todo changes.
func (s *Service) SetNotificationWake(wake func()) {
	s.notificationWake.Store(&wake)
}

func (s *Service) notifyChanged() {
	if wake := s.notificationWake.Load(); wake != nil && *wake != nil {
		(*wake)()
	}
}

func (s *Service) SetAuthorization(access *authorization.Service, relations *authorization.RelationProjector) {
	if s != nil {
		s.access = access
		s.relations = relations
	}
}

func NewService(opts ServiceOptions) *Service {
	serviceClock := opts.Clock
	if serviceClock == nil {
		serviceClock = clock.Real{}
	}
	ids := opts.IDGenerator
	if ids == nil {
		ids = idgen.NewRandom("task")
	}
	ideaIDs := opts.IdeaIDGenerator
	if ideaIDs == nil {
		ideaIDs = idgen.NewRandom("idea")
	}
	batchSize := opts.AutomationBatchSize
	if batchSize <= 0 || batchSize > 100 {
		batchSize = 10
	}
	return &Service{
		attachments:            opts.AttachmentStore,
		sourceDiscussion:       opts.SourceDiscussion,
		clock:                  serviceClock,
		ids:                    ids,
		ideaIDs:                ideaIDs,
		repo:                   opts.Repository,
		draftSuggester:         opts.DraftSuggester,
		automationDiscussion:   opts.AutomationDiscussion,
		automationAudit:        opts.AutomationAudit,
		missionExecution:       opts.MissionExecution,
		missionPause:           opts.MissionPause,
		developmentExecution:   opts.DevelopmentExecution,
		executionProgress:      opts.ExecutionProgress,
		workspaceExecution:     opts.WorkspaceExecution,
		decisionPrincipal:      opts.DecisionPrincipal,
		hilDecisionService:     opts.HILDecisionService,
		automationAgentIDs:     normalizeTags(opts.AutomationAgentIDs),
		executionSupportIDs:    normalizeTags(opts.ExecutionSupportIDs),
		automationBatchSize:    batchSize,
		allowOwnerSelfApproval: opts.AllowOwnerSelfApproval,
		humanReviewEnabled:     opts.HumanReviewEnabled,
	}
}

func (s *Service) requiresHumanReview(ctx context.Context) (bool, error) {
	if s == nil || s.humanReviewEnabled == nil {
		return true, nil
	}
	return s.humanReviewEnabled(ctx)
}

// TodoHILDecisionService is the narrow HIL boundary used when a Todo
// execution plan reaches human approval. The production composition root
// supplies the shared decision service that is also used by HTTP.
type TodoHILDecisionService interface {
	CreateDecision(context.Context, hil.CreateDecisionInput) (hil.CreateDecisionResult, error)
}

// TodoHILDecisionResolutionReader is an optional recovery boundary for a
// Todo approval whose HIL observer delivery was interrupted after commit.
// Production's shared HIL service implements it; creation-only adapters stay
// compatible and simply skip terminal-resolution reconciliation.
type TodoHILDecisionResolutionReader interface {
	GetDecisionResolution(context.Context, string, string) (hil.DecisionResolution, error)
}

// SetHILDecisionService supports composition roots that create Todo and HIL
// services in separate phases. It must be set before automation scans run.
func (s *Service) SetHILDecisionService(service TodoHILDecisionService) {
	if s != nil {
		s.hilDecisionService = service
	}
}

type TodoAutomationDiscussionPort interface {
	SendTodoAutomationMessage(context.Context, discussionsession.ViewerRef, discussionsession.SendTodoAutomationMessageCommand) (discussionsession.ConversationDTO, error)
	AppendNotice(context.Context, discussionsession.ViewerRef, discussionsession.AppendNoticeCommand) (discussionsession.ConversationDTO, error)
	AddConversationMembers(context.Context, discussionsession.ViewerRef, discussionsession.AddConversationMembersCommand) (discussionsession.ConversationDTO, error)
	EnsureTodoConversation(context.Context, discussionsession.ViewerRef, discussionsession.EnsureTodoConversationCommand) (discussionsession.ConversationDTO, error)
	ReconcileConversationMembers(context.Context, discussionsession.ViewerRef, discussionsession.ReconcileConversationMembersCommand) (discussionsession.ConversationDTO, error)
}

type TodoMissionExecutionPort interface {
	CreateMissionFromTodo(context.Context, TodoMissionExecutionRequest) (TodoMissionExecutionReceipt, error)
}

type TodoMissionPausePort interface {
	PauseMissionForTodo(context.Context, TodoMissionPauseRequest) error
}

type TodoWorkspaceExecutionPort interface {
	ProvisionTodoExecution(context.Context, TodoWorkspaceExecutionRequest) (TodoWorkspaceExecutionReceipt, error)
}

type TodoWorkspaceExecutionRequest struct {
	TenantID, DevelopmentProjectID, TodoID, TodoTitle         string
	PlanID, PlanDigest, ApprovalID, MissionID, ConversationID string
	ResourceSnapshotDigest, RepositoryBindingID, BaseCommit   string
	AllowedFileScopes, PlannedCommands, AllowedHosts          []string
	RequiredSecretRefs                                        []string
	NetworkMode                                               string
	MissionTasks                                              []TodoMissionTaskExecution
	LeaseOwnerRunID                                           string
}

type TodoWorkspaceExecutionReceipt struct {
	WorkspaceID     string
	ExecutionRunIDs []string
}

type TodoDevelopmentExecutionGuard interface {
	EnsureTodoBindingAllowed(context.Context, TodoDevelopmentExecutionAuthorization) error
	EnsureTodoPlanApprovalAllowed(context.Context, TodoDevelopmentExecutionAuthorization) error
	EnsureTodoExecutionAllowed(context.Context, TodoDevelopmentExecutionAuthorization) error
	ResolveTodoExecutionPlanContext(context.Context, TodoDevelopmentExecutionPlanContextRequest) (TodoDevelopmentExecutionPlanContext, error)
}

type TodoExecutionProgressPort interface {
	GetTodoExecutionProgress(context.Context, ViewerRef, TodoExecutionAttempt) (TodoExecutionProgress, bool, error)
}

// TodoDecisionPrincipalResolver reloads the current authoritative identity and
// permission scopes for a persisted human approval principal.
type TodoDecisionPrincipalResolver interface {
	ResolveTodoDecisionPrincipal(context.Context, ViewerRef) (ViewerRef, error)
}

type TodoDevelopmentExecutionAuthorization struct {
	TenantID               string
	DevelopmentProjectID   string
	RequesterID            string
	RequesterKind          ViewerKind
	RequesterSubjectID     string
	RequesterScopes        []string
	AssignedAgentProfileID []string
}

type TodoDevelopmentExecutionPlanContextRequest struct {
	TenantID             string
	DevelopmentProjectID string
}

type TodoDevelopmentExecutionPlanContext struct {
	DevelopmentProjectID    string
	ResourceSnapshotDigest  string
	Repositories            []TodoDevelopmentPlanRepository
	AllowedCommandPrefixes  []string
	AllowedFileScopes       []string
	AllowedNetworkMode      string
	AllowedNetworkHosts     []string
	AllowedSecretRefDigests []string
}

type TodoDevelopmentPlanRepository struct {
	BindingID          string
	DisplayName        string
	Role               string
	AccessMode         string
	DefaultBranch      string
	SourceSubdirectory string
	BuildCommands      []string
	TestCommands       []string
	BaseCommit         string
}

type TodoMissionExecutionRequest struct {
	TenantID                       string
	TodoID                         string
	TodoTitle                      string
	TodoDescription                string
	AcceptanceCriteria             string
	DevelopmentProjectID           string
	ConversationID                 string
	Plan                           TodoExecutionPlan
	PlanDigest                     string
	ExecutionAttempt               int
	ApprovedBy                     string
	ApproverAuthorizationSubjectID string
	ApproverDisplayName            string
	IdempotencyKey                 string
}

type TodoMissionExecutionReceipt struct {
	MissionID      string
	ConversationID string
	Tasks          []TodoMissionTaskExecution
	// RequiresWorkspace keeps development tasks unassigned until their managed
	// repository workspace has been provisioned and bound to the runtime.
	RequiresWorkspace bool
}

type TodoMissionTaskExecution struct {
	MissionID   string
	TaskID      string
	AgentID     string
	Goal        string
	RuntimeKind string
}

type TodoAutomationAuditPort interface {
	RecordAuditEvent(context.Context, operations.AuditEventDTO) error
}

func (s *Service) SuggestDraft(ctx context.Context, viewer ViewerRef, req SuggestDraftRequest) (DraftSuggestion, error) {
	viewer, err := normalizeViewer(viewer)
	if err != nil {
		return DraftSuggestion{}, err
	}
	if s == nil || s.draftSuggester == nil {
		return DraftSuggestion{}, ErrServiceDependency
	}
	if viewer.ViewerID == "" {
		return DraftSuggestion{}, ErrInvalidRequest
	}
	req.Kind = strings.TrimSpace(req.Kind)
	req.Title = strings.TrimSpace(req.Title)
	req.Content = strings.TrimSpace(req.Content)
	req.CurrentTags = normalizeTags(req.CurrentTags)
	if req.Kind == "" {
		req.Kind = "todo"
	}
	if req.Kind != "todo" && req.Kind != "idea" {
		return DraftSuggestion{}, ErrInvalidRequest
	}
	if req.Title == "" && req.Content == "" {
		return DraftSuggestion{}, ErrInvalidRequest
	}
	suggestion, err := s.draftSuggester.SuggestDraft(ctx, req)
	if err != nil {
		return DraftSuggestion{}, err
	}
	suggestion.Title = strings.TrimSpace(suggestion.Title)
	suggestion.Tags = normalizeTags(suggestion.Tags)
	if suggestion.Title == "" || len(suggestion.Tags) == 0 {
		return DraftSuggestion{}, ErrInvalidRequest
	}
	return suggestion, nil
}

func (s *Service) GetWorkHubSummary(ctx context.Context, viewer ViewerRef) (WorkHubSummary, error) {
	viewer, err := normalizeViewer(viewer)
	if err != nil {
		return WorkHubSummary{}, err
	}
	if s == nil || s.repo == nil || s.clock == nil {
		return WorkHubSummary{}, ErrServiceDependency
	}
	location, err := time.LoadLocation(viewer.TimeZone)
	if err != nil {
		return WorkHubSummary{}, ErrInvalidRequest
	}
	now := s.clock.Now().UTC()
	localNow := now.In(location)
	todayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location).UTC()
	tomorrow := todayStart.In(location).AddDate(0, 0, 1).UTC()
	summary, err := s.repo.QueryWorkHubSummary(ctx, viewer, now, todayStart, tomorrow)
	if err != nil || s.access == nil {
		return summary, err
	}
	query := ListTodosQuery{IncludeCompleted: true, Limit: 200}
	if err := s.authorizeTodoListQuery(ctx, viewer, &query); err != nil {
		return WorkHubSummary{}, err
	}
	resetTodoSummary(&summary)
	for {
		page, err := s.repo.QueryTodos(ctx, viewer, query)
		if err != nil {
			return WorkHubSummary{}, err
		}
		for _, item := range page.Todos {
			addTodoToSummary(&summary, viewer, item, now, todayStart, tomorrow)
		}
		if !page.HasMore || len(page.Todos) == 0 {
			break
		}
		query.Offset += len(page.Todos)
	}
	return summary, nil
}

func resetTodoSummary(summary *WorkHubSummary) {
	if summary == nil {
		return
	}
	summary.MyOpenTasks = 0
	summary.DueToday = 0
	summary.Overdue = 0
	summary.Blocked = 0
	summary.AwaitingTaskReview = 0
	summary.TasksInbox = 0
	summary.TasksPlanned = 0
	summary.TasksInProgress = 0
	summary.UnassignedTeamTasks = 0
}

func addTodoToSummary(summary *WorkHubSummary, viewer ViewerRef, item TodoItem, now, todayStart, tomorrow time.Time) {
	if summary == nil {
		return
	}
	switch item.Status {
	case TodoStatusBlocked:
		summary.Blocked++
	case TodoStatusReview:
		summary.AwaitingTaskReview++
	case TodoStatusInbox:
		summary.TasksInbox++
	case TodoStatusPlanned:
		summary.TasksPlanned++
	case TodoStatusInProgress:
		summary.TasksInProgress++
	}
	if isTerminalTodoStatus(item.Status) {
		return
	}
	assignedToViewer := viewerIdentityMatches(viewer, item.AssigneeID)
	createdByViewer := viewerIdentityMatches(viewer, item.CreatedBy) || viewerIdentityMatches(viewer, item.CreatedBySubjectID)
	mine := assignedToViewer || (item.AssigneeID == "" && createdByViewer)
	if mine {
		summary.MyOpenTasks++
		if !item.DueAt.IsZero() && !item.DueAt.Before(todayStart) && item.DueAt.Before(tomorrow) {
			summary.DueToday++
		}
		if !item.DueAt.IsZero() && item.DueAt.Before(now) {
			summary.Overdue++
		}
	}
	if item.AssigneeID == "" {
		summary.UnassignedTeamTasks++
	}
}

func viewerIdentityMatches(viewer ViewerRef, candidate string) bool {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return false
	}
	return candidate == strings.TrimSpace(viewer.ViewerID) || candidate == strings.TrimSpace(viewer.AuthorizationSubjectID)
}

func normalizeViewer(viewer ViewerRef) (ViewerRef, error) {
	viewer.TenantID = normalizedTenantID(viewer.TenantID)
	viewer.OrganizationID = strings.TrimSpace(viewer.OrganizationID)
	viewer.DepartmentID = strings.TrimSpace(viewer.DepartmentID)
	viewer.TimeZone = strings.TrimSpace(viewer.TimeZone)
	if viewer.TimeZone == "" {
		viewer.TimeZone = "UTC"
	}
	if len(viewer.TimeZone) > 128 {
		return ViewerRef{}, ErrInvalidRequest
	}
	viewer.ViewerID = strings.TrimSpace(viewer.ViewerID)
	viewer.AuthorizationSubjectID = strings.TrimSpace(viewer.AuthorizationSubjectID)
	viewer.DisplayName = strings.TrimSpace(viewer.DisplayName)
	if viewer.ViewerID == "" {
		return ViewerRef{}, ErrInvalidRequest
	}
	if viewer.ViewerKind == "" {
		viewer.ViewerKind = ViewerKindUser
	}
	if viewer.AuthorizationSubjectID == "" {
		viewer.AuthorizationSubjectID = viewer.ViewerID
	}
	if viewer.ViewerKind == ViewerKindHuman {
		viewer.ViewerKind = ViewerKindUser
	}
	if viewer.ViewerKind != ViewerKindUser && viewer.ViewerKind != ViewerKindAgent && viewer.ViewerKind != ViewerKindSystem {
		return ViewerRef{}, ErrInvalidRequest
	}
	if !validTodoIdentity(viewer.AuthorizationSubjectID) {
		return ViewerRef{}, ErrInvalidRequest
	}
	viewer.PermissionScope = normalizeTags(viewer.PermissionScope)
	return viewer, nil
}

func validTodoIdentity(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

func todoPrincipal(viewer ViewerRef) identity.Principal {
	kind := identity.ViewerKindUser
	switch viewer.ViewerKind {
	case ViewerKindAgent:
		kind = identity.ViewerKindAgent
	case ViewerKindSystem:
		kind = identity.ViewerKindSystem
	}
	return identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: identity.ProviderLocal,
		Subject:         viewer.AuthorizationSubjectID,
		UserID:          viewer.AuthorizationSubjectID,
		Username:        viewer.AuthorizationSubjectID,
		ViewerKind:      kind,
		TenantID:        viewer.TenantID,
		CoarseScopes:    append([]string(nil), viewer.PermissionScope...),
	})
}

func todoObject(item TodoItem) identity.ObjectRef {
	return identity.ObjectRef{Type: "todo", ID: item.TodoID, TenantID: item.TenantID}
}

func (s *Service) authorizeTodo(ctx context.Context, viewer ViewerRef, item TodoItem, action string) error {
	if s == nil || s.access == nil {
		return nil
	}
	if isBreakglassTodoViewer(viewer) {
		return nil
	}
	if err := s.access.Can(ctx, todoPrincipal(viewer), action, todoObject(item)); err != nil {
		if errors.Is(err, authorization.ErrDenied) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func todoSubject(kind ViewerKind, id string) authorization.SubjectRef {
	id = strings.TrimSpace(id)
	if id == "" {
		return authorization.SubjectRef{}
	}
	if kind == ViewerKindSystem {
		return authorization.SubjectRef{}
	}
	subjectKind := "user"
	if kind == ViewerKindAgent {
		subjectKind = "agent"
	}
	return authorization.SubjectRef{Kind: subjectKind, ID: id}
}

func isBreakglassTodoViewer(viewer ViewerRef) bool {
	return viewer.ViewerKind == ViewerKindSystem &&
		strings.EqualFold(strings.TrimSpace(viewer.ViewerID), agentcore.SystemSuperadminPrincipalID) &&
		strings.EqualFold(strings.TrimSpace(viewer.AuthorizationSubjectID), agentcore.SystemSuperadminPrincipalID)
}

func todoRelations(item TodoItem) map[string][]authorization.SubjectRef {
	proposerID := strings.TrimSpace(item.CreatedBySubjectID)
	if proposerID == "" {
		proposerID = strings.TrimSpace(item.CreatedBy)
	}
	relations := map[string][]authorization.SubjectRef{}
	if subject := todoSubject(item.CreatedByKind, proposerID); subject.ID != "" {
		relations["proposer"] = []authorization.SubjectRef{subject}
	}
	assigneeKind := ViewerKindUser
	if item.AssigneeKind == AssigneeKindAgent {
		assigneeKind = ViewerKindAgent
	}
	if subject := todoSubject(assigneeKind, item.AssigneeID); subject.ID != "" {
		relations["assignee"] = []authorization.SubjectRef{subject}
	}
	if subject := todoSubject(ViewerKindUser, item.ReviewerID); subject.ID != "" {
		relations["reviewer"] = []authorization.SubjectRef{subject}
	}
	if projectID := strings.TrimSpace(item.DevelopmentProjectID); projectID != "" {
		relations["development_project"] = []authorization.SubjectRef{{Kind: "development_project", ID: projectID}}
	}
	return relations
}

func todoRelationDifference(left, right map[string][]authorization.SubjectRef) map[string][]authorization.SubjectRef {
	rightSet := map[string]struct{}{}
	for relation, subjects := range right {
		for _, subject := range subjects {
			rightSet[relation+"\x00"+subject.Kind+"\x00"+subject.ID] = struct{}{}
		}
	}
	out := map[string][]authorization.SubjectRef{}
	for relation, subjects := range left {
		for _, subject := range subjects {
			if _, exists := rightSet[relation+"\x00"+subject.Kind+"\x00"+subject.ID]; !exists {
				out[relation] = append(out[relation], subject)
			}
		}
	}
	return out
}

func (s *Service) writeTodoRelations(ctx context.Context, item TodoItem) error {
	if s == nil || s.access == nil {
		return nil
	}
	if s.relations == nil {
		return ErrServiceDependency
	}
	return s.relations.WriteTodoRelations(ctx, item.TodoID, item.TenantID, todoRelations(item))
}

func (s *Service) updateTodoRelations(ctx context.Context, previous, next TodoItem, persist func() error) error {
	if s == nil || s.access == nil {
		return persist()
	}
	if s.relations == nil {
		return ErrServiceDependency
	}
	previousRelations := todoRelations(previous)
	nextRelations := todoRelations(next)
	removed := todoRelationDifference(previousRelations, nextRelations)
	added := todoRelationDifference(nextRelations, previousRelations)
	if err := s.relations.DeleteTodoRelations(ctx, previous.TodoID, previous.TenantID, removed); err != nil {
		return err
	}
	if err := s.relations.WriteTodoRelations(ctx, next.TodoID, next.TenantID, added); err != nil {
		cleanupCtx := context.WithoutCancel(ctx)
		cleanupErr := s.relations.DeleteTodoRelations(cleanupCtx, next.TodoID, next.TenantID, added)
		restoreErr := s.relations.WriteTodoRelations(cleanupCtx, previous.TodoID, previous.TenantID, removed)
		return errors.Join(err, cleanupErr, restoreErr)
	}
	if err := persist(); err != nil {
		cleanupCtx := context.WithoutCancel(ctx)
		cleanupErr := s.relations.DeleteTodoRelations(cleanupCtx, next.TodoID, next.TenantID, added)
		restoreErr := s.relations.WriteTodoRelations(cleanupCtx, previous.TodoID, previous.TenantID, removed)
		return errors.Join(err, cleanupErr, restoreErr)
	}
	return nil
}

func hasPermission(viewer ViewerRef, permission string) bool {
	permission = strings.TrimSpace(permission)
	for _, value := range viewer.PermissionScope {
		if strings.EqualFold(strings.TrimSpace(value), permission) {
			return true
		}
	}
	return false
}

func validListSearch(value string) bool {
	return len(value) <= 512 && len(strings.Fields(value)) <= 16
}

func expectedVersion(requested *int64, current int64) int64 {
	if requested == nil || *requested <= 0 {
		return current
	}
	return *requested
}

func normalizeTags(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func viewerDisplayName(viewer ViewerRef) string {
	if viewer.DisplayName != "" {
		return viewer.DisplayName
	}
	return viewer.ViewerID
}

func issueNumberFromID(parts ...string) string {
	kind := "work"
	id := ""
	if len(parts) == 1 {
		id = strings.TrimSpace(parts[0])
		if strings.HasPrefix(strings.ToLower(id), "idea") {
			kind = "idea"
		} else {
			kind = "task"
		}
	} else if len(parts) > 1 {
		kind = strings.TrimSpace(parts[0])
		id = strings.TrimSpace(parts[1])
	}
	prefix := "WRK"
	if strings.EqualFold(kind, "idea") {
		prefix = "IDEA"
	} else if strings.EqualFold(kind, "task") || strings.EqualFold(kind, "todo") {
		prefix = "TASK"
	}
	hash := sha256.Sum256([]byte(kind + ":" + id))
	code := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(hash[:5])
	return prefix + "-" + code[:7]
}
