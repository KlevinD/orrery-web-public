package missioncontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	discussion "orrery_v2/internal/mission/discussion"
	"orrery_v2/internal/mission/projection"
	"orrery_v2/internal/mission/storage"
	missiontask "orrery_v2/internal/mission/task"
	missionteam "orrery_v2/internal/mission/team"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

func (s *LifecycleService) CreateTask(ctx context.Context, viewer ViewerRef, cmd CreateTaskCommand) (TaskCardDTO, error) {
	if s == nil || s.uow == nil || s.repo == nil || s.taskRepo == nil {
		return TaskCardDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return TaskCardDTO{}, err
	}
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	if cmd.IdempotencyKey == "" || cmd.MissionID.IsZero() || strings.TrimSpace(cmd.Goal) == "" {
		return TaskCardDTO{}, ErrInvalidRequest
	}
	if cmd.TaskID.IsZero() {
		cmd.TaskID = lifecycleTaskID(viewer, cmd.MissionID, cmd.IdempotencyKey)
	}
	if envelope := cmd.ExecutionEnvelope; envelope != nil {
		if err := envelope.Verify(); err != nil {
			return TaskCardDTO{}, ErrInvalidRequest
		}
		if !envelope.ExpiresAt.IsZero() && !envelope.ExpiresAt.After(s.now()) {
			return TaskCardDTO{}, ErrInvalidRequest
		}
		if tenantID := strings.TrimSpace(envelope.TenantID); tenantID != "" && tenantID != strings.TrimSpace(viewer.Tenant()) {
			return TaskCardDTO{}, ErrInvalidRequest
		}
		if missionID := strings.TrimSpace(envelope.MissionID); missionID != "" && missionID != strings.TrimSpace(cmd.MissionID.String()) {
			return TaskCardDTO{}, ErrInvalidRequest
		}
		if taskID := strings.TrimSpace(envelope.TaskID); taskID != "" && taskID != strings.TrimSpace(cmd.TaskID.String()) {
			return TaskCardDTO{}, ErrInvalidRequest
		}
	}
	if err := s.authorizeMissionAction(ctx, viewer, cmd.MissionID, "mission.edit"); err != nil {
		return TaskCardDTO{}, err
	}
	model, err := loadMissionProjection(ctx, s.repo, viewer.Tenant(), cmd.MissionID)
	if err != nil {
		return TaskCardDTO{}, err
	}
	var assignee taskAssigneeBinding
	if strings.TrimSpace(cmd.AssigneeID) != "" {
		assignee, err = s.resolveTaskAssignee(ctx, model, cmd.AssigneeID)
		if err != nil {
			return TaskCardDTO{}, err
		}
	}
	if err := validateTaskExecutionEnvelopeAssignee(cmd.ExecutionEnvelope, assignee); err != nil {
		return TaskCardDTO{}, err
	}
	qaPolicy := taskQAPolicyForAssigneeAndSource(assignee, cmd.SourceKind)
	now := s.now()
	contract := taskContractFromCommand(cmd, assignee, qaPolicy)
	fingerprint, err := missionTaskCommandFingerprint(contract)
	if err != nil {
		return TaskCardDTO{}, err
	}
	record := missiontask.TaskRecord{
		Contract: contract, Status: missiontask.TaskStatusDraft, Version: 1, StatusChangedAt: now,
		CreationIdempotencyKey: cmd.IdempotencyKey, CreationRequestFingerprint: fingerprint,
	}
	var replaying bool
	var card TaskCardDTO
	err = s.uow.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, storage.TableTaskRecords.LogicalName, missiontask.TaskID(cmd.TaskID).String()); err != nil {
			return err
		}
		if existing, getErr := s.taskRepo.GetTask(ctx, tx, missiontask.TaskID(cmd.TaskID)); getErr == nil {
			if existing.CreationIdempotencyKey == cmd.IdempotencyKey {
				if existing.CreationRequestFingerprint != "" && existing.CreationRequestFingerprint != fingerprint {
					return fmt.Errorf("%w: task idempotency key was reused with a different request", ErrInvalidRequest)
				}
				record = existing
				replaying = true
				return nil
			}
			if existing.Status != missiontask.TaskStatusDraft {
				return fmt.Errorf("%w: an active task contract cannot be replaced", ErrConflict)
			}
			if err := validateTaskExecutionEnvelopeRevision(existing.Contract.ExecutionEnvelope, cmd.ExecutionEnvelope); err != nil {
				return err
			}
			record.Status = existing.Status
			record.Version = existing.Version + 1
			record.StatusChangedAt = firstTime(existing.StatusChangedAt, now)
			record.LastTransitionReason = existing.LastTransitionReason
		} else if !errors.Is(getErr, missiontask.ErrNotFound) {
			return getErr
		}
		return s.taskRepo.SaveTask(ctx, tx, record)
	})
	if err != nil {
		return TaskCardDTO{}, err
	}
	if replaying {
		if index, ok := findTaskIndex(model, cmd.TaskID); ok {
			if hasTaskAssignee(assignee) {
				if err := s.reconcileTaskAssigneeAccess(ctx, viewer, model, cmd.MissionID, assignee, now); err != nil {
					return TaskCardDTO{}, err
				}
			}
			return MapViewDTO(model, viewer).TaskBoard.Tasks[index], nil
		}
	}
	assignee = taskAssigneeBindingFromRecord(record, assignee)
	status := projectionTaskStatusForExisting(record, projection.TaskStatusCreated)
	model, err = s.persistTaskProjection(ctx, viewer, cmd.MissionID, model, record, status, assignee, now, taskProjectionCreate)
	if err != nil {
		return TaskCardDTO{}, err
	}
	if hasTaskAssignee(assignee) {
		if err := s.addTaskAssigneeToDiscussion(ctx, model, assignee, now); err != nil {
			return TaskCardDTO{}, err
		}
		if err := s.reconcileTaskAssigneeRelations(ctx, viewer, model, cmd.MissionID, assignee); err != nil {
			return TaskCardDTO{}, err
		}
	}
	index, ok := findTaskIndex(model, cmd.TaskID)
	if !ok {
		return TaskCardDTO{}, ErrProjectionNotFound
	}
	card = MapViewDTO(model, viewer).TaskBoard.Tasks[index]
	return card, nil
}

