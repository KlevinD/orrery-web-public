package missioncontrol

import (
	"context"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/authorization"
	missioncore "orrery_v2/internal/mission/core"
	discussion "orrery_v2/internal/mission/discussion"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/projection"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
)

type CommandReceiptDTO struct {
	CommandID  string    `json:"command_id"`
	Status     string    `json:"status"`
	MissionID  string    `json:"mission_id,omitempty"`
	TaskID     string    `json:"task_id,omitempty"`
	Version    int       `json:"version,omitempty"`
	AcceptedAt time.Time `json:"accepted_at"`
}

type SubmitResultCommand struct {
	RequestID       string
	IdempotencyKey  string
	ExpectedVersion int
	MissionID       missioncore.MissionID
	TaskID          missioncore.TaskID
	Result          agentcore.AgentResult
}

type TaskDeliveryPort interface {
	SubmitAgentResult(ctx context.Context, taskID missiontask.TaskID, result agentcore.AgentResult, opts missiontask.SubmitOptions) (missiontask.TaskSubmission, error)
}

type QAPort interface {
	SubmitQAResult(ctx context.Context, taskID missiontask.TaskID, result missiontask.QAResultRecord, opts missiontask.QASubmitOptions) (missiontask.QAResultRecord, error)
}

type DiscussionPort interface {
	PostMessage(ctx context.Context, cmd discussion.PostMessageCommand) (discussion.Message, error)
	Reply(ctx context.Context, cmd discussion.ReplyCommand) (discussion.Message, error)
}

type InboxPort interface {
	ListInbox(ctx context.Context, recipient discussion.ViewerRef, q discussion.InboxQuery) (discussion.InboxPage, error)
	MarkRead(ctx context.Context, itemID discussion.InboxItemID, viewer discussion.ViewerRef) error
	CloseWithReply(ctx context.Context, itemID discussion.InboxItemID, viewer discussion.ViewerRef, reply discussion.MessageID) (discussion.InboxItem, error)
}

type MissionTaskPort interface {
	CreateMission(ctx context.Context, viewer ViewerRef, cmd CreateMissionCommand) (MissionDTO, error)
	UpdateMissionBrief(ctx context.Context, viewer ViewerRef, cmd UpdateMissionBriefCommand) (MissionDTO, error)
	CreateTask(ctx context.Context, viewer ViewerRef, cmd CreateTaskCommand) (TaskCardDTO, error)
	AssignTask(ctx context.Context, viewer ViewerRef, cmd AssignTaskCommand) (CommandReceiptDTO, error)
}

type SchedulerMissionLifecyclePort interface {
	SyncSchedulerMission(ctx context.Context, viewer ViewerRef, cmd SyncSchedulerMissionCommand) (MissionDTO, error)
}

type AuditEvent struct {
	AuditID       string
	TenantID      string
	ActorID       string
	ViewerID      string
	MissionID     string
	EntityType    string
	EntityID      string
	Action        string
	Result        string
	Severity      string
	CorrelationID string
	Summary       string
	Metadata      map[string]string
	OccurredAt    time.Time
}

type AuditRecorderPort interface {
	RecordAuditEvent(ctx context.Context, event AuditEvent) error
}

type TaskResponsibilityReviewSignal struct {
	TenantID string
	TaskID   string
	Approved bool
}

type TaskResponsibilityReviewPort interface {
	ObserveTaskReview(ctx context.Context, signal TaskResponsibilityReviewSignal) error
}

type RuntimeRunStorePort interface {
	Enqueue(ctx context.Context, run runtimedaemon.AgentRun) error
	Cancel(ctx context.Context, runID agentcore.AgentRunID, reason string) error
}

type RuntimeTaskProjectionReaderPort interface {
	ResolveRuntimeTask(ctx context.Context, tenantID string, missionID missioncore.MissionID, taskID missioncore.TaskID) (projection.TaskCardView, error)
}

type PromptBlockProvider interface {
	PromptBlocks(ctx context.Context) ([]agentcore.RuntimePromptBlock, error)
}

