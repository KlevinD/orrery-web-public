package missioncontrol

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	missioncore "orrery_v2/internal/mission/core"
	discussion "orrery_v2/internal/mission/discussion"
	"orrery_v2/internal/mission/projection"
	missiontask "orrery_v2/internal/mission/task"
	missionteam "orrery_v2/internal/mission/team"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/uow"
)

type LifecycleOptions struct {
	Clock                  clock.Clock
	IDGenerator            idgen.ErrorGenerator
	TaskRepo               missiontask.Repository
	AgentDirectory         AgentDirectoryPort
	Discussion             DiscussionInitializerPort
	TeamBootstrap          TeamBootstrapPort
	SystemRoles            []SystemRoleBootstrap
	SystemAutomation       []authorization.SubjectRef
	HumanProfiles          HumanProfilePort
	HumanBootstrap         HumanAgentBootstrap
	AuthorizationRelations *authorization.RelationProjector
	AuthorizationService   *authorization.Service
	TaskResponsibility     TaskResponsibilityAssignmentPort
	AuditRecorder          AuditRecorderPort
	MissionEvents          MissionEventSinkPort
}

type TaskResponsibilityAssignmentSignal struct {
	TenantID                      string
	MissionID                     string
	TaskID                        string
	SourceKind                    string
	SourceID                      string
	SourceStepID                  string
	ActorMemberID                 string
	AgentProfileID                string
	Priority                      int
	DueAt                         time.Time
	ActorID                       string
	Reason                        string
	ExpectedResponsibilityVersion int64
}

type TaskResponsibilityAssignmentPort interface {
	RecordTaskAssignment(ctx context.Context, tx uow.Tx, signal TaskResponsibilityAssignmentSignal) error
}

type HistoricalTaskResponsibilityAssignmentSignal struct {
	TenantID       string
	MissionID      string
	TaskID         string
	SourceKind     string
	SourceID       string
	SourceStepID   string
	AgentProfileID string
	State          string
	Priority       int
	DueAt          time.Time
	ActorID        string
	Reason         string
	OccurredAt     time.Time
}

type TaskResponsibilityHistoricalImportPort interface {
	RecordHistoricalTaskAssignment(ctx context.Context, tx uow.Tx, signal HistoricalTaskResponsibilityAssignmentSignal) error
}

type CanonicalConversationMembershipRequest struct {
	ActorKind      string
	ActorID        string
	TenantID       string
	ConversationID string
	MissionID      string
	TodoID         string
	AgentID        string
	ManagedSource  string
}

type CanonicalConversationMembershipSetRequest struct {
	ActorKind      string
	ActorID        string
	TenantID       string
	ConversationID string
	MissionID      string
	TodoID         string
	AgentIDs       []string
	HumanIDs       []string
	ManagedSource  string
}

type CanonicalConversationMembershipPort interface {
	EnsureMissionAgentMember(context.Context, CanonicalConversationMembershipRequest) error
	EnsureTodoMissionAgentMember(context.Context, CanonicalConversationMembershipRequest) error
	ReconcileMissionAgentMembers(context.Context, CanonicalConversationMembershipSetRequest) error
	ReconcileTodoMissionAgentMembers(context.Context, CanonicalConversationMembershipSetRequest) error
}

type AgentDirectoryPort interface {
	QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
}

type DiscussionInitializerPort interface {
	CreateGroup(ctx context.Context, missionID missioncore.MissionID, goal string) (discussion.DiscussionGroup, error)
	AddMember(ctx context.Context, groupID discussion.DiscussionGroupID, member discussion.DiscussionMember) error
}

type TeamBootstrapPort interface {
	Appoint(ctx context.Context, cmd missionteam.AppointmentCommand) (missionteam.MissionAssignment, error)
}

type HumanProfilePort interface {
	EnsureHumanProfile(ctx context.Context, cmd EnsureHumanProfileCommand) (agentcore.AgentProfile, error)
}

type EnsureHumanProfileCommand struct {
	HumanAgentID string
	DisplayName  string
}

type SystemRoleBootstrap struct {
	ProfileID         agentcore.AgentProfileID
	Role              missionteam.MissionRole
	Responsibility    string
	AuthorityScope    string
	IdempotencySuffix string
}