func (s *LifecycleService) AssignTask(ctx context.Context, viewer ViewerRef, cmd AssignTaskCommand) (CommandReceiptDTO, error) {
	if s == nil || s.uow == nil || s.repo == nil || s.taskRepo == nil {
		return CommandReceiptDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return CommandReceiptDTO{}, err
	}
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	if cmd.IdempotencyKey == "" || cmd.MissionID.IsZero() || cmd.TaskID.IsZero() || strings.TrimSpace(cmd.AssigneeID) == "" || cmd.ResponsibilityExpectedVersion < 0 {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	if err := s.authorizeMissionAction(ctx, viewer, cmd.MissionID, "mission.edit"); err != nil {
		return CommandReceiptDTO{}, err
	}
	model, err := loadMissionProjection(ctx, s.repo, viewer.Tenant(), cmd.MissionID)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	assignee, err := s.resolveTaskAssignee(ctx, model, cmd.AssigneeID)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	now := s.now()
	var version int64
	var updated missiontask.TaskRecord
	var replaying bool
	assignmentFingerprint, err := missionTaskCommandFingerprint(struct {
		TaskID     string
		AssigneeID string
		Reason     string
	}{TaskID: cmd.TaskID.String(), AssigneeID: assignee.ProfileID.String(), Reason: strings.TrimSpace(cmd.Reason)})
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	err = s.uow.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, storage.TableTaskRecords.LogicalName, missiontask.TaskID(cmd.TaskID).String()); err != nil {
			return err
		}
		record, err := s.taskRepo.GetTask(ctx, tx, missiontask.TaskID(cmd.TaskID))
		if err != nil {
			return err
		}
		if record.AssignmentIdempotencyKey == cmd.IdempotencyKey {
			if record.AssignmentRequestFingerprint != "" && record.AssignmentRequestFingerprint != assignmentFingerprint {
				return fmt.Errorf("%w: assignment idempotency key was reused with a different request", ErrInvalidRequest)
			}
			updated = record
			version = record.Version
			replaying = true
			return nil
		}
		if err := validateTaskExecutionEnvelopeAssignee(record.Contract.ExecutionEnvelope, assignee); err != nil {
			return fmt.Errorf("%w: task execution envelope is bound to another assignee", err)
		}
		qaPolicy := taskQAPolicyForAssigneeAndSource(assignee, record.Contract.SourceKind)
		assigneeRef := missiontask.TaskAssigneeRef{AgentProfileRef: &agentcore.AgentProfileRef{ID: assignee.ProfileID}}
		applyTaskQAPolicy(&record.Contract, qaPolicy)
		updatedRecord, err := record.Reassign(assigneeRef, missiontask.TransitionOptions{ExpectedVersion: record.Version, Reason: cmd.Reason, Now: now})
		if err != nil {
			return err
		}
		updatedRecord.AssignmentIdempotencyKey = cmd.IdempotencyKey
		updatedRecord.AssignmentRequestFingerprint = assignmentFingerprint
		if err := s.taskRepo.SaveTask(ctx, tx, updatedRecord); err != nil {
			return err
		}
		if s.responsibility != nil {
			if err := s.responsibility.RecordTaskAssignment(ctx, tx, TaskResponsibilityAssignmentSignal{
				TenantID: viewer.Tenant(), MissionID: cmd.MissionID.String(), TaskID: cmd.TaskID.String(),
				SourceKind: updatedRecord.Contract.SourceKind, SourceID: updatedRecord.Contract.SourceID, SourceStepID: updatedRecord.Contract.SourceStepID,
				ActorMemberID:  assignee.MemberID,
				AgentProfileID: assignee.ProfileID.String(), Priority: responsibilityPriority(updatedRecord.Contract.Priority),
				DueAt: updatedRecord.Contract.DuePolicy.DueAt, ActorID: viewer.ViewerID, Reason: strings.TrimSpace(cmd.Reason),
				ExpectedResponsibilityVersion: cmd.ResponsibilityExpectedVersion,
			}); err != nil {
				return err
			}
		}
		updated = updatedRecord
		version = updatedRecord.Version
		return nil
	})
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	if replaying {
		if index, ok := findTaskIndex(model, cmd.TaskID); ok && model.TaskBoard.Tasks[index].Assignee == assignee.StableAgentID {
			if err := s.reconcileTaskAssigneeAccess(ctx, viewer, model, cmd.MissionID, assignee, now); err != nil {
				return CommandReceiptDTO{}, err
			}
			return CommandReceiptDTO{CommandID: cmd.IdempotencyKey, Status: "accepted", MissionID: cmd.MissionID.String(), TaskID: cmd.TaskID.String(), Version: int(version), AcceptedAt: updated.StatusChangedAt}, nil
		}
	}
	model, err = s.persistTaskProjection(ctx, viewer, cmd.MissionID, model, updated, projection.TaskStatusAssigned, assignee, now, taskProjectionAssign)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	if err := s.addTaskAssigneeToDiscussion(ctx, model, assignee, now); err != nil {
		return CommandReceiptDTO{}, err
	}
	if err := s.reconcileTaskAssigneeRelations(ctx, viewer, model, cmd.MissionID, assignee); err != nil {
		return CommandReceiptDTO{}, err
	}
	return CommandReceiptDTO{CommandID: cmd.IdempotencyKey, Status: "accepted", MissionID: cmd.MissionID.String(), TaskID: cmd.TaskID.String(), Version: int(version), AcceptedAt: now}, nil
}

