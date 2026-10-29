package hil

import (
	"testing"
	"time"
)

func TestResolveCouncilMediumRiskAuthorizes(t *testing.T) {
	session := testCouncilSession(RiskMedium)
	session.Votes = []CouncilVote{
		{Actor: ActorRef{Kind: ActorHuman, ID: "ding"}, Choice: VoteApprove},
		{Actor: ActorRef{Kind: ActorAgent, ID: "agent-1"}, Choice: VoteApprove},
	}
	resolution, err := ResolveCouncil(session, testCouncilNow)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Status != CouncilPassed || resolution.Effect != EffectAuthorize {
		t.Fatalf("resolution = %+v, want passed authorization", resolution)
	}
	if resolution.RequestID != session.RequestID || resolution.ArgsHash != session.ArgsHash {
		t.Fatalf("resolution is not bound to the request snapshot: %+v", resolution)
	}
}

func TestResolveCouncilHighRiskRequiresApproval(t *testing.T) {
	session := testCouncilSession(RiskHigh)
	session.Votes = []CouncilVote{
		{Actor: ActorRef{Kind: ActorHuman, ID: "ding"}, Choice: VoteApprove},
		{Actor: ActorRef{Kind: ActorAgent, ID: "agent-1"}, Choice: VoteApprove},
	}
	resolution, err := ResolveCouncil(session, testCouncilNow)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Status != CouncilPassed || resolution.Effect != EffectRequiresApproval {
		t.Fatalf("resolution = %+v, want passed recommendation requiring approval", resolution)
	}
}

func TestResolveCouncilWaitsForRequiredHuman(t *testing.T) {
	session := testCouncilSession(RiskMedium)
	session.Votes = []CouncilVote{
		{Actor: ActorRef{Kind: ActorAgent, ID: "agent-1"}, Choice: VoteApprove},
		{Actor: ActorRef{Kind: ActorAgent, ID: "agent-2"}, Choice: VoteApprove},
	}
	resolution, err := ResolveCouncil(session, testCouncilNow)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Status != CouncilOpen {
		t.Fatalf("status = %q, want %q", resolution.Status, CouncilOpen)
	}
}

func TestResolveCouncilWaitsWithoutQuorum(t *testing.T) {
	session := testCouncilSession(RiskMedium)
	session.Votes = []CouncilVote{{Actor: ActorRef{Kind: ActorHuman, ID: "ding"}, Choice: VoteApprove}}
	resolution, err := ResolveCouncil(session, testCouncilNow)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Status != CouncilOpen {
		t.Fatalf("status = %q, want %q", resolution.Status, CouncilOpen)
	}
}

func TestResolveCouncilRejectsWhenVotesCannotPass(t *testing.T) {
	session := testCouncilSession(RiskMedium)
	session.Votes = []CouncilVote{
		{Actor: ActorRef{Kind: ActorHuman, ID: "ding"}, Choice: VoteReject},
		{Actor: ActorRef{Kind: ActorAgent, ID: "agent-1"}, Choice: VoteReject},
		{Actor: ActorRef{Kind: ActorAgent, ID: "agent-2"}, Choice: VoteApprove},
	}
	resolution, err := ResolveCouncil(session, testCouncilNow)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Status != CouncilRejected || resolution.Effect != EffectDeny {
		t.Fatalf("resolution = %+v, want rejected", resolution)
	}
	if len(resolution.Dissent) != 1 || resolution.Dissent[0].ID != "agent-2" {
		t.Fatalf("dissent = %+v, want approving minority", resolution.Dissent)
	}
}

func TestResolveCouncilExpiresClosed(t *testing.T) {
	session := testCouncilSession(RiskMedium)
	resolution, err := ResolveCouncil(session, session.ExpiresAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Status != CouncilExpired || resolution.Effect != EffectDeny {
		t.Fatalf("resolution = %+v, want expired denial", resolution)
	}
}

func TestResolveCouncilRejectsDuplicateVote(t *testing.T) {
	session := testCouncilSession(RiskMedium)
	session.Votes = []CouncilVote{
		{Actor: ActorRef{Kind: ActorHuman, ID: "ding"}, Choice: VoteApprove},
		{Actor: ActorRef{Kind: ActorHuman, ID: "ding"}, Choice: VoteReject},
	}
	if _, err := ResolveCouncil(session, testCouncilNow); err == nil {
		t.Fatal("ResolveCouncil error = nil, want duplicate vote rejection")
	}
}

func TestResolveCouncilRejectsNonParticipantVote(t *testing.T) {
	session := testCouncilSession(RiskMedium)
	session.Votes = []CouncilVote{{Actor: ActorRef{Kind: ActorAgent, ID: "outsider"}, Choice: VoteApprove}}
	if _, err := ResolveCouncil(session, testCouncilNow); err == nil {
		t.Fatal("ResolveCouncil error = nil, want non-participant rejection")
	}
}

var testCouncilNow = time.Date(2026, 8, 9, 8, 0, 0, 0, time.UTC)

func testCouncilSession(risk RiskLevel) CouncilSession {
	return CouncilSession{
		ID:                "council-1",
		RequestID:         "decision-1",
		ArgsHash:          "sha256:args",
		RiskLevel:         risk,
		Status:            CouncilOpen,
		Quorum:            2,
		ApprovalThreshold: 2,
		Participants: []CouncilParticipant{
			{Actor: ActorRef{Kind: ActorHuman, ID: "ding"}, Required: true},
			{Actor: ActorRef{Kind: ActorAgent, ID: "agent-1"}},
			{Actor: ActorRef{Kind: ActorAgent, ID: "agent-2"}},
		},
		ExpiresAt: testCouncilNow.Add(time.Hour),
	}
}
