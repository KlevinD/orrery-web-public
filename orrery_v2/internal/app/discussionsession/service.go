package discussionsession

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	NewIDContext() (string, error)
}

type Logger interface {
	Debug(ctx context.Context, msg string, fields ...any)
	Warn(ctx context.Context, msg string, fields ...any)
}

type DiscussionExecutionClaims interface {
	Acquire(ctx context.Context, claimID, workerID string) (bool, error)
	Renew(ctx context.Context, claimID, workerID string) (bool, error)
	Release(ctx context.Context, claimID, workerID string) (bool, error)
}

type MissionLinkAvailabilityPort interface {
	MissionExists(ctx context.Context, tenantID, missionID string) (bool, error)
}

type ServiceOptions struct {
	Repository                 *Repository
	DiscussionAgent            DiscussionAgentRunner
	MentionResponder           DiscussionAgentMentionResponder
	MentionConfig              DiscussionMentionConfigProvider
	MissionTooling             MissionToolingPort
	MissionMessenger           MissionMessagePort
	ExecutionDeleter           ExecutionDeleter
	ArtifactCleaner            ConversationArtifactCleaner
	AttachmentStore            AttachmentStore
	WorkspaceAllowlist         WorkspaceAllowlistProvider
	ManagedWorkspaces          ManagedWorkspaceResolver
	RuntimeRecordCleaner       DiscussionRuntimeRecordCleaner
	MemoryProvider             DiscussionAgentMemoryProvider
	KnowledgeProvider          DiscussionAgentKnowledgeProvider
	AgentDirectory             AgentDirectoryPort
	HumanProfiles              DiscussionHumanProfileBootstrapPort
	EvaluationReviews          EvaluationReviewUsagePort
	AuthorizationRelations     ConversationAuthorizationRelations
	RealtimePublisher          RealtimePublisher
	RuntimeApprovalCoordinator *RuntimeApprovalCoordinator
	RuntimeSetupError          error
	InitialAgentMembers        []InitialAgentMemberSeed
	LegacyStreamHub            bool
	TurnRecoveryTimeout        time.Duration
	DiscussionExecutionClaims  DiscussionExecutionClaims
	MissionLinkAvailability    MissionLinkAvailabilityPort
	DiscussionExecutorID       string
	DiscussionLeaseRenew       time.Duration
	Clock                      Clock
	IDGenerator                IDGenerator
	Logger                     Logger
}

type Service struct {
	repo                 *Repository
	discussionAgent      DiscussionAgentRunner
	mentionResponder     DiscussionAgentMentionResponder
	mentionConfig        DiscussionMentionConfigProvider
	missionTooling       MissionToolingPort
	missionMessenger     MissionMessagePort
	executionDeleter     ExecutionDeleter
	artifactCleaner      ConversationArtifactCleaner
	attachments          AttachmentStore
	workspaceAllowlist   WorkspaceAllowlistProvider
	managedWorkspaces    ManagedWorkspaceResolver
	runtimeRecordCleaner DiscussionRuntimeRecordCleaner
	memory               DiscussionAgentMemoryProvider
	knowledge            DiscussionAgentKnowledgeProvider
	agents               AgentDirectoryPort
	humanProfiles        DiscussionHumanProfileBootstrapPort
	evaluationReviews    EvaluationReviewUsagePort
	authorization        ConversationAuthorizationRelations
	realtime             RealtimePublisher
	runtimeApprovals     *RuntimeApprovalCoordinator
	runtimeSetupError    error
	initialAgentMembers  []InitialAgentMemberSeed
	clock                Clock
	generator            IDGenerator
	streams              *conversationStreamHub
	logger               Logger
	turnRecoveryTimeout  time.Duration
	discussionClaims     DiscussionExecutionClaims
	missionLinks         MissionLinkAvailabilityPort
	discussionExecutorID string
	discussionLeaseRenew time.Duration
	activeRunsMu         sync.Mutex
	activeRuns           map[string]context.CancelFunc
	invalidatedRuns      map[string]bool
	streamTokenBuffers   map[string]*discussionStreamTokenBuffer
	runLifecycleLocksMu  sync.Mutex
	runLifecycleLocks    map[string]*discussionRunLifecycleLock
}

type InitialAgentMemberSeed struct {
	AgentID string
	Role    string
}