type HumanAgentBootstrap struct {
	Enabled        bool
	MissionRole    missionteam.MissionRole
	Responsibility string
	AuthorityScope string
}

type LifecycleService struct {
	uow    *uow.UnitOfWork
	router *db.Router
	repo   interface {
		ProjectionRepository
		Save(context.Context, projection.ReadModel) error
	}
	taskRepo         missiontask.Repository
	clock            clock.Clock
	generator        idgen.ErrorGenerator
	agents           AgentDirectoryPort
	discuss          DiscussionInitializerPort
	teamBootstrap    TeamBootstrapPort
	systemRoles      []SystemRoleBootstrap
	systemAutomation []authorization.SubjectRef
	humanProfiles    HumanProfilePort
	humanBootstrap   HumanAgentBootstrap
	authzRelations   *authorization.RelationProjector
	authorization    *authorization.Service
	responsibility   TaskResponsibilityAssignmentPort
	canonicalChat    CanonicalConversationMembershipPort
	audit            AuditRecorderPort
	missionEvents    MissionEventSinkPort
}

func NewLifecycleService(unit *uow.UnitOfWork, router *db.Router, repo interface {
	ProjectionRepository
	Save(context.Context, projection.ReadModel) error
}, opts LifecycleOptions) *LifecycleService {
	taskRepo := opts.TaskRepo
	if taskRepo == nil {
		taskRepo = missiontask.NewPlatformRepository()
	}
	return &LifecycleService{uow: unit, router: router, repo: repo, taskRepo: taskRepo, clock: opts.Clock, generator: opts.IDGenerator, agents: opts.AgentDirectory, discuss: opts.Discussion, teamBootstrap: opts.TeamBootstrap, systemRoles: append([]SystemRoleBootstrap(nil), opts.SystemRoles...), systemAutomation: normalizeSystemAutomation(opts.SystemAutomation), humanProfiles: opts.HumanProfiles, humanBootstrap: opts.HumanBootstrap, authzRelations: opts.AuthorizationRelations, authorization: opts.AuthorizationService, responsibility: opts.TaskResponsibility, audit: opts.AuditRecorder, missionEvents: opts.MissionEvents}
}

func normalizeSystemAutomation(subjects []authorization.SubjectRef) []authorization.SubjectRef {
	unique := make(map[string]authorization.SubjectRef, len(subjects))
	for _, subject := range subjects {
		subject.Kind = strings.TrimSpace(subject.Kind)
		subject.ID = strings.TrimSpace(subject.ID)
		if subject.Kind != string(ViewerKindSystem) || subject.ID == "" {
			continue
		}
		unique[subject.Kind+":"+subject.ID] = subject
	}
	keys := make([]string, 0, len(unique))
	for key := range unique {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]authorization.SubjectRef, 0, len(keys))
	for _, key := range keys {
		out = append(out, unique[key])
	}
	return out
}

func (s *LifecycleService) SetCanonicalConversationMembership(port CanonicalConversationMembershipPort) {
	if s != nil {
		s.canonicalChat = port
	}
}

func (s *LifecycleService) CreateMission(ctx context.Context, viewer ViewerRef, cmd CreateMissionCommand) (MissionDTO, error) {
	return s.createMission(ctx, viewer, cmd, PersonalAssistantMissionOrigin{})
}

func (s *LifecycleService) UpdateMissionBrief(ctx context.Context, viewer ViewerRef, cmd UpdateMissionBriefCommand) (MissionDTO, error) {
	if s == nil || s.repo == nil {
		return MissionDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return MissionDTO{}, err
	}
	if cmd.IdempotencyKey == "" || cmd.MissionID.IsZero() || (strings.TrimSpace(cmd.Title) == "" && strings.TrimSpace(cmd.FinalGoal) == "") {
		return MissionDTO{}, ErrInvalidRequest
	}
	if err := s.authorizeMissionAction(ctx, viewer, cmd.MissionID, "mission.edit"); err != nil {
		return MissionDTO{}, err
	}
	model, err := loadMissionProjection(ctx, s.repo, viewer.Tenant(), cmd.MissionID)
	if err != nil {
		return MissionDTO{}, err
	}
	if title := strings.TrimSpace(cmd.Title); title != "" {
		model.Mission.Title = title
	}
	if finalGoal := strings.TrimSpace(cmd.FinalGoal); finalGoal != "" {
		model.Mission.FinalGoal = finalGoal
	}
	now := s.now()
	model = bumpModel(model, now)
	if err := saveMissionProjection(ctx, s.repo, viewer.Tenant(), model); err != nil {
		return MissionDTO{}, err
	}
	return MapViewDTO(model, viewer).Mission, nil
}

