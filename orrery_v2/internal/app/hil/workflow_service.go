package hil

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type WorkflowServiceOptions struct {
	Repository          WorkflowRepository
	Assignees           WorkflowAssigneeResolver
	IDs                 IDGenerator
	Clock               func() time.Time
	NotificationChannel string
}

type WorkflowService struct {
	repository          WorkflowRepository
	assignees           WorkflowAssigneeResolver
	ids                 IDGenerator
	now                 func() time.Time
	notificationChannel string
	observer            ResolutionObserver
	notificationWake    atomic.Pointer[func()]
}

func NewWorkflowService(options WorkflowServiceOptions) *WorkflowService {
	now := options.Clock
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	notificationChannel := strings.ToLower(strings.TrimSpace(options.NotificationChannel))
	if notificationChannel == "" {
		notificationChannel = "in_app"
	}
	return &WorkflowService{
		repository: options.Repository, assignees: options.Assignees, ids: options.IDs, now: now,
		notificationChannel: notificationChannel,
	}
}

func (s *WorkflowService) setResolutionObserver(observer ResolutionObserver) {
	if s != nil {
		s.observer = observer
	}
}

func (s *WorkflowService) applyWorkflowCommand(ctx context.Context, command WorkflowCommand, actor ActorRef, reason string) (WorkflowInstanceDetail, error) {
	if command.Receipt != nil {
		for _, event := range command.Audit {
			if evidence, ok := event.Metadata["delegation"].(DelegationEvidence); ok {
				if command.Receipt.ResolverSummary == nil {
					command.Receipt.ResolverSummary = map[string]any{}
				}
				command.Receipt.ResolverSummary["delegation"] = evidence
			}
		}
	}
	detail, err := s.repository.ApplyWorkflowCommand(ctx, command)
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	s.notifyWorkflowResolution(ctx, detail, actor, reason)
	if detail.Decision.Status == DecisionPending {
		s.notifyPending()
	}
	return detail, nil
}

func (s *WorkflowService) notifyPending() {
	if wake := s.notificationWake.Load(); wake != nil && *wake != nil {
		(*wake)()
	}
}

func (s *WorkflowService) notifyWorkflowResolution(ctx context.Context, detail WorkflowInstanceDetail, actor ActorRef, reason string) {
	if s == nil || s.observer == nil || (detail.Decision.Status != DecisionResolved && detail.Decision.Status != DecisionDenied && detail.Instance.Status != WorkflowChangesRequested) {
		return
	}
	effect := EffectRequiresApproval
	if detail.Decision.Status == DecisionResolved {
		effect = EffectAuthorize
	} else if detail.Decision.Status == DecisionDenied {
		effect = EffectDeny
	}
	for index := len(detail.Receipts) - 1; index >= 0; index-- {
		receipt := detail.Receipts[index]
		if receipt.RequestID != detail.Decision.ID || receipt.Effect != effect {
			continue
		}
		s.observer.DecisionResolved(ctx, DecisionResolution{
			Decision: detail.Decision,
			Receipt:  receipt,
			Actor:    normalizeActor(actor),
			Reason:   strings.TrimSpace(reason),
		})
		return
	}
}

type SaveTemplateDraftInput struct {
	ID                     string
	TenantID               string
	Name                   string
	Description            string
	Definition             WorkflowDefinition
	Status                 WorkflowTemplateStatus
	LatestPublishedVersion int
	IdempotencyKey         string
}

type PublishTemplateInput struct {
	Template       WorkflowTemplate
	Bindings       []WorkflowTemplateBinding
	IdempotencyKey string
}

type DisableTemplateInput struct {
	TenantID   string
	TemplateID string
	Reason     string
}

type PutTemplateBindingInput struct {
	Binding WorkflowTemplateBinding
	Reason  string
}

type PreparedWorkflow struct {
	Case             WorkflowCase
	Instance         WorkflowInstance
	Stages           []WorkflowStageInstance
	Tasks            []WorkflowTask
	Audit            []WorkflowAuditEvent
	Notifications    []WorkflowNotification
	EscalationActors map[string][]ActorRef
}

func (s *WorkflowService) SaveTemplateDraft(ctx context.Context, actor ActorRef, input SaveTemplateDraftInput) (WorkflowTemplate, error) {
	actor = normalizeActor(actor)
	if s == nil || s.repository == nil || s.ids == nil || !validWorkflowTemplateActor(actor) || strings.TrimSpace(input.TenantID) == "" || strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.IdempotencyKey) == "" {
		return WorkflowTemplate{}, ErrInvalidRequest
	}
	id := strings.TrimSpace(input.ID)
	if id == "" {
		var err error
		id, err = s.ids.NewIDContext()
		if err != nil {
			return WorkflowTemplate{}, err
		}
	}
	now := s.now().UTC()
	status := input.Status
	if status == "" {
		status = WorkflowTemplateDisabled
	}
	template := WorkflowTemplate{
		ID: id, TenantID: strings.TrimSpace(input.TenantID), Name: strings.TrimSpace(input.Name),
		Description: strings.TrimSpace(input.Description), Status: status,
		DraftDefinition: input.Definition, LatestPublishedVersion: input.LatestPublishedVersion,
		CreatedBy: actor, CreatedAt: now, UpdatedAt: now,
	}
	fingerprint := stableFingerprint(struct {
		ID         string
		TenantID   string
		Name       string
		Definition WorkflowDefinition
	}{template.ID, template.TenantID, template.Name, template.DraftDefinition})
	if err := s.repository.SaveTemplateDraft(ctx, template, strings.TrimSpace(input.IdempotencyKey), fingerprint); err != nil {
		return WorkflowTemplate{}, err
	}
	return template, nil
}

func (s *WorkflowService) PublishTemplate(ctx context.Context, actor ActorRef, input PublishTemplateInput) (WorkflowTemplateVersion, error) {
	actor = normalizeActor(actor)
	template := input.Template
	idempotencyKey := strings.TrimSpace(input.IdempotencyKey)
	if s == nil || s.repository == nil || s.ids == nil || !validWorkflowTemplateActor(actor) || strings.TrimSpace(template.ID) == "" || strings.TrimSpace(template.TenantID) == "" || idempotencyKey == "" {
		return WorkflowTemplateVersion{}, ErrInvalidRequest
	}
	if err := ValidateWorkflowDefinition(template.DraftDefinition); err != nil {
		return WorkflowTemplateVersion{}, err
	}
	id, err := s.ids.NewIDContext()
	if err != nil {
		return WorkflowTemplateVersion{}, err
	}
	now := s.now().UTC()
	version := WorkflowTemplateVersion{
		ID: id, TenantID: template.TenantID, TemplateID: template.ID,
		Version: template.LatestPublishedVersion + 1, Definition: template.DraftDefinition,
		DefinitionHash: workflowDefinitionHash(template.DraftDefinition), PublishedBy: actor, PublishedAt: now,
	}
	bindings := append([]WorkflowTemplateBinding(nil), input.Bindings...)
	fingerprintBindings := make([]struct {
		ActionTypeID string
		ToolName     string
		RiskLevel    RiskLevel
		Active       bool
	}, len(bindings))
	for index, binding := range bindings {
		fingerprintBindings[index] = struct {
			ActionTypeID string
			ToolName     string
			RiskLevel    RiskLevel
			Active       bool
		}{strings.TrimSpace(binding.ActionTypeID), strings.TrimSpace(binding.ToolName), binding.RiskLevel, binding.Active}
	}
	sort.Slice(fingerprintBindings, func(i, j int) bool {
		left, right := fingerprintBindings[i], fingerprintBindings[j]
		if left.ActionTypeID != right.ActionTypeID {
			return left.ActionTypeID < right.ActionTypeID
		}
		if left.ToolName != right.ToolName {
			return left.ToolName < right.ToolName
		}
		if left.RiskLevel != right.RiskLevel {
			return left.RiskLevel < right.RiskLevel
		}
		return !left.Active && right.Active
	})
	fingerprint := stableFingerprint(struct {
		TenantID       string
		TemplateID     string
		DefinitionHash string
		Bindings       any
	}{template.TenantID, template.ID, version.DefinitionHash, fingerprintBindings})
	for index := range bindings {
		if strings.TrimSpace(bindings[index].ID) == "" {
			bindings[index].ID, err = s.ids.NewIDContext()
			if err != nil {
				return WorkflowTemplateVersion{}, err
			}
		}
		bindings[index].TenantID = template.TenantID
		bindings[index].TemplateID = template.ID
		bindings[index].CreatedBy = actor
		bindings[index].CreatedAt = now
		bindings[index].UpdatedAt = now
	}
	published, err := s.repository.PublishTemplate(ctx, template, version, bindings, idempotencyKey, fingerprint)
	if err != nil {
		return WorkflowTemplateVersion{}, err
	}
	return published, nil
}

