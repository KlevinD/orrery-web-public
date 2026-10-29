package skill

import (
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type SkillBundle struct {
	ID          string              `json:"bundle_id"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	SkillIDs    []agentcore.SkillID `json:"skill_ids"`
	CreatedBy   string              `json:"created_by,omitempty"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
}

func (b SkillBundle) Clone() SkillBundle {
	b.SkillIDs = append([]agentcore.SkillID(nil), b.SkillIDs...)
	return b
}

func normalizeSkillBundle(bundle SkillBundle) (SkillBundle, error) {
	bundle.ID = strings.TrimSpace(bundle.ID)
	bundle.Name = strings.TrimSpace(bundle.Name)
	bundle.Description = strings.TrimSpace(bundle.Description)
	bundle.CreatedBy = strings.TrimSpace(bundle.CreatedBy)
	if bundle.ID == "" {
		return SkillBundle{}, fmt.Errorf("%w: bundle_id is required", ErrInvalidDefinition)
	}
	if bundle.Name == "" {
		return SkillBundle{}, fmt.Errorf("%w: name is required", ErrInvalidDefinition)
	}
	bundle.SkillIDs = uniqueSkillIDs(bundle.SkillIDs)
	return bundle, nil
}

func uniqueSkillIDs(values []agentcore.SkillID) []agentcore.SkillID {
	out := make([]agentcore.SkillID, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		id := strings.TrimSpace(string(value))
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, agentcore.SkillID(id))
	}
	return out
}