type taskProjectionMutation string

const (
	taskProjectionCreate taskProjectionMutation = "create"
	taskProjectionAssign taskProjectionMutation = "assign"
)

// persistTaskProjection retries optimistic projection conflicts so a durable
// task command cannot be left out of Mission Control by an unrelated write.
func (s *LifecycleService) persistTaskProjection(ctx context.Context, viewer ViewerRef, missionID missioncore.MissionID, model projection.ReadModel, record missiontask.TaskRecord, status projection.TaskStatus, assignee taskAssigneeBinding, now time.Time, mutation taskProjectionMutation) (projection.ReadModel, error) {
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 && taskProjectionAlreadyApplied(model, record.Contract.ID, status, assignee.StableAgentID, mutation) {
			return model, nil
		}
		next := upsertTaskProjection(model, record, status, assignee.StableAgentID, now)
		if hasTaskAssignee(assignee) && !assignee.HistoricalOnly {
			next = upsertTaskAssigneeTeamProjection(next, assignee, now)
		}
		next = bumpModel(next, now)
		if err := saveMissionProjection(ctx, s.repo, viewer.Tenant(), next); err == nil {
			return next, nil
		} else if !errors.Is(err, ErrConflict) {
			return projection.ReadModel{}, err
		}
		latest, err := loadMissionProjection(ctx, s.repo, viewer.Tenant(), missionID)
		if err != nil {
			return projection.ReadModel{}, err
		}
		model = latest
	}
	return projection.ReadModel{}, ErrConflict
}

func taskProjectionAlreadyApplied(model projection.ReadModel, taskID missioncore.TaskID, status projection.TaskStatus, assignee agentcore.AgentID, mutation taskProjectionMutation) bool {
	index, ok := findTaskIndex(model, taskID)
	if !ok {
		return false
	}
	current := model.TaskBoard.Tasks[index]
	switch mutation {
	case taskProjectionCreate:
		return true
	case taskProjectionAssign:
		return current.Assignee == assignee && current.Status != projection.TaskStatusCreated
	default:
		return current.Status == status && current.Assignee == assignee
	}
}