func NewService(opts ServiceOptions) *Service {
	initialMembers := append([]InitialAgentMemberSeed(nil), opts.InitialAgentMembers...)
	runtimeRecordCleaner := opts.RuntimeRecordCleaner
	if runtimeRecordCleaner == nil {
		if cleaner, ok := opts.DiscussionAgent.(DiscussionRuntimeRecordCleaner); ok {
			runtimeRecordCleaner = cleaner
		}
	}
	var streams *conversationStreamHub
	if opts.LegacyStreamHub {
		streams = newConversationStreamHub()
	}
	turnRecoveryTimeout := opts.TurnRecoveryTimeout
	if turnRecoveryTimeout <= 0 {
		turnRecoveryTimeout = 40 * time.Minute
	}
	discussionLeaseRenew := opts.DiscussionLeaseRenew
	if discussionLeaseRenew <= 0 {
		discussionLeaseRenew = 10 * time.Second
	}
	return &Service{
		repo:                 opts.Repository,
		discussionAgent:      opts.DiscussionAgent,
		mentionResponder:     opts.MentionResponder,
		mentionConfig:        opts.MentionConfig,
		missionTooling:       opts.MissionTooling,
		missionMessenger:     opts.MissionMessenger,
		executionDeleter:     opts.ExecutionDeleter,
		artifactCleaner:      opts.ArtifactCleaner,
		attachments:          opts.AttachmentStore,
		workspaceAllowlist:   opts.WorkspaceAllowlist,
		managedWorkspaces:    opts.ManagedWorkspaces,
		runtimeRecordCleaner: runtimeRecordCleaner,
		memory:               opts.MemoryProvider,
		knowledge:            opts.KnowledgeProvider,
		agents:               opts.AgentDirectory,
		humanProfiles:        opts.HumanProfiles,
		evaluationReviews:    opts.EvaluationReviews,
		authorization:        opts.AuthorizationRelations,
		realtime:             opts.RealtimePublisher,
		runtimeApprovals:     opts.RuntimeApprovalCoordinator,
		runtimeSetupError:    RuntimeUnavailableError(opts.RuntimeSetupError),
		initialAgentMembers:  initialMembers,
		clock:                opts.Clock,
		generator:            opts.IDGenerator,
		streams:              streams,
		logger:               opts.Logger,
		turnRecoveryTimeout:  turnRecoveryTimeout,
		discussionClaims:     opts.DiscussionExecutionClaims,
		missionLinks:         opts.MissionLinkAvailability,
		discussionExecutorID: strings.TrimSpace(opts.DiscussionExecutorID),
		discussionLeaseRenew: discussionLeaseRenew,
		activeRuns:           map[string]context.CancelFunc{},
		invalidatedRuns:      map[string]bool{},
		streamTokenBuffers:   map[string]*discussionStreamTokenBuffer{},
		runLifecycleLocks:    map[string]*discussionRunLifecycleLock{},
	}
}

func (s *Service) DeleteConversation(ctx context.Context, viewer ViewerRef, conversationID string) error {
	if s == nil || s.repo == nil {
		return ErrServiceDependency
	}
	conversationID = strings.TrimSpace(conversationID)
	if strings.TrimSpace(viewer.ViewerID) == "" || conversationID == "" {
		return ErrInvalidRequest
	}
	unlock := s.lockRunLifecycle(conversationID)
	defer unlock()
	if _, err := s.requireConversationAccess(ctx, viewer, conversationID); err != nil {
		return err
	}
	if err := s.deleteConversationLocked(ctx, conversationID); err != nil {
		if s.logger != nil {
			s.logger.Warn(ctx, "discussion deletion failed", "conversation_id", conversationID, "error", err)
		}
		return err
	}
	return nil
}

