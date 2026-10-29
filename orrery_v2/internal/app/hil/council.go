package hil

import (
	"fmt"
	"strings"
	"time"
)

func ResolveCouncil(session CouncilSession, now time.Time) (CouncilResolution, error) {
	if err := validateCouncil(session); err != nil {
		return CouncilResolution{}, err
	}
	resolution := CouncilResolution{
		CouncilID: session.ID,
		RequestID: session.RequestID,
		ArgsHash:  session.ArgsHash,
		Status:    CouncilOpen,
	}
	if !session.ExpiresAt.IsZero() && now.After(session.ExpiresAt) {
		resolution.Status = CouncilExpired
		resolution.Effect = EffectDeny
		resolution.ResolvedAt = now
		return resolution, nil
	}

	participantByKey := make(map[string]CouncilParticipant, len(session.Participants))
	for _, participant := range session.Participants {
		participantByKey[actorKey(participant.Actor)] = participant
	}
	voteByKey := make(map[string]CouncilVote, len(session.Votes))
	for _, vote := range session.Votes {
		key := actorKey(vote.Actor)
		if _, ok := participantByKey[key]; !ok {
			return CouncilResolution{}, fmt.Errorf("vote actor %s is not a council participant", key)
		}
		if _, exists := voteByKey[key]; exists {
			return CouncilResolution{}, fmt.Errorf("duplicate vote from %s", key)
		}
		if !validVoteChoice(vote.Choice) {
			return CouncilResolution{}, fmt.Errorf("invalid vote choice %q", vote.Choice)
		}
		voteByKey[key] = vote
		switch vote.Choice {
		case VoteApprove:
			resolution.Approvals++
		case VoteReject:
			resolution.Rejections++
		case VoteAbstain:
			resolution.Abstentions++
		}
	}

	for _, participant := range session.Participants {
		if !participant.Required || participant.Actor.Kind != ActorHuman {
			continue
		}
		vote, ok := voteByKey[actorKey(participant.Actor)]
		if !ok || vote.Choice == VoteAbstain {
			return resolution, nil
		}
	}
	nonAbstaining := resolution.Approvals + resolution.Rejections
	if nonAbstaining < session.Quorum {
		return resolution, nil
	}
	if resolution.Approvals >= session.ApprovalThreshold && resolution.Approvals > resolution.Rejections {
		resolution.Status = CouncilPassed
		resolution.Effect = EffectAuthorize
		if normalizeRisk(session.RiskLevel) == RiskHigh || normalizeRisk(session.RiskLevel) == RiskCritical {
			resolution.Effect = EffectRequiresApproval
		}
		resolution.Dissent = votersWithChoice(session.Votes, VoteReject)
		resolution.ResolvedAt = now
		return resolution, nil
	}
	if resolution.Rejections >= session.ApprovalThreshold || len(voteByKey) == len(session.Participants) {
		resolution.Status = CouncilRejected
		resolution.Effect = EffectDeny
		resolution.Dissent = votersWithChoice(session.Votes, VoteApprove)
		resolution.ResolvedAt = now
	}
	return resolution, nil
}

func validateCouncil(session CouncilSession) error {
	if strings.TrimSpace(session.ID) == "" || strings.TrimSpace(session.RequestID) == "" || strings.TrimSpace(session.ArgsHash) == "" {
		return fmt.Errorf("council id, request id, and args hash are required")
	}
	if session.Quorum <= 0 || session.ApprovalThreshold <= 0 || session.ApprovalThreshold > len(session.Participants) || session.Quorum > len(session.Participants) {
		return fmt.Errorf("council quorum and approval threshold are invalid")
	}
	seen := make(map[string]struct{}, len(session.Participants))
	for _, participant := range session.Participants {
		key := actorKey(participant.Actor)
		if key == ":" {
			return fmt.Errorf("council participant identity is required")
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate council participant %s", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validVoteChoice(choice VoteChoice) bool {
	return choice == VoteApprove || choice == VoteReject || choice == VoteAbstain
}

func actorKey(actor ActorRef) string {
	return strings.ToLower(strings.TrimSpace(string(actor.Kind))) + ":" + strings.TrimSpace(actor.ID)
}

func votersWithChoice(votes []CouncilVote, choice VoteChoice) []ActorRef {
	out := make([]ActorRef, 0)
	for _, vote := range votes {
		if vote.Choice == choice {
			out = append(out, vote.Actor)
		}
	}
	return out
}
