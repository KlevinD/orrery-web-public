package missioncontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	agentassembly "orrery_v2/internal/agent/assembly"
	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/outputpolicy"
	runtimeworkspace "orrery_v2/internal/agent/runtime/workspace"
	missioncore "orrery_v2/internal/mission/core"
	missiondiscussion "orrery_v2/internal/mission/discussion"
	missionevent "orrery_v2/internal/mission/event"
	missionprojection "orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/logging"
)

type MentionAgentAssembler interface {
	Build(ctx context.Context, profile agentcore.AgentProfile, opts agentassembly.BuildOptions) (agentcore.AgentInstance, error)
}

type MentionAgentProfileStore interface {
	GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error)
}

type MentionAgentCommandPort interface {
	ReplyDiscussionMessage(ctx context.Context, viewer ViewerRef, cmd ReplyDiscussionMessageCommand) (CommandReceiptDTO, error)
}

type MentionAgentQueryPort interface {
	GetMissionControlView(ctx context.Context, viewer ViewerRef, missionID missioncore.MissionID) (MissionControlViewDTO, error)
}

type MentionAgentProjectionRepository interface {
	Save(ctx context.Context, model missionprojection.ReadModel) error
	GetMissionControl(ctx context.Context, missionID missioncore.MissionID) (missionprojection.ReadModel, error)
}

type MentionAgentEventSink interface {
	Append(ctx context.Context, event missionevent.Envelope) error
}

type MentionAgentEventReader interface {
	LoadMission(ctx context.Context, missionID missioncore.MissionID) ([]missionevent.Envelope, error)
}

const MentionAgentRunSource = "mission_discussion_mention"

type MentionAgentRunnerOptions struct {
	Assembler     MentionAgentAssembler
	Profiles      MentionAgentProfileStore
	Commands      MentionAgentCommandPort
	Query         MentionAgentQueryPort
	Repo          MentionAgentProjectionRepository
	Events        MentionAgentEventSink
	EventReader   MentionAgentEventReader
	Clock         interface{ Now() time.Time }
	IDGenerator   interface{ NewIDContext() (string, error) }
	Logger        logging.Logger
	Timeout       time.Duration
	PromptBlocks  PromptBlockProvider
	WorkspaceRoot string
}

type MentionAgentRunner struct {
	assembler     MentionAgentAssembler
	profiles      MentionAgentProfileStore
	commands      MentionAgentCommandPort
	query         MentionAgentQueryPort
	repo          MentionAgentProjectionRepository
	events        MentionAgentEventSink
	reader        MentionAgentEventReader
	clock         interface{ Now() time.Time }
	ids           interface{ NewIDContext() (string, error) }
	logger        logging.Logger
	timeout       time.Duration
	promptBlocks  PromptBlockProvider
	workspaceRoot string
}

func NewMentionAgentRunner(opts MentionAgentRunnerOptions) *MentionAgentRunner {
	return &MentionAgentRunner{
		assembler:     opts.Assembler,
		profiles:      opts.Profiles,
		commands:      opts.Commands,
		query:         opts.Query,
		repo:          opts.Repo,
		events:        opts.Events,
		reader:        opts.EventReader,
		clock:         opts.Clock,
		ids:           opts.IDGenerator,
		logger:        opts.Logger,
		timeout:       opts.Timeout,
		promptBlocks:  opts.PromptBlocks,
		workspaceRoot: strings.TrimSpace(opts.WorkspaceRoot),
	}
}

func (r *MentionAgentRunner) HandleExecution(ctx context.Context, job execution.Job) error {
	var payload MentionDispatchPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("decode mention dispatch payload: %w", err)
	}
	if err := r.handlePayload(ctx, job.ID, payload); err != nil {
		_ = r.recordFailure(ctx, job.ID, payload, err)
		return err
	}
	return nil
}

