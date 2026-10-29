package discussionsession

import "strings"

func runningDiscussionRuns(runs []RunRecord) []RunRecord {
	out := make([]RunRecord, 0, len(runs))
	for _, run := range runs {
		if runningDiscussionStatus(run.Status) {
			out = append(out, run)
		}
	}
	return out
}

func runningDiscussionStatus(status string) bool {
	status = strings.TrimSpace(strings.ToLower(status))
	return status == "running" || status == "started"
}
