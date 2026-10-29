package discussionsession

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

type AttachmentStore interface {
	SaveAttachment(ctx context.Context, conversationID string, workspaceRootDir string, attachmentID string, fileName string, reader io.Reader) (StoredAttachmentFile, error)
	OpenAttachment(ctx context.Context, record AttachmentRecord, workspaceRootDir string) (io.ReadCloser, error)
	DeleteConversationWorkspace(ctx context.Context, conversationID string, workspaceRootDir string) error
	SessionWorkspace(ctx context.Context, conversationID string, workspaceRootDir string) (DiscussionWorkspace, error)
	AgentWorkspace(ctx context.Context, conversationID string, workspaceRootDir string, agentID string) (DiscussionAgentWorkspace, error)
}

type StoredAttachmentFile struct {
	Path string
	Size int64
}

type DiscussionWorkspace struct {
	RootDir        string
	AttachmentsDir string
	AgentsDir      string
}

type DiscussionAgentWorkspace struct {
	DiscussionWorkspace
	AgentID      string
	AgentWorkDir string
}

type LocalAttachmentStore struct {
	root string
}

func NewLocalAttachmentStore(root string) *LocalAttachmentStore {
	return &LocalAttachmentStore{root: strings.TrimSpace(root)}
}

func (s *LocalAttachmentStore) SaveAttachment(ctx context.Context, conversationID string, workspaceRootDir string, attachmentID string, fileName string, reader io.Reader) (StoredAttachmentFile, error) {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return StoredAttachmentFile{}, ErrServiceDependency
	}
	if reader == nil || strings.TrimSpace(conversationID) == "" || strings.TrimSpace(attachmentID) == "" {
		return StoredAttachmentFile{}, ErrInvalidRequest
	}
	workspace, err := s.SessionWorkspace(ctx, conversationID, workspaceRootDir)
	if err != nil {
		return StoredAttachmentFile{}, err
	}
	name := safeWorkspaceFileName(fileName)
	path, err := safeJoin(workspace.AttachmentsDir, safeWorkspaceSegment(attachmentID)+"-"+name)
	if err != nil {
		return StoredAttachmentFile{}, err
	}
	if err := ctx.Err(); err != nil {
		return StoredAttachmentFile{}, err
	}
	file, err := os.Create(path)
	if err != nil {
		return StoredAttachmentFile{}, err
	}
	defer file.Close()
	size, err := io.Copy(file, reader)
	if err != nil {
		_ = os.Remove(path)
		return StoredAttachmentFile{}, err
	}
	return StoredAttachmentFile{Path: path, Size: size}, nil
}

func (s *LocalAttachmentStore) OpenAttachment(ctx context.Context, record AttachmentRecord, workspaceRootDir string) (io.ReadCloser, error) {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return nil, ErrServiceDependency
	}
	if strings.TrimSpace(record.StoragePath) == "" || strings.TrimSpace(record.ConversationID) == "" {
		return nil, ErrInvalidRequest
	}
	workspace, err := s.SessionWorkspace(ctx, record.ConversationID, workspaceRootDir)
	if err != nil {
		return nil, err
	}
	path, err := filepath.Abs(record.StoragePath)
	if err != nil {
		return nil, err
	}
	if !pathWithin(path, workspace.RootDir) {
		return nil, ErrForbidden
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	return file, err
}

func (s *LocalAttachmentStore) DeleteConversationWorkspace(ctx context.Context, conversationID string, workspaceRootDir string) error {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return nil
	}
	root, custom, err := s.workspaceRoot(conversationID, workspaceRootDir)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if custom {
		managedDir, err := safeJoin(root, ".orrery", "discussions", safeWorkspaceSegment(conversationID))
		if err != nil {
			return err
		}
		if !pathWithin(managedDir, root) {
			return ErrForbidden
		}
		return os.RemoveAll(managedDir)
	}
	if !pathWithin(root, s.absoluteRoot()) {
		return ErrForbidden
	}
	return os.RemoveAll(root)
}

// RemoveAttachment validates the same workspace boundary as downloads before deleting one file.
func (s *LocalAttachmentStore) RemoveAttachment(ctx context.Context, record AttachmentRecord) error {
	file, err := s.OpenAttachment(ctx, record, "")
	if err == ErrNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Remove(record.StoragePath)
}