func (s *LifecycleService) authorizeMissionAction(ctx context.Context, viewer ViewerRef, missionID missioncore.MissionID, action string) error {
	if s == nil || s.authorization == nil {
		return ErrServiceDependency
	}
	if isBreakglassSystemViewer(viewer) {
		return nil
	}
	err := s.authorization.Can(ctx, missionPrincipal(viewer), action, identity.ObjectRef{Type: "mission", ID: missionID.String(), TenantID: viewer.Tenant()})
	if errors.Is(err, authorization.ErrDenied) {
		return ErrProjectionNotFound
	}
	return err
}

func (s *LifecycleService) bootstrapSystemRoles(ctx context.Context, groupID discussion.DiscussionGroupID, missionID missioncore.MissionID, cmd CreateMissionCommand) ([]missionteam.MissionAssignment, error) {
	if s == nil || s.teamBootstrap == nil {
		return nil, nil
	}
	assignments := make([]missionteam.MissionAssignment, 0, len(s.systemRoles))
	for _, role := range s.systemRoles {
		if role.ProfileID.IsZero() || !role.Role.IsValid() {
			continue
		}
		assignment, err := s.teamBootstrap.Appoint(ctx, missionteam.AppointmentCommand{
			IDempotencyKey: systemRoleIdempotencyKey(cmd.IdempotencyKey, role),
			MissionID:      missionID,
			ProfileID:      role.ProfileID,
			Role:           role.Role,
			Responsibility: firstString(role.Responsibility, "Required Mission system role"),
			AuthorityScope: role.AuthorityScope,
			AppointedBy:    missionteam.ActorRef{ID: "mission-bootstrap", Kind: missionteam.ActorKindSystem},
			Reason:         "Default Mission system role bootstrap",
			AllowAI:        true,
		})
		if err != nil {
			return nil, err
		}
		if s.discuss != nil && groupID != "" {
			if err := s.discuss.AddMember(ctx, groupID, discussion.NewDiscussionMemberFromAssignment(groupID, assignment)); err != nil {
				return nil, err
			}
		}
		assignments = append(assignments, assignment)
	}
	initialAgentID := agentcore.AgentProfileID(strings.TrimSpace(cmd.InitialAgentProfileID))
	if !initialAgentID.IsZero() {
		alreadyAssigned := false
		for _, assignment := range assignments {
			if assignment.AgentProfileID == initialAgentID {
				alreadyAssigned = true
				break
			}
		}
		if !alreadyAssigned {
			assignment, err := s.teamBootstrap.Appoint(ctx, missionteam.AppointmentCommand{
				IDempotencyKey: cmd.IdempotencyKey + "|agent|" + initialAgentID.String(), MissionID: missionID,
				ProfileID: initialAgentID, Role: missionteam.MissionRoleWorker, Responsibility: "Execute the scheduled Mission Task",
				AppointedBy: missionteam.ActorRef{ID: "mission-bootstrap", Kind: missionteam.ActorKindSystem}, Reason: "Scheduled Mission Agent bootstrap",
				AllowUnknownAvailability: true, AllowAI: true, AllowOrganizationalRoleBridge: true,
			})
			if err != nil {
				return nil, err
			}
			if s.discuss != nil && groupID != "" {
				if err := s.discuss.AddMember(ctx, groupID, discussion.NewDiscussionMemberFromAssignment(groupID, assignment)); err != nil {
					return nil, err
				}
			}
			assignments = append(assignments, assignment)
		}
	}
	return assignments, nil
}