func (r *MentionAgentRunner) handlePayload(ctx context.Context, jobID string, payload MentionDispatchPayload) error {
	if r == nil || r.assembler == nil || r.profiles == nil || r.commands == nil || r.query == nil {
		return ErrServiceDependency
	}
	stableAgentID := strings.TrimSpace(payload.AgentID)
	if stableAgentID == "" || strings.TrimSpace(payload.MissionID) == "" || strings.TrimSpace(payload.MessageID) == "" || strings.TrimSpace(payload.DiscussionGroupID) == "" || strings.TrimSpace(payload.InboxItemID) == "" {
		return ErrInvalidRequest
	}
	tenantID := missionTenantID(payload.TenantID)
	viewer := r.mentionViewer(tenantID, stableAgentID)
	missionID := missioncore.MissionID(strings.TrimSpace(payload.MissionID))
	view, err := r.query.GetMissionControlView(ctx, viewer, missionID)
	if err != nil {
		return fmt.Errorf("load mention mission view for tenant %q mission %q: %w", tenantID, missionID, err)
	}
	parent := findDiscussionMessage(view.Discussion.Messages, payload.MessageID)
	if parent.MessageID == "" {
		refreshed, found, err := r.viewFromEvents(ctx, tenantID, missionID, payload.MessageID, stableAgentID)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: message %s", missiondiscussion.ErrNotFound, payload.MessageID)
		}
		view = mergeMentionView(view, refreshed)
		parent = findDiscussionMessage(view.Discussion.Messages, payload.MessageID)
	}
	identity, err := mentionAgentIdentityFromView(stableAgentID, view)
	if err != nil {
		return err
	}
	profile, err := r.profiles.GetProfile(ctx, identity.ProfileID)
	if err != nil {
		return err
	}
	agent, err := r.assembler.Build(ctx, profile, agentassembly.BuildOptions{
		AgentID:    identity.AgentID,
		InstanceID: identity.InstanceID,
	})
	if err != nil {
		return err
	}
	base := agent.Base()
	if base == nil {
		return fmt.Errorf("agent %s has no base agent", identity.AgentID)
	}
	runID := agentcore.AgentRunID(firstNonEmptyMentionValue(jobID, "mention-"+payload.InboxItemID))
	runCtx := ctx
	cancel := func() {}
	if r.timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, r.timeout)
	}
	defer cancel()
	bindings := agentcore.AgentBindings{}
	if bound, ok := agent.(interface {
		Bindings() agentcore.AgentBindings
	}); ok {
		bindings = bound.Bindings()
	}
	promptBlocks, err := r.promptBlocksForRun(runCtx)
	if err != nil {
		return err
	}
	input, err := r.agentInput(runID, profile, bindings, view, parent, payload, promptBlocks)
	if err != nil {
		return err
	}
	result, err := base.Handle(runCtx, input)
	if err != nil {
		return err
	}
	reply := outputpolicy.PublicText(result.Summary.Text, "\u4efb\u52a1\u5df2\u5904\u7406\uff0c\u8be6\u7ec6\u7ed3\u679c\u5df2\u8bb0\u5f55\u3002")
	if reply == "" {
		reply = strings.TrimSpace(firstNonEmptyMentionValue(result.InternalTraceRef, "已处理。"))
	}
	_, err = r.commands.ReplyDiscussionMessage(ctx, viewer, ReplyDiscussionMessageCommand{
		RequestID:         runID.String(),
		IdempotencyKey:    "mention-agent-reply|" + payload.InboxItemID + "|" + runID.String(),
		MissionID:         missionID,
		DiscussionGroupID: missiondiscussion.DiscussionGroupID(payload.DiscussionGroupID),
		MessageID:         missiondiscussion.MessageID(payload.MessageID),
		Content:           reply,
		Summary:           truncateMentionText(reply, 120),
		InboxItemID:       missiondiscussion.InboxItemID(payload.InboxItemID),
	})
	return err
}

func (r *MentionAgentRunner) mentionViewer(tenantID, agentID string) ViewerRef {
	return ViewerRef{ViewerID: strings.TrimSpace(agentID), ViewerKind: ViewerKindAgent, TenantID: missionTenantID(tenantID), PermissionScope: []string{"mission.read", "discussion.write"}}
}

type mentionAgentIdentity struct {
	AgentID    agentcore.AgentID
	ProfileID  agentcore.AgentProfileID
	InstanceID agentcore.AgentInstanceID
}

func mentionAgentIdentityFromView(agentID string, view MissionControlViewDTO) (mentionAgentIdentity, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return mentionAgentIdentity{}, ErrInvalidRequest
	}
	for _, assignment := range view.Team.Assignments {
		if mentionTeamBindingMatches(agentID, assignment.AgentID, assignment.AgentProfileID, assignment.AgentInstanceID) && strings.TrimSpace(assignment.MissionRole) != "" {
			return mentionAgentIdentityForBinding(assignment.AgentID, assignment.AgentProfileID, assignment.AgentInstanceID)
		}
	}
	for _, member := range view.Team.Members {
		if mentionTeamBindingMatches(agentID, member.AgentID, member.AgentProfileID, member.AgentInstanceID) {
			return mentionAgentIdentityForBinding(member.AgentID, member.AgentProfileID, member.AgentInstanceID)
		}
	}
	return mentionAgentIdentity{}, fmt.Errorf("%w: mention agent must be bound to a mission team identity", ErrInvalidRequest)
}