func taskAssigneeBindingFromRecord(record missiontask.TaskRecord, fallback taskAssigneeBinding) taskAssigneeBinding {
	ref := record.Contract.Assignee.AgentProfileRef
	if ref == nil || ref.ID.IsZero() || ref.ID == fallback.ProfileID {
		return fallback
	}
	return taskAssigneeBinding{
		ProfileID:     ref.ID,
		InstanceID:    agentcore.AgentInstanceID(ref.ID),
		StableAgentID: agentcore.AgentID(ref.ID),
	}
}

func responsibilityPriority(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "critical", "urgent":
		return 100
	case "high":
		return 75
	case "low":
		return 25
	default:
		return 50
	}
}

func (s *LifecycleService) addTaskAssigneeToDiscussion(ctx context.Context, model projection.ReadModel, assignee taskAssigneeBinding, joinedAt time.Time) error {
	if assignee.InstanceID.IsZero() {
		return nil
	}
	if groupID := strings.TrimSpace(model.Discussion.DiscussionGroupID); groupID != "" {
		if s.discuss == nil {
			return ErrServiceDependency
		}
		if err := s.discuss.AddMember(ctx, discussion.DiscussionGroupID(groupID), discussion.DiscussionMember{
			AgentProfileID:  assignee.ProfileID,
			AgentInstanceID: assignee.InstanceID,
			MemberKind:      discussion.DiscussionMemberAgent,
			MissionRole:     missionteam.MissionRoleWorker,
			VisibilityScope: discussion.VisibilityMission,
			CanPost:         true,
			CanMention:      true,
			JoinedAt:        joinedAt,
		}); err != nil {
			return err
		}
	}
	conversationID := strings.TrimSpace(model.Discussion.ConversationID)
	todoID := strings.TrimSpace(model.Mission.OriginTodoID)
	missionID := model.Mission.MissionID.String()
	if conversationID == "" || missionID == "" {
		return nil
	}
	if s.canonicalChat == nil {
		return ErrServiceDependency
	}
	tenantID := missionModelTenantID(model)
	agentIDs := canonicalTaskAssigneeIDs(model.TaskBoard.Tasks, model.Team.Members)
	if strings.TrimSpace(model.Mission.OriginKind) != "todo" || todoID == "" {
		return s.canonicalChat.ReconcileMissionAgentMembers(ctx, CanonicalConversationMembershipSetRequest{
			ActorKind: "system", ActorID: "mission-lifecycle",
			TenantID: tenantID, ConversationID: conversationID, MissionID: missionID,
			AgentIDs: agentIDs, HumanIDs: canonicalHumanTaskAssigneeIDs(model.TaskBoard.Tasks, model.Team.Members),
			ManagedSource: "mission-task-assignee:" + tenantID + "/" + missionID,
		})
	}
	return s.canonicalChat.ReconcileTodoMissionAgentMembers(ctx, CanonicalConversationMembershipSetRequest{
		ActorKind: "system", ActorID: "mission-lifecycle",
		TenantID: tenantID, ConversationID: conversationID, MissionID: missionID, TodoID: todoID,
		AgentIDs: agentIDs, ManagedSource: "todo-mission:" + tenantID + "/" + todoID + "/" + missionID,
	})
}

func (s *LifecycleService) writeDiscussionMemberRelationForTaskAssignee(ctx context.Context, model projection.ReadModel, assignee taskAssigneeBinding) error {
	discussionID := strings.TrimSpace(model.Discussion.DiscussionGroupID)
	if discussionID == "" {
		return nil
	}
	return s.authzRelations.WriteDiscussionMember(ctx, missionSubjectFromTaskAssignee(assignee), discussionID, missionModelTenantID(model))
}

func upsertTaskAssigneeTeamProjection(model projection.ReadModel, assignee taskAssigneeBinding, joinedAt time.Time) projection.ReadModel {
	if assignee.ProfileID.IsZero() || assignee.InstanceID.IsZero() {
		return model
	}
	missionRole := string(missionteam.MissionRoleWorker)
	roleName := firstString(assignee.Entry.RoleName, missionRole)
	for _, member := range model.Team.Members {
		if member.AgentProfileID == assignee.ProfileID || member.AgentID == agentcore.AgentID(assignee.ProfileID) {
			missionRole = firstString(member.MissionRole, missionRole)
			roleName = firstString(member.RoleName, roleName)
			break
		}
	}
	model.Team.Members = upsertProjectionTeamMember(model.Team.Members, projection.TeamMemberView{
		MemberID:        assignee.MemberID,
		AgentID:         agentcore.AgentID(assignee.ProfileID),
		AgentProfileID:  assignee.ProfileID,
		AgentInstanceID: assignee.InstanceID,
		AgentKind:       assignee.Entry.AgentKind,
		DisplayName:     firstString(assignee.Entry.DisplayName, assignee.Entry.RealName),
		RealName:        assignee.Entry.RealName,
		RoleName:        roleName,
		MissionRole:     missionRole,
		Status:          projection.TeamMemberJoined,
		JoinedAt:        joinedAt,
	})
	return model
}

