package team

import (
	"context"
	"errors"
	"fmt"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/uow"
)

type MissionTeamService interface {
	RequestStaffing(ctx context.Context, req StaffingRequest) (StaffingRequestID, error)
	Appoint(ctx context.Context, cmd AppointmentCommand) (MissionAssignment, error)
	Release(ctx context.Context, cmd ReleaseCommand) error
	Replace(ctx context.Context, cmd ReplaceCommand) (MissionAssignment, error)
	ListTeam(ctx context.Context, missionID missioncore.MissionID) (MissionTeam, error)
}

type ServiceOptions struct {
	UnitOfWork  *uow.UnitOfWork
	Repository  *PlatformTeamRepository
	ProfileRepo agentcore.AgentProfileRepository
	Provisioner agentcore.AgentInstanceProvisioner
	EventSink   *StaffingEventSink
	Policy      AssignmentPolicy
	Clock       clock.Clock
	IDGenerator idgen.ErrorGenerator
	Logger      logging.Logger
	Metrics     StaffingMetricRecorder
	Tracer      StaffingTracer
}

type Service struct {
	unit        *uow.UnitOfWork
	repo        *PlatformTeamRepository
	profileRepo agentcore.AgentProfileRepository
	provisioner agentcore.AgentInstanceProvisioner
	events      *StaffingEventSink
	policy      AssignmentPolicy
	clock       clock.Clock
	ids         idgen.ErrorGenerator
	logger      logging.Logger
	metrics     StaffingMetricRecorder
	tracer      StaffingTracer
}

type StaffingMetricRecorder interface {
	CaptureContext(ctx context.Context, name string, fields map[string]any) bool
}

type StaffingTracer interface {
	Start(ctx context.Context, name string) (context.Context, func(error))
}

func NewService(opts ServiceOptions) *Service {
	repo := opts.Repository
	if repo == nil {
		repo = NewPlatformTeamRepository()
	}
	events := opts.EventSink
	if events == nil {
		events = NewStaffingEventSink()
	}
	policy := opts.Policy
	if policy.defaults == (StaffingPolicy{}) {
		policy = NewAssignmentPolicy(DefaultStaffingPolicy())
	}
	return &Service{
		unit:        opts.UnitOfWork,
		repo:        repo,
		profileRepo: opts.ProfileRepo,
		provisioner: opts.Provisioner,
		events:      events,
		policy:      policy,
		clock:       opts.Clock,
		ids:         opts.IDGenerator,
		logger:      opts.Logger,
		metrics:     opts.Metrics,
		tracer:      opts.Tracer,
	}
}

func (s *Service) RequestStaffing(ctx context.Context, req StaffingRequest) (StaffingRequestID, error) {
	ctx, finish := s.startSpan(ctx, "mission_team.request_staffing")
	var opErr error
	defer func() {
		finish(opErr)
		s.observeMutation(ctx, "request_staffing", req.MissionID, req.ID, "", opErr)
	}()
	if req.MissionID.IsZero() {
		opErr = fmt.Errorf("%w: mission_id is required", ErrInvalidArgument)
		return "", opErr
	}
	if req.ID == "" {
		id, err := s.newID("staffing-request")
		if err != nil {
			opErr = err
			return "", opErr
		}
		req.ID = StaffingRequestID(id)
	}
	if req.Status == "" {
		req.Status = StaffingRequestStatusRequested
	}
	if req.CreatedAt.IsZero() {
		req.CreatedAt = s.now()
	}
	if err := s.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := s.repo.SaveStaffingRequest(ctx, tx, req); err != nil {
			return err
		}
		return s.events.Emit(ctx, tx, StaffingEventCandidate{
			Type:           missionevent.EventStaffingRequested,
			MissionID:      req.MissionID,
			RequestID:      req.ID,
			Actor:          req.RequestedBy,
			IdempotencyKey: firstNonEmpty(req.IdempotencyKey, string(req.ID)),
			Reason:         req.Reason,
		})
	}); err != nil {
		opErr = err
		return "", opErr
	}
	return req.ID, nil
}

func firstPolicyCode(codes []PolicyCode) PolicyCode {
	if len(codes) == 0 {
		return ""
	}
	return codes[0]
}

func duplicateActiveAssignment(team MissionTeam, profileID agentcore.AgentProfileID, role MissionRole) bool {
	for _, member := range team.Members {
		if member.AgentProfileID != profileID {
			continue
		}
		for _, ref := range member.AssignmentRefs {
			if ref.Role == role && ref.Status == AssignmentStatusActive {
				return true
			}
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func scopedEventKey(commandKey, fallback, event string) string {
	base := firstNonEmpty(commandKey, fallback)
	if base == "" {
		return event
	}
	return base + "|" + event
}

func IsConflict(err error) bool {
	return errors.Is(err, ErrConflict)
}
