package ontologyreasoning

import (
	"encoding/json"
	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
)

func cloneReasoningRun(run ReasoningRun) ReasoningRun {
	run.Request = cloneCreateRunRequest(run.Request)
	run.ContextPack = cloneContextPack(run.ContextPack)
	run.Candidates = cloneCandidates(run.Candidates)
	run.Validation = cloneValidationResults(run.Validation)
	run.Proposals = cloneReasoningProposals(run.Proposals)
	if run.CurrentCandidates != nil {
		data, _ := json.Marshal(run.CurrentCandidates)
		var cloned []CandidateState
		_ = json.Unmarshal(data, &cloned)
		run.CurrentCandidates = cloned
	}
	return run
}

func cloneCreateRunRequest(req CreateRunRequest) CreateRunRequest {
	if req.Evaluation != nil {
		data, _ := json.Marshal(req.Evaluation)
		var cloned EvaluationRecord
		_ = json.Unmarshal(data, &cloned)
		req.Evaluation = &cloned
	}
	req.ContextPack = cloneContextPack(req.ContextPack)
	req.Candidates = cloneCandidates(req.Candidates)
	return req
}

func cloneContextPack(pack ContextPack) ContextPack {
	pack.RetractionEndpoints = append([]ObjectRef(nil), pack.RetractionEndpoints...)
	pack.CandidateObjects = append([]contextgraph.Object(nil), pack.CandidateObjects...)
	pack.NeighborLinks = append([]contextgraph.Link(nil), pack.NeighborLinks...)
	pack.Facts = append([]contextgraph.Fact(nil), pack.Facts...)
	pack.EvidenceRefs = append([]contextgraph.Ref(nil), pack.EvidenceRefs...)
	pack.Evidence = append([]contextgraph.Evidence(nil), pack.Evidence...)
	pack.AllowedObjectTypes = append([]ontology.ObjectType(nil), pack.AllowedObjectTypes...)
	pack.AllowedLinkTypes = append([]ontology.LinkType(nil), pack.AllowedLinkTypes...)
	pack.AllowedPropertyTypes = append([]ontology.PropertyType(nil), pack.AllowedPropertyTypes...)
	pack.AllowedActions = append([]actioncontract.ActionType(nil), pack.AllowedActions...)
	pack.KnownConflicts = append([]ValidationIssue(nil), pack.KnownConflicts...)
	return pack
}

func cloneCandidates(candidates []Candidate) []Candidate {
	if len(candidates) == 0 {
		return nil
	}
	cloned := make([]Candidate, len(candidates))
	for i, candidate := range candidates {
		cloned[i] = candidate
		if candidate.From != nil {
			from := *candidate.From
			cloned[i].From = &from
		}
		if candidate.To != nil {
			to := *candidate.To
			cloned[i].To = &to
		}
		if candidate.Subject != nil {
			subject := *candidate.Subject
			cloned[i].Subject = &subject
		}
		if candidate.MetadataTarget != nil {
			target := *candidate.MetadataTarget
			cloned[i].MetadataTarget = &target
		}
		cloned[i].ObjectRefs = append([]actioncontract.ObjectRef(nil), candidate.ObjectRefs...)
		cloned[i].EvidenceRefs = append([]contextgraph.Ref(nil), candidate.EvidenceRefs...)
		if candidate.Properties != nil {
			cloned[i].Properties = map[string]any{}
			for key, value := range candidate.Properties {
				cloned[i].Properties[key] = value
			}
		}
	}
	return cloned
}

func cloneValidationResults(results []ValidationResult) []ValidationResult {
	if len(results) == 0 {
		return nil
	}
	cloned := make([]ValidationResult, len(results))
	for i, result := range results {
		cloned[i] = result
		cloned[i].Issues = append([]ValidationIssue(nil), result.Issues...)
	}
	return cloned
}

func cloneReasoningProposals(proposals []ReasoningProposal) []ReasoningProposal {
	if len(proposals) == 0 {
		return nil
	}
	cloned := make([]ReasoningProposal, len(proposals))
	for i, proposal := range proposals {
		cloned[i] = proposal
		cloned[i].EvidenceRefs = append([]contextgraph.Ref(nil), proposal.EvidenceRefs...)
		if proposal.GovernanceRef != nil {
			ref := *proposal.GovernanceRef
			cloned[i].GovernanceRef = &ref
		}
	}
	return cloned
}