func mentionAgentIdentityForBinding(agentID string, profileID string, instanceID string) (mentionAgentIdentity, error) {
	agentID = strings.TrimSpace(agentID)
	profileID = strings.TrimSpace(profileID)
	instanceID = strings.TrimSpace(instanceID)
	if profileID == "" || instanceID == "" {
		return mentionAgentIdentity{}, fmt.Errorf("%w: mention agent team binding is incomplete", ErrInvalidRequest)
	}
	if agentID != "" && agentID != profileID {
		return mentionAgentIdentity{}, fmt.Errorf("%w: mention agent stable id must match profile id", ErrInvalidRequest)
	}
	if instanceID != profileID {
		return mentionAgentIdentity{}, fmt.Errorf("%w: mention agent instance id must match profile id", ErrInvalidRequest)
	}
	return mentionAgentIdentity{
		AgentID:    agentcore.AgentID(profileID),
		ProfileID:  agentcore.AgentProfileID(profileID),
		InstanceID: agentcore.AgentInstanceID(profileID),
	}, nil
}

func mentionTeamBindingMatches(id string, agentID string, profileID string, _ string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	for _, candidate := range []string{agentID, profileID} {
		if strings.TrimSpace(candidate) == id {
			return true
		}
	}
	return false
}

func (r *MentionAgentRunner) promptBlocksForRun(ctx context.Context) ([]agentcore.RuntimePromptBlock, error) {
	if r == nil || r.promptBlocks == nil {
		return nil, nil
	}
	blocks, err := r.promptBlocks.PromptBlocks(ctx)
	if err != nil {
		return nil, err
	}
	return agentcore.NormalizeRuntimePromptBlocks(blocks), nil
}

func (r *MentionAgentRunner) agentInput(runID agentcore.AgentRunID, profile agentcore.AgentProfile, bindings agentcore.AgentBindings, view MissionControlViewDTO, parent MessageDTO, payload MentionDispatchPayload, promptBlocks []agentcore.RuntimePromptBlock) (agentcore.AgentInput, error) {
	agentID := agentcore.AgentID(payload.AgentID)
	missionID := missioncore.MissionID(payload.MissionID)
	taskID := missioncore.TaskID("mention-" + payload.InboxItemID)
	goal := strings.TrimSpace(parent.Body)
	if goal == "" {
		goal = "Respond to the mission discussion mention."
	}
	prompt := mentionAgentPromptWithResources(profile, bindings, view, parent)
	pack := agentcore.ContextPack{
		ID:                      agentcore.ContextPackID("ctx-" + runID.String()),
		MissionID:               missionID,
		DiscussionGroupID:       agentcore.DiscussionGroupID(payload.DiscussionGroupID),
		AgentID:                 agentID,
		TaskID:                  taskID,
		MissionFinalGoal:        prompt,
		DiscussionGroupGoal:     prompt,
		AgentMissionRole:        profile.RoleName,
		WorkerPersonalGoal:      prompt,
		TaskContractRef:         agentcore.TaskContractRef{ID: string(taskID)},
		TaskContractSummary:     goal,
		DiscussionDigestRef:     agentcore.DiscussionDigestRef{ID: agentcore.DiscussionDigestID("discussion-" + payload.DiscussionGroupID), SummaryRef: discussionSummary(view.Discussion, parent)},
		DiscussionDigestSummary: discussionSummary(view.Discussion, parent),
		SourceRefs: []agentcore.ContextSourceRef{
			{
				Kind: agentcore.ContextSourceKindUserInstruction,
				Ref: agentcore.ContextSourceTypedRef{UserInstructionRef: &agentcore.UserInstructionRef{
					ID: agentcore.UserInstructionID(MentionAgentRunSource),
				}},
				Priority:  agentcore.SourcePriorityUserInstruction,
				Immutable: true,
			},
			{
				Kind:      agentcore.ContextSourceKindTaskContract,
				Ref:       agentcore.ContextSourceTypedRef{TaskContractRef: &agentcore.TaskContractRef{ID: string(taskID)}},
				Priority:  agentcore.SourcePriorityContract,
				Immutable: true,
			},
			{
				Kind:     agentcore.ContextSourceKindDiscussionDigest,
				Ref:      agentcore.ContextSourceTypedRef{DiscussionDigestRef: &agentcore.DiscussionDigestRef{ID: agentcore.DiscussionDigestID("discussion-" + payload.DiscussionGroupID), SummaryRef: discussionSummary(view.Discussion, parent)}},
				Priority: agentcore.SourcePriorityContext,
			},
		},
		OverridePolicy: agentcore.OverridePolicyPreserveProtected,
		CreatedAt:      r.now(),
	}
	runtimeOptions := agentcore.RuntimeOptions{
		SystemPrompt: profile.BaseAgent.SystemPrompt,
		PromptBlocks: append([]agentcore.RuntimePromptBlock(nil), promptBlocks...),
		Timeout:      r.timeout,
		ProviderSpecific: map[string]string{
			"mention_inbox_item_id": payload.InboxItemID,
			"mention_message_id":    payload.MessageID,
			"discussion_group_id":   payload.DiscussionGroupID,
		},
	}
	if r != nil && r.workspaceRoot != "" {
		policy, err := runtimeworkspace.RuntimePolicyForRun(r.workspaceRoot, payload.MissionID, string(taskID), runID.String())
		if err != nil {
			return agentcore.AgentInput{}, fmt.Errorf("build mention runtime workspace policy: %w", err)
		}
		if strings.TrimSpace(policy.WorkDir) == "" {
			return agentcore.AgentInput{}, fmt.Errorf("mention runtime workspace policy has no working directory")
		}
		if err := os.MkdirAll(policy.WorkDir, 0o755); err != nil {
			return agentcore.AgentInput{}, fmt.Errorf("create mention runtime workspace: %w", err)
		}
		runtimeOptions.WorkingDirectory = policy.WorkDir
		runtimeOptions.WorkspacePolicy = policy
		runtimeOptions.SandboxProfile = policy.Profile.Clone()
	}
	return agentcore.AgentInput{
		RunID:       runID,
		TenantID:    missionTenantID(payload.TenantID),
		ContextPack: pack,
		Task: agentcore.TaskInput{
			ID:                 string(taskID),
			MissionID:          payload.MissionID,
			Goal:               goal,
			WorkerPersonalGoal: prompt,
		},
		RuntimeOptions: runtimeOptions,
	}, nil
}