func (s *LifecycleService) bootstrapInitialHumanAgent(ctx context.Context, groupID discussion.DiscussionGroupID, missionID missioncore.MissionID, cmd CreateMissionCommand) (missionteam.MissionAssignment, error) {
	humanID := strings.TrimSpace(cmd.InitialHumanAgentID)
	if s == nil || !s.humanBootstrap.Enabled || s.teamBootstrap == nil || s.humanProfiles == nil || humanID == "" {
		return missionteam.MissionAssignment{}, nil
	}
	profile, err := s.humanProfiles.EnsureHumanProfile(ctx, EnsureHumanProfileCommand{HumanAgentID: humanID, DisplayName: cmd.InitialHumanDisplayName})
	if err != nil {
		return missionteam.MissionAssignment{}, err
	}
	role := s.humanBootstrap.MissionRole
	if !role.IsValid() {
		role = missionteam.MissionRoleRealHumanContributor
	}
	assignment, err := s.teamBootstrap.Appoint(ctx, missionteam.AppointmentCommand{
		IDempotencyKey: humanAgentIdempotencyKey(cmd.IdempotencyKey),
		MissionID:      missionID,
		ProfileID:      profile.ID,
		Role:           role,
		Responsibility: firstString(s.humanBootstrap.Responsibility, "Initial human Mission contributor"),
		AuthorityScope: s.humanBootstrap.AuthorityScope,
		AppointedBy:    missionteam.ActorRef{ID: "mission-bootstrap", Kind: missionteam.ActorKindSystem},
		Reason:         "Initial human task author bootstrap",
		AllowHuman:     true, AllowAI: false, AllowOrganizationalRoleBridge: true,
	})
	if err != nil {
		return missionteam.MissionAssignment{}, err
	}
	if s.discuss != nil && groupID != "" {
		if err := s.discuss.AddMember(ctx, groupID, discussion.NewHumanDiscussionMemberFromAssignment(groupID, humanID, assignment)); err != nil {
			return missionteam.MissionAssignment{}, err
		}
	}
	return assignment, nil
}

func (s *LifecycleService) shouldAddViewerDiscussionMember(viewer ViewerRef, cmd CreateMissionCommand) bool {
	if viewer.ViewerKind == ViewerKindSystem {
		return false
	}
	viewerHumanID := firstString(viewer.AuthorizationSubjectID, viewer.ViewerID)
	if strings.TrimSpace(cmd.InitialHumanAgentID) != "" && (viewer.ViewerKind == ViewerKindUser || viewer.ViewerKind == ViewerKindHuman) && viewerHumanID == strings.TrimSpace(cmd.InitialHumanAgentID) {
		return false
	}
	if viewer.ViewerKind == ViewerKindAgent && s.bootstrapsSystemRoleProfile(viewer.ViewerID) {
		return false
	}
	return true
}

func (s *LifecycleService) bootstrapsSystemRoleProfile(profileID string) bool {
	if s == nil || strings.TrimSpace(profileID) == "" {
		return false
	}
	for _, role := range s.systemRoles {
		if role.ProfileID.String() == strings.TrimSpace(profileID) {
			return true
		}
	}
	return false
}

func humanAgentIdempotencyKey(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "mission"
	}
	return base + "|human|initial"
}

func (s *LifecycleService) upsertSystemRoleAssignments(ctx context.Context, model projection.ReadModel, assignments []missionteam.MissionAssignment, now time.Time) projection.ReadModel {
	for _, assignment := range assignments {
		if assignment.AgentInstanceID.IsZero() || assignment.ID == "" {
			continue
		}
		identity := s.assignmentAgentIdentity(ctx, assignment)
		model.Team.Members = upsertProjectionTeamMember(model.Team.Members, projection.TeamMemberView{
			MemberID:        string(assignment.MemberID),
			AgentID:         agentcore.AgentID(assignment.AgentProfileID),
			AgentProfileID:  assignment.AgentProfileID,
			AgentInstanceID: assignment.AgentInstanceID,
			AgentKind:       firstAgentKind(assignment.AgentKind, identity.AgentKind),
			DisplayName:     identity.DisplayName,
			RealName:        identity.RealName,
			RoleName:        firstString(identity.RoleName, string(assignment.Role)),
			MissionRole:     string(assignment.Role),
			Status:          projection.TeamMemberJoined,
			JoinedAt:        firstTime(assignment.AssignedAt, now),
		})
		model.Team.Assignments = upsertProjectionAssignment(model.Team.Assignments, projection.AssignmentView{
			AssignmentID:    string(assignment.ID),
			MemberID:        string(assignment.MemberID),
			AgentID:         agentcore.AgentID(assignment.AgentProfileID),
			AgentProfileID:  assignment.AgentProfileID,
			AgentInstanceID: assignment.AgentInstanceID,
			AgentKind:       firstAgentKind(assignment.AgentKind, identity.AgentKind),
			MissionRole:     string(assignment.Role),
			ChangedAt:       firstTime(assignment.AssignedAt, now),
		})
	}
	return model
}

