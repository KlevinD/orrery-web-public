package missioncontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/authorization"
	missioncore "orrery_v2/internal/mission/core"
	discussion "orrery_v2/internal/mission/discussion"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/projection"
	missionteam "orrery_v2/internal/mission/team"
	"orrery_v2/internal/platform/db"
)

const missionCreationIntentTable = "core.mission_creation_intents"

type missionCreationIntent struct {
	IntentKey                        string               `json:"intent_key"`
	TenantID                         string               `json:"tenant_id"`
	Viewer                           ViewerRef            `json:"viewer"`
	Command                          CreateMissionCommand `json:"command"`
	PersonalAssistantOriginOwnerID   string               `json:"personal_assistant_origin_owner_id,omitempty"`
	PersonalAssistantOriginSubjectID string               `json:"personal_assistant_origin_subject_id,omitempty"`
	MissionCreatedEventRequired      bool                 `json:"mission_created_event_required,omitempty"`
	MissionCreatedEventPublished     bool                 `json:"mission_created_event_published,omitempty"`
	Model                            projection.ReadModel `json:"model"`
	DiscussionReady                  bool                 `json:"discussion_ready"`
	SystemRolesReady                 bool                 `json:"system_roles_ready"`
	InitialHumanReady                bool                 `json:"initial_human_ready"`
	ViewerDiscussionMemberReady      bool                 `json:"viewer_discussion_member_ready"`
	MissionRelationsReady            bool                 `json:"mission_relations_ready"`
	DiscussionRelationsReady         bool                 `json:"discussion_relations_ready"`
	ProjectionPublished              bool                 `json:"projection_published"`
	AuditRecorded                    bool                 `json:"audit_recorded"`
	Complete                         bool                 `json:"complete"`
	CreatedAt                        time.Time            `json:"created_at"`
	UpdatedAt                        time.Time            `json:"updated_at"`
	Version                          int64                `json:"version"`
}