type taskQAPolicy struct {
	Enabled       bool
	PlanRequired  bool
	CheckRequired bool
}

type taskAssigneeBinding struct {
	MemberID       string
	ProfileID      agentcore.AgentProfileID
	InstanceID     agentcore.AgentInstanceID
	StableAgentID  agentcore.AgentID
	Entry          agentcore.AgentDirectoryEntry
	FromDirectory  bool
	HistoricalOnly bool
}

func hasTaskAssignee(assignee taskAssigneeBinding) bool {
	return !assignee.ProfileID.IsZero() || !assignee.InstanceID.IsZero() || assignee.StableAgentID != ""
}

func taskContractFromCommand(cmd CreateTaskCommand, assignee taskAssigneeBinding, qaPolicy taskQAPolicy) missiontask.TaskContract {
	contract := missiontask.TaskContract{
		ID:                        missiontask.TaskID(cmd.TaskID),
		MissionID:                 cmd.MissionID,
		Assignee:                  taskAssigneeRef(assignee),
		Title:                     cmd.Goal,
		Goal:                      cmd.Goal,
		SourceKind:                strings.TrimSpace(cmd.SourceKind),
		SourceID:                  strings.TrimSpace(cmd.SourceID),
		SourceStepID:              strings.TrimSpace(cmd.SourceStepID),
		WorkerPersonalGoal:        cmd.WorkerPersonalGoal,
		Priority:                  strings.TrimSpace(cmd.Priority),
		Scope:                     cmd.Scope,
		ExpectedDeliverables:      taskExpectedDeliverables(cmd),
		QAEnabled:                 qaPolicy.Enabled,
		QAPlanRequired:            qaPolicy.PlanRequired,
		QACheckRequired:           qaPolicy.CheckRequired,
		DuePolicy:                 taskDuePolicy(cmd.DueAt),
		ExecutionPermissionScopes: cleanMissionTaskScopes(cmd.ExecutionPermissionScopes),
		ExecutionAllowedToolIDs:   cleanMissionTaskScopes(cmd.ExecutionAllowedToolIDs),
		ExecutionEnvelope:         agentcore.CloneExecutionEnvelope(cmd.ExecutionEnvelope),
		RequireExplicitToolScopes: cmd.RequireExplicitToolScopes || strings.EqualFold(strings.TrimSpace(cmd.SourceKind), missiontask.SourceKindResponsibilityAutoplan),
	}
	for index, description := range cmd.AcceptanceCriteria {
		if description = strings.TrimSpace(description); description != "" {
			contract.AcceptanceCriteria = append(contract.AcceptanceCriteria, missiontask.AcceptanceCriterion{ID: missiontask.AcceptanceCriterionID(fmt.Sprintf("acceptance-%d", index+1)), Description: description})
		}
	}
	for _, dependency := range cmd.Dependencies {
		if !dependency.IsZero() {
			contract.Dependencies = append(contract.Dependencies, missiontask.TaskDependencyRef{TaskID: dependency})
		}
	}
	for index, requirement := range cmd.EvidenceRequirements {
		description := strings.TrimSpace(requirement.Description)
		if description == "" {
			continue
		}
		id := strings.TrimSpace(requirement.ID)
		if id == "" {
			id = fmt.Sprintf("evidence-%d", index+1)
		}
		kind := missiontask.EvidenceRequirementKind(strings.TrimSpace(requirement.Kind))
		if !kind.IsValid() {
			kind = missiontask.EvidenceRequirementKindArtifact
		}
		contract.EvidenceRequirements = append(contract.EvidenceRequirements, missiontask.EvidenceRequirement{
			ID: missiontask.EvidenceRequirementID(id), Description: description, Kind: kind, Required: requirement.Required,
		})
	}
	if len(contract.EvidenceRequirements) == 0 {
		for index, criterion := range contract.AcceptanceCriteria {
			contract.EvidenceRequirements = append(contract.EvidenceRequirements, missiontask.EvidenceRequirement{
				ID:          missiontask.EvidenceRequirementID(fmt.Sprintf("evidence-acceptance-%d", index+1)),
				Description: criterion.Description,
				Kind:        missiontask.EvidenceRequirementKindArtifact,
				Required:    true,
			})
		}
	}
	if strings.TrimSpace(cmd.WorkerPersonalGoal) != "" {
		contract.WorkerPersonalGoalSources = []missiontask.WorkerPersonalGoalSourceRef{{
			Kind:             missiontask.WorkerPersonalGoalSourceGoal,
			ContractFieldRef: missiontask.ContractFieldRef{Field: missiontask.ContractFieldGoal},
		}}
	}
	applyTaskQAPolicy(&contract, qaPolicy)
	return contract
}