func (r *MentionAgentRunner) recordFailure(ctx context.Context, jobID string, payload MentionDispatchPayload, err error) error {
	if err == nil {
		return nil
	}
	failureErr := err
	if r != nil && r.logger != nil {
		r.logger.Warn(ctx, "mission discussion mention agent run failed",
			logging.String("operation", "mission.discussion.mention.agent"),
			logging.String("mission_id", payload.MissionID),
			logging.String("agent_id", payload.AgentID),
			logging.String("inbox_item_id", payload.InboxItemID),
			logging.String("error", err.Error()),
		)
	}
	if r == nil || r.events == nil {
		return nil
	}
	eventID, idErr := r.newID()
	if idErr != nil {
		return idErr
	}
	now := r.now()
	missionID := missioncore.MissionID(payload.MissionID)
	tenantID := missionTenantID(payload.TenantID)
	streamVersion, err := r.nextStreamVersion(ctx, tenantID, missionID)
	if err != nil {
		return err
	}
	event := missionevent.Envelope{
		EventID:        eventID,
		TenantID:       tenantID,
		MissionID:      missionID,
		StreamID:       missionevent.MissionStreamID(tenantID, missionID),
		StreamVersion:  streamVersion,
		EventType:      missionevent.EventAgentRunFailed,
		EventVersion:   1,
		Actor:          missionevent.ActorRef{Kind: missionevent.ActorKindAgent, ID: payload.AgentID},
		IdempotencyKey: "mention-agent-failed|" + payload.InboxItemID + "|" + jobID,
		OccurredAt:     now,
		Payload: missionevent.RuntimeFailurePayload{
			TaskID:  missioncore.TaskID("mention-" + payload.InboxItemID),
			AgentID: agentcore.AgentID(payload.AgentID),
			RunID:   agentcore.AgentRunID(jobID),
			Code:    "mention_agent_run_failed",
			Message: truncateMentionText(failureErr.Error(), 500),
		},
	}
	if err := r.events.Append(ctx, event); err != nil {
		return err
	}
	return r.refreshProjection(ctx, tenantID, missionID, event)
}

func (r *MentionAgentRunner) now() time.Time {
	if r != nil && r.clock != nil {
		return r.clock.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *MentionAgentRunner) newID() (string, error) {
	if r == nil || r.ids == nil {
		return "", fmt.Errorf("id generator is not configured")
	}
	id, err := r.ids.NewIDContext()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("generated id is empty")
	}
	return id, nil
}