func (s *LifecycleService) ensureMissionCreationIntent(ctx context.Context, viewer ViewerRef, cmd CreateMissionCommand, personalAssistantOrigin PersonalAssistantMissionOrigin) (missionCreationIntent, error) {
	if s == nil || s.router == nil {
		return missionCreationIntent{}, ErrServiceDependency
	}
	cmd = normalizeMissionCreationCommand(cmd)
	key := missionCreationIntentKey(viewer, cmd.IdempotencyKey)
	if existing, _, ok, err := s.loadMissionCreationIntent(ctx, key); err != nil {
		return missionCreationIntent{}, err
	} else if ok {
		if !sameMissionCreationRequest(existing, viewer, cmd, personalAssistantOrigin) {
			return missionCreationIntent{}, fmt.Errorf("%w: idempotency key was already used for a different mission request", ErrInvalidRequest)
		}
		return existing, nil
	}

	missionID := cmd.MissionID
	if missionID == "" {
		var err error
		missionID, err = s.newID("mission")
		if err != nil {
			return missionCreationIntent{}, err
		}
	}
	now := s.now()
	intent := missionCreationIntent{
		IntentKey: key, TenantID: viewer.Tenant(), Viewer: normalizeMissionCreationViewer(viewer), Command: cmd,
		PersonalAssistantOriginOwnerID: personalAssistantOrigin.owner(), PersonalAssistantOriginSubjectID: personalAssistantOrigin.subject(),
		MissionCreatedEventRequired: s.missionEvents != nil,
		Model: projection.ReadModel{
			TenantID: viewer.Tenant(),
			Mission: projection.MissionView{
				TenantID: viewer.Tenant(), MissionID: missioncore.MissionID(missionID), Title: cmd.Title, FinalGoal: cmd.FinalGoal,
				Status: projection.MissionStatusActive, OriginKind: cmd.OriginKind, OriginID: cmd.OriginID, OriginTodoID: cmd.OriginTodoID,
				ExecutionAttempt: cmd.ExecutionAttempt, DiscussionMode: cmd.DiscussionMode,
				OriginDevelopmentProjectID:       cmd.OriginDevelopmentProjectID,
				PersonalAssistantOriginOwnerID:   personalAssistantOrigin.owner(),
				PersonalAssistantOriginSubjectID: personalAssistantOrigin.subject(),
				CreatedByID:                      strings.TrimSpace(viewer.ViewerID), CreatedByKind: string(viewer.ViewerKind), CreatedAt: now,
			},
			UpdatedAt: now, Checkpoint: projection.Checkpoint{MissionID: missioncore.MissionID(missionID), Version: 1, UpdatedAt: now},
		},
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	created, err := s.router.CreateRecord(ctx, missionCreationIntentTable, key, missionCreationIntentRecord(intent))
	if err != nil {
		return missionCreationIntent{}, err
	}
	if created {
		return intent, nil
	}
	existing, _, ok, err := s.loadMissionCreationIntent(ctx, key)
	if err != nil {
		return missionCreationIntent{}, err
	}
	if !ok || !sameMissionCreationRequest(existing, viewer, cmd, personalAssistantOrigin) {
		return missionCreationIntent{}, fmt.Errorf("%w: mission creation idempotency conflict", ErrInvalidRequest)
	}
	return existing, nil
}

func (s *LifecycleService) advanceMissionCreation(ctx context.Context, intentKey string) (missionCreationIntent, error) {
	for attempt := 0; attempt < 64; attempt++ {
		if err := ctx.Err(); err != nil {
			return missionCreationIntent{}, err
		}
		intent, _, ok, err := s.loadMissionCreationIntent(ctx, intentKey)
		if err != nil {
			return missionCreationIntent{}, err
		}
		if !ok {
			return missionCreationIntent{}, ErrProjectionNotFound
		}
		if err := intent.validatePersonalAssistantOrigin(); err != nil {
			return missionCreationIntent{}, err
		}
		if intent.Complete {
			return intent, nil
		}
		updated, err := s.advanceMissionCreationStep(ctx, intent)
		if err != nil {
			return intent, err
		}
		if err := s.saveMissionCreationIntent(ctx, updated, intent.Version); err != nil {
			if errors.Is(err, errMissionCreationIntentConflict) {
				continue
			}
			return intent, err
		}
	}
	return missionCreationIntent{}, fmt.Errorf("%w: mission creation did not converge", ErrServiceDependency)
}

func (s *LifecycleService) advanceMissionCreationStep(ctx context.Context, intent missionCreationIntent) (missionCreationIntent, error) {
	missionID := intent.Model.Mission.MissionID
	groupID := discussion.DiscussionGroupID(intent.Model.Discussion.DiscussionGroupID)
	switch {
	case intent.MissionCreatedEventRequired && !intent.MissionCreatedEventPublished:
		if err := s.publishMissionCreatedEvent(ctx, intent); err != nil {
			return intent, err
		}
		intent.MissionCreatedEventPublished = true
	case !intent.DiscussionReady:
		if intent.Command.ExistingConversationID != "" {
			intent.Model.Discussion.ConversationID = intent.Command.ExistingConversationID
		} else if s.discuss != nil {
			var group discussion.DiscussionGroup
			var err error
			if initializer, ok := s.discuss.(interface {
				CreateGroupForTenant(context.Context, string, missioncore.MissionID, string) (discussion.DiscussionGroup, error)
			}); ok {
				group, err = initializer.CreateGroupForTenant(ctx, intent.TenantID, missionID, intent.Command.FinalGoal)
			} else if intent.TenantID == "global" {
				group, err = s.discuss.CreateGroup(ctx, missionID, intent.Command.FinalGoal)
			} else {
				err = ErrServiceDependency
			}
			if err != nil {
				return intent, err
			}
			intent.Model.Discussion.DiscussionGroupID = string(group.ID)
		}
		intent.DiscussionReady = true
	case !intent.SystemRolesReady:
		assignments, err := s.bootstrapSystemRoles(ctx, groupID, missionID, intent.Command)
		if err != nil {
			return intent, err
		}
		intent.Model = s.upsertSystemRoleAssignments(ctx, intent.Model, assignments, intent.Model.UpdatedAt)
		intent.SystemRolesReady = true
	case !intent.InitialHumanReady:
		assignment, err := s.bootstrapInitialHumanAgent(ctx, groupID, missionID, intent.Command)
		if err != nil {
			return intent, err
		}
		if assignment.ID != "" {
			intent.Model = s.upsertSystemRoleAssignments(ctx, intent.Model, []missionteam.MissionAssignment{assignment}, intent.Model.UpdatedAt)
		}
		intent.InitialHumanReady = true
	case !intent.ViewerDiscussionMemberReady:
		if s.discuss != nil && groupID != "" && s.shouldAddViewerDiscussionMember(intent.Viewer, intent.Command) {
			member, err := s.discussionMemberForViewer(ctx, intent.Viewer, intent.Model.UpdatedAt)
			if err != nil {
				return intent, err
			}
			if err := s.discuss.AddMember(ctx, groupID, member); err != nil {
				return intent, err
			}
		}
		intent.ViewerDiscussionMemberReady = true
	case !intent.MissionRelationsReady:
		participants := missionParticipantsFromTeam(intent.Model.Team.Members)
		if intent.Viewer.ViewerKind == ViewerKindSystem {
			if err := s.authzRelations.WriteMissionParticipants(ctx, missionID.String(), intent.TenantID, participants); err != nil {
				return intent, err
			}
		} else if strings.TrimSpace(intent.Model.Mission.OriginTodoID) != "" {
			if err := s.authzRelations.WriteMissionParticipants(ctx, missionID.String(), intent.TenantID, participants); err != nil {
				return intent, err
			}
		} else if err := s.authzRelations.WriteMissionCreated(ctx, missionSubjectFromViewer(intent.Viewer), missionID.String(), intent.TenantID, participants); err != nil {
			return intent, err
		}
		for _, owner := range missionControlOwnersFromTeam(intent.Model.Team.Members) {
			if err := s.authzRelations.WriteMissionOwner(ctx, owner, missionID.String(), intent.TenantID); err != nil {
				return intent, err
			}
		}
		for _, subject := range s.missionAutomationSubjects(intent.Viewer) {
			if err := s.authzRelations.WriteMissionAutomation(ctx, subject, missionID.String(), intent.TenantID); err != nil {
				return intent, err
			}
		}
		if err := s.authzRelations.WriteMissionOrigins(ctx, missionID.String(), intent.TenantID, intent.Model.Mission.OriginTodoID, intent.Model.Mission.OriginDevelopmentProjectID); err != nil {
			return intent, err
		}
		intent.MissionRelationsReady = true
	case !intent.DiscussionRelationsReady:
		if groupID != "" {
			owner := missionSubjectFromViewer(intent.Viewer)
			if intent.Viewer.ViewerKind == ViewerKindSystem {
				owner = authorization.SubjectRef{}
			}
			if err := s.authzRelations.WriteDiscussionCreated(ctx, owner, string(groupID), missionID.String(), intent.TenantID, missionParticipantsFromTeam(intent.Model.Team.Members)); err != nil {
				return intent, err
			}
		}
		intent.DiscussionRelationsReady = true
	case !intent.ProjectionPublished:
		if err := saveMissionProjection(ctx, s.repo, intent.TenantID, intent.Model); err != nil {
			return intent, err
		}
		intent.ProjectionPublished = true
	case !intent.AuditRecorded:
		if s.audit != nil {
			if err := s.audit.RecordAuditEvent(ctx, missionCreationAuditEvent(intent.Viewer, intent.Command, missionID.String(), intent.Model.Mission.CreatedAt)); err != nil {
				return intent, err
			}
		}
		intent.AuditRecorded = true
	default:
		intent.Complete = true
	}
	intent.Version++
	intent.UpdatedAt = s.now()
	return intent, nil
}

func (s *LifecycleService) publishMissionCreatedEvent(ctx context.Context, intent missionCreationIntent) error {
	if s == nil || s.missionEvents == nil {
		return ErrServiceDependency
	}
	mission := intent.Model.Mission
	tenantID := missionTenantID(intent.TenantID)
	missionID := mission.MissionID
	if missionID.IsZero() || mission.CreatedAt.IsZero() {
		return ErrInvalidRequest
	}
	return s.missionEvents.Append(ctx, missionevent.Envelope{
		EventID:        missionCreatedEventID(tenantID, missionID),
		TenantID:       tenantID,
		MissionID:      missionID,
		StreamID:       missionevent.MissionStreamID(tenantID, missionID),
		StreamVersion:  1,
		EventType:      missionevent.EventMissionCreated,
		EventVersion:   1,
		Actor:          actorFromViewer(intent.Viewer),
		CorrelationID:  firstString(intent.Command.RequestID, intent.Command.IdempotencyKey),
		IdempotencyKey: missionCreatedEventIdempotencyKey(tenantID, missionID),
		OccurredAt:     mission.CreatedAt,
		Payload: missionevent.MissionCreatedPayload{
			Title:                            mission.Title,
			FinalGoal:                        mission.FinalGoal,
			OriginKind:                       mission.OriginKind,
			OriginID:                         mission.OriginID,
			OriginTodoID:                     mission.OriginTodoID,
			ExecutionAttempt:                 mission.ExecutionAttempt,
			DiscussionMode:                   mission.DiscussionMode,
			OriginDevelopmentProjectID:       mission.OriginDevelopmentProjectID,
			PersonalAssistantOriginOwnerID:   mission.PersonalAssistantOriginOwnerID,
			PersonalAssistantOriginSubjectID: mission.PersonalAssistantOriginSubjectID,
		},
	})
}

func missionCreatedEventID(tenantID string, missionID missioncore.MissionID) string {
	sum := sha256.Sum256([]byte("mission-created\x00" + missionTenantID(tenantID) + "\x00" + missionID.String()))
	return "event-mission-created-" + hex.EncodeToString(sum[:16])
}

func missionCreatedEventIdempotencyKey(tenantID string, missionID missioncore.MissionID) string {
	return "mission-created|" + missionTenantID(tenantID) + "|" + missionID.String()
}

func (s *LifecycleService) missionAutomationSubjects(viewer ViewerRef) []authorization.SubjectRef {
	subjects := append([]authorization.SubjectRef(nil), s.systemAutomation...)
	if viewer.ViewerKind == ViewerKindSystem {
		subjects = append(subjects, missionSubjectFromViewer(viewer))
	}
	return normalizeSystemAutomation(subjects)
}

func missionCreationAuditEvent(viewer ViewerRef, cmd CreateMissionCommand, missionID string, occurredAt time.Time) AuditEvent {
	tenantID := viewer.Tenant()
	metadata := map[string]string{
		"tenant_id": tenantID,
	}
	for key, value := range map[string]string{
		"origin_kind":                    cmd.OriginKind,
		"origin_id":                      cmd.OriginID,
		"origin_todo_id":                 cmd.OriginTodoID,
		"discussion_mode":                cmd.DiscussionMode,
		"origin_development_project_id":  cmd.OriginDevelopmentProjectID,
		"requester_id":                   cmd.RequesterID,
		"initial_human_agent_profile_id": cmd.InitialHumanAgentID,
	} {
		if value = strings.TrimSpace(value); value != "" {
			metadata[key] = value
		}
	}
	if cmd.ExecutionAttempt > 0 {
		metadata["execution_attempt"] = strconv.Itoa(cmd.ExecutionAttempt)
	}
	return AuditEvent{
		AuditID: missionCreationAuditID(tenantID, missionID), TenantID: tenantID,
		ActorID: viewer.ViewerID, ViewerID: viewer.ViewerID, MissionID: missionID,
		EntityType: "mission", EntityID: missionID, Action: "mission.create", Result: "succeeded", Severity: "info",
		CorrelationID: firstString(cmd.RequestID, cmd.IdempotencyKey), Summary: "created mission", Metadata: metadata, OccurredAt: occurredAt,
	}
}

func missionCreationAuditID(tenantID, missionID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(tenantID) + "\x00" + strings.TrimSpace(missionID)))
	return "audit-mission-create-" + hex.EncodeToString(sum[:12])
}

