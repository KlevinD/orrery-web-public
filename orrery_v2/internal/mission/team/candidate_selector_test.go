package team

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestDirectoryCandidateSelectorScoresAndReturnsRejectedReasons(t *testing.T) {
	request := validStaffingRequest()
	strong := validDirectoryEntry()
	strong.AgentProfileID = "profile-strong"
	strong.AgentInstanceID = "instance-strong"
	weak := validDirectoryEntry()
	weak.AgentProfileID = "profile-weak"
	weak.AgentInstanceID = "instance-weak"
	weak.QualitySignal.Score = 0.5
	directory := stubDirectory{page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{weak, strong}}}
	selector := NewDirectoryCandidateSelector(directory, NewAssignmentPolicy(DefaultStaffingPolicy()))

	result, err := selector.Select(context.Background(), request, MissionTeam{})
	if err != nil {
		t.Fatalf("select candidates: %v", err)
	}

	if len(result.Accepted) != 1 || result.Accepted[0].CandidateProfileID != strong.AgentProfileID {
		t.Fatalf("accepted = %#v", result.Accepted)
	}
	if len(result.Rejected) != 1 || !hasPolicyCode(result.Rejected[0].RejectionReasons, CodeQualityDenied) {
		t.Fatalf("rejected = %#v", result.Rejected)
	}
	if result.Selected.CandidateProfileID != strong.AgentProfileID {
		t.Fatalf("selected = %#v", result.Selected)
	}
}

func TestDirectoryCandidateSelectorCarriesDirectoryRejectionsIntoAudit(t *testing.T) {
	request := validStaffingRequest()
	directory := stubDirectory{page: agentcore.AgentDirectoryPage{
		Rejected: []agentcore.DirectoryRejection{{AgentProfileID: "profile-tool", AgentInstanceID: "tool-001", Code: "candidate_not_agent", Reason: "tool cannot be member"}},
	}}
	selector := NewDirectoryCandidateSelector(directory, NewAssignmentPolicy(DefaultStaffingPolicy()))

	result, err := selector.Select(context.Background(), request, MissionTeam{})
	if err != nil {
		t.Fatalf("select candidates: %v", err)
	}
	if len(result.Rejected) != 1 || result.Rejected[0].RejectionReasons[0] != CodeCandidateNotAgent {
		t.Fatalf("directory rejection not carried into selector audit: %#v", result.Rejected)
	}
}

type stubDirectory struct {
	page agentcore.AgentDirectoryPage
}

func (s stubDirectory) QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	return s.page, ctx.Err()
}