func (s *LocalAttachmentStore) SessionWorkspace(ctx context.Context, conversationID string, workspaceRootDir string) (DiscussionWorkspace, error) {
	root, custom, err := s.workspaceRoot(conversationID, workspaceRootDir)
	if err != nil {
		return DiscussionWorkspace{}, err
	}
	attachmentsDir, agentsDir, err := s.workspaceManagedDirs(root, conversationID, custom)
	if err != nil {
		return DiscussionWorkspace{}, err
	}
	for _, dir := range []string{root, attachmentsDir, agentsDir} {
		if err := ctx.Err(); err != nil {
			return DiscussionWorkspace{}, err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return DiscussionWorkspace{}, err
		}
	}
	return DiscussionWorkspace{RootDir: root, AttachmentsDir: attachmentsDir, AgentsDir: agentsDir}, nil
}

func (s *LocalAttachmentStore) AgentWorkspace(ctx context.Context, conversationID string, workspaceRootDir string, agentID string) (DiscussionAgentWorkspace, error) {
	workspace, err := s.SessionWorkspace(ctx, conversationID, workspaceRootDir)
	if err != nil {
		return DiscussionAgentWorkspace{}, err
	}
	if strings.TrimSpace(agentID) == "" {
		return DiscussionAgentWorkspace{}, ErrInvalidRequest
	}
	workDir, err := safeJoin(workspace.AgentsDir, safeWorkspaceSegment(agentID))
	if err != nil {
		return DiscussionAgentWorkspace{}, err
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return DiscussionAgentWorkspace{}, err
	}
	return DiscussionAgentWorkspace{DiscussionWorkspace: workspace, AgentID: strings.TrimSpace(agentID), AgentWorkDir: workDir}, nil
}

func (s *LocalAttachmentStore) sessionRoot(conversationID string) (string, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return "", ErrInvalidRequest
	}
	return safeJoin(s.absoluteRoot(), safeWorkspaceSegment(conversationID))
}

func (s *LocalAttachmentStore) workspaceRoot(conversationID string, workspaceRootDir string) (string, bool, error) {
	if strings.TrimSpace(conversationID) == "" {
		return "", false, ErrInvalidRequest
	}
	workspaceRootDir = strings.TrimSpace(workspaceRootDir)
	if workspaceRootDir == "" {
		root, err := s.sessionRoot(conversationID)
		return root, false, err
	}
	root, err := normalizeServerWorkspaceRoot(workspaceRootDir)
	if err != nil {
		return "", false, err
	}
	return root, true, nil
}

func (s *LocalAttachmentStore) workspaceManagedDirs(root string, conversationID string, custom bool) (string, string, error) {
	if custom {
		managedBase, err := safeJoin(root, ".orrery", "discussions", safeWorkspaceSegment(conversationID))
		if err != nil {
			return "", "", err
		}
		attachmentsDir, err := safeJoin(managedBase, "attachments")
		if err != nil {
			return "", "", err
		}
		agentsDir, err := safeJoin(managedBase, "agents")
		return attachmentsDir, agentsDir, err
	}
	attachmentsDir, err := safeJoin(root, "attachments")
	if err != nil {
		return "", "", err
	}
	agentsDir, err := safeJoin(root, "agents")
	return attachmentsDir, agentsDir, err
}

func (s *LocalAttachmentStore) absoluteRoot() string {
	root, err := filepath.Abs(s.root)
	if err != nil {
		return s.root
	}
	return root
}

func safeJoin(root string, elem ...string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	parts := append([]string{absRoot}, elem...)
	out, err := filepath.Abs(filepath.Join(parts...))
	if err != nil {
		return "", err
	}
	if !pathWithin(out, absRoot) {
		return "", fmt.Errorf("%w: workspace path escaped root", ErrForbidden)
	}
	return out, nil
}

func pathWithin(path string, root string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func safeWorkspaceFileName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "attachment"
	}
	return safeWorkspaceSegment(name)
}

func safeWorkspaceSegment(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	out := strings.Trim(b.String(), " .")
	if out == "" {
		return "item"
	}
	if len(out) > 120 {
		return out[:120]
	}
	return out
}
