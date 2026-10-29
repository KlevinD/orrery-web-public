package store

import memcore "orrery_v2/internal/memory/core"

func cloneCandidate(candidate memcore.MemoryCandidate) memcore.MemoryCandidate {
	return candidate.Clone()
}