type RuntimeConversationContextRequest struct {
	TenantID       string
	MissionID      string
	ConversationID string
	AgentID        agentcore.AgentID
}

type RuntimeConversationContext struct {
	ConversationID string
	Digest         agentcore.DiscussionDigestRef
}

type RuntimeConversationContextProvider interface {
	RuntimeConversationContext(context.Context, RuntimeConversationContextRequest) (RuntimeConversationContext, error)
}

type MissionEventSinkPort interface {
	Append(ctx context.Context, event missionevent.Envelope) error
}

type MissionEventReaderPort interface {
	LoadMission(ctx context.Context, missionID missioncore.MissionID) ([]missionevent.Envelope, error)
}

type TenantMissionEventReaderPort interface {
	LoadMissionForTenant(ctx context.Context, tenantID string, missionID missioncore.MissionID) ([]missionevent.Envelope, error)
}

func loadMissionEvents(ctx context.Context, reader MissionEventReaderPort, tenantID string, missionID missioncore.MissionID) ([]missionevent.Envelope, error) {
	if tenantReader, ok := reader.(TenantMissionEventReaderPort); ok {
		return tenantReader.LoadMissionForTenant(ctx, missionTenantID(tenantID), missionID)
	}
	if missionTenantID(tenantID) != "global" {
		return nil, ErrServiceDependency
	}
	return reader.LoadMission(ctx, missionID)
}

type CommandService struct {
	delivery TaskDeliveryPort
	qa       QAPort
	discuss  DiscussionPort
	inbox    InboxPort
	missions MissionTaskPort
	audit    AuditRecorderPort

	runStore            RuntimeRunStorePort
	runtimeRestartGuard RuntimeTaskRestartGuard
	repo                interface {
		ProjectionRepository
		Save(context.Context, projection.ReadModel) error
	}
	events              MissionEventSinkPort
	eventReader         MissionEventReaderPort
	ids                 idgen.ErrorGenerator
	clock               clock.Clock
	projector           projection.Projector
	promptBlocks        PromptBlockProvider
	conversationContext RuntimeConversationContextProvider
	runtimeTasks        RuntimeTaskProjectionReaderPort
	authorization       *authorization.Service
	responsibility      TaskResponsibilityReviewPort
}

// RuntimeTaskRestartGuard fences replacement runs while an earlier external
// operation on the task remains uncertain, regardless of the selected runtime.
type RuntimeTaskRestartGuard interface {
	CheckTaskRestart(context.Context, string, string, string) error
}

func (s *CommandService) SetRuntimeTaskRestartGuard(guard RuntimeTaskRestartGuard) {
	if s != nil {
		s.runtimeRestartGuard = guard
	}
}

func NewCommandService(delivery TaskDeliveryPort) *CommandService {
	return &CommandService{delivery: delivery}
}

func NewCommandServiceWithPorts(delivery TaskDeliveryPort, qa QAPort, discuss DiscussionPort, inbox InboxPort) *CommandService {
	return &CommandService{delivery: delivery, qa: qa, discuss: discuss, inbox: inbox}
}

func NewCommandServiceWithMissionPorts(delivery TaskDeliveryPort, qa QAPort, discuss DiscussionPort, inbox InboxPort, missions MissionTaskPort) *CommandService {
	return &CommandService{delivery: delivery, qa: qa, discuss: discuss, inbox: inbox, missions: missions}
}

func (s *CommandService) SetAuditRecorder(recorder AuditRecorderPort) {
	if s != nil {
		s.audit = recorder
	}
}

func (s *CommandService) SetRuntimeCommandPorts(runStore RuntimeRunStorePort, repo interface {
	ProjectionRepository
	Save(context.Context, projection.ReadModel) error
}, events MissionEventSinkPort, ids idgen.ErrorGenerator, platformClock clock.Clock) {
	if s == nil {
		return
	}
	s.runStore = runStore
	s.repo = repo
	s.events = events
	s.ids = ids
	s.clock = platformClock
	s.projector = projection.NewProjector()
}

