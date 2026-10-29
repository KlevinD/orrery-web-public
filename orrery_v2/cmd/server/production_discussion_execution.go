package main

import (
	"github.com/google/uuid"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/platform"
)

func withProductionDiscussionExecution(rt *platform.Runtime, opts discussionsession.ServiceOptions) discussionsession.ServiceOptions {
	opts.DiscussionExecutionClaims = rt.ExecutionClaims
	opts.DiscussionExecutorID = productionDiscussionExecutorID()
	opts.DiscussionLeaseRenew = rt.Config.Queue.ClaimTTL / 3
	return opts
}

func productionDiscussionExecutorID() string {
	return "discussion-executor-" + uuid.NewString()
}