func (s *Service) GetConversation(ctx context.Context, viewer ViewerRef, conversationID string) (ConversationDTO, error) {
	if s == nil || s.repo == nil {
		return ConversationDTO{}, ErrServiceDependency
	}
	if strings.TrimSpace(viewer.ViewerID) == "" || strings.TrimSpace(conversationID) == "" {
		return ConversationDTO{}, ErrInvalidRequest
	}
	view, err := s.requireConversationAccess(ctx, viewer, conversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	if err := s.markCompletedConversationViewed(ctx, &view); err != nil {
		return ConversationDTO{}, err
	}
	return mapConversationDTO(view), nil
}

func (s *Service) SendMessage(ctx context.Context, viewer ViewerRef, cmd SendMessageCommand) (ConversationDTO, error) {
	if err := s.validateSendMessageCommand(viewer, cmd); err != nil {
		return ConversationDTO{}, err
	}
	commandRecord, claimed, recovered, releaseCommand, err := s.claimSendMessageCommand(ctx, viewer, cmd)
	if err != nil {
		return ConversationDTO{}, err
	}
	defer releaseCommand()
	if !claimed {
		return s.replayDiscussionCommand(ctx, commandRecord)
	}
	failCommand := func(cause error) {
		_ = s.failDiscussionCommand(context.WithoutCancel(ctx), commandRecord, cause)
	}
	if err := s.validateSendMessageRuntime(ctx, cmd, false); err != nil {
		failCommand(err)
		return ConversationDTO{}, err
	}
	if recovered {
		plan, found, err := s.prepareRecoveredSendMessage(ctx, viewer, cmd, commandRecord, false)
		if err != nil {
			if !errors.Is(err, ErrCommandInProgress) {
				failCommand(err)
			}
			return ConversationDTO{}, err
		}
		if found {
			commandRecord = plan.Command
			if plan.Completed {
				return s.replayDiscussionCommand(ctx, commandRecord)
			}
			if plan.Release != nil {
				defer plan.Release()
			}
			dto, err := s.executeDiscussionTurn(ctx, plan.Turn)
			if err != nil {
				failCommand(err)
				return dto, err
			}
			if err := s.completeDiscussionCommand(context.WithoutCancel(ctx), commandRecord, plan.Turn.ConversationID, plan.Source.MessageID, plan.Turn.TurnID); err != nil {
				return ConversationDTO{}, err
			}
			return dto, nil
		}
	}

	conversationID, err := s.resolvePersonalAssistantConversationForSend(ctx, viewer, &cmd)
	if err != nil {
		failCommand(err)
		return ConversationDTO{}, err
	}
	if conversationID == "" {
		conversationID = strings.TrimSpace(cmd.ConversationID)
	}
	var unlock func()
	if conversationID != "" {
		unlock = s.lockRunLifecycle(conversationID)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	conversationID, userMessage, err := s.beginUserMessage(ctx, viewer, cmd)
	if err != nil {
		failCommand(err)
		return ConversationDTO{}, err
	}
	if err := s.savePendingSendMessageProgress(ctx, &commandRecord, conversationID, userMessage); err != nil {
		failCommand(err)
		return ConversationDTO{}, err
	}
	if unlock == nil {
		unlock = s.lockRunLifecycle(conversationID)
	}
	current, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		failCommand(err)
		return ConversationDTO{}, err
	}
	turn := s.newDiscussionTurnCommand(current, userMessage, viewer, DiscussionTurnOriginMessage, cmd.IdempotencyKey, nil)
	dto, err := s.executeDiscussionTurn(ctx, turn)
	if err != nil {
		failCommand(err)
		return dto, err
	}
	if err := s.completeDiscussionCommand(context.WithoutCancel(ctx), commandRecord, conversationID, userMessage.MessageID, turn.TurnID); err != nil {
		return ConversationDTO{}, err
	}
	return dto, nil
}

func (s *Service) SendMessageStream(ctx context.Context, viewer ViewerRef, cmd SendMessageCommand) (stream <-chan StreamEventDTO, err error) {
	finishStage := s.startDiscussionStage(ctx, cmd.ConversationID, "", "", "stream_prepare")
	defer func() { finishStage(err) }()
	if err := s.validateSendMessageCommand(viewer, cmd); err != nil {
		return nil, err
	}
	commandRecord, claimed, recovered, releaseCommand, err := s.claimSendMessageCommand(ctx, viewer, cmd)
	if err != nil {
		return nil, err
	}
	if !claimed {
		releaseCommand()
		return s.replayDiscussionCommandStream(ctx, commandRecord)
	}
	if err := s.validateSendMessageRuntime(ctx, cmd, true); err != nil {
		_ = s.failDiscussionCommand(ctx, commandRecord, err)
		releaseCommand()
		return nil, err
	}
	if recovered {
		plan, found, err := s.prepareRecoveredSendMessage(ctx, viewer, cmd, commandRecord, true)
		if err != nil {
			if !errors.Is(err, ErrCommandInProgress) {
				_ = s.failDiscussionCommand(ctx, commandRecord, err)
			}
			releaseCommand()
			return nil, err
		}
		if found {
			commandRecord = plan.Command
			if plan.Completed {
				releaseCommand()
				return s.replayDiscussionCommandStream(ctx, commandRecord)
			}
			return s.executeDiscussionTurnStream(ctx, commandRecord, plan.Source, plan.Turn, plan.Initial, combineReleases(plan.Release, releaseCommand)), nil
		}
	}

	conversationID, err := s.resolvePersonalAssistantConversationForSend(ctx, viewer, &cmd)
	if err != nil {
		_ = s.failDiscussionCommand(ctx, commandRecord, err)
		releaseCommand()
		return nil, err
	}
	if conversationID == "" {
		conversationID = strings.TrimSpace(cmd.ConversationID)
	}
	var unlock func()
	if conversationID != "" {
		unlock = s.lockRunLifecycle(conversationID)
	}
	release := func() {
		if unlock != nil {
			unlock()
			unlock = nil
		}
		releaseCommand()
	}
	conversationID, userMessage, err := s.beginUserMessage(ctx, viewer, cmd)
	if err != nil {
		release()
		_ = s.failDiscussionCommand(ctx, commandRecord, err)
		return nil, err
	}
	if err := s.savePendingSendMessageProgress(ctx, &commandRecord, conversationID, userMessage); err != nil {
		release()
		_ = s.failDiscussionCommand(ctx, commandRecord, err)
		return nil, err
	}
	if unlock == nil {
		unlock = s.lockRunLifecycle(conversationID)
	}
	current, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		release()
		_ = s.failDiscussionCommand(ctx, commandRecord, err)
		return nil, err
	}

	turn := s.newDiscussionTurnCommand(current, userMessage, viewer, DiscussionTurnOriginMessage, cmd.IdempotencyKey, nil)
	return s.executeDiscussionTurnStream(ctx, commandRecord, userMessage, turn, current, release), nil
}