func (s *CommandService) SetRuntimeEventReader(reader MissionEventReaderPort) {
	if s != nil {
		s.eventReader = reader
	}
}

func (s *CommandService) SetRuntimeTaskProjectionReader(reader RuntimeTaskProjectionReaderPort) {
	if s != nil {
		s.runtimeTasks = reader
	}
}

func (s *CommandService) SetPromptBlockProvider(provider PromptBlockProvider) {
	if s != nil {
		s.promptBlocks = provider
	}
}

func (s *CommandService) SetRuntimeConversationContextProvider(provider RuntimeConversationContextProvider) {
	if s != nil {
		s.conversationContext = provider
	}
}

func (s *CommandService) SetAuthorizationService(service *authorization.Service) {
	if s != nil {
		s.authorization = service
	}
}

func (s *CommandService) SetTaskResponsibilityReviewPort(port TaskResponsibilityReviewPort) {
	if s != nil {
		s.responsibility = port
	}
}

func (s *CommandService) SubmitAgentResult(ctx context.Context, viewer ViewerRef, cmd SubmitResultCommand) (CommandReceiptDTO, error) {
	if s == nil || s.delivery == nil {
		return CommandReceiptDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return CommandReceiptDTO{}, err
	}
	if cmd.IdempotencyKey == "" {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	if err := s.authorizeMissionAction(ctx, viewer, cmd.MissionID, "mission.view"); err != nil {
		return CommandReceiptDTO{}, err
	}
	cmd.Result.MissionID = firstMissionID(cmd.Result.MissionID, cmd.MissionID)
	cmd.Result.TaskID = firstTaskID(cmd.Result.TaskID, cmd.TaskID)
	if cmd.Result.AgentID == "" {
		cmd.Result.AgentID = agentcore.AgentID(viewer.ViewerID)
	}
	cmd.Result.InternalTraceRef = ""
	submission, err := s.delivery.SubmitAgentResult(ctx, missiontask.TaskID(cmd.TaskID), cmd.Result, missiontask.SubmitOptions{IdempotencyKey: cmd.IdempotencyKey, ExpectedVersion: int64(cmd.ExpectedVersion)})
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	receipt := CommandReceiptDTO{
		CommandID:  string(submission.ID),
		Status:     "accepted",
		MissionID:  cmd.MissionID.String(),
		TaskID:     cmd.TaskID.String(),
		Version:    int(submission.TaskVersionAccepted),
		AcceptedAt: submission.SubmittedAt,
	}
	if err := s.recordViewerAudit(ctx, viewer, AuditEvent{
		ActorID:       viewer.ViewerID,
		ViewerID:      viewer.ViewerID,
		MissionID:     cmd.MissionID.String(),
		EntityType:    "task",
		EntityID:      cmd.TaskID.String(),
		Action:        "task.submit_result",
		Result:        "succeeded",
		Severity:      "info",
		CorrelationID: firstString(cmd.RequestID, cmd.IdempotencyKey),
		Summary:       "agent submitted task result",
		OccurredAt:    receipt.AcceptedAt,
	}); err != nil {
		return CommandReceiptDTO{}, err
	}
	return receipt, nil
}

type QADecisionCommand struct {
	RequestID        string
	IdempotencyKey   string
	MissionID        missioncore.MissionID
	TaskID           missioncore.TaskID
	QARef            string
	SubmissionID     string
	Decision         string
	Message          string
	CheckedCriteria  []CheckedCriterionDTO
	EvidenceResults  []EvidenceCheckDTO
	MissingEvidence  []string
	RejectionReasons []string
}

type CheckedCriterionDTO struct {
	CriterionID string `json:"criterion_id"`
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty"`
}

type EvidenceCheckDTO struct {
	RequirementID string `json:"requirement_id"`
	EvidenceID    string `json:"evidence_id,omitempty"`
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
}

type PostDiscussionMessageCommand struct {
	RequestID         string
	IdempotencyKey    string
	MissionID         missioncore.MissionID
	DiscussionGroupID discussion.DiscussionGroupID
	Content           string
	Summary           string
	MessageKind       string
	Visibility        string
	RelatedTaskID     missioncore.TaskID
	RelatedEvidenceID string
	SourceRefs        []string
	RequiredResponse  bool
	DueAt             time.Time
}

type ReplyDiscussionMessageCommand struct {
	RequestID         string
	IdempotencyKey    string
	MissionID         missioncore.MissionID
	DiscussionGroupID discussion.DiscussionGroupID
	MessageID         discussion.MessageID
	Content           string
	Summary           string
	InboxItemID       discussion.InboxItemID
}

type ListInboxCommand struct {
	MissionID     missioncore.MissionID
	IncludeClosed bool
	Limit         int
}

type InboxItemCommand struct {
	RequestID      string
	IdempotencyKey string
	InboxItemID    discussion.InboxItemID
	ReplyMessageID discussion.MessageID
}

type InboxPageDTO struct {
	Items []InboxItemDTO `json:"items"`
}

type UpdateMissionBriefCommand struct {
	RequestID      string
	IdempotencyKey string
	MissionID      missioncore.MissionID
	Title          string
	FinalGoal      string
}

type CreateTaskCommand struct {
	RequestID                 string
	IdempotencyKey            string
	MissionID                 missioncore.MissionID
	TaskID                    missioncore.TaskID
	AssigneeID                string
	Goal                      string
	WorkerPersonalGoal        string
	Scope                     string
	Priority                  string
	DueAt                     time.Time
	ExpectedDeliverables      []ExpectedDeliverableCommand
	EvidenceRequirements      []EvidenceRequirementCommand
	AcceptanceCriteria        []string
	Dependencies              []missioncore.TaskID
	SourceKind                string
	SourceID                  string
	SourceStepID              string
	ExecutionPermissionScopes []string
	ExecutionAllowedToolIDs   []string
	ExecutionEnvelope         *agentcore.ExecutionEnvelope
	RequireExplicitToolScopes bool
}

type ExpectedDeliverableCommand struct {
	ID    string
	Title string
}

type EvidenceRequirementCommand struct {
	ID          string
	Description string
	Kind        string
	Required    bool
}

type AssignTaskCommand struct {
	RequestID                     string
	IdempotencyKey                string
	MissionID                     missioncore.MissionID
	TaskID                        missioncore.TaskID
	AssigneeID                    string
	Reason                        string
	ResponsibilityExpectedVersion int64
}

func (s *CommandService) UpdateMissionBrief(ctx context.Context, viewer ViewerRef, cmd UpdateMissionBriefCommand) (MissionDTO, error) {
	if s == nil || s.missions == nil {
		return MissionDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return MissionDTO{}, err
	}
	if cmd.IdempotencyKey == "" || cmd.MissionID.IsZero() {
		return MissionDTO{}, ErrInvalidRequest
	}
	if err := s.authorizeMissionAction(ctx, viewer, cmd.MissionID, "mission.edit"); err != nil {
		return MissionDTO{}, err
	}
	mission, err := s.missions.UpdateMissionBrief(ctx, viewer, cmd)
	if err != nil {
		return MissionDTO{}, err
	}
	if err := s.recordViewerAudit(ctx, viewer, AuditEvent{ActorID: viewer.ViewerID, ViewerID: viewer.ViewerID, MissionID: mission.MissionID, EntityType: "mission", EntityID: mission.MissionID, Action: "mission.update_brief", Result: "succeeded", Severity: "info", CorrelationID: firstString(cmd.RequestID, cmd.IdempotencyKey), Summary: "updated mission brief", OccurredAt: mission.PhaseUpdatedAt}); err != nil {
		return MissionDTO{}, err
	}
	return mission, nil
}

func (s *CommandService) SyncSchedulerMission(ctx context.Context, viewer ViewerRef, cmd SyncSchedulerMissionCommand) (MissionDTO, error) {
	if s == nil || s.missions == nil {
		return MissionDTO{}, ErrServiceDependency
	}
	port, ok := s.missions.(SchedulerMissionLifecyclePort)
	if !ok {
		return MissionDTO{}, ErrServiceDependency
	}
	return port.SyncSchedulerMission(ctx, viewer, cmd)
}

func (s *CommandService) CreateTask(ctx context.Context, viewer ViewerRef, cmd CreateTaskCommand) (TaskCardDTO, error) {
	if s == nil || s.missions == nil {
		return TaskCardDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return TaskCardDTO{}, err
	}
	if cmd.IdempotencyKey == "" {
		return TaskCardDTO{}, ErrInvalidRequest
	}
	if err := s.authorizeMissionAction(ctx, viewer, cmd.MissionID, "mission.edit"); err != nil {
		return TaskCardDTO{}, err
	}
	task, err := s.missions.CreateTask(ctx, viewer, cmd)
	if err != nil {
		return TaskCardDTO{}, err
	}
	if err := s.recordViewerAudit(ctx, viewer, AuditEvent{ActorID: viewer.ViewerID, ViewerID: viewer.ViewerID, MissionID: cmd.MissionID.String(), EntityType: "task", EntityID: task.TaskID, Action: "task.create", Result: "succeeded", Severity: "info", CorrelationID: firstString(cmd.RequestID, cmd.IdempotencyKey), Summary: "created task", OccurredAt: task.UpdatedAt}); err != nil {
		return TaskCardDTO{}, err
	}
	return task, nil
}

func (s *CommandService) AssignTask(ctx context.Context, viewer ViewerRef, cmd AssignTaskCommand) (CommandReceiptDTO, error) {
	if s == nil || s.missions == nil {
		return CommandReceiptDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return CommandReceiptDTO{}, err
	}
	if cmd.IdempotencyKey == "" {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	if err := s.authorizeMissionAction(ctx, viewer, cmd.MissionID, "mission.edit"); err != nil {
		return CommandReceiptDTO{}, err
	}
	receipt, err := s.missions.AssignTask(ctx, viewer, cmd)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	if err := s.recordViewerAudit(ctx, viewer, AuditEvent{ActorID: viewer.ViewerID, ViewerID: viewer.ViewerID, MissionID: cmd.MissionID.String(), EntityType: "task", EntityID: cmd.TaskID.String(), Action: "task.assign", Result: "succeeded", Severity: "info", CorrelationID: firstString(cmd.RequestID, cmd.IdempotencyKey), Summary: "assigned task", OccurredAt: receipt.AcceptedAt}); err != nil {
		return CommandReceiptDTO{}, err
	}
	return receipt, nil
}

func (s *CommandService) SubmitQADecision(ctx context.Context, viewer ViewerRef, cmd QADecisionCommand) (CommandReceiptDTO, error) {
	if s == nil || s.qa == nil {
		return CommandReceiptDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return CommandReceiptDTO{}, err
	}
	if cmd.IdempotencyKey == "" {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	if err := s.authorizeMissionAction(ctx, viewer, cmd.MissionID, "mission.qa_approve"); err != nil {
		return CommandReceiptDTO{}, err
	}
	result, err := s.qa.SubmitQAResult(ctx, missiontask.TaskID(cmd.TaskID), missiontask.QAResultRecord{
		ID:               missiontask.QAResultID(cmd.QARef),
		TaskID:           missiontask.TaskID(cmd.TaskID),
		SubmissionID:     cmd.SubmissionID,
		CheckedBy:        agentcore.AgentID(viewer.ViewerID),
		Status:           missiontask.QAStatus(cmd.Decision),
		CheckedCriteria:  mapCheckedCriteria(cmd.CheckedCriteria),
		EvidenceResults:  mapEvidenceChecks(cmd.EvidenceResults),
		MissingEvidence:  mapMissingEvidence(cmd.MissingEvidence),
		RejectionReasons: append([]string(nil), cmd.RejectionReasons...),
	}, missiontask.QASubmitOptions{IdempotencyKey: cmd.IdempotencyKey})
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	receipt := CommandReceiptDTO{CommandID: string(result.ID), Status: "accepted", MissionID: cmd.MissionID.String(), TaskID: cmd.TaskID.String(), AcceptedAt: result.CheckedAt}
	if s.responsibility != nil && (result.Status == missiontask.QAStatusApproved || result.Status == missiontask.QAStatusRejected || result.Status == missiontask.QAStatusNeedsRework) {
		if err := s.responsibility.ObserveTaskReview(ctx, TaskResponsibilityReviewSignal{
			TenantID: viewer.Tenant(), TaskID: cmd.TaskID.String(), Approved: result.Status == missiontask.QAStatusApproved,
		}); err != nil {
			return CommandReceiptDTO{}, err
		}
	}
	if err := s.recordAudit(ctx, AuditEvent{ActorID: viewer.ViewerID, ViewerID: viewer.ViewerID, MissionID: cmd.MissionID.String(), EntityType: "qa", EntityID: cmd.QARef, Action: "qa.decision", Result: "succeeded", Severity: "info", CorrelationID: firstString(cmd.RequestID, cmd.IdempotencyKey), Summary: "submitted qa decision", OccurredAt: receipt.AcceptedAt}); err != nil {
		return CommandReceiptDTO{}, err
	}
	return receipt, nil
}

func (s *CommandService) PostDiscussionMessage(ctx context.Context, viewer ViewerRef, cmd PostDiscussionMessageCommand) (CommandReceiptDTO, error) {
	if s == nil || s.discuss == nil {
		return CommandReceiptDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return CommandReceiptDTO{}, err
	}
	if cmd.IdempotencyKey == "" {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	if err := s.rejectLegacyDiscussionForTodoMission(ctx, viewer, cmd.MissionID); err != nil {
		return CommandReceiptDTO{}, err
	}
	if err := s.authorizeDiscussionAction(ctx, viewer, cmd.DiscussionGroupID, "discussion.write"); err != nil {
		return CommandReceiptDTO{}, err
	}
	message, err := s.discuss.PostMessage(ctx, discussion.PostMessageCommand{
		TenantID:          viewer.Tenant(),
		MissionID:         cmd.MissionID,
		DiscussionGroupID: cmd.DiscussionGroupID,
		Author:            discussionViewer(viewer),
		Content:           cmd.Content,
		Summary:           cmd.Summary,
		MessageKind:       discussion.MessageKind(cmd.MessageKind),
		Visibility:        discussion.MessageVisibility{Scope: discussion.VisibilityScope(cmd.Visibility)},
		RelatedTaskID:     cmd.RelatedTaskID,
		RelatedEvidenceID: cmd.RelatedEvidenceID,
		SourceRefs:        append([]string(nil), cmd.SourceRefs...),
		RequiredResponse:  cmd.RequiredResponse,
		DueAt:             cmd.DueAt,
		IdempotencyKey:    cmd.IdempotencyKey,
	})
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	receipt := messageReceipt(message)
	if err := s.recordViewerAudit(ctx, viewer, AuditEvent{ActorID: viewer.ViewerID, ViewerID: viewer.ViewerID, MissionID: cmd.MissionID.String(), EntityType: "discussion_message", EntityID: string(message.ID), Action: "discussion.message.post", Result: "succeeded", Severity: "info", CorrelationID: firstString(cmd.RequestID, cmd.IdempotencyKey), Summary: "posted discussion message", OccurredAt: receipt.AcceptedAt}); err != nil {
		return CommandReceiptDTO{}, err
	}
	return receipt, nil
}

func (s *CommandService) ReplyDiscussionMessage(ctx context.Context, viewer ViewerRef, cmd ReplyDiscussionMessageCommand) (CommandReceiptDTO, error) {
	if s == nil || s.discuss == nil {
		return CommandReceiptDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return CommandReceiptDTO{}, err
	}
	if cmd.IdempotencyKey == "" {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	if err := s.rejectLegacyDiscussionForTodoMission(ctx, viewer, cmd.MissionID); err != nil {
		return CommandReceiptDTO{}, err
	}
	if err := s.authorizeDiscussionAction(ctx, viewer, cmd.DiscussionGroupID, "discussion.write"); err != nil {
		return CommandReceiptDTO{}, err
	}
	message, err := s.discuss.Reply(ctx, discussion.ReplyCommand{
		TenantID:          viewer.Tenant(),
		MissionID:         cmd.MissionID,
		DiscussionGroupID: cmd.DiscussionGroupID,
		ReplyToMessageID:  cmd.MessageID,
		Author:            discussionViewer(viewer),
		Content:           cmd.Content,
		Summary:           cmd.Summary,
		InboxItemID:       cmd.InboxItemID,
		IdempotencyKey:    cmd.IdempotencyKey,
	})
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	receipt := messageReceipt(message)
	if err := s.recordViewerAudit(ctx, viewer, AuditEvent{ActorID: viewer.ViewerID, ViewerID: viewer.ViewerID, MissionID: cmd.MissionID.String(), EntityType: "discussion_message", EntityID: string(message.ID), Action: "discussion.message.reply", Result: "succeeded", Severity: "info", CorrelationID: firstString(cmd.RequestID, cmd.IdempotencyKey), Summary: "replied to discussion message", OccurredAt: receipt.AcceptedAt}); err != nil {
		return CommandReceiptDTO{}, err
	}
	return receipt, nil
}

func (s *CommandService) rejectLegacyDiscussionForTodoMission(ctx context.Context, viewer ViewerRef, missionID missioncore.MissionID) error {
	// Older standalone Mission tests and adapters may not configure the runtime
	// projection repository. Production does, and its wiring is asserted in
	// server tests. When configured, the legacy discussion path must never
	// become a second message store for a Todo-origin Mission.
	if s == nil || s.repo == nil || missionID.IsZero() {
		return nil
	}
	model, err := loadMissionProjection(ctx, s.repo, viewer.Tenant(), missionID)
	if err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(model.Mission.OriginKind), "todo") {
		return ErrInvalidRequest
	}
	return nil
}

func (s *CommandService) ListInbox(ctx context.Context, viewer ViewerRef, cmd ListInboxCommand) (InboxPageDTO, error) {
	if s == nil || s.inbox == nil {
		return InboxPageDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return InboxPageDTO{}, err
	}
	if !cmd.MissionID.IsZero() {
		if err := s.authorizeMissionAction(ctx, viewer, cmd.MissionID, "mission.view"); err != nil {
			return InboxPageDTO{}, err
		}
	}
	page, err := s.inbox.ListInbox(ctx, discussionViewer(viewer), discussion.InboxQuery{MissionID: cmd.MissionID, IncludeClosed: cmd.IncludeClosed, Limit: cmd.Limit})
	if err != nil {
		return InboxPageDTO{}, err
	}
	return mapInboxPage(page), nil
}

func (s *CommandService) MarkInboxRead(ctx context.Context, viewer ViewerRef, cmd InboxItemCommand) (CommandReceiptDTO, error) {
	if s == nil || s.inbox == nil {
		return CommandReceiptDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return CommandReceiptDTO{}, err
	}
	if err := s.inbox.MarkRead(ctx, cmd.InboxItemID, discussionViewer(viewer)); err != nil {
		return CommandReceiptDTO{}, err
	}
	receipt := CommandReceiptDTO{CommandID: string(cmd.InboxItemID), Status: "accepted", AcceptedAt: time.Now().UTC()}
	if err := s.recordViewerAudit(ctx, viewer, AuditEvent{ActorID: viewer.ViewerID, ViewerID: viewer.ViewerID, EntityType: "inbox_item", EntityID: string(cmd.InboxItemID), Action: "inbox.mark_read", Result: "succeeded", Severity: "info", CorrelationID: firstString(cmd.RequestID, cmd.IdempotencyKey), Summary: "marked inbox item read", OccurredAt: receipt.AcceptedAt}); err != nil {
		return CommandReceiptDTO{}, err
	}
	return receipt, nil
}

func (s *CommandService) CloseInboxItem(ctx context.Context, viewer ViewerRef, cmd InboxItemCommand) (CommandReceiptDTO, error) {
	if s == nil || s.inbox == nil {
		return CommandReceiptDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return CommandReceiptDTO{}, err
	}
	item, err := s.inbox.CloseWithReply(ctx, cmd.InboxItemID, discussionViewer(viewer), cmd.ReplyMessageID)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	receipt := CommandReceiptDTO{CommandID: string(cmd.InboxItemID), Status: "accepted", MissionID: item.MissionID.String(), AcceptedAt: time.Now().UTC()}
	if err := s.recordViewerAudit(ctx, viewer, AuditEvent{ActorID: viewer.ViewerID, ViewerID: viewer.ViewerID, EntityType: "inbox_item", EntityID: string(cmd.InboxItemID), Action: "inbox.close", Result: "succeeded", Severity: "info", CorrelationID: firstString(cmd.RequestID, cmd.IdempotencyKey), Summary: "closed inbox item", OccurredAt: receipt.AcceptedAt}); err != nil {
		return CommandReceiptDTO{}, err
	}
	return receipt, nil
}

func firstMissionID(values ...missioncore.MissionID) missioncore.MissionID {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return ""
}

func firstTaskID(values ...missioncore.TaskID) missioncore.TaskID {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return ""
}

func discussionViewer(viewer ViewerRef) discussion.ViewerRef {
	switch viewer.ViewerKind {
	case ViewerKindAgent:
		return discussion.ViewerRef{Kind: discussion.ViewerAgent, Ref: viewer.ViewerID}
	case ViewerKindHuman, ViewerKindUser:
		ref := firstString(viewer.AuthorizationSubjectID, viewer.ViewerID)
		if strings.EqualFold(strings.TrimSpace(ref), agentcore.SystemSuperadminPrincipalID) {
			return discussion.ViewerRef{Kind: discussion.ViewerSystem, Ref: agentcore.SystemSuperadminPrincipalID}
		}
		return discussion.ViewerRef{Kind: discussion.ViewerUser, Ref: ref}
	default:
		return discussion.ViewerRef{Kind: discussion.ViewerSystem, Ref: viewer.ViewerID}
	}
}

func mapCheckedCriteria(items []CheckedCriterionDTO) []missiontask.CheckedCriterion {
	out := make([]missiontask.CheckedCriterion, 0, len(items))
	for _, item := range items {
		out = append(out, missiontask.CheckedCriterion{CriterionID: missiontask.AcceptanceCriterionID(item.CriterionID), Status: missiontask.CheckStatus(item.Status), Reason: item.Reason})
	}
	return out
}

func mapEvidenceChecks(items []EvidenceCheckDTO) []missiontask.EvidenceCheckResult {
	out := make([]missiontask.EvidenceCheckResult, 0, len(items))
	for _, item := range items {
		out = append(out, missiontask.EvidenceCheckResult{RequirementID: missiontask.EvidenceRequirementID(item.RequirementID), EvidenceID: agentcore.EvidenceID(item.EvidenceID), Status: missiontask.CheckStatus(item.Status), Reason: item.Reason})
	}
	return out
}

func mapMissingEvidence(items []string) []missiontask.EvidenceRequirementID {
	out := make([]missiontask.EvidenceRequirementID, 0, len(items))
	for _, item := range items {
		out = append(out, missiontask.EvidenceRequirementID(item))
	}
	return out
}

func messageReceipt(message discussion.Message) CommandReceiptDTO {
	return CommandReceiptDTO{CommandID: string(message.ID), Status: "accepted", MissionID: message.MissionID.String(), AcceptedAt: message.CreatedAt}
}

func firstString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func mapInboxPage(page discussion.InboxPage) InboxPageDTO {
	dto := InboxPageDTO{Items: make([]InboxItemDTO, 0, len(page.Items))}
	for _, item := range page.Items {
		dto.Items = append(dto.Items, InboxItemDTO{
			InboxItemID: string(item.ID),
			OwnerID:     item.Recipient.Ref,
			MessageID:   string(item.MessageID),
			Reason:      string(item.NotificationState.Priority),
			Status:      string(item.Status),
			CreatedAt:   item.CreatedAt,
			ClosedAt:    item.ClosedAt,
		})
	}
	return dto
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