func (s *LifecycleService) ReconcilePendingMissionCreations(ctx context.Context, limit int) error {
	if s == nil || s.router == nil || s.repo == nil || s.authorization == nil || s.authzRelations == nil {
		return ErrServiceDependency
	}
	if limit <= 0 {
		limit = 100
	}
	const pageSize = 200
	after := ""
	processed := 0
	var reconcileErrors []error
	for processed < limit {
		page, err := s.router.ListRecords(ctx, missionCreationIntentTable, after, pageSize)
		if err != nil {
			return errors.Join(append(reconcileErrors, err)...)
		}
		for _, item := range page.Items {
			after = item.Key
			intent, err := missionCreationIntentFromRecord(item.Record)
			if err != nil {
				reconcileErrors = append(reconcileErrors, err)
				continue
			}
			if intent.Complete {
				continue
			}
			processed++
			if _, err := s.advanceMissionCreation(ctx, item.Key); err != nil {
				reconcileErrors = append(reconcileErrors, err)
			}
			if processed >= limit {
				break
			}
		}
		if !page.HasMore || len(page.Items) == 0 {
			break
		}
	}
	return errors.Join(reconcileErrors...)
}

var errMissionCreationIntentConflict = errors.New("mission creation intent version conflict")

func (s *LifecycleService) loadMissionCreationIntent(ctx context.Context, key string) (missionCreationIntent, db.Record, bool, error) {
	if s == nil || s.router == nil {
		return missionCreationIntent{}, nil, false, ErrServiceDependency
	}
	record, ok, err := s.router.GetRecord(ctx, missionCreationIntentTable, key)
	if err != nil || !ok {
		return missionCreationIntent{}, record, ok, err
	}
	intent, err := missionCreationIntentFromRecord(record)
	return intent, record, true, err
}