func cleanMissionTaskScopes(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func taskAssigneeRef(assignee taskAssigneeBinding) missiontask.TaskAssigneeRef {
	if assignee.ProfileID.IsZero() {
		return missiontask.TaskAssigneeRef{}
	}
	return missiontask.TaskAssigneeRef{AgentProfileRef: &agentcore.AgentProfileRef{ID: assignee.ProfileID}}
}

func applyTaskQAPolicy(contract *missiontask.TaskContract, qaPolicy taskQAPolicy) {
	if contract == nil {
		return
	}
	contract.QAEnabled = qaPolicy.Enabled
	contract.QAPlanRequired = qaPolicy.PlanRequired
	contract.QAPlanCompleted = !qaPolicy.PlanRequired
	contract.QAPlanRef = ""
	contract.QAPlanArtifactRef = agentcore.ArtifactRef{}
	contract.QAPlanIdempotencyKey = ""
	contract.QACheckRequired = qaPolicy.CheckRequired
}

func taskExpectedDeliverables(cmd CreateTaskCommand) []missiontask.ExpectedDeliverable {
	if len(cmd.ExpectedDeliverables) == 0 {
		return []missiontask.ExpectedDeliverable{{
			ID:    missiontask.ExpectedDeliverableID("deliverable-" + missiontask.TaskID(cmd.TaskID).String()),
			Title: cmd.Goal,
		}}
	}
	out := make([]missiontask.ExpectedDeliverable, 0, len(cmd.ExpectedDeliverables))
	for i, item := range cmd.ExpectedDeliverables {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = "expected-" + fmt.Sprint(i+1)
		}
		title := strings.TrimSpace(item.Title)
		if title == "" {
			title = cmd.Goal
		}
		out = append(out, missiontask.ExpectedDeliverable{ID: missiontask.ExpectedDeliverableID(id), Title: title})
	}
	return out
}

func taskDuePolicy(dueAt time.Time) missiontask.DuePolicy {
	if dueAt.IsZero() {
		return missiontask.DuePolicy{Policy: missiontask.DuePolicyBestEffort}
	}
	return missiontask.DuePolicy{Policy: missiontask.DuePolicyFixed, DueAt: dueAt}
}

func (s *LifecycleService) resolveTaskAssignee(ctx context.Context, model projection.ReadModel, assigneeID string) (taskAssigneeBinding, error) {
	assigneeID = strings.TrimSpace(assigneeID)
	if assigneeID == "" {
		return taskAssigneeBinding{}, ErrInvalidRequest
	}
	if assignee, ok, err := taskAssigneeFromMissionTeam(model, assigneeID); err != nil {
		return taskAssigneeBinding{}, err
	} else if ok {
		return assignee, nil
	}
	if s.agents == nil {
		return taskAssigneeBinding{}, ErrServiceDependency
	}
	page, err := s.agents.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{EnabledOnly: true, IncludeAI: true, IncludeHumans: true})
	if err != nil {
		return taskAssigneeBinding{}, err
	}
	for _, entry := range page.Entries {
		if !entry.AgentKind.IsValid() || !entry.Enabled {
			continue
		}
		if entry.AgentProfileID.String() != assigneeID {
			continue
		}
		if entry.AgentProfileID.IsZero() {
			return taskAssigneeBinding{}, fmt.Errorf("%w: assignee_id must reference an enabled agent profile", ErrInvalidRequest)
		}
		if entry.AgentInstanceID != agentcore.AgentInstanceID(entry.AgentProfileID) {
			return taskAssigneeBinding{}, fmt.Errorf("%w: assignee_id must reference a stable agent identity", ErrInvalidRequest)
		}
		return taskAssigneeBinding{
			ProfileID:     entry.AgentProfileID,
			InstanceID:    agentcore.AgentInstanceID(entry.AgentProfileID),
			StableAgentID: agentcore.AgentID(entry.AgentProfileID),
			Entry:         entry,
			FromDirectory: true,
		}, nil
	}
	return taskAssigneeBinding{}, fmt.Errorf("%w: assignee_id must reference an enabled agent profile", ErrInvalidRequest)
}