func (s *WorkflowService) DisableTemplate(ctx context.Context, actor ActorRef, input DisableTemplateInput) error {
	if s == nil || s.repository == nil {
		return ErrInvalidRequest
	}
	return s.repository.DisableTemplate(ctx, input.TenantID, input.TemplateID, actor, input.Reason)
}

func (s *WorkflowService) PutTemplateBinding(ctx context.Context, actor ActorRef, input PutTemplateBindingInput) (WorkflowTemplateBinding, error) {
	actor = normalizeActor(actor)
	if s == nil || s.repository == nil || s.ids == nil || !validWorkflowTemplateActor(actor) {
		return WorkflowTemplateBinding{}, ErrInvalidRequest
	}
	now := s.now().UTC()
	if strings.TrimSpace(input.Binding.ID) == "" {
		id, err := s.ids.NewIDContext()
		if err != nil {
			return WorkflowTemplateBinding{}, err
		}
		input.Binding.ID = id
	}
	input.Binding.CreatedBy = actor
	input.Binding.CreatedAt = now
	input.Binding.UpdatedAt = now
	if err := s.repository.PutTemplateBinding(ctx, input.Binding, actor, input.Reason); err != nil {
		return WorkflowTemplateBinding{}, err
	}
	return input.Binding, nil
}

func (s *WorkflowService) ListTemplates(ctx context.Context, query WorkflowTemplateListQuery) ([]WorkflowTemplateSummary, error) {
	if s == nil || s.repository == nil {
		return nil, ErrInvalidRequest
	}
	return s.repository.ListTemplates(ctx, query)
}

func (s *WorkflowService) GetTemplate(ctx context.Context, tenantID, templateID string) (WorkflowTemplate, error) {
	if s == nil || s.repository == nil {
		return WorkflowTemplate{}, ErrInvalidRequest
	}
	return s.repository.GetWorkflowTemplate(ctx, tenantID, templateID)
}

func (s *WorkflowService) PublishTemplateByID(ctx context.Context, actor ActorRef, tenantID, templateID string, bindings []WorkflowTemplateBinding, idempotencyKey string) (WorkflowTemplateVersion, error) {
	template, err := s.GetTemplate(ctx, tenantID, templateID)
	if err != nil {
		return WorkflowTemplateVersion{}, err
	}
	return s.PublishTemplate(ctx, actor, PublishTemplateInput{Template: template, Bindings: bindings, IdempotencyKey: idempotencyKey})
}