type assignmentAgentIdentity struct {
	DisplayName string
	RealName    string
	RoleName    string
	AgentKind   agentcore.AgentKind
}

func (s *LifecycleService) assignmentAgentIdentity(ctx context.Context, assignment missionteam.MissionAssignment) assignmentAgentIdentity {
	if s == nil || s.agents == nil {
		return assignmentAgentIdentity{RoleName: string(assignment.Role)}
	}
	page, err := s.agents.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{EnabledOnly: false, IncludeAI: true, IncludeHumans: true})
	if err != nil {
		return assignmentAgentIdentity{RoleName: string(assignment.Role)}
	}
	for _, entry := range page.Entries {
		if entry.AgentProfileID != assignment.AgentProfileID {
			continue
		}
		if entry.AgentInstanceID != agentcore.AgentInstanceID(entry.AgentProfileID) {
			return assignmentAgentIdentity{RoleName: string(assignment.Role)}
		}
		return assignmentAgentIdentity{
			DisplayName: firstString(entry.DisplayName, entry.RealName),
			RealName:    entry.RealName,
			RoleName:    firstString(entry.RoleName, string(assignment.Role)),
			AgentKind:   entry.AgentKind,
		}
	}
	return assignmentAgentIdentity{RoleName: string(assignment.Role)}
}

func upsertProjectionTeamMember(members []projection.TeamMemberView, next projection.TeamMemberView) []projection.TeamMemberView {
	for i := range members {
		if members[i].AgentID == next.AgentID {
			if next.MemberID != "" {
				members[i].MemberID = next.MemberID
			}
			if next.AgentProfileID != "" {
				members[i].AgentProfileID = next.AgentProfileID
			}
			if next.AgentInstanceID != "" {
				members[i].AgentInstanceID = next.AgentInstanceID
			}
			if next.AgentKind != "" {
				members[i].AgentKind = next.AgentKind
			}
			if next.DisplayName != "" {
				members[i].DisplayName = next.DisplayName
			}
			if next.RealName != "" {
				members[i].RealName = next.RealName
			}
			if next.RoleName != "" {
				members[i].RoleName = next.RoleName
			}
			if next.MissionRole != "" {
				members[i].MissionRole = next.MissionRole
			}
			members[i].Status = next.Status
			members[i].JoinedAt = next.JoinedAt
			return members
		}
	}
	return append(members, next)
}

func upsertProjectionAssignment(assignments []projection.AssignmentView, next projection.AssignmentView) []projection.AssignmentView {
	for i := range assignments {
		if assignments[i].AssignmentID == next.AssignmentID {
			if next.MemberID == "" {
				next.MemberID = assignments[i].MemberID
			}
			assignments[i] = next
			return assignments
		}
	}
	return append(assignments, next)
}

func firstTime(value time.Time, fallback time.Time) time.Time {
	if !value.IsZero() {
		return value
	}
	return fallback
}