func (s *LifecycleService) saveMissionCreationIntent(ctx context.Context, intent missionCreationIntent, expectedVersion int64) error {
	current, ok, err := s.router.GetRecord(ctx, missionCreationIntentTable, intent.IntentKey)
	if err != nil {
		return err
	}
	if !ok || missionCreationIntentRecordVersion(current) != expectedVersion {
		return errMissionCreationIntentConflict
	}
	swapped, err := s.router.CompareAndSwapRecord(ctx, missionCreationIntentTable, intent.IntentKey, current, missionCreationIntentRecord(intent))
	if err != nil {
		return err
	}
	if !swapped {
		return errMissionCreationIntentConflict
	}
	return nil
}

func missionCreationIntentRecord(intent missionCreationIntent) db.Record {
	payload, _ := json.Marshal(intent)
	return db.Record{
		"intent_key": intent.IntentKey, "tenant_id": intent.TenantID, "mission_id": intent.Model.Mission.MissionID.String(),
		"complete": intent.Complete, "version": intent.Version, "payload": string(payload),
	}
}

func missionCreationIntentFromRecord(record db.Record) (missionCreationIntent, error) {
	payload, _ := record["payload"].(string)
	if payload == "" {
		return missionCreationIntent{}, fmt.Errorf("%w: mission creation intent payload is missing", ErrServiceDependency)
	}
	var intent missionCreationIntent
	if err := json.Unmarshal([]byte(payload), &intent); err != nil {
		return missionCreationIntent{}, fmt.Errorf("decode mission creation intent: %w", err)
	}
	if err := intent.validatePersonalAssistantOrigin(); err != nil {
		return missionCreationIntent{}, err
	}
	return intent, nil
}