func (s *WorkflowService) RecordViewed(ctx context.Context, tenantID, instanceID string, actor ActorRef, idempotencyKey string) error {
	actor = normalizeActor(actor)
	tenantID = strings.TrimSpace(tenantID)
	instanceID = strings.TrimSpace(instanceID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if s == nil || s.repository == nil || tenantID == "" || instanceID == "" || actor.Kind == "" || actor.ID == "" || idempotencyKey == "" {
		return ErrInvalidRequest
	}
	fingerprint := stableFingerprint(struct {
		TenantID   string
		InstanceID string
		Actor      ActorRef
	}{tenantID, instanceID, actor})
	return s.repository.RecordWorkflowViewed(ctx, tenantID, instanceID, actor, idempotencyKey, fingerprint)
}

func (s *WorkflowService) PrepareDecisionWorkflow(ctx context.Context, decision DecisionRequest) (PreparedWorkflow, bool, error) {
	if s == nil || s.repository == nil || s.assignees == nil || s.ids == nil {
		return PreparedWorkflow{}, false, ErrInvalidRequest
	}
	toolName, _ := decision.PayloadPreview["approval_tool_name"].(string)
	version, err := s.repository.ResolveTemplate(ctx, WorkflowTemplateSelector{
		TenantID: decision.TenantID, ActionTypeID: decision.ActionTypeID, ToolName: strings.TrimSpace(toolName), RiskLevel: decision.RiskLevel,
	})
	if errors.Is(err, ErrNotFound) {
		return PreparedWorkflow{}, false, nil
	}
	if err != nil {
		return PreparedWorkflow{}, false, err
	}
	if !workflowSupportsRisk(version.Definition, decision.RiskLevel) {
		return PreparedWorkflow{}, false, ErrInvalidRequest
	}
	materialHash, _ := decision.PayloadPreview["material_hash"].(string)
	materialHash = strings.TrimSpace(materialHash)
	if materialHash == "" {
		return PreparedWorkflow{}, false, ErrInvalidRequest
	}
	now := s.now().UTC()
	caseID, err := s.ids.NewIDContext()
	if err != nil {
		return PreparedWorkflow{}, false, err
	}
	instanceID, err := s.ids.NewIDContext()
	if err != nil {
		return PreparedWorkflow{}, false, err
	}
	caseRecord := WorkflowCase{
		ID: caseID, TenantID: decision.TenantID, Requester: decision.Requester,
		ActionTypeID: decision.ActionTypeID, ToolName: strings.TrimSpace(toolName), Resource: decision.Resource, CreatedAt: now,
	}
	prepared := PreparedWorkflow{
		Case: caseRecord,
		Instance: WorkflowInstance{
			ID: instanceID, TenantID: decision.TenantID, CaseID: caseID, Revision: 1,
			DecisionRequestID: decision.ID, TemplateVersionID: version.ID, DefinitionHash: version.DefinitionHash,
			MaterialHash: materialHash, ArgsHash: decision.ArgsHash, Status: WorkflowRunning,
			CurrentStageKey: version.Definition.Stages[0].Key, Version: 1, CreatedAt: now, UpdatedAt: now, ExpiresAt: decision.ExpiresAt,
		},
		EscalationActors: map[string][]ActorRef{},
	}
	recommendedActors := make([]ActorRef, 0, len(decision.Context.RecommendedParticipants))
	for _, participant := range decision.Context.RecommendedParticipants {
		recommendedActors = append(recommendedActors, participant.Actor)
	}
	baseRequest := WorkflowAssigneeRequest{
		TenantID: decision.TenantID, Requester: decision.Requester, Resource: decision.Resource,
		ActionTypeID: decision.ActionTypeID, TemplateID: version.TemplateID,
		RecommendedActors: recommendedActors,
	}
	reservedApprovers := map[string]bool{}
	for index, definition := range version.Definition.Stages {
		stageID, err := s.ids.NewIDContext()
		if err != nil {
			return PreparedWorkflow{}, false, err
		}
		baseRequest.StageKey = definition.Key
		status := WorkflowStageScheduled
		if index == 0 {
			status = WorkflowStageActive
		}
		stage := WorkflowStageInstance{
			ID: stageID, TenantID: decision.TenantID, WorkflowInstanceID: instanceID,
			Definition: definition, Status: status, EscalationIndex: -1, CreatedAt: now,
		}
		if status == WorkflowStageActive {
			stage.ActivatedAt = now
		}
		switch definition.Type {
		case WorkflowStageApproval:
			actors, err := resolveWorkflowActorSelectors(ctx, s.assignees, baseRequest, definition.Approval.Assignees, workflowAssigneeResolutionOptions{FormalApproval: true, ExcludeRequester: true})
			if err != nil {
				return PreparedWorkflow{}, false, err
			}
			if version.Definition.RequireDistinctApprovers {
				actors = workflowActorsWithoutReserved(actors, reservedApprovers)
				if len(actors) == 0 {
					return PreparedWorkflow{}, false, ErrInvalidRequest
				}
			}
			stage.ResolvedParticipants = actors
			if status == WorkflowStageActive {
				stage.DeadlineAt = workflowDeadlineAt(now, time.Duration(definition.Approval.DeadlineMinutes)*time.Minute, decision.ExpiresAt)
			}
			for _, rung := range definition.Approval.Escalation {
				actors, err := resolveWorkflowActorSelectors(ctx, s.assignees, baseRequest, rung.Actors, workflowAssigneeResolutionOptions{FormalApproval: true, ExcludeRequester: true, ExactlyOne: true})
				if err != nil {
					return PreparedWorkflow{}, false, err
				}
				if version.Definition.RequireDistinctApprovers && reservedApprovers[actorKey(actors[0])] {
					return PreparedWorkflow{}, false, ErrInvalidRequest
				}
				stage.ResolvedEscalationActors = append(stage.ResolvedEscalationActors, actors[0])
			}
			if version.Definition.RequireDistinctApprovers {
				for _, actor := range actors {
					reservedApprovers[actorKey(actor)] = true
				}
				for _, actor := range stage.ResolvedEscalationActors {
					reservedApprovers[actorKey(actor)] = true
				}
			}
			prepared.EscalationActors[stageID] = append([]ActorRef(nil), stage.ResolvedEscalationActors...)
			for seat, actor := range actors {
				taskID, err := s.ids.NewIDContext()
				if err != nil {
					return PreparedWorkflow{}, false, err
				}
				taskStatus := TaskScheduled
				if status == WorkflowStageActive {
					taskStatus = TaskPending
				}
				task := WorkflowTask{
					ID: taskID, TenantID: decision.TenantID, WorkflowInstanceID: instanceID, StageInstanceID: stageID,
					SeatKey: definition.Key + ":" + actorKey(actor), OriginalAssignee: actor, CurrentAssignee: actor,
					Status: taskStatus, DeadlineAt: stage.DeadlineAt, CreatedAt: now, UpdatedAt: now,
				}
				prepared.Tasks = append(prepared.Tasks, task)
				if taskStatus == TaskPending {
					notificationID, err := s.ids.NewIDContext()
					if err != nil {
						return PreparedWorkflow{}, false, err
					}
					prepared.Notifications = append(prepared.Notifications, WorkflowNotification{
						ID: notificationID, TenantID: decision.TenantID, WorkflowInstanceID: instanceID,
						StageInstanceID: stageID, TaskID: taskID, Channel: s.notificationChannel, Recipient: actor,
						EventType: "task.assigned", Status: WorkflowNotificationPending, NextAttemptAt: now,
						IdempotencyKey: "workflow:" + instanceID + ":stage:" + definition.Key + ":seat:" + strconv.Itoa(seat), CreatedAt: now, UpdatedAt: now,
					})
				}
			}
		case WorkflowStageCouncil:
			actors, err := resolveWorkflowActorSelectors(ctx, s.assignees, baseRequest, definition.Council.Participants, workflowAssigneeResolutionOptions{})
			if err != nil {
				return PreparedWorkflow{}, false, err
			}
			stage.ResolvedParticipants = actors
			if status == WorkflowStageActive {
				stage.DeadlineAt = workflowDeadlineAt(now, time.Duration(definition.Council.DeadlineMinutes)*time.Minute, decision.ExpiresAt)
			}
		}
		prepared.Stages = append(prepared.Stages, stage)
	}
	auditID, err := s.ids.NewIDContext()
	if err != nil {
		return PreparedWorkflow{}, false, err
	}
	prepared.Audit = []WorkflowAuditEvent{{
		ID: auditID, TenantID: decision.TenantID, CaseID: caseID, WorkflowInstanceID: instanceID,
		Actor: decision.Requester, EventType: "workflow.created", IdempotencyKey: "workflow:create:" + decision.IdempotencyKey,
		RequestFingerprint: decision.RequestFingerprint, CreatedAt: now,
	}}
	return prepared, true, nil
}

func workflowSupportsRisk(definition WorkflowDefinition, risk RiskLevel) bool {
	risk = normalizeRisk(risk)
	for _, supported := range definition.RiskLevels {
		if normalizeRisk(supported) == risk {
			return true
		}
	}
	return false
}

func workflowActorsWithoutReserved(actors []ActorRef, reserved map[string]bool) []ActorRef {
	result := make([]ActorRef, 0, len(actors))
	for _, actor := range actors {
		if !reserved[actorKey(actor)] {
			result = append(result, actor)
		}
	}
	return result
}

func (s *WorkflowService) DecideTask(ctx context.Context, tenantID, taskID string, actor ActorRef, input DecideWorkflowTaskInput) (WorkflowInstanceDetail, error) {
	actor = normalizeActor(actor)
	assignee := actor
	if input.delegation != nil {
		if actor.Kind != ActorAgent || input.delegation.Agent != actor || input.delegation.OnBehalfOf.Kind != ActorHuman ||
			input.delegation.TenantID != tenantID || input.delegation.GrantID == "" {
			return WorkflowInstanceDetail{}, ErrParticipantRequired
		}
		assignee = input.delegation.OnBehalfOf
	}
	tenantID = strings.TrimSpace(tenantID)
	taskID = strings.TrimSpace(taskID)
	input.Reason = strings.TrimSpace(input.Reason)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if s == nil || s.repository == nil || s.ids == nil || tenantID == "" || taskID == "" || (actor.Kind != ActorHuman && input.delegation == nil) || actor.ID == "" || input.IdempotencyKey == "" || input.ExpectedInstanceVersion <= 0 || !validWorkflowTaskDecision(input.Decision) {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	if (input.Decision == WorkflowTaskReject || input.Decision == WorkflowTaskRequestChanges) && input.Reason == "" {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	detail, err := s.repository.GetWorkflowInstanceByTaskID(ctx, tenantID, taskID)
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	if input.delegation != nil && (input.delegation.ReviewFingerprint != DecisionReviewFingerprint(detail.Decision) || !s.now().Before(input.delegation.ExpiresAt)) {
		return WorkflowInstanceDetail{}, ErrConflict
	}
	fingerprint := stableFingerprint(struct {
		TenantID string
		TaskID   string
		Actor    ActorRef
		Input    DecideWorkflowTaskInput
	}{tenantID, taskID, actor, input})
	if input.delegation != nil {
		fingerprint = stableFingerprint(struct {
			Command    string
			Delegation *DelegationEvidence
		}{fingerprint, input.delegation})
	}
	if replayed, replayErr := workflowCommandReplay(detail, input.IdempotencyKey, fingerprint); replayErr != nil {
		return WorkflowInstanceDetail{}, replayErr
	} else if replayed {
		s.notifyWorkflowResolution(ctx, detail, actor, input.Reason)
		return detail, nil
	}
	if detail.Instance.Version != input.ExpectedInstanceVersion {
		return WorkflowInstanceDetail{}, ErrConflict
	}
	if detail.Instance.Status != WorkflowRunning || actorKey(detail.Case.Requester) == actorKey(actor) || actorKey(detail.Case.Requester) == actorKey(assignee) {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	taskIndex := workflowTaskIndex(detail.Tasks, taskID)
	if taskIndex < 0 {
		return WorkflowInstanceDetail{}, ErrNotFound
	}
	task := detail.Tasks[taskIndex]
	if task.Status != TaskPending || actorKey(task.CurrentAssignee) != actorKey(assignee) {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	stageIndex := workflowStageIndex(detail.Stages, task.StageInstanceID)
	if stageIndex < 0 || detail.Stages[stageIndex].Status != WorkflowStageActive || detail.Stages[stageIndex].Definition.Type != WorkflowStageApproval || detail.Stages[stageIndex].Definition.Approval == nil {
		return WorkflowInstanceDetail{}, ErrConflict
	}
	now := s.now().UTC()
	if (workflowInstanceExpired(detail, now) || (!task.DeadlineAt.IsZero() && !now.Before(task.DeadlineAt)) || (!detail.Stages[stageIndex].DeadlineAt.IsZero() && !now.Before(detail.Stages[stageIndex].DeadlineAt))) &&
		!workflowTerminalResolutionPrecedesDeadline(detail, task, detail.Stages[stageIndex]) {
		return WorkflowInstanceDetail{}, ErrConflict
	}

	task.Decision = input.Decision
	task.Reason = input.Reason
	task.UpdatedAt = now
	task.CompletedAt = now
	switch input.Decision {
	case WorkflowTaskApprove:
		task.Status = TaskApproved
	case WorkflowTaskReject:
		task.Status = TaskRejected
	case WorkflowTaskRequestChanges:
		task.Status = TaskChangesRequested
	}
	auditID, err := s.ids.NewIDContext()
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	command := WorkflowCommand{
		TenantID: tenantID, InstanceID: detail.Instance.ID, ExpectedInstanceVersion: input.ExpectedInstanceVersion,
		Status: WorkflowRunning, CurrentStageKey: detail.Instance.CurrentStageKey, UpdatedAt: now,
		Tasks: []WorkflowTask{task}, IdempotencyKey: input.IdempotencyKey, RequestFingerprint: fingerprint,
		Audit: []WorkflowAuditEvent{{
			ID: auditID, TenantID: tenantID, CaseID: detail.Case.ID, WorkflowInstanceID: detail.Instance.ID,
			StageInstanceID: task.StageInstanceID, TaskID: task.ID, Actor: actor,
			EventType: "task." + string(input.Decision), Reason: input.Reason,
			IdempotencyKey: input.IdempotencyKey, RequestFingerprint: fingerprint, CreatedAt: now,
		}},
	}
	stage := detail.Stages[stageIndex]
	if input.delegation != nil {
		command.Audit[0].Metadata = map[string]any{"delegation": *input.delegation}
	}
	if input.Decision == WorkflowTaskRequestChanges {
		if detail.Decision.Status != DecisionPending {
			return WorkflowInstanceDetail{}, ErrConflict
		}
		stage.Status = WorkflowStageChangesRequested
		stage.ResolvedAt = now
		command.Stages = append(command.Stages, stage)
		command.Tasks = supersedePendingWorkflowTasks(command.Tasks, detail.Tasks, stage.ID, task.ID, now)
		command.Status = WorkflowChangesRequested
		command.CurrentStageKey = ""
		command.Receipt, err = s.newWorkflowReceipt(detail, actor, input.Reason, EffectRequiresApproval, false, now)
		if err != nil {
			return WorkflowInstanceDetail{}, err
		}
		return s.applyWorkflowCommand(ctx, command, actor, input.Reason)
	}

	statuses := workflowStageTaskStatuses(detail.Tasks, stage.ID, task)
	outcome := ResolveApprovalStage(stage.Definition.Approval.Strategy, stage.Definition.Approval.Threshold, statuses)
	if outcome == StageOpen {
		if detail.Decision.Status != DecisionPending {
			return WorkflowInstanceDetail{}, ErrConflict
		}
		return s.applyWorkflowCommand(ctx, command, actor, input.Reason)
	}
	stage.ResolvedAt = now
	if outcome == StageFailed {
		stage.Status = WorkflowStageFailed
		command.Stages = append(command.Stages, stage)
		command.Tasks = supersedePendingWorkflowTasks(command.Tasks, detail.Tasks, stage.ID, task.ID, now)
		command.Status = WorkflowRejected
		command.CurrentStageKey = ""
		command.DecisionStatus = DecisionDenied
		command.Receipt, err = s.workflowResolutionReceipt(detail, actor, input.Reason, DecisionDenied, EffectDeny, false, now)
		if err != nil {
			return WorkflowInstanceDetail{}, err
		}
		return s.applyWorkflowCommand(ctx, command, actor, input.Reason)
	}

	stage.Status = WorkflowStagePassed
	command.Stages = append(command.Stages, stage)
	if stage.Definition.Approval.Strategy == ApprovalAny || stage.Definition.Approval.Strategy == ApprovalThreshold {
		command.Tasks = supersedePendingWorkflowTasks(command.Tasks, detail.Tasks, stage.ID, task.ID, now)
	}
	nextStageIndex := nextWorkflowStageIndex(detail.Stages, stage.Definition.Order)
	if nextStageIndex >= 0 {
		if detail.Decision.Status != DecisionPending {
			return WorkflowInstanceDetail{}, ErrConflict
		}
		nextStage := detail.Stages[nextStageIndex]
		nextStage.Status = WorkflowStageActive
		nextStage.ActivatedAt = now
		nextStage.DeadlineAt = workflowDeadlineAt(now, workflowStageDeadline(nextStage.Definition), workflowExpiresAt(detail))
		command.Stages = append(command.Stages, nextStage)
		command.CurrentStageKey = nextStage.Definition.Key
		if nextStage.Definition.Type == WorkflowStageCouncil {
			council, councilErr := workflowCouncilForStage(detail.Decision, nextStage)
			if councilErr != nil {
				return WorkflowInstanceDetail{}, councilErr
			}
			command.CouncilToCreate = &council
		} else {
			for _, nextTask := range detail.Tasks {
				if nextTask.StageInstanceID != nextStage.ID || nextTask.Status != TaskScheduled {
					continue
				}
				nextTask.Status = TaskPending
				nextTask.DeadlineAt = nextStage.DeadlineAt
				nextTask.UpdatedAt = now
				command.Tasks = append(command.Tasks, nextTask)
				notification, notifyErr := s.newWorkflowNotification(detail.Instance.ID, nextStage.ID, nextTask, "task.assigned", now)
				if notifyErr != nil {
					return WorkflowInstanceDetail{}, notifyErr
				}
				command.Notifications = append(command.Notifications, notification)
			}
		}
		return s.applyWorkflowCommand(ctx, command, actor, input.Reason)
	}

	command.Status = WorkflowApproved
	command.CurrentStageKey = ""
	command.DecisionStatus = DecisionResolved
	command.Receipt, err = s.workflowResolutionReceipt(detail, actor, input.Reason, DecisionResolved, EffectAuthorize, true, now)
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	return s.applyWorkflowCommand(ctx, command, actor, input.Reason)
}

func (s *WorkflowService) TransferTask(ctx context.Context, tenantID, taskID string, actor ActorRef, input TransferWorkflowTaskInput) (WorkflowInstanceDetail, error) {
	return s.changeTaskAssignee(ctx, tenantID, taskID, actor, input, false)
}

func (s *WorkflowService) ReassignTask(ctx context.Context, tenantID, taskID string, actor ActorRef, input ReassignWorkflowTaskInput) (WorkflowInstanceDetail, error) {
	return s.changeTaskAssignee(ctx, tenantID, taskID, actor, input, true)
}

func (s *WorkflowService) changeTaskAssignee(ctx context.Context, tenantID, taskID string, actor ActorRef, input TransferWorkflowTaskInput, administrator bool) (WorkflowInstanceDetail, error) {
	actor = normalizeActor(actor)
	input.NewAssignee = normalizeActor(input.NewAssignee)
	tenantID = strings.TrimSpace(tenantID)
	taskID = strings.TrimSpace(taskID)
	input.Reason = strings.TrimSpace(input.Reason)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if s == nil || s.repository == nil || s.ids == nil || tenantID == "" || taskID == "" || actor.Kind != ActorHuman || actor.ID == "" || input.NewAssignee.Kind != ActorHuman || input.NewAssignee.ID == "" || input.Reason == "" || input.IdempotencyKey == "" || input.ExpectedInstanceVersion <= 0 {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	detail, err := s.repository.GetWorkflowInstanceByTaskID(ctx, tenantID, taskID)
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	fingerprint := stableFingerprint(struct {
		TenantID      string
		TaskID        string
		Actor         ActorRef
		Input         TransferWorkflowTaskInput
		Administrator bool
	}{tenantID, taskID, actor, input, administrator})
	if replayed, replayErr := workflowCommandReplay(detail, input.IdempotencyKey, fingerprint); replayErr != nil {
		return WorkflowInstanceDetail{}, replayErr
	} else if replayed {
		return detail, nil
	}
	if detail.Instance.Version != input.ExpectedInstanceVersion {
		return WorkflowInstanceDetail{}, ErrConflict
	}
	if detail.Instance.Status != WorkflowRunning || actorKey(input.NewAssignee) == actorKey(detail.Case.Requester) {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	taskIndex := workflowTaskIndex(detail.Tasks, taskID)
	if taskIndex < 0 {
		return WorkflowInstanceDetail{}, ErrNotFound
	}
	task := detail.Tasks[taskIndex]
	if task.Status != TaskPending || actorKey(task.CurrentAssignee) == actorKey(input.NewAssignee) {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	if !administrator {
		if actorKey(task.CurrentAssignee) != actorKey(actor) || task.TransferCount != 0 || actorKey(task.OriginalAssignee) == actorKey(input.NewAssignee) {
			return WorkflowInstanceDetail{}, ErrInvalidRequest
		}
	}
	stageIndex := workflowStageIndex(detail.Stages, task.StageInstanceID)
	if stageIndex < 0 || detail.Stages[stageIndex].Status != WorkflowStageActive {
		return WorkflowInstanceDetail{}, ErrConflict
	}
	now := s.now().UTC()
	if workflowInstanceExpired(detail, now) || (!task.DeadlineAt.IsZero() && !now.Before(task.DeadlineAt)) || (!detail.Stages[stageIndex].DeadlineAt.IsZero() && !now.Before(detail.Stages[stageIndex].DeadlineAt)) {
		return WorkflowInstanceDetail{}, ErrConflict
	}
	previousAssignee := task.CurrentAssignee
	task.CurrentAssignee = input.NewAssignee
	task.UpdatedAt = now
	eventType := "task.reassigned"
	if !administrator {
		task.TransferCount++
		task.TransferredFromID = previousAssignee.ID
		task.TransferredToID = input.NewAssignee.ID
		task.TransferReason = input.Reason
		eventType = "task.transferred"
	}
	auditID, err := s.ids.NewIDContext()
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	notification, err := s.newWorkflowNotification(detail.Instance.ID, task.StageInstanceID, task, eventType, now)
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	notification.IdempotencyKey += ":" + input.IdempotencyKey
	return s.applyWorkflowCommand(ctx, WorkflowCommand{
		TenantID: tenantID, InstanceID: detail.Instance.ID, ExpectedInstanceVersion: input.ExpectedInstanceVersion,
		Status: WorkflowRunning, CurrentStageKey: detail.Instance.CurrentStageKey, UpdatedAt: now,
		Tasks: []WorkflowTask{task}, Notifications: []WorkflowNotification{notification},
		Audit: []WorkflowAuditEvent{{
			ID: auditID, TenantID: tenantID, CaseID: detail.Case.ID, WorkflowInstanceID: detail.Instance.ID,
			StageInstanceID: task.StageInstanceID, TaskID: task.ID, Actor: actor, EventType: eventType,
			Reason: input.Reason, Metadata: map[string]any{"from": previousAssignee, "to": input.NewAssignee},
			IdempotencyKey: input.IdempotencyKey, RequestFingerprint: fingerprint, CreatedAt: now,
		}},
		IdempotencyKey: input.IdempotencyKey, RequestFingerprint: fingerprint,
	}, actor, input.Reason)
}

func workflowCommandReplay(detail WorkflowInstanceDetail, idempotencyKey, fingerprint string) (bool, error) {
	for _, event := range detail.Audit {
		if event.IdempotencyKey != idempotencyKey {
			continue
		}
		if event.RequestFingerprint != fingerprint {
			return false, ErrIdempotencyConflict
		}
		return true, nil
	}
	return false, nil
}

func (s *WorkflowService) Resubmit(ctx context.Context, tenantID, instanceID string, actor ActorRef, input ResubmitWorkflowInput) (WorkflowInstanceDetail, error) {
	actor = normalizeActor(actor)
	tenantID = strings.TrimSpace(tenantID)
	instanceID = strings.TrimSpace(instanceID)
	input.ArgsHash = strings.TrimSpace(input.ArgsHash)
	input.MaterialHash = strings.TrimSpace(input.MaterialHash)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if s == nil || s.repository == nil || s.ids == nil || tenantID == "" || instanceID == "" || actor.Kind == "" || actor.ID == "" || input.ArgsHash == "" || input.MaterialHash == "" || input.IdempotencyKey == "" || input.ExpectedInstanceVersion <= 0 || input.ExpiresAt.IsZero() {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	fingerprint := stableFingerprint(struct {
		TenantID   string
		InstanceID string
		Actor      ActorRef
		Input      ResubmitWorkflowInput
	}{tenantID, instanceID, actor, input})
	if existing, err := s.repository.GetDecisionByIdempotencyKey(ctx, tenantID, input.IdempotencyKey); err == nil {
		if existing.RequestFingerprint != fingerprint {
			return WorkflowInstanceDetail{}, ErrIdempotencyConflict
		}
		return s.repository.GetWorkflowInstanceByDecisionID(ctx, tenantID, existing.ID)
	} else if !errors.Is(err, ErrNotFound) {
		return WorkflowInstanceDetail{}, err
	}
	previous, err := s.repository.GetWorkflowInstance(ctx, tenantID, instanceID)
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	if previous.Instance.Version != input.ExpectedInstanceVersion {
		return WorkflowInstanceDetail{}, ErrConflict
	}
	if previous.Instance.Status != WorkflowChangesRequested || actorKey(previous.Case.Requester) != actorKey(actor) || previous.Instance.ArgsHash == input.ArgsHash || previous.Instance.MaterialHash == input.MaterialHash {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	now := s.now().UTC()
	if !input.ExpiresAt.After(now) {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	decisionID, err := s.ids.NewIDContext()
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	newInstanceID, err := s.ids.NewIDContext()
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	payloadPreview := cloneMap(input.PayloadPreview)
	if payloadPreview == nil {
		payloadPreview = map[string]any{}
	}
	payloadPreview["material_hash"] = input.MaterialHash
	decision := previous.Decision
	decision.ID = decisionID
	decision.ArgsHash = input.ArgsHash
	decision.PayloadPreview = payloadPreview
	decision.IdempotencyKey = input.IdempotencyKey
	decision.RequestFingerprint = fingerprint
	decision.Status = DecisionPending
	decision.CouncilID = ""
	decision.CreatedAt = now
	decision.ExpiresAt = input.ExpiresAt.UTC()
	instance := WorkflowInstance{
		ID: newInstanceID, TenantID: tenantID, CaseID: previous.Case.ID, Revision: previous.Instance.Revision + 1,
		DecisionRequestID: decisionID, TemplateVersionID: previous.Instance.TemplateVersionID,
		PredecessorInstanceID: previous.Instance.ID, DefinitionHash: previous.Instance.DefinitionHash,
		MaterialHash: input.MaterialHash, ArgsHash: input.ArgsHash, Status: WorkflowRunning,
		CurrentStageKey: previous.Stages[0].Definition.Key, Version: 1, CreatedAt: now, UpdatedAt: now,
		ExpiresAt: input.ExpiresAt.UTC(),
	}
	stages := make([]WorkflowStageInstance, 0, len(previous.Stages))
	tasks := make([]WorkflowTask, 0)
	notifications := make([]WorkflowNotification, 0)
	for stageIndex, priorStage := range previous.Stages {
		stageID, idErr := s.ids.NewIDContext()
		if idErr != nil {
			return WorkflowInstanceDetail{}, idErr
		}
		status := WorkflowStageScheduled
		if stageIndex == 0 {
			status = WorkflowStageActive
		}
		stage := WorkflowStageInstance{
			ID: stageID, TenantID: tenantID, WorkflowInstanceID: newInstanceID, Definition: priorStage.Definition,
			ResolvedParticipants:     append([]ActorRef(nil), priorStage.ResolvedParticipants...),
			ResolvedEscalationActors: append([]ActorRef(nil), priorStage.ResolvedEscalationActors...),
			Status:                   status, EscalationIndex: -1, CreatedAt: now,
		}
		if status == WorkflowStageActive {
			stage.ActivatedAt = now
			stage.DeadlineAt = workflowDeadlineAt(now, workflowStageDeadline(stage.Definition), input.ExpiresAt)
		}
		stages = append(stages, stage)
		if stage.Definition.Type != WorkflowStageApproval {
			continue
		}
		for _, assignee := range stage.ResolvedParticipants {
			taskID, taskIDErr := s.ids.NewIDContext()
			if taskIDErr != nil {
				return WorkflowInstanceDetail{}, taskIDErr
			}
			taskStatus := TaskScheduled
			if status == WorkflowStageActive {
				taskStatus = TaskPending
			}
			task := WorkflowTask{
				ID: taskID, TenantID: tenantID, WorkflowInstanceID: newInstanceID, StageInstanceID: stageID,
				SeatKey: stage.Definition.Key + ":" + actorKey(assignee), OriginalAssignee: assignee,
				CurrentAssignee: assignee, Status: taskStatus, DeadlineAt: stage.DeadlineAt, CreatedAt: now, UpdatedAt: now,
			}
			tasks = append(tasks, task)
			if taskStatus == TaskPending {
				notification, notificationErr := s.newWorkflowNotification(newInstanceID, stageID, task, "task.resubmitted", now)
				if notificationErr != nil {
					return WorkflowInstanceDetail{}, notificationErr
				}
				notifications = append(notifications, notification)
			}
		}
	}
	auditID, err := s.ids.NewIDContext()
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	audit := []WorkflowAuditEvent{{
		ID: auditID, TenantID: tenantID, CaseID: previous.Case.ID, WorkflowInstanceID: newInstanceID,
		Actor: actor, EventType: "workflow.resubmitted", Metadata: map[string]any{"predecessor_instance_id": previous.Instance.ID},
		IdempotencyKey: input.IdempotencyKey, RequestFingerprint: fingerprint, CreatedAt: now,
	}}
	if err := s.repository.CreateDecisionWorkflow(ctx, decision, previous.Case, instance, stages, tasks, audit, notifications); err != nil {
		return WorkflowInstanceDetail{}, err
	}
	s.notifyPending()
	return s.repository.GetWorkflowInstance(ctx, tenantID, newInstanceID)
}

func (s *WorkflowService) SubmitCouncilContribution(ctx context.Context, tenantID, councilID string, actor ActorRef, input SubmitContributionInput) (WorkflowInstanceDetail, error) {
	actor = normalizeActor(actor)
	tenantID = strings.TrimSpace(tenantID)
	councilID = strings.TrimSpace(councilID)
	input.Position = strings.TrimSpace(input.Position)
	input.Reasoning = strings.TrimSpace(input.Reasoning)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if s == nil || s.repository == nil || tenantID == "" || councilID == "" || actor.Kind == "" || actor.ID == "" || input.Position == "" || input.Reasoning == "" || input.IdempotencyKey == "" {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	detail, err := s.repository.GetWorkflowInstanceByCouncilID(ctx, tenantID, councilID)
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	stageIndex := workflowStageIndex(detail.Stages, councilID)
	if detail.Instance.Status != WorkflowRunning || stageIndex < 0 || detail.Stages[stageIndex].Status != WorkflowStageActive {
		return WorkflowInstanceDetail{}, ErrConflict
	}
	now := s.now().UTC()
	if workflowInstanceExpired(detail, now) || (!detail.Stages[stageIndex].DeadlineAt.IsZero() && !now.Before(detail.Stages[stageIndex].DeadlineAt)) {
		return WorkflowInstanceDetail{}, ErrConflict
	}
	session, err := s.repository.GetCouncil(ctx, tenantID, councilID)
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	if !councilHasParticipant(session, actor) {
		return WorkflowInstanceDetail{}, ErrParticipantRequired
	}
	contribution := CouncilContribution{
		Actor: actor, Position: input.Position, Reasoning: input.Reasoning,
		EvidenceRefs: normalizeStrings(input.EvidenceRefs), CreatedAt: now, UpdatedAt: now,
	}
	fingerprint := stableFingerprint(struct {
		CouncilID   string
		Actor       ActorRef
		Position    string
		Reasoning   string
		EvidenceRef []string
	}{councilID, actor, contribution.Position, contribution.Reasoning, contribution.EvidenceRefs})
	if err := s.repository.SaveContribution(ctx, tenantID, councilID, contribution, input.IdempotencyKey, fingerprint); err != nil {
		return WorkflowInstanceDetail{}, err
	}
	return s.repository.GetWorkflowInstance(ctx, tenantID, detail.Instance.ID)
}

func (s *WorkflowService) CastCouncilVote(ctx context.Context, tenantID, councilID string, actor ActorRef, input CastVoteInput) (WorkflowInstanceDetail, error) {
	actor = normalizeActor(actor)
	tenantID = strings.TrimSpace(tenantID)
	councilID = strings.TrimSpace(councilID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.Reason = strings.TrimSpace(input.Reason)
	if s == nil || s.repository == nil || s.ids == nil || tenantID == "" || councilID == "" || actor.Kind == "" || actor.ID == "" || input.IdempotencyKey == "" || !validVoteChoice(input.Choice) {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	fingerprint := stableFingerprint(struct {
		CouncilID string
		Actor     ActorRef
		Choice    VoteChoice
		Reason    string
	}{councilID, actor, input.Choice, input.Reason})
	detail, err := s.repository.GetWorkflowInstanceByCouncilID(ctx, tenantID, councilID)
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	for _, event := range detail.Audit {
		if event.IdempotencyKey != input.IdempotencyKey {
			continue
		}
		if event.RequestFingerprint != fingerprint {
			return WorkflowInstanceDetail{}, ErrIdempotencyConflict
		}
		s.notifyWorkflowResolution(ctx, detail, actor, input.Reason)
		return detail, nil
	}
	stageIndex := workflowStageIndex(detail.Stages, councilID)
	if detail.Instance.Status != WorkflowRunning || stageIndex < 0 || detail.Stages[stageIndex].Status != WorkflowStageActive {
		return WorkflowInstanceDetail{}, ErrConflict
	}
	now := s.now().UTC()
	if workflowInstanceExpired(detail, now) || (!detail.Stages[stageIndex].DeadlineAt.IsZero() && !now.Before(detail.Stages[stageIndex].DeadlineAt)) {
		return WorkflowInstanceDetail{}, ErrConflict
	}
	session, err := s.repository.GetCouncil(ctx, tenantID, councilID)
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	if !councilHasParticipant(session, actor) {
		return WorkflowInstanceDetail{}, ErrParticipantRequired
	}
	if !councilHasContribution(session, actor) {
		return WorkflowInstanceDetail{}, ErrContributionRequired
	}
	for _, existingVote := range session.Votes {
		if actorKey(existingVote.Actor) == actorKey(actor) {
			return WorkflowInstanceDetail{}, ErrVoteImmutable
		}
	}
	vote := CouncilVote{Actor: actor, Choice: input.Choice, Reason: input.Reason, CreatedAt: now}
	session.Votes = append(session.Votes, vote)
	resolution, err := ResolveCouncil(session, now)
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	auditID, err := s.ids.NewIDContext()
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	command := WorkflowCommand{
		TenantID: tenantID, InstanceID: detail.Instance.ID, ExpectedInstanceVersion: detail.Instance.Version,
		Status: WorkflowRunning, CurrentStageKey: detail.Instance.CurrentStageKey, UpdatedAt: now,
		CouncilVote: &vote, CouncilID: councilID, IdempotencyKey: input.IdempotencyKey, RequestFingerprint: fingerprint,
		Audit: []WorkflowAuditEvent{{
			ID: auditID, TenantID: tenantID, CaseID: detail.Case.ID, WorkflowInstanceID: detail.Instance.ID,
			StageInstanceID: councilID, Actor: actor, EventType: "council.voted", Reason: input.Reason,
			Metadata: map[string]any{"choice": input.Choice}, IdempotencyKey: input.IdempotencyKey,
			RequestFingerprint: fingerprint, CreatedAt: now,
		}},
	}
	if resolution.Status == CouncilOpen {
		return s.repository.ApplyWorkflowCommand(ctx, command)
	}
	session.Status = resolution.Status
	session.Effect = resolution.Effect
	session.Dissent = append([]ActorRef(nil), resolution.Dissent...)
	session.ResolvedAt = resolution.ResolvedAt
	session.UpdatedAt = resolution.ResolvedAt
	command.CouncilToResolve = &session
	stage := detail.Stages[stageIndex]
	stage.ResolvedAt = now
	if resolution.Status == CouncilRejected || resolution.Status == CouncilExpired {
		stage.Status = WorkflowStageFailed
		command.Stages = []WorkflowStageInstance{stage}
		command.Status = WorkflowRejected
		command.CurrentStageKey = ""
		command.DecisionStatus = DecisionDenied
		command.Receipt, err = s.newWorkflowReceipt(detail, actor, input.Reason, EffectDeny, false, now)
		if err != nil {
			return WorkflowInstanceDetail{}, err
		}
		command.Receipt.CouncilID = councilID
		return s.applyWorkflowCommand(ctx, command, actor, input.Reason)
	}
	stage.Status = WorkflowStagePassed
	command.Stages = []WorkflowStageInstance{stage}
	nextStageIndex := nextWorkflowStageIndex(detail.Stages, stage.Definition.Order)
	if nextStageIndex < 0 {
		command.Status = WorkflowApproved
		command.CurrentStageKey = ""
		command.DecisionStatus = DecisionResolved
		command.Receipt, err = s.newWorkflowReceipt(detail, actor, input.Reason, EffectAuthorize, true, now)
		if err != nil {
			return WorkflowInstanceDetail{}, err
		}
		command.Receipt.CouncilID = councilID
		return s.applyWorkflowCommand(ctx, command, actor, input.Reason)
	}
	nextStage := detail.Stages[nextStageIndex]
	nextStage.Status = WorkflowStageActive
	nextStage.ActivatedAt = now
	nextStage.DeadlineAt = workflowDeadlineAt(now, workflowStageDeadline(nextStage.Definition), workflowExpiresAt(detail))
	command.Stages = append(command.Stages, nextStage)
	command.CurrentStageKey = nextStage.Definition.Key
	if nextStage.Definition.Type == WorkflowStageCouncil {
		council, councilErr := workflowCouncilForStage(detail.Decision, nextStage)
		if councilErr != nil {
			return WorkflowInstanceDetail{}, councilErr
		}
		command.CouncilToCreate = &council
	} else {
		for _, nextTask := range detail.Tasks {
			if nextTask.StageInstanceID != nextStage.ID || nextTask.Status != TaskScheduled {
				continue
			}
			nextTask.Status = TaskPending
			nextTask.DeadlineAt = nextStage.DeadlineAt
			nextTask.UpdatedAt = now
			command.Tasks = append(command.Tasks, nextTask)
			notification, notificationErr := s.newWorkflowNotification(detail.Instance.ID, nextStage.ID, nextTask, "task.assigned", now)
			if notificationErr != nil {
				return WorkflowInstanceDetail{}, notificationErr
			}
			command.Notifications = append(command.Notifications, notification)
		}
	}
	command.Receipt, err = s.newWorkflowReceipt(detail, actor, input.Reason, EffectRequiresApproval, false, now)
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	command.Receipt.CouncilID = councilID
	return s.applyWorkflowCommand(ctx, command, actor, input.Reason)
}

func (s *WorkflowService) SweepDue(ctx context.Context, tenantID string, now time.Time, limit int) (WorkflowSweepResult, error) {
	if s == nil || s.repository == nil || s.ids == nil || strings.TrimSpace(tenantID) == "" || now.IsZero() {
		return WorkflowSweepResult{}, ErrInvalidRequest
	}
	now = now.UTC()
	items, err := s.repository.ClaimDueWorkflowWork(ctx, strings.TrimSpace(tenantID), now, limit)
	if err != nil {
		return WorkflowSweepResult{}, err
	}
	result := WorkflowSweepResult{}
	for _, item := range items {
		detail, getErr := s.repository.GetWorkflowInstance(ctx, item.Instance.TenantID, item.Instance.ID)
		if getErr != nil {
			return result, getErr
		}
		stageIndex := workflowStageIndex(detail.Stages, item.Stage.ID)
		if stageIndex < 0 || detail.Stages[stageIndex].Status != WorkflowStageActive || detail.Instance.Status != WorkflowRunning {
			result.Conflicts++
			continue
		}
		stage := detail.Stages[stageIndex]
		stage.DeadlineAt = workflowEffectiveDeadline(detail, stage)
		if _, assignmentErr := s.enqueueMissingWorkflowAssignments(ctx, detail, stage, now); assignmentErr != nil {
			return result, assignmentErr
		}
		if now.Before(stage.DeadlineAt) {
			count, reminderErr := s.enqueueDueWorkflowReminders(ctx, detail, stage, now)
			if reminderErr != nil {
				return result, reminderErr
			}
			result.Reminders += count
			continue
		}
		command, escalated, commandErr := s.deadlineWorkflowCommand(detail, stage, now)
		if commandErr != nil {
			return result, commandErr
		}
		if _, applyErr := s.applyWorkflowCommand(ctx, command, ActorRef{Kind: ActorService, ID: "workflow-sweeper"}, "approval deadline expired"); applyErr != nil {
			if errors.Is(applyErr, ErrConflict) {
				result.Conflicts++
				continue
			}
			return result, applyErr
		}
		if escalated {
			result.Escalations++
		} else {
			result.Expired++
		}
	}
	return result, nil
}

func (s *WorkflowService) enqueueDueWorkflowReminders(ctx context.Context, detail WorkflowInstanceDetail, stage WorkflowStageInstance, now time.Time) (int, error) {
	if stage.Definition.Approval == nil {
		return 0, nil
	}
	existing := make(map[string]struct{}, len(detail.Notifications))
	for _, notification := range detail.Notifications {
		existing[notification.IdempotencyKey] = struct{}{}
	}
	notifications := make([]WorkflowNotification, 0)
	for _, offset := range stage.Definition.Approval.ReminderOffsetsMinutes {
		if offset <= 0 || now.Before(stage.DeadlineAt.Add(-time.Duration(offset)*time.Minute)) {
			continue
		}
		baseKey := workflowReminderIdempotencyKey(detail.Instance.ID, stage.ID, offset)
		for _, task := range detail.Tasks {
			if task.StageInstanceID != stage.ID || task.Status != TaskPending {
				continue
			}
			key := baseKey + ":task:" + task.ID
			if _, found := existing[key]; found {
				continue
			}
			id, err := s.ids.NewIDContext()
			if err != nil {
				return 0, err
			}
			notifications = append(notifications, WorkflowNotification{
				ID: id, TenantID: detail.Instance.TenantID, WorkflowInstanceID: detail.Instance.ID,
				StageInstanceID: stage.ID, TaskID: task.ID, Channel: s.notificationChannel, Recipient: task.CurrentAssignee,
				EventType: "task.reminder", Payload: map[string]any{"deadline_at": stage.DeadlineAt, "offset_minutes": offset},
				Status: WorkflowNotificationPending, NextAttemptAt: now,
				IdempotencyKey: key, CreatedAt: now, UpdatedAt: now,
			})
		}
	}
	if len(notifications) == 0 {
		return 0, nil
	}
	if err := s.repository.EnqueueWorkflowNotifications(ctx, notifications); err != nil {
		return 0, err
	}
	return len(notifications), nil
}

func (s *WorkflowService) enqueueMissingWorkflowAssignments(ctx context.Context, detail WorkflowInstanceDetail, stage WorkflowStageInstance, now time.Time) (int, error) {
	assigned := make(map[string]struct{}, len(detail.Notifications))
	for _, notification := range detail.Notifications {
		if workflowAssignmentNotification(notification.EventType) {
			assigned[notification.TaskID] = struct{}{}
		}
	}
	notifications := make([]WorkflowNotification, 0)
	for _, task := range detail.Tasks {
		if task.StageInstanceID != stage.ID || task.Status != TaskPending {
			continue
		}
		if _, found := assigned[task.ID]; found {
			continue
		}
		notification, err := s.newWorkflowNotification(detail.Instance.ID, stage.ID, task, "task.assigned", now)
		if err != nil {
			return 0, err
		}
		notification.Payload = map[string]any{"recovered": true}
		notifications = append(notifications, notification)
	}
	if len(notifications) == 0 {
		return 0, nil
	}
	if err := s.repository.EnqueueWorkflowNotifications(ctx, notifications); err != nil {
		return 0, err
	}
	return len(notifications), nil
}

func workflowAssignmentNotification(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "task.assigned", "task.escalated", "task.transferred", "task.reassigned":
		return true
	default:
		return false
	}
}

func (s *WorkflowService) deadlineWorkflowCommand(detail WorkflowInstanceDetail, stage WorkflowStageInstance, now time.Time) (WorkflowCommand, bool, error) {
	nextEscalation := stage.EscalationIndex + 1
	instanceExpired := workflowInstanceExpired(detail, now)
	canEscalate := !instanceExpired && stage.Definition.Type == WorkflowStageApproval && stage.Definition.Approval != nil && nextEscalation >= 0 && nextEscalation < len(stage.ResolvedEscalationActors) && nextEscalation < len(stage.Definition.Approval.Escalation)
	action := "expired"
	if canEscalate {
		action = "escalated"
	}
	idempotencyKey := "workflow:sweep:" + detail.Instance.ID + ":stage:" + stage.ID + ":" + action + ":" + strconv.Itoa(nextEscalation)
	fingerprint := stableFingerprint(struct {
		InstanceID      string
		StageID         string
		Version         int64
		EscalationIndex int
		Action          string
	}{detail.Instance.ID, stage.ID, detail.Instance.Version, stage.EscalationIndex, action})
	auditID, err := s.ids.NewIDContext()
	if err != nil {
		return WorkflowCommand{}, false, err
	}
	command := WorkflowCommand{
		TenantID: detail.Instance.TenantID, InstanceID: detail.Instance.ID,
		ExpectedInstanceVersion: detail.Instance.Version, Status: WorkflowRunning,
		CurrentStageKey: detail.Instance.CurrentStageKey, UpdatedAt: now,
		IdempotencyKey: idempotencyKey, RequestFingerprint: fingerprint,
		Audit: []WorkflowAuditEvent{{
			ID: auditID, TenantID: detail.Instance.TenantID, CaseID: detail.Case.ID,
			WorkflowInstanceID: detail.Instance.ID, StageInstanceID: stage.ID,
			Actor: ActorRef{Kind: ActorService, ID: "workflow-sweeper"}, EventType: "stage." + action,
			IdempotencyKey: idempotencyKey, RequestFingerprint: fingerprint, CreatedAt: now,
		}},
	}
	for _, task := range detail.Tasks {
		if task.StageInstanceID != stage.ID || task.Status != TaskPending {
			continue
		}
		task.Status = TaskExpired
		task.UpdatedAt = now
		task.CompletedAt = now
		command.Tasks = append(command.Tasks, task)
	}
	if canEscalate {
		stage.EscalationIndex = nextEscalation
		stage.DeadlineAt = workflowDeadlineAt(now, time.Duration(stage.Definition.Approval.Escalation[nextEscalation].DeadlineMinutes)*time.Minute, workflowExpiresAt(detail))
		command.Stages = []WorkflowStageInstance{stage}
		actor := stage.ResolvedEscalationActors[nextEscalation]
		taskID, taskErr := s.ids.NewIDContext()
		if taskErr != nil {
			return WorkflowCommand{}, false, taskErr
		}
		task := WorkflowTask{
			ID: taskID, TenantID: detail.Instance.TenantID, WorkflowInstanceID: detail.Instance.ID,
			StageInstanceID: stage.ID, SeatKey: stage.Definition.Key + ":escalation:" + strconv.Itoa(nextEscalation),
			OriginalAssignee: actor, CurrentAssignee: actor, Status: TaskPending, DeadlineAt: stage.DeadlineAt,
			CreatedAt: now, UpdatedAt: now,
		}
		command.NewTasks = []WorkflowTask{task}
		notification, notificationErr := s.newWorkflowNotification(detail.Instance.ID, stage.ID, task, "task.escalated", now)
		if notificationErr != nil {
			return WorkflowCommand{}, false, notificationErr
		}
		command.Notifications = []WorkflowNotification{notification}
		return command, true, nil
	}
	stage.Status = WorkflowStageExpired
	stage.ResolvedAt = now
	command.Stages = []WorkflowStageInstance{stage}
	if stage.Definition.Type == WorkflowStageCouncil {
		command.CouncilToResolve = &CouncilSession{
			ID: stage.ID, TenantID: detail.Instance.TenantID, Status: CouncilExpired,
			Effect: EffectDeny, UpdatedAt: now, ResolvedAt: now,
		}
	}
	command.Status = WorkflowExpired
	command.CurrentStageKey = ""
	command.DecisionStatus = DecisionDenied
	command.Receipt, err = s.newWorkflowReceipt(detail, ActorRef{Kind: ActorService, ID: "workflow-sweeper"}, "approval deadline expired", EffectDeny, false, now)
	if err != nil {
		return WorkflowCommand{}, false, err
	}
	return command, false, nil
}

func validWorkflowTaskDecision(decision WorkflowTaskDecision) bool {
	return decision == WorkflowTaskApprove || decision == WorkflowTaskReject || decision == WorkflowTaskRequestChanges
}

func workflowTaskIndex(tasks []WorkflowTask, taskID string) int {
	for index := range tasks {
		if tasks[index].ID == taskID {
			return index
		}
	}
	return -1
}

func workflowStageIndex(stages []WorkflowStageInstance, stageID string) int {
	for index := range stages {
		if stages[index].ID == stageID {
			return index
		}
	}
	return -1
}

func nextWorkflowStageIndex(stages []WorkflowStageInstance, currentOrder int) int {
	selected := -1
	for index := range stages {
		if stages[index].Status != WorkflowStageScheduled || stages[index].Definition.Order <= currentOrder {
			continue
		}
		if selected < 0 || stages[index].Definition.Order < stages[selected].Definition.Order {
			selected = index
		}
	}
	return selected
}

func workflowStageTaskStatuses(tasks []WorkflowTask, stageID string, changed WorkflowTask) []WorkflowTaskStatus {
	statuses := make([]WorkflowTaskStatus, 0)
	for _, task := range tasks {
		if task.StageInstanceID != stageID {
			continue
		}
		if task.ID == changed.ID {
			statuses = append(statuses, changed.Status)
		} else {
			statuses = append(statuses, task.Status)
		}
	}
	return statuses
}

func supersedePendingWorkflowTasks(updated []WorkflowTask, tasks []WorkflowTask, stageID, exceptTaskID string, now time.Time) []WorkflowTask {
	for _, task := range tasks {
		if task.StageInstanceID != stageID || task.ID == exceptTaskID || task.Status != TaskPending {
			continue
		}
		task.Status = TaskSuperseded
		task.UpdatedAt = now
		task.CompletedAt = now
		updated = append(updated, task)
	}
	return updated
}

func workflowStageDeadline(definition WorkflowStageDefinition) time.Duration {
	if definition.Approval != nil {
		return time.Duration(definition.Approval.DeadlineMinutes) * time.Minute
	}
	if definition.Council != nil {
		return time.Duration(definition.Council.DeadlineMinutes) * time.Minute
	}
	return 0
}

func workflowDeadlineAt(now time.Time, duration time.Duration, expiresAt time.Time) time.Time {
	deadline := now.Add(duration)
	if !expiresAt.IsZero() && expiresAt.Before(deadline) {
		return expiresAt.UTC()
	}
	return deadline.UTC()
}

func workflowEffectiveDeadline(detail WorkflowInstanceDetail, stage WorkflowStageInstance) time.Time {
	deadline := stage.DeadlineAt
	expiresAt := workflowExpiresAt(detail)
	if !expiresAt.IsZero() && (deadline.IsZero() || expiresAt.Before(deadline)) {
		return expiresAt.UTC()
	}
	return deadline.UTC()
}

func workflowExpiresAt(detail WorkflowInstanceDetail) time.Time {
	expiresAt := detail.Instance.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = detail.Decision.ExpiresAt
	}
	return expiresAt.UTC()
}

func workflowInstanceExpired(detail WorkflowInstanceDetail, now time.Time) bool {
	expiresAt := workflowExpiresAt(detail)
	return !expiresAt.IsZero() && !now.Before(expiresAt)
}

func (s *WorkflowService) newWorkflowNotification(instanceID, stageID string, task WorkflowTask, eventType string, now time.Time) (WorkflowNotification, error) {
	id, err := s.ids.NewIDContext()
	if err != nil {
		return WorkflowNotification{}, err
	}
	return WorkflowNotification{
		ID: id, TenantID: task.TenantID, WorkflowInstanceID: instanceID, StageInstanceID: stageID,
		TaskID: task.ID, Channel: s.notificationChannel, Recipient: task.CurrentAssignee, EventType: eventType,
		Status: WorkflowNotificationPending, NextAttemptAt: now,
		IdempotencyKey: "workflow:" + instanceID + ":task:" + task.ID + ":" + eventType,
		CreatedAt:      now, UpdatedAt: now,
	}, nil
}

func validWorkflowTemplateActor(actor ActorRef) bool {
	return actor.ID != "" && (actor.Kind == ActorHuman || actor.Kind == ActorService)
}

func (s *WorkflowService) newWorkflowReceipt(detail WorkflowInstanceDetail, actor ActorRef, reason string, effect DecisionEffect, singleUse bool, now time.Time) (*DecisionReceipt, error) {
	id, err := s.ids.NewIDContext()
	if err != nil {
		return nil, err
	}
	return &DecisionReceipt{
		ID: id, TenantID: detail.Instance.TenantID, RequestID: detail.Instance.DecisionRequestID,
		WorkflowInstanceID: detail.Instance.ID, ArgsHash: detail.Instance.ArgsHash, MaterialHash: detail.Instance.MaterialHash,
		Effect: effect, ResolverActorKind: actor.Kind, ResolverActorID: actor.ID, ResolverReason: reason,
		ResolverSummary: map[string]any{"final_actor": actor, "final_reason": reason}, SingleUse: singleUse,
		CreatedAt: now, ExpiresAt: detail.Instance.ExpiresAt,
	}, nil
}

func (s *WorkflowService) workflowResolutionReceipt(detail WorkflowInstanceDetail, actor ActorRef, reason string, status DecisionStatus, effect DecisionEffect, singleUse bool, now time.Time) (*DecisionReceipt, error) {
	if detail.Decision.Status == DecisionPending {
		return s.newWorkflowReceipt(detail, actor, reason, effect, singleUse, now)
	}
	if detail.Decision.Status != status {
		return nil, ErrConflict
	}
	for index := len(detail.Receipts) - 1; index >= 0; index-- {
		receipt := detail.Receipts[index]
		if receipt.RequestID == detail.Decision.ID && receipt.Effect == effect {
			return nil, nil
		}
	}
	return nil, ErrConflict
}

func workflowTerminalResolutionPrecedesDeadline(detail WorkflowInstanceDetail, task WorkflowTask, stage WorkflowStageInstance) bool {
	effect := EffectDeny
	if detail.Decision.Status == DecisionResolved {
		effect = EffectAuthorize
	} else if detail.Decision.Status != DecisionDenied {
		return false
	}
	deadlines := []time.Time{workflowExpiresAt(detail), task.DeadlineAt, stage.DeadlineAt}
	for index := len(detail.Receipts) - 1; index >= 0; index-- {
		receipt := detail.Receipts[index]
		if receipt.RequestID != detail.Decision.ID || receipt.Effect != effect || receipt.CreatedAt.IsZero() || receipt.CreatedAt.Before(detail.Instance.CreatedAt) {
			continue
		}
		valid := true
		for _, deadline := range deadlines {
			if !deadline.IsZero() && !receipt.CreatedAt.Before(deadline) {
				valid = false
				break
			}
		}
		if valid {
			return true
		}
	}
	return false
}

func workflowDefinitionHash(definition WorkflowDefinition) string {
	encoded, _ := json.Marshal(definition)
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}