func taskAssigneeFromMissionTeam(model projection.ReadModel, assigneeID string) (taskAssigneeBinding, bool, error) {
	for _, member := range model.Team.Members {
		if !missionTeamAgentMatches(member.AgentProfileID, member.AgentInstanceID, member.AgentID, assigneeID) {
			continue
		}
		binding, err := taskAssigneeFromMissionTeamBinding(member.MemberID, member.AgentID, member.AgentProfileID, member.AgentInstanceID)
		if err != nil {
			return taskAssigneeBinding{}, true, err
		}
		return binding, true, nil
	}
	for _, assignment := range model.Team.Assignments {
		if !missionTeamAgentMatches(assignment.AgentProfileID, assignment.AgentInstanceID, assignment.AgentID, assigneeID) {
			continue
		}
		binding, err := taskAssigneeFromMissionTeamBinding(assignment.MemberID, assignment.AgentID, assignment.AgentProfileID, assignment.AgentInstanceID)
		if err != nil {
			return taskAssigneeBinding{}, true, err
		}
		return binding, true, nil
	}
	return taskAssigneeBinding{}, false, nil
}

func taskAssigneeFromMissionTeamBinding(memberID string, agentID agentcore.AgentID, profileID agentcore.AgentProfileID, instanceID agentcore.AgentInstanceID) (taskAssigneeBinding, error) {
	if profileID.IsZero() {
		return taskAssigneeBinding{}, fmt.Errorf("%w: assignee_id must reference a mission team agent profile", ErrInvalidRequest)
	}
	stableAgentID := agentID
	if stableAgentID == "" {
		stableAgentID = agentcore.AgentID(profileID)
	}
	if stableAgentID == "" {
		return taskAssigneeBinding{}, fmt.Errorf("%w: assignee_id must reference a stable mission team agent", ErrInvalidRequest)
	}
	if stableAgentID != agentcore.AgentID(profileID) || instanceID != agentcore.AgentInstanceID(profileID) {
		return taskAssigneeBinding{}, fmt.Errorf("%w: assignee_id must reference a stable mission team agent identity", ErrInvalidRequest)
	}
	return taskAssigneeBinding{MemberID: strings.TrimSpace(memberID), ProfileID: profileID, InstanceID: agentcore.AgentInstanceID(profileID), StableAgentID: stableAgentID}, nil
}

func missionTeamAgentMatches(profileID agentcore.AgentProfileID, _ agentcore.AgentInstanceID, agentID agentcore.AgentID, candidate string) bool {
	return candidate != "" && (profileID.String() == candidate || agentID.String() == candidate)
}

func taskQAPolicyForAssignee(assignee taskAssigneeBinding) taskQAPolicy {
	return taskQAPolicyForAssigneeAndSource(assignee, "")
}

func taskQAPolicyForAssigneeAndSource(assignee taskAssigneeBinding, sourceKind string) taskQAPolicy {
	if isQAAgentAssignee(assignee) {
		return taskQAPolicy{}
	}
	if strings.EqualFold(strings.TrimSpace(sourceKind), "todo_plan_step") {
		return taskQAPolicy{Enabled: true, PlanRequired: true, CheckRequired: true}
	}
	if !assignee.FromDirectory || !assignee.Entry.QAEnabled {
		return taskQAPolicy{}
	}
	return taskQAPolicy{Enabled: true, PlanRequired: true, CheckRequired: true}
}

func isQAAgentAssignee(assignee taskAssigneeBinding) bool {
	if assignee.FromDirectory && isQAAgentEntry(assignee.Entry) {
		return true
	}
	return missionteam.IsWorkerInternalQAAgent(assignee.ProfileID, nil)
}

func isQAAgentEntry(entry agentcore.AgentDirectoryEntry) bool {
	return missionteam.IsWorkerInternalQAAgent(entry.AgentProfileID, entry.OrgIdentity.RoleRefs)
}

func isQARoleIdentifier(value string) bool {
	return missionteam.IsWorkerInternalQARoleIdentifier(value)
}