func missionCreationIntentRecordVersion(record db.Record) int64 {
	switch value := record["version"].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	default:
		return 0
	}
}

func missionCreationIntentKey(viewer ViewerRef, idempotencyKey string) string {
	identity := viewer.Tenant() + "|" + string(viewer.ViewerKind) + "|" + firstString(viewer.AuthorizationSubjectID, viewer.ViewerID) + "|" + strings.TrimSpace(idempotencyKey)
	sum := sha256.Sum256([]byte(identity))
	return viewer.Tenant() + "/" + hex.EncodeToString(sum[:])
}

func normalizeMissionCreationCommand(cmd CreateMissionCommand) CreateMissionCommand {
	cmd.MissionID = strings.TrimSpace(cmd.MissionID)
	cmd.RequestID = strings.TrimSpace(cmd.RequestID)
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	cmd.Title = strings.TrimSpace(cmd.Title)
	cmd.FinalGoal = strings.TrimSpace(cmd.FinalGoal)
	cmd.BusinessDirection = strings.TrimSpace(cmd.BusinessDirection)
	cmd.RequesterID = strings.TrimSpace(cmd.RequesterID)
	cmd.InitialHumanAgentID = strings.TrimSpace(cmd.InitialHumanAgentID)
	cmd.InitialHumanDisplayName = strings.TrimSpace(cmd.InitialHumanDisplayName)
	cmd.InitialAgentProfileID = strings.TrimSpace(cmd.InitialAgentProfileID)
	cmd.OriginKind = strings.TrimSpace(cmd.OriginKind)
	cmd.OriginID = strings.TrimSpace(cmd.OriginID)
	cmd.OriginTodoID = strings.TrimSpace(cmd.OriginTodoID)
	cmd.DiscussionMode = strings.TrimSpace(cmd.DiscussionMode)
	cmd.OriginDevelopmentProjectID = strings.TrimSpace(cmd.OriginDevelopmentProjectID)
	cmd.ExistingConversationID = strings.TrimSpace(cmd.ExistingConversationID)
	cmd.SourcePersonalAssistantConversationID = strings.TrimSpace(cmd.SourcePersonalAssistantConversationID)
	return cmd
}

