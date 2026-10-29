package discussionsession

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

const maxDiscussionRuntimeImageBytes = 16 << 20

func discussionRuntimeInputMedia(request DiscussionAgentRequest) ([]agentcore.ContentBlock, error) {
	attachments := currentMessageAttachments(request)
	if len(attachments) == 0 {
		return nil, nil
	}
	attachmentsDir := strings.TrimSpace(request.Workspace.AttachmentsDir)
	if attachmentsDir == "" {
		return nil, fmt.Errorf("%w: discussion image attachment directory is unavailable", ErrInvalidRequest)
	}
	realAttachmentsDir, err := filepath.EvalSymlinks(attachmentsDir)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve discussion attachment directory", ErrInvalidRequest)
	}
	blocks := make([]agentcore.ContentBlock, 0, len(attachments))
	totalBytes := 0
	for _, attachment := range attachments {
		declaredType, _, err := mime.ParseMediaType(strings.TrimSpace(attachment.ContentType))
		if err != nil || !strings.HasPrefix(strings.ToLower(declaredType), "image/") {
			continue
		}
		attachmentPath := strings.TrimSpace(attachment.StoragePath)
		if attachmentPath == "" || !pathWithin(attachmentPath, attachmentsDir) {
			return nil, fmt.Errorf("%w: discussion image attachment path is invalid", ErrForbidden)
		}
		realPath, err := filepath.EvalSymlinks(attachmentPath)
		if err != nil || !pathWithin(realPath, realAttachmentsDir) {
			return nil, fmt.Errorf("%w: resolve discussion image attachment", ErrForbidden)
		}
		file, err := os.Open(realPath)
		if err != nil {
			return nil, fmt.Errorf("open discussion image attachment: %w", err)
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxDiscussionRuntimeImageBytes {
			_ = file.Close()
			return nil, fmt.Errorf("%w: discussion image attachment is empty, oversized, or not a regular file", ErrInvalidRequest)
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxDiscussionRuntimeImageBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read discussion image attachment: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close discussion image attachment: %w", closeErr)
		}
		if len(data) == 0 || len(data) > maxDiscussionRuntimeImageBytes {
			return nil, fmt.Errorf("%w: discussion image attachment is empty or oversized", ErrInvalidRequest)
		}
		totalBytes += len(data)
		if totalBytes > maxDiscussionRuntimeImageBytes {
			return nil, fmt.Errorf("%w: combined discussion images exceed the 16 MiB limit", ErrInvalidRequest)
		}
		detectedType := strings.ToLower(strings.TrimSpace(http.DetectContentType(data)))
		if !supportedDiscussionRuntimeImageType(detectedType) || !sameDiscussionRuntimeImageType(declaredType, detectedType) {
			return nil, fmt.Errorf("%w: discussion image attachment content does not match its media type", ErrInvalidRequest)
		}
		blocks = append(blocks, agentcore.ContentBlock{Kind: agentcore.ContentBlockImage, Data: data, MediaType: detectedType})
	}
	return blocks, nil
}

func supportedDiscussionRuntimeImageType(mediaType string) bool {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image/jpeg", "image/jpg", "image/png", "image/gif", "image/webp", "image/bmp":
		return true
	default:
		return false
	}
}

func sameDiscussionRuntimeImageType(left, right string) bool {
	left = strings.ToLower(strings.TrimSpace(left))
	right = strings.ToLower(strings.TrimSpace(right))
	if left == "image/jpg" {
		left = "image/jpeg"
	}
	if right == "image/jpg" {
		right = "image/jpeg"
	}
	return left == right
}