func (s *Service) validateSendMessageCommand(viewer ViewerRef, cmd SendMessageCommand) error {
	if s == nil || s.repo == nil {
		return ErrServiceDependency
	}
	hasAttachment := false
	for _, attachmentID := range cmd.AttachmentIDs {
		if strings.TrimSpace(attachmentID) != "" {
			hasAttachment = true
			break
		}
	}
	if strings.TrimSpace(viewer.ViewerID) == "" || (strings.TrimSpace(cmd.Content) == "" && !hasAttachment) || strings.TrimSpace(cmd.IdempotencyKey) == "" {
		return ErrInvalidRequest
	}
	if (strings.TrimSpace(cmd.MissionID) == "") != (strings.TrimSpace(cmd.MissionTaskID) == "") {
		return ErrInvalidRequest
	}
	if strings.TrimSpace(cmd.ConversationID) == "" {
		conversationKind, ok := normalizeCreatableConversationKind(cmd.ConversationKind)
		if !ok || (conversationKind == ConversationKindPersonalAssistant && directTargetFromCommand(cmd).ID != "") {
			return ErrInvalidRequest
		}
	} else if strings.TrimSpace(cmd.ConversationKind) != "" {
		if _, ok := normalizeCreatableConversationKind(cmd.ConversationKind); !ok {
			return ErrInvalidRequest
		}
	}
	return nil
}

func (s *Service) validateSendMessageRuntime(ctx context.Context, cmd SendMessageCommand, streaming bool) error {
	directConversation := s.commandTargetsDirectConversation(ctx, cmd)
	directMention := s.commandMayDirectHumanAgentMention(ctx, cmd)
	if directMention || directConversation {
		if s.mentionResponder == nil && directMention {
			return ErrRuntimeUnavailable
		}
		return nil
	}
	if s.discussionAgent == nil {
		if err := s.validateInitialAgentMembers(ctx, cmd); err != nil {
			return err
		}
		return s.runtimeUnavailableError()
	}
	if streaming {
		if _, ok := s.discussionAgent.(DiscussionAgentStreamRunner); !ok {
			return s.runtimeUnavailableError()
		}
	}
	_, err := s.configuredRunAgentID()
	return err
}
