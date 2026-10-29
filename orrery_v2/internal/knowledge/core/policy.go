package core

import (
	"fmt"

	doccore "orrery_v2/internal/platform/document/core"
)

func ValidateChunkPolicy(policy doccore.ChunkPolicy) error {
	if policy.MaxRunes <= 0 {
		return fmt.Errorf("%w: max_runes must be positive", ErrInvalid)
	}
	if policy.OverlapRunes < 0 {
		return fmt.Errorf("%w: overlap_runes must be non-negative", ErrInvalid)
	}
	if policy.MaxRunes <= policy.OverlapRunes {
		return fmt.Errorf("%w: max_runes must be greater than overlap_runes", ErrInvalid)
	}
	if policy.MinRunes < 0 {
		return fmt.Errorf("%w: min_runes must be non-negative", ErrInvalid)
	}
	if policy.MinRunes > policy.MaxRunes {
		return fmt.Errorf("%w: min_runes must not exceed max_runes", ErrInvalid)
	}
	return nil
}
