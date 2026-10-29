package discussionsession

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxGeneratedWorkspaceFiles     = 50
	maxGeneratedWorkspaceFileBytes = 64 << 20
)

type workspaceFileState struct {
	size             int64
	modifiedUnixNano int64
}

type generatedWorkspaceFile struct {
	relativePath string
	absolutePath string
}

// PreparedAgentRunFile is a host-verified file changed after the Mission run
// workspace snapshot was captured. Its private root prevents callers from
// constructing a handle for an arbitrary server path.
type PreparedAgentRunFile struct {
	RelativePath     string
	Size             int64
	workDir          string
	modifiedUnixNano int64
}

// GeneratedFiles returns visible regular files changed by this Agent run.
func (workspace PreparedAgentRunWorkspace) GeneratedFiles() []PreparedAgentRunFile {
	workDir := strings.TrimSpace(workspace.workspace.AgentWorkDir)
	if workDir == "" || filepath.Clean(workDir) != filepath.Clean(strings.TrimSpace(workspace.WorkDir)) ||
		!pathWithin(workDir, workspace.workspace.RootDir) || !pathWithin(workDir, workspace.RootDir) {
		return nil
	}
	files := generatedWorkspaceFiles(workspace.workspace)
	out := make([]PreparedAgentRunFile, 0, len(files))
	for _, file := range files {
		info, err := os.Lstat(file.absolutePath)
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxGeneratedWorkspaceFileBytes {
			continue
		}
		out = append(out, PreparedAgentRunFile{
			RelativePath: file.relativePath, Size: info.Size(), workDir: workDir, modifiedUnixNano: info.ModTime().UnixNano(),
		})
	}
	return out
}

// Open revalidates the file through an os.Root so a replaced symlink cannot
// escape the prepared Agent workspace between discovery and persistence.
func (file PreparedAgentRunFile) Open() (*os.File, error) {
	relativePath := filepath.FromSlash(strings.TrimSpace(file.RelativePath))
	if strings.TrimSpace(file.workDir) == "" || relativePath == "" || !filepath.IsLocal(relativePath) || file.Size < 0 || file.Size > maxGeneratedWorkspaceFileBytes {
		return nil, fmt.Errorf("invalid prepared Agent run file")
	}
	absolutePath := filepath.Join(file.workDir, relativePath)
	info, err := os.Lstat(absolutePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() != file.Size || info.ModTime().UnixNano() != file.modifiedUnixNano || !pathWithin(absolutePath, file.workDir) {
		return nil, fmt.Errorf("prepared Agent run file changed before persistence")
	}
	root, err := os.OpenRoot(file.workDir)
	if err != nil {
		return nil, err
	}
	reader, openErr := root.Open(relativePath)
	closeErr := root.Close()
	if openErr != nil {
		return nil, openErr
	}
	if closeErr != nil {
		_ = reader.Close()
		return nil, closeErr
	}
	info, err = reader.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != file.Size || info.ModTime().UnixNano() != file.modifiedUnixNano || info.Size() > maxGeneratedWorkspaceFileBytes {
		_ = reader.Close()
		return nil, fmt.Errorf("prepared Agent run file changed before persistence")
	}
	return reader, nil
}

func snapshotWorkspaceFiles(root string) map[string]workspaceFileState {
	files := map[string]workspaceFileState{}
	_ = walkVisibleWorkspaceFiles(root, func(relativePath string, _ string, info os.FileInfo) error {
		files[relativePath] = workspaceFileState{size: info.Size(), modifiedUnixNano: info.ModTime().UnixNano()}
		return nil
	})
	return files
}

func generatedWorkspaceFiles(workspace DiscussionWorkspaceContext) []generatedWorkspaceFile {
	root := strings.TrimSpace(workspace.AgentWorkDir)
	if root == "" || workspace.fileSnapshot == nil {
		return nil
	}
	files := make([]generatedWorkspaceFile, 0)
	_ = walkVisibleWorkspaceFiles(root, func(relativePath string, absolutePath string, info os.FileInfo) error {
		if info.Size() > maxGeneratedWorkspaceFileBytes {
			return nil
		}
		current := workspaceFileState{size: info.Size(), modifiedUnixNano: info.ModTime().UnixNano()}
		if previous, existed := workspace.fileSnapshot[relativePath]; existed && previous == current {
			return nil
		}
		files = append(files, generatedWorkspaceFile{relativePath: relativePath, absolutePath: absolutePath})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].relativePath < files[j].relativePath })
	if len(files) > maxGeneratedWorkspaceFiles {
		files = files[:maxGeneratedWorkspaceFiles]
	}
	return files
}