func upsertTaskProjection(model projection.ReadModel, record missiontask.TaskRecord, status projection.TaskStatus, assignee agentcore.AgentID, now time.Time) projection.ReadModel {
	card := projection.TaskCardView{
		TaskID:                    record.Contract.ID,
		Assignee:                  assignee,
		Goal:                      record.Contract.Goal,
		SourceKind:                record.Contract.SourceKind,
		SourceID:                  record.Contract.SourceID,
		SourceStepID:              record.Contract.SourceStepID,
		ExecutionPermissionScopes: append([]string(nil), record.Contract.ExecutionPermissionScopes...),
		ExecutionAllowedToolIDs:   append([]string(nil), record.Contract.ExecutionAllowedToolIDs...),
		ExecutionEnvelope:         agentcore.CloneExecutionEnvelope(record.Contract.ExecutionEnvelope),
		WorkerPersonalGoalSummary: record.Contract.WorkerPersonalGoal,
		Status:                    status,
		Priority:                  record.Contract.Priority,
		DueAt:                     record.Contract.DuePolicy.DueAt,
		UpdatedAt:                 now,
	}
	for _, dependency := range record.Contract.Dependencies {
		model.TaskBoard.Dependencies = upsertTaskDependencyProjection(model.TaskBoard.Dependencies, projection.TaskDependencyView{TaskID: record.Contract.ID, DependsOn: dependency.TaskID})
	}
	for _, item := range record.Contract.ExpectedDeliverables {
		card.ExpectedDeliverables = append(card.ExpectedDeliverables, projection.ExpectedDeliverableView{ID: string(item.ID), Title: item.Title})
	}
	for _, item := range record.Contract.AcceptanceCriteria {
		card.AcceptanceCriteria = append(card.AcceptanceCriteria, projection.AcceptanceCriterionView{ID: string(item.ID), Description: item.Description})
	}
	for _, item := range record.Contract.EvidenceRequirements {
		card.RequiredEvidence = append(card.RequiredEvidence, projection.RequiredEvidenceView{ID: string(item.ID), Description: item.Description, Kind: string(item.Kind), Required: item.Required})
	}
	for i := range model.TaskBoard.Tasks {
		if model.TaskBoard.Tasks[i].TaskID == card.TaskID {
			model.TaskBoard.Tasks[i] = card
			return model
		}
	}
	model.TaskBoard.Tasks = append(model.TaskBoard.Tasks, card)
	return model
}

func upsertTaskDependencyProjection(dependencies []projection.TaskDependencyView, next projection.TaskDependencyView) []projection.TaskDependencyView {
	for _, dependency := range dependencies {
		if dependency == next {
			return dependencies
		}
	}
	return append(dependencies, next)
}

func projectionTaskStatusForExisting(record missiontask.TaskRecord, fallback projection.TaskStatus) projection.TaskStatus {
	switch record.Status {
	case missiontask.TaskStatusAssigned:
		return projection.TaskStatusAssigned
	case missiontask.TaskStatusAccepted:
		return projection.TaskStatusAccepted
	case missiontask.TaskStatusInProgress:
		return projection.TaskStatusStarted
	case missiontask.TaskStatusSubmitted:
		return projection.TaskStatusSubmitted
	case missiontask.TaskStatusInReview:
		return projection.TaskStatusSubmitted
	case missiontask.TaskStatusApproved:
		return projection.TaskStatusApproved
	case missiontask.TaskStatusRejected:
		return projection.TaskStatusRejected
	case missiontask.TaskStatusReworkRequested:
		return projection.TaskStatusReworkRequested
	default:
		return fallback
	}
}

func findTaskIndex(model projection.ReadModel, taskID missioncore.TaskID) (int, bool) {
	for i, task := range model.TaskBoard.Tasks {
		if task.TaskID == taskID {
			return i, true
		}
	}
	return 0, false
}

func lifecycleTaskID(viewer ViewerRef, missionID missioncore.MissionID, idempotencyKey string) missioncore.TaskID {
	key := missionCreationIntentKey(viewer, "task|"+missionID.String()+"|"+strings.TrimSpace(idempotencyKey))
	digest := key[strings.LastIndex(key, "/")+1:]
	if len(digest) > 24 {
		digest = digest[:24]
	}
	return missioncore.TaskID("task-" + digest)
}

func missionTaskCommandFingerprint(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode task command fingerprint: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (s *LifecycleService) reconcileTaskAssigneeAccess(ctx context.Context, viewer ViewerRef, model projection.ReadModel, missionID missioncore.MissionID, assignee taskAssigneeBinding, now time.Time) error {
	if err := s.addTaskAssigneeToDiscussion(ctx, model, assignee, now); err != nil {
		return err
	}
	return s.reconcileTaskAssigneeRelations(ctx, viewer, model, missionID, assignee)
}

func (s *LifecycleService) reconcileTaskAssigneeRelations(ctx context.Context, viewer ViewerRef, model projection.ReadModel, missionID missioncore.MissionID, assignee taskAssigneeBinding) error {
	if err := s.authzRelations.WriteMissionParticipant(ctx, missionSubjectFromTaskAssignee(assignee), missionID.String(), viewer.Tenant()); err != nil {
		return err
	}
	return s.writeDiscussionMemberRelationForTaskAssignee(ctx, model, assignee)
}
