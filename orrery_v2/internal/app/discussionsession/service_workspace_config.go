package discussionsession

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

type WorkspaceAllowlistProvider interface {
	DiscussionWorkspaceAllowlist(ctx context.Context) ([]string, error)
}

type SetConversationWorkspaceCommand struct {
	ConversationID   string
	WorkspaceRootDir string
	IdempotencyKey   string
}

// applyRequestedMessageWorkspace binds a workspace supplied by trusted system
// automation while the conversation is being created or its first message is
// committed. Other viewers must use the explicit workspace endpoint.
func (s *Service) applyRequestedMessageWorkspace(ctx context.Context, viewer ViewerRef, cmd SendMessageCommand, conversation *ConversationRecord) error {
	if conversation == nil || strings.TrimSpace(cmd.WorkspaceRootDir) == "" {
		return nil
	}
	if viewer.ViewerKind != ViewerKindSystem {
		return ErrForbidden
	}
	root, err := normalizeServerWorkspaceRoot(cmd.WorkspaceRootDir)
	if err != nil {
		return err
	}
	allowlist, err := s.validateConversationWorkspaceRootAllowed(ctx, root)
	if err != nil {
		return err
	}
	if err := ensureServerWorkspaceDir(root); err != nil {
		return err
	}
	if err := validateServerWorkspaceRealpathAllowed(root, allowlist); err != nil {
		return err
	}
	if existing := strings.TrimSpace(conversation.WorkspaceRootDir); existing != "" && existing != root {
		return ErrInvalidRequest
	}
	conversation.WorkspaceRootDir = root
	return nil
}

func (s *Service) SetConversationWorkspace(ctx context.Context, viewer ViewerRef, cmd SetConversationWorkspaceCommand) (ConversationDTO, error) {
	if s == nil || s.repo == nil {
		return ConversationDTO{}, ErrServiceDependency
	}
	conversationID := strings.TrimSpace(cmd.ConversationID)
	if conversationID == "" || strings.TrimSpace(viewer.ViewerID) == "" {
		return ConversationDTO{}, ErrInvalidRequest
	}
	root, err := normalizeServerWorkspaceRoot(cmd.WorkspaceRootDir)
	if err != nil {
		return ConversationDTO{}, err
	}
	view, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	if conversationMemberName(view.Members, conversationViewerMemberKind(viewer.ViewerKind), strings.TrimSpace(viewer.ViewerID)) == "" {
		return ConversationDTO{}, ErrForbidden
	}
	if root != "" {
		allowlist, err := s.validateConversationWorkspaceRootAllowed(ctx, root)
		if err != nil {
			return ConversationDTO{}, err
		}
		if err := ensureServerWorkspaceDir(root); err != nil {
			return ConversationDTO{}, err
		}
		if err := validateServerWorkspaceRealpathAllowed(root, allowlist); err != nil {
			return ConversationDTO{}, err
		}
	}
	view.WorkspaceRootDir = root
	view.UpdatedAt = firstTimeAfter(s.now(), view.UpdatedAt)
	if err := s.repo.SaveConversation(ctx, view.ConversationRecord); err != nil {
		return ConversationDTO{}, err
	}
	updated, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	if err := s.appendRealtimeSnapshotInvalidated(ctx, conversationID, "workspace-updated", "workspace"); err != nil {
		return ConversationDTO{}, err
	}
	if err := s.appendRealtimeConversationListInvalidated(ctx, updated, "workspace-"+conversationID); err != nil {
		return ConversationDTO{}, err
	}
	return mapConversationDTO(updated), nil
}

func (s *Service) normalizedConversationWorkspaceAllowlist(ctx context.Context) ([]string, error) {
	if s == nil || s.workspaceAllowlist == nil {
		return nil, ErrInvalidRequest
	}
	allowlist, err := s.workspaceAllowlist.DiscussionWorkspaceAllowlist(ctx)
	if err != nil {
		return nil, err
	}
	return normalizeServerWorkspaceAllowlist(allowlist)
}

func (s *Service) validateConversationWorkspaceRootAllowed(ctx context.Context, root string) ([]string, error) {
	if strings.TrimSpace(root) == "" {
		return nil, nil
	}
	allowlist, err := s.normalizedConversationWorkspaceAllowlist(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateServerWorkspaceAllowed(root, allowlist); err != nil {
		return nil, err
	}
	return allowlist, nil
}

func normalizeServerWorkspaceRoot(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if !filepath.IsAbs(value) {
		return "", ErrInvalidRequest
	}
	root, err := filepath.Abs(filepath.Clean(value))
	if err != nil {
		return "", err
	}
	if root == string(filepath.Separator) {
		return "", ErrInvalidRequest
	}
	return root, nil
}

func normalizeServerWorkspaceAllowlist(values []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		root, err := normalizeServerWorkspaceRoot(value)
		if err != nil {
			return nil, err
		}
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		out = append(out, root)
	}
	return out, nil
}

func validateServerWorkspaceAllowed(root string, allowlist []string) error {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	for _, allowed := range allowlist {
		if pathWithin(root, allowed) && existingWorkspacePathStaysWithinAllowedRoot(root, allowed) {
			return nil
		}
	}
	return ErrInvalidRequest
}

func validateServerWorkspaceRealpathAllowed(root string, allowlist []string) error {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	for _, allowed := range allowlist {
		allowedReal, err := filepath.EvalSymlinks(allowed)
		if err != nil {
			continue
		}
		if pathWithin(rootReal, allowedReal) {
			return nil
		}
	}
	return ErrInvalidRequest
}

func existingWorkspacePathStaysWithinAllowedRoot(root string, allowed string) bool {
	root = filepath.Clean(root)
	allowed = filepath.Clean(allowed)
	if !pathWithin(root, allowed) {
		return false
	}
	allowedReal, err := filepath.EvalSymlinks(allowed)
	if err != nil {
		allowedReal = allowed
	}
	rel, err := filepath.Rel(allowed, root)
	if err != nil {
		return false
	}
	current := allowed
	for _, segment := range strings.Split(rel, string(filepath.Separator)) {
		if segment == "" || segment == "." {
			continue
		}
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return true
		}
		if err != nil {
			return false
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		real, err := filepath.EvalSymlinks(current)
		if err != nil {
			return false
		}
		if !pathWithin(real, allowedReal) {
			return false
		}
	}
	return true
}

func ensureServerWorkspaceDir(root string) error {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return ErrInvalidRequest
	}
	managed, err := safeJoin(root, ".orrery", "discussions")
	if err != nil {
		return err
	}
	return os.MkdirAll(managed, 0o755)
}
