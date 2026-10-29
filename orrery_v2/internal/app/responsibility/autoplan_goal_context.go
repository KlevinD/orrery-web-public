package responsibility

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// AutoplanGoalContextProvider resolves explicit goals owned by People before
// autonomous planning. Responsibilities and duties are deliberately excluded:
// they constrain what the Agent may do, but they are not substitutes for a
// goal from which work can be decomposed.
type AutoplanGoalContextProvider interface {
	ResolveAutoplanGoalContext(context.Context, ContextRequest) (AutoplanGoalContext, error)
}

// AutoplanGoalContext is the bounded, immutable goal projection used by one
// planning attempt. Its digest participates in plan identity so a changed goal
// is a new planning signal even when the responsibility itself is unchanged.
type AutoplanGoalContext struct {
	ContextID             string
	ResponsibilityID      string
	ResponsibilityVersion int64
	AgentProfileID        string
	Goals                 []AutoplanGoalAnchor
	SourceRefs            []string
	CapturedAt            time.Time
}

// AutoplanGoalAnchor names one existing People goal. Goal is source text, not
// model-authored text; SuccessCriteria carries source metrics that can guide a
// smaller task without allowing the planner to invent a parent objective.
type AutoplanGoalAnchor struct {
	Ref             string
	Scope           string
	Goal            string
	SuccessCriteria []string
}

func (value AutoplanGoalContext) Normalized() AutoplanGoalContext {
	value.ContextID = strings.TrimSpace(value.ContextID)
	value.ResponsibilityID = strings.TrimSpace(value.ResponsibilityID)
	value.AgentProfileID = strings.TrimSpace(value.AgentProfileID)
	value.SourceRefs = cleanAutoplanStrings(value.SourceRefs)
	value.Goals = append([]AutoplanGoalAnchor(nil), value.Goals...)
	for index := range value.Goals {
		value.Goals[index] = value.Goals[index].normalized()
	}
	sort.Slice(value.Goals, func(i, j int) bool {
		leftRank, rightRank := autoplanGoalScopeRank(value.Goals[i].Scope), autoplanGoalScopeRank(value.Goals[j].Scope)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return value.Goals[i].Ref < value.Goals[j].Ref
	})
	if !value.CapturedAt.IsZero() {
		value.CapturedAt = value.CapturedAt.UTC()
	}
	return value
}

func (value AutoplanGoalContext) Validate(request ContextRequest) error {
	value = value.Normalized()
	if value.ContextID == "" || value.ResponsibilityID == "" || value.ResponsibilityVersion < 1 || value.AgentProfileID == "" {
		return fmt.Errorf("%w: autonomous planning goal context identity is incomplete", ErrInvalidContract)
	}
	if responsibilityID := strings.TrimSpace(request.ResponsibilityID); responsibilityID != "" && value.ResponsibilityID != responsibilityID {
		return fmt.Errorf("%w: autonomous planning goal context responsibility does not match", ErrInvalidContract)
	}
	if request.AssignmentVersion > 0 && value.ResponsibilityVersion < request.AssignmentVersion {
		return fmt.Errorf("%w: autonomous planning goal context is older than the responsibility", ErrInvalidContract)
	}
	if agentProfileID := strings.TrimSpace(request.AgentProfileID); agentProfileID != "" && value.AgentProfileID != agentProfileID {
		return fmt.Errorf("%w: autonomous planning goal context Agent does not match", ErrInvalidContract)
	}
	seen := make(map[string]struct{}, len(value.Goals))
	for _, goal := range value.Goals {
		if err := goal.validate(); err != nil {
			return err
		}
		if _, exists := seen[goal.Ref]; exists {
			return fmt.Errorf("%w: autonomous planning goal references must be unique", ErrInvalidContract)
		}
		seen[goal.Ref] = struct{}{}
	}
	return nil
}

func AutoplanGoalContextDigest(value AutoplanGoalContext) (string, error) {
	value = value.Normalized()
	if value.ContextID == "" {
		return "", fmt.Errorf("%w: autonomous planning goal context identity is required", ErrInvalidContract)
	}
	// Context IDs may be newly minted for equivalent snapshots. Goal identity,
	// source versions, text, and metrics are the durable planning signal.
	payload, err := json.Marshal(struct {
		Goals      []AutoplanGoalAnchor `json:"goals"`
		SourceRefs []string             `json:"source_refs"`
	}{Goals: value.Goals, SourceRefs: value.SourceRefs})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func (goal AutoplanGoalAnchor) normalized() AutoplanGoalAnchor {
	goal.Ref = strings.TrimSpace(goal.Ref)
	goal.Scope = strings.ToLower(strings.TrimSpace(goal.Scope))
	goal.Goal = strings.TrimSpace(goal.Goal)
	goal.SuccessCriteria = cleanAutoplanStrings(goal.SuccessCriteria)
	return goal
}

func (goal AutoplanGoalAnchor) validate() error {
	goal = goal.normalized()
	if goal.Ref == "" || goal.Goal == "" || autoplanGoalScopeRank(goal.Scope) == 100 {
		return fmt.Errorf("%w: autonomous planning goal reference, scope, and source goal are required", ErrInvalidContract)
	}
	return nil
}

func autoplanGoalScopeRank(scope string) int {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "assignment":
		return 0
	case "position":
		return 1
	case "organization":
		return 2
	case "company":
		return 3
	default:
		return 100
	}
}

func validateAutoplanGoalDerivation(context AutoplanGoalContext, proposal AutoplanProposal) error {
	context = context.Normalized()
	proposal = proposal.Normalized()
	if proposal.Version != AutoplanProposalVersion {
		return fmt.Errorf("%w: new autonomous plans must use proposal version %d", ErrInvalidContract, AutoplanProposalVersion)
	}
	if len(proposal.Steps) == 0 {
		return nil
	}
	goals := make(map[string]AutoplanGoalAnchor, len(context.Goals))
	for _, goal := range context.Goals {
		goals[goal.Ref] = goal
	}
	for _, step := range proposal.Steps {
		goal, exists := goals[step.ParentGoalRef]
		if !exists {
			return fmt.Errorf("%w: plan step %s does not reference an authorized existing goal", ErrInvalidContract, step.StepID)
		}
		if step.ParentGoal != goal.Goal {
			return fmt.Errorf("%w: plan step %s changed its parent goal text", ErrInvalidContract, step.StepID)
		}
		if !containsString(proposal.SourceRefs, step.ParentGoalRef) {
			return fmt.Errorf("%w: plan step %s parent goal is missing from source references", ErrInvalidContract, step.StepID)
		}
	}
	return nil
}
