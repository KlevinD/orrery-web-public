package resourceadmin

import (
	"context"
	"errors"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
)

type memoryProfileWriter interface {
	SaveProfile(ctx context.Context, profile memcore.MemoryProfile) error
}

func (s *QueryService) UpdateMemoryProfile(ctx context.Context, viewer ViewerRef, profileID string, req UpdateMemoryProfileRequest) (MemoryProfileDTO, error) {
	if err := ctx.Err(); err != nil {
		return MemoryProfileDTO{}, err
	}
	if s == nil || s.memory == nil {
		return MemoryProfileDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return MemoryProfileDTO{}, err
	}
	profileID = strings.TrimSpace(profileID)
	if profileID == "" || req.MaxRecords <= 0 || req.MaxSummaryBytes <= 0 {
		return MemoryProfileDTO{}, ErrInvalidRequest
	}
	writer, ok := s.memory.(memoryProfileWriter)
	if !ok {
		return MemoryProfileDTO{}, ErrServiceDependency
	}
	profile, err := s.memory.GetProfile(ctx, agentcore.MemoryProfileID(profileID))
	if err != nil {
		if errors.Is(err, memcore.ErrNotFound) {
			return MemoryProfileDTO{}, ErrNotFound
		}
		return MemoryProfileDTO{}, err
	}
	if isPersonalAssistantMemoryProfile(profile) {
		return MemoryProfileDTO{}, ErrNotFound
	}
	profile.Enabled = req.Enabled
	profile.RetrievalPolicy.MaxRecords = req.MaxRecords
	profile.RetrievalPolicy.Tags = normalizedMemoryTags(req.RetrievalTags)
	profile.CompressionPolicy.MaxSummaryBytes = req.MaxSummaryBytes
	if err := writer.SaveProfile(ctx, profile); err != nil {
		return MemoryProfileDTO{}, err
	}
	return s.GetMemoryProfile(ctx, viewer, profileID)
}

func normalizedMemoryTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}
