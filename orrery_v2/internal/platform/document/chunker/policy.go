package chunker

import (
	"fmt"

	"orrery_v2/internal/platform/document/core"
)

const (
	defaultMinRunes = 1
)

func NormalizePolicy(policy core.ChunkPolicy) (core.ChunkPolicy, error) {
	if policy.MaxRunes <= 0 {
		return core.ChunkPolicy{}, fmt.Errorf("%w: max_runes must be positive", core.ErrInvalid)
	}
	if policy.OverlapRunes < 0 {
		return core.ChunkPolicy{}, fmt.Errorf("%w: overlap_runes must be non-negative", core.ErrInvalid)
	}
	if policy.MaxRunes <= policy.OverlapRunes {
		return core.ChunkPolicy{}, fmt.Errorf("%w: max_runes must be greater than overlap_runes", core.ErrInvalid)
	}
	if policy.MinRunes <= 0 {
		policy.MinRunes = defaultMinRunes
	}
	if policy.MinRunes > policy.MaxRunes {
		return core.ChunkPolicy{}, fmt.Errorf("%w: min_runes must not exceed max_runes", core.ErrInvalid)
	}
	return policy, nil
}
