package main

import (
	agentcore "orrery_v2/internal/agent/core"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func hasLearningReviewCapability(refs []agentcore.ToolRef) bool {
	for _, ref := range refs {
		if ref.ToolID == toolstd.RSILearningPlan {
			return true
		}
	}
	return false
}