func firstAgentKind(values ...agentcore.AgentKind) agentcore.AgentKind {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func systemRoleIdempotencyKey(base string, role SystemRoleBootstrap) string {
	suffix := strings.TrimSpace(role.IdempotencySuffix)
	if suffix == "" {
		suffix = strings.ToLower(string(role.Role))
	}
	base = strings.TrimSpace(base)
	if base == "" {
		base = "mission-bootstrap"
	}
	return base + "|system-role|" + suffix
}

func (s *LifecycleService) discussionMemberForViewer(ctx context.Context, viewer ViewerRef, joinedAt time.Time) (discussion.DiscussionMember, error) {
	if viewer.ViewerKind == ViewerKindSystem {
		return discussion.DiscussionMember{}, ErrInvalidRequest
	}
	member := discussion.DiscussionMember{
		VisibilityScope: discussion.VisibilityMission,
		CanPost:         true,
		CanMention:      true,
		JoinedAt:        joinedAt,
	}
	switch viewer.ViewerKind {
	case ViewerKindAgent:
		binding, err := s.resolveViewerAgent(ctx, viewer.ViewerID)
		if err != nil {
			return discussion.DiscussionMember{}, err
		}
		member.AgentProfileID = binding.ProfileID
		member.AgentInstanceID = binding.InstanceID
		member.MemberKind = discussion.DiscussionMemberAgent
	default:
		member.UserID = viewer.ViewerID
		member.MemberKind = discussion.DiscussionMemberHuman
	}
	return member, nil
}

type viewerAgentBinding struct {
	ProfileID  agentcore.AgentProfileID
	InstanceID agentcore.AgentInstanceID
}

func (s *LifecycleService) resolveViewerAgent(ctx context.Context, viewerID string) (viewerAgentBinding, error) {
	viewerID = strings.TrimSpace(viewerID)
	if viewerID == "" {
		return viewerAgentBinding{}, ErrInvalidRequest
	}
	if s == nil || s.agents == nil {
		return viewerAgentBinding{}, ErrServiceDependency
	}
	page, err := s.agents.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{EnabledOnly: true, IncludeAI: true, IncludeHumans: true})
	if err != nil {
		return viewerAgentBinding{}, err
	}
	for _, entry := range page.Entries {
		if !entry.AgentKind.IsValid() || !entry.Enabled {
			continue
		}
		if entry.AgentProfileID.String() != viewerID {
			continue
		}
		profileID := entry.AgentProfileID
		if profileID.IsZero() {
			return viewerAgentBinding{}, fmt.Errorf("%w: agent viewer must reference an enabled agent profile", ErrInvalidRequest)
		}
		if entry.AgentInstanceID != agentcore.AgentInstanceID(profileID) {
			return viewerAgentBinding{}, fmt.Errorf("%w: agent viewer must reference a stable agent identity", ErrInvalidRequest)
		}
		return viewerAgentBinding{ProfileID: profileID, InstanceID: agentcore.AgentInstanceID(profileID)}, nil
	}
	return viewerAgentBinding{}, fmt.Errorf("%w: agent viewer must reference an enabled agent profile", ErrInvalidRequest)
}

func (s *LifecycleService) ListMissions(ctx context.Context, viewer ViewerRef, query ListMissionsQuery) (MissionListDTO, error) {
	if s == nil || s.router == nil || s.authorization == nil {
		return MissionListDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return MissionListDTO{}, err
	}
	var models []projection.ReadModel
	if tenantRepo, ok := s.repo.(TenantProjectionListRepository); ok {
		var err error
		models, err = tenantRepo.ListMissionControlsForTenant(ctx, viewer.Tenant())
		if err != nil {
			return MissionListDTO{}, err
		}
	} else {
		repo, ok := s.repo.(ProjectionListRepository)
		if !ok {
			return MissionListDTO{}, ErrServiceDependency
		}
		var err error
		models, err = repo.ListMissionControls(ctx)
		if err != nil {
			return MissionListDTO{}, err
		}
		filtered := models[:0]
		for _, model := range models {
			if missionModelTenantID(model) == viewer.Tenant() {
				filtered = append(filtered, model)
			}
		}
		models = filtered
	}
	visible := models[:0]
	for _, model := range models {
		if !isBreakglassSystemViewer(viewer) {
			decision, err := s.authorization.Check(ctx, authorization.CheckRequest{Principal: missionPrincipal(viewer), Action: "mission.view", Object: identity.ObjectRef{Type: "mission", ID: model.Mission.MissionID.String(), TenantID: viewer.Tenant()}})
			if err != nil {
				return MissionListDTO{}, err
			}
			if !decision.Allowed {
				continue
			}
		}
		if query.Status != "" && string(model.Mission.Status) != query.Status {
			continue
		}
		if query.OriginKind != "" && model.Mission.OriginKind != query.OriginKind {
			continue
		}
		if query.OriginID != "" && model.Mission.OriginID != query.OriginID {
			continue
		}
		if query.Search != "" && !strings.Contains(strings.ToLower(model.Mission.Title+" "+model.Mission.FinalGoal), strings.ToLower(query.Search)) {
			continue
		}
		visible = append(visible, model)
	}
	models = visible
	sort.Slice(models, func(i, j int) bool { return models[i].Mission.CreatedAt.After(models[j].Mission.CreatedAt) })
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	limit := query.Limit
	if limit <= 0 || limit > len(models)-offset {
		limit = len(models) - offset
	}
	if offset > len(models) {
		offset, limit = len(models), 0
	}
	out := MissionListDTO{Page: PageDTO{Limit: query.Limit, Offset: query.Offset, Total: len(models)}}
	for _, model := range models[offset : offset+limit] {
		view := MapViewDTO(model, viewer)
		dto := view.Mission
		out.Missions = append(out.Missions, dto)
		out.Summaries = append(out.Summaries, MapMissionSummaryDTO(model, viewer))
	}
	out.Page.Returned = len(out.Missions)
	out.Page.HasMore = offset+limit < len(models)
	return out, nil
}

