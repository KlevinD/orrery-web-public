package discussionsession

import (
	"context"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

// PreparedAgentRunWorkspace carries the canonical discussion sandbox and the
// pre-run file snapshot used to expose only files changed by this Agent run.
type PreparedAgentRunWorkspace struct {
	ConversationID string
	AgentID        string
	AgentName      string
	RootDir        string
	WorkDir        string
	Policy         agentcore.RuntimeWorkspacePolicy
	workspace      DiscussionWorkspaceContext
}

// PrepareAgentRunWorkspace resolves an existing conversation member to the
// same isolated workspace used by direct discussion turns.
func (s *Service) PrepareAgentRunWorkspace(ctx context.Context, conversationID, agentID string) (PreparedAgentRunWorkspace, error) {
	conversationID = strings.TrimSpace(conversationID)
	agentID = strings.TrimSpace(agentID)
	if s == nil || s.repo == nil || s.attachments == nil {
		return PreparedAgentRunWorkspace{}, ErrServiceDependency
	}
	if conversationID == "" || agentID == "" {
		return PreparedAgentRunWorkspace{}, ErrInvalidRequest
	}
	view, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return PreparedAgentRunWorkspace{}, err
	}
	memberName := ""
	for _, candidate := range view.Members {
		if strings.EqualFold(strings.TrimSpace(candidate.MemberKind), "agent") && strings.EqualFold(strings.TrimSpace(candidate.MemberID), agentID) {
			memberName = firstNonBlank(strings.TrimSpace(candidate.DisplayName), agentID)
			break
		}
	}
	if memberName == "" || strings.TrimSpace(view.WorkspaceID) != "" {
		return PreparedAgentRunWorkspace{}, ErrForbidden
	}
	request := DiscussionAgentRequest{
		ConversationID: conversationID,
		AgentID:        agentID,
		Members:        mapConversationMembers(view.Members),
	}
	if err := s.attachDiscussionWorkspace(ctx, &request); err != nil {
		return PreparedAgentRunWorkspace{}, err
	}
	policy, err := discussionWorkspaceRuntimePolicy(request.Workspace)
	if err != nil {
		return PreparedAgentRunWorkspace{}, err
	}
	if strings.TrimSpace(policy.Root) == "" || strings.TrimSpace(policy.WorkDir) == "" || !policy.EnforceBoundary {
		return PreparedAgentRunWorkspace{}, ErrServiceDependency
	}
	return PreparedAgentRunWorkspace{
		ConversationID: conversationID,
		AgentID:        agentID,
		AgentName:      memberName,
		RootDir:        policy.Root,
		WorkDir:        policy.WorkDir,
		Policy:         policy,
		workspace:      request.Workspace,
	}, nil
}

func (s *Service) attachDiscussionWorkspace(ctx context.Context, request *DiscussionAgentRequest) error {
	if request == nil || s == nil || s.attachments == nil {
		return nil
	}
	conversationID := strings.TrimSpace(request.ConversationID)
	agentID := strings.TrimSpace(request.AgentID)
	if conversationID == "" {
		return nil
	}
	view, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if workspaceID := strings.TrimSpace(view.WorkspaceID); workspaceID != "" {
		// A canonical Todo conversation identifies the managed workspace, but a
		// chat turn is not an execution lease. Mission Agent runs receive the
		// server-resolved worktree only after matching task/Agent authorization.
		request.Workspace.Managed = true
		return nil
	}
	workspaceRootDir := strings.TrimSpace(view.WorkspaceRootDir)
	var workspaceAllowlist []string
	if strings.TrimSpace(workspaceRootDir) != "" {
		var err error
		workspaceAllowlist, err = s.validateConversationWorkspaceRootAllowed(ctx, workspaceRootDir)
		if err != nil {
			return err
		}
	}
	workspace, err := s.attachments.SessionWorkspace(ctx, conversationID, workspaceRootDir)
	if err != nil {
		return err
	}
	if strings.TrimSpace(workspaceRootDir) != "" {
		if err := validateServerWorkspaceRealpathAllowed(workspace.RootDir, workspaceAllowlist); err != nil {
			return err
		}
	}
	request.Workspace.RootDir = workspace.RootDir
	request.Workspace.AttachmentsDir = workspace.AttachmentsDir
	request.Workspace.AgentsDir = workspace.AgentsDir
	if agentID != "" {
		agentWorkspace, err := s.attachments.AgentWorkspace(ctx, conversationID, workspaceRootDir, s.discussionAgentWorkspaceSegment(agentID, request.Members))
		if err != nil {
			return err
		}
		request.Workspace.AgentID = agentID
		request.Workspace.AgentWorkDir = agentWorkspace.AgentWorkDir
		request.Workspace.fileSnapshot = snapshotWorkspaceFiles(agentWorkspace.AgentWorkDir)
	}
	request.Workspace.MemberWorkDirs = s.discussionMemberWorkDirs(ctx, conversationID, workspaceRootDir, agentID, request.Members)
	return nil
}

func (s *Service) attachDiscussionMentionWorkspace(ctx context.Context, request *DiscussionAgentMentionRequest) error {
	if request == nil || s == nil || s.attachments == nil {
		return nil
	}
	agentRequest := discussionAgentRequestFromMentionRequest(*request)
	if err := s.attachDiscussionWorkspace(ctx, &agentRequest); err != nil {
		return err
	}
	request.Attachments = append([]AttachmentDTO(nil), agentRequest.Attachments...)
	request.Workspace = agentRequest.Workspace
	return nil
}

func (s *Service) discussionMemberWorkDirs(ctx context.Context, conversationID string, workspaceRootDir string, currentAgentID string, members []ConversationMemberDTO) []DiscussionMemberWorkDir {
	if s == nil || s.attachments == nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]DiscussionMemberWorkDir, 0)
	for _, member := range members {
		if !strings.EqualFold(strings.TrimSpace(member.Kind), "agent") {
			continue
		}
		agentID := strings.TrimSpace(member.ID)
		if agentID == "" || seen[agentID] {
			continue
		}
		seen[agentID] = true
		workspace, err := s.attachments.AgentWorkspace(ctx, conversationID, workspaceRootDir, s.discussionAgentWorkspaceSegment(agentID, members))
		if err != nil {
			continue
		}
		out = append(out, DiscussionMemberWorkDir{
			AgentID:     agentID,
			DisplayName: strings.TrimSpace(member.DisplayName),
			Role:        strings.TrimSpace(member.Role),
			Path:        workspace.AgentWorkDir,
			Current:     strings.EqualFold(agentID, strings.TrimSpace(currentAgentID)),
		})
	}
	return out
}

func (s *Service) conversationWorkspaceRootDir(ctx context.Context, conversationID string) (string, error) {
	if s == nil || s.repo == nil || strings.TrimSpace(conversationID) == "" {
		return "", nil
	}
	view, err := s.repo.GetConversation(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(view.WorkspaceRootDir), nil
}

func (s *Service) discussionAgentWorkspaceSegment(agentID string, members []ConversationMemberDTO) string {
	agentID = strings.TrimSpace(agentID)
	for _, member := range members {
		if !strings.EqualFold(strings.TrimSpace(member.Kind), "agent") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(member.ID), agentID) {
			continue
		}
		if segment := readableWorkspaceSegment(member.DisplayName); segment != "" {
			return segment
		}
	}
	return readableWorkspaceSegment(agentID)
}

func readableWorkspaceSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.TrimPrefix(value, "@")
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r + ('a' - 'A'))
			lastDash = false
			continue
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if r > 127 {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-_. ")
}