func walkVisibleWorkspaceFiles(root string, visit func(relativePath string, absolutePath string, info os.FileInfo) error) error {
	root = strings.TrimSpace(root)
	if root == "" || visit == nil {
		return nil
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	return filepath.WalkDir(absRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == absRoot {
			return nil
		}
		relativePath, err := filepath.Rel(absRoot, path)
		if err != nil || !pathWithin(path, absRoot) {
			return nil
		}
		if workspacePathExcluded(relativePath) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		return visit(filepath.ToSlash(relativePath), path, info)
	})
}

func workspacePathExcluded(relativePath string) bool {
	switch strings.ToLower(filepath.Base(filepath.Clean(relativePath))) {
	case "agent_context.md", "agents.md", "tool_summary.md", "skills.md", "mcp_config.json":
		return true
	}
	for _, part := range strings.FieldsFunc(filepath.ToSlash(relativePath), func(r rune) bool { return r == '/' }) {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, ".") {
			return true
		}
		switch strings.ToLower(part) {
		case "node_modules", "__pycache__":
			return true
		}
	}
	return false
}

func (s *Service) persistAssistantMessageWithWorkspaceFiles(ctx context.Context, record MessageRecord, workspace DiscussionWorkspaceContext) (MessageDTO, error) {
	if err := s.repo.SaveMessage(ctx, record); err != nil {
		return MessageDTO{}, err
	}
	attachments := s.captureGeneratedWorkspaceFiles(ctx, record, workspace)
	if err := s.appendRealtimeMessageCompleted(ctx, record); err != nil {
		return MessageDTO{}, err
	}
	dto := mapMessages([]MessageRecord{record})[0]
	dto.Attachments = mapAttachments(attachments)
	return dto, nil
}

func (s *Service) captureGeneratedWorkspaceFiles(ctx context.Context, message MessageRecord, workspace DiscussionWorkspaceContext) []AttachmentRecord {
	if s == nil || s.repo == nil || s.attachments == nil || strings.TrimSpace(message.MessageID) == "" {
		return nil
	}
	files := generatedWorkspaceFiles(workspace)
	if len(files) == 0 {
		return nil
	}
	workspaceRootDir, err := s.conversationWorkspaceRootDir(ctx, message.ConversationID)
	if err != nil {
		s.warnWorkspaceArtifactCapture(ctx, message, err)
		return nil
	}
	created := make([]AttachmentRecord, 0, len(files))
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			break
		}
		absolutePath, err := filepath.Abs(file.absolutePath)
		if err != nil || !pathWithin(absolutePath, workspace.AgentWorkDir) || !pathWithin(absolutePath, workspace.RootDir) {
			continue
		}
		absolutePath = filepath.Clean(absolutePath)
		info, err := os.Lstat(absolutePath)
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxGeneratedWorkspaceFileBytes {
			continue
		}
		attachmentID, err := s.newID()
		if err != nil {
			s.warnWorkspaceArtifactCapture(ctx, message, err)
			break
		}
		contentType := mime.TypeByExtension(filepath.Ext(file.relativePath))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		reader, err := os.Open(absolutePath)
		if err != nil {
			s.warnWorkspaceArtifactCapture(ctx, message, err)
			continue
		}
		stored, saveErr := s.attachments.SaveAttachment(ctx, message.ConversationID, workspaceRootDir, attachmentID, filepath.Base(file.relativePath), reader)
		closeErr := reader.Close()
		if saveErr != nil {
			s.warnWorkspaceArtifactCapture(ctx, message, saveErr)
			continue
		}
		if closeErr != nil {
			s.warnWorkspaceArtifactCapture(ctx, message, closeErr)
		}
		uploadedBy := strings.TrimSpace(message.AuthorID)
		uploadedByName := strings.TrimSpace(message.AuthorName)
		if agentID := strings.TrimSpace(message.Metadata["agent_id"]); agentID != "" {
			uploadedBy = agentID
			uploadedByName = firstNonBlank(strings.TrimSpace(message.Metadata["agent_name"]), agentID)
		}
		record := AttachmentRecord{
			AttachmentID:     attachmentID,
			ConversationID:   message.ConversationID,
			MessageID:        message.MessageID,
			FileName:         file.relativePath,
			ContentType:      contentType,
			SizeBytes:        stored.Size,
			UploadedBy:       uploadedBy,
			UploadedByKind:   "agent",
			UploadedByName:   uploadedByName,
			StoragePath:      stored.Path,
			WorkspaceRootDir: workspaceRootDir,
			CreatedAt:        message.CreatedAt,
		}
		if err := s.repo.SaveAttachment(ctx, record); err != nil {
			s.warnWorkspaceArtifactCapture(ctx, message, err)
			continue
		}
		created = append(created, record)
	}
	return created
}

func (s *Service) warnWorkspaceArtifactCapture(ctx context.Context, message MessageRecord, err error) {
	if s == nil || s.logger == nil || err == nil {
		return
	}
	s.logger.Warn(ctx, "capture generated discussion workspace file", "conversation_id", message.ConversationID, "message_id", message.MessageID, "run_id", message.RunID, "error", err.Error())
}