func missionPrincipal(viewer ViewerRef) identity.Principal {
	subjectID := firstString(viewer.AuthorizationSubjectID, viewer.ViewerID)
	return identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "internal",
		Subject:         subjectID,
		UserID:          subjectID,
		Username:        subjectID,
		ViewerKind:      string(viewer.ViewerKind),
		TenantID:        viewer.Tenant(),
		CoarseScopes:    append([]string(nil), viewer.PermissionScope...),
	})
}

func bumpModel(model projection.ReadModel, now time.Time) projection.ReadModel {
	model.UpdatedAt = now
	model.Checkpoint.Version++
	model.Checkpoint.UpdatedAt = now
	return model
}

func (s *LifecycleService) now() time.Time {
	if s.clock != nil {
		return s.clock.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *LifecycleService) newID(prefix string) (string, error) {
	if s.generator == nil {
		return idgen.NewRandom(prefix).NewIDContext()
	}
	return s.generator.NewIDContext()
}

func missionSubjectFromViewer(viewer ViewerRef) authorization.SubjectRef {
	return authorization.SubjectRef{Kind: string(viewer.ViewerKind), ID: firstString(viewer.AuthorizationSubjectID, viewer.ViewerID)}
}

func missionParticipantsFromTeam(members []projection.TeamMemberView) []authorization.SubjectRef {
	out := make([]authorization.SubjectRef, 0, len(members))
	for _, member := range members {
		subject := missionSubjectFromTeamMember(member)
		if subject.Kind == "" || subject.ID == "" {
			continue
		}
		out = append(out, subject)
	}
	return out
}

func missionSubjectFromTeamMember(member projection.TeamMemberView) authorization.SubjectRef {
	id := strings.TrimSpace(string(member.AgentID))
	if member.AgentKind == agentcore.AgentKindHuman {
		id = firstString(strings.TrimSpace(string(member.AgentProfileID)), id)
	} else if id == "" {
		id = strings.TrimSpace(string(member.AgentProfileID))
	}
	if id == "" {
		return authorization.SubjectRef{}
	}
	kind := "agent"
	if member.AgentKind == agentcore.AgentKindHuman {
		kind = "user"
	}
	return authorization.SubjectRef{Kind: kind, ID: id}
}

func missionSubjectFromTaskAssignee(assignee taskAssigneeBinding) authorization.SubjectRef {
	id := strings.TrimSpace(string(assignee.StableAgentID))
	if id == "" {
		id = strings.TrimSpace(string(assignee.ProfileID))
	}
	if id == "" {
		return authorization.SubjectRef{}
	}
	kind := "agent"
	if assignee.Entry.AgentKind == agentcore.AgentKindHuman {
		kind = "user"
	}
	return authorization.SubjectRef{Kind: kind, ID: id}
}