func normalizeMissionCreationViewer(viewer ViewerRef) ViewerRef {
	viewer.ViewerID = strings.TrimSpace(viewer.ViewerID)
	viewer.AuthorizationSubjectID = strings.TrimSpace(viewer.AuthorizationSubjectID)
	viewer.TenantID = viewer.Tenant()
	viewer.OrganizationScope = nil
	viewer.MissionRoles = nil
	viewer.PermissionScope = nil
	return viewer
}

func sameMissionCreationRequest(intent missionCreationIntent, viewer ViewerRef, cmd CreateMissionCommand, personalAssistantOrigin PersonalAssistantMissionOrigin) bool {
	wantedViewer := normalizeMissionCreationViewer(viewer)
	wantedCommand := normalizeMissionCreationCommand(cmd)
	wantedCommand.RequestID = intent.Command.RequestID
	originMatches := personalAssistantOrigin.isZero() ||
		(strings.TrimSpace(intent.PersonalAssistantOriginOwnerID) == personalAssistantOrigin.owner() &&
			strings.TrimSpace(intent.PersonalAssistantOriginSubjectID) == personalAssistantOrigin.subject())
	return intent.TenantID == wantedViewer.Tenant() &&
		intent.Viewer.ViewerID == wantedViewer.ViewerID && intent.Viewer.ViewerKind == wantedViewer.ViewerKind &&
		intent.Viewer.AuthorizationSubjectID == wantedViewer.AuthorizationSubjectID && intent.Command == wantedCommand &&
		originMatches
}

func (intent missionCreationIntent) validatePersonalAssistantOrigin() error {
	if intent.MissionCreatedEventPublished && !intent.MissionCreatedEventRequired {
		return ErrInvalidRequest
	}
	ownerID := strings.TrimSpace(intent.PersonalAssistantOriginOwnerID)
	subjectID := strings.TrimSpace(intent.PersonalAssistantOriginSubjectID)
	if (ownerID == "") != (subjectID == "") {
		return ErrInvalidRequest
	}
	if strings.TrimSpace(intent.Model.Mission.PersonalAssistantOriginOwnerID) != ownerID ||
		strings.TrimSpace(intent.Model.Mission.PersonalAssistantOriginSubjectID) != subjectID {
		return ErrInvalidRequest
	}
	return nil
}
