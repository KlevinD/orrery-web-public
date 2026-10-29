package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	profilemodel "orrery_v2/internal/agent/runtime/providers/profilemodel"
	"orrery_v2/internal/app/resultreview"
	"orrery_v2/internal/app/runtimeapproval"
)

const (
	scheduledResultAIReviewProviderKey      = "scheduled_result_ai_review"
	scheduledResultAIReviewProfileEnv       = "ORRERY_SCHEDULED_RESULT_AI_REVIEW_PROFILE"
	scheduledResultAIReviewTimeoutEnv       = "ORRERY_SCHEDULED_RESULT_AI_REVIEW_TIMEOUT_MS"
	scheduledResultAIReviewDefaultTimeoutMS = 15000
)

type productionScheduledResultReviewer struct {
	settings productionSettingsStore
	reviewer resultreview.Reviewer
}

func newProductionScheduledResultReviewer(
	settings productionSettingsStore,
	secrets platformSecretResolver,
) resultreview.Service {
	models := resultReviewModelFactory(settings, secrets)
	if models == nil {
		return nil
	}
	return productionScheduledResultReviewer{
		settings: settings,
		reviewer: resultreview.NewReviewer(productionRuntimeApprovalCompletion{models: models}),
	}
}

// resultReviewModelFactory is kept behind a small helper so feature wiring is
// easy to disable in tests and does not create a model client when the flag is
// off.
func resultReviewModelFactory(
	settings productionSettingsStore,
	secrets platformSecretResolver,
) profilemodel.ModelFactory {
	return profilemodel.NewProfileModelFactory(profilemodel.ProfileModelFactoryConfig{
		Profiles: settings,
		Secrets:  secrets,
	})
}

func (r productionScheduledResultReviewer) Review(ctx context.Context, input resultreview.Input) resultreview.Result {
	settings, err := r.settings.GetSettings(ctx)
	if err != nil {
		return resultreview.Result{
			Decision:      resultreview.DecisionHold,
			ReasonCode:    "reviewer_settings_error",
			Rationale:     "The result-review settings could not be loaded.",
			PromptVersion: resultreview.PromptVersion,
		}
	}
	policy := runtimeapproval.Normalize(settings.RuntimeApprovals)
	profileID := strings.TrimSpace(os.Getenv(scheduledResultAIReviewProfileEnv))
	if profileID == "" {
		profileID = policy.Reviewer.LLMProfileID
	}
	timeoutMS := policy.Reviewer.TimeoutMS
	if configured := strings.TrimSpace(os.Getenv(scheduledResultAIReviewTimeoutEnv)); configured != "" {
		if parsed, parseErr := strconv.Atoi(configured); parseErr == nil && parsed > 0 {
			timeoutMS = parsed
		}
	}
	if timeoutMS <= 0 || timeoutMS > scheduledResultAIReviewDefaultTimeoutMS {
		timeoutMS = scheduledResultAIReviewDefaultTimeoutMS
	}
	return r.reviewer.Review(ctx, resultreview.Settings{
		LLMProfileID: profileID,
		TimeoutMS:    timeoutMS,
		MaxTokens:    policy.Reviewer.MaxTokens,
	}, input)
}

// productionScheduledResultAIReviewEnabled is an explicit opt-in. A per-run
// provider value can enable or disable the feature without changing process
// configuration; otherwise the process environment controls the default.
func productionScheduledResultAIReviewEnabled(providerSpecific map[string]string) bool {
	if providerSpecific != nil {
		if value, ok := providerSpecific[scheduledResultAIReviewProviderKey]; ok {
			return parseProductionBoolean(value)
		}
	}
	return parseProductionBoolean(os.Getenv("ORRERY_SCHEDULED_RESULT_AI_REVIEW"))
}

func parseProductionBoolean(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on", "enabled":
		return true
	default:
		return false
	}
}

func reviewScheduledAgentResult(
	ctx context.Context,
	input agentcore.AgentInput,
	result agentcore.AgentResult,
	reviewer resultreview.Service,
	publicOutput ...string,
) agentcore.AgentResult {
	if reviewer == nil || !productionScheduledResultAIReviewEnabled(input.RuntimeOptions.ProviderSpecific) || result.Blocker != nil {
		return result
	}
	if len(input.Task.ExpectedDeliverables) == 0 && len(input.Task.EvidenceRequirements) == 0 && len(input.Task.AcceptanceCriteria) == 0 {
		return result
	}
	packet := scheduledResultReviewInput(input, result)
	// The manifest summary is often a short receipt. Review the Agent's actual
	// public explanation when present, so formatting requirements are checked
	// against the final response rather than the compact evidence artifact.
	if len(publicOutput) > 0 {
		if summary, ok := extractScheduledFeishuPublicSummary(publicOutput[0]); ok {
			// ChatText unwraps public fences for delivery. Preserve a verified
			// fence in the review packet so the execution contract can check it.
			const ending = "\n```"
			limit := 1000 - len([]rune(scheduledFeishuPublicSummaryFence+"\n"+ending))
			packet.Summary = scheduledFeishuPublicSummaryFence + "\n" + boundedReviewText(scheduledAgentPublicText(summary), limit) + ending
		} else if text := scheduledAgentPublicText(publicOutput[0]); text != "" {
			packet.Summary = boundedReviewText(text, 1000)
		}
	}
	review := reviewer.Review(ctx, packet)
	reason := strings.TrimSpace(review.Rationale)
	if reason == "" {
		reason = "The independent result reviewer did not provide a confirmation."
	}
	decisionText := string(review.Decision)
	if decisionText == "" {
		decisionText = string(resultreview.DecisionHold)
	}
	result.Decisions = append(result.Decisions, agentcore.Decision{
		ID:      agentcore.DecisionID("scheduled-result-review-" + shortReviewDigest(review.InputDigest)),
		Summary: fmt.Sprintf("AI result review %s: %s", decisionText, boundedReviewText(reason, 360)),
	})
	if review.Decision == resultreview.DecisionApprove {
		return result
	}
	missing := ""
	if len(review.Missing) > 0 {
		missing = " Missing: " + boundedReviewText(strings.Join(review.Missing, "; "), 260)
	}
	result.Blocker = &agentcore.Blocker{Summary: "Independent AI result review did not confirm this completion: " + boundedReviewText(reason, 360) + missing}
	result.Summary = agentcore.Summary{Text: "The result passed deterministic checks but remains blocked pending independent AI review."}
	result.Deliverables = nil
	result.Evidence = nil
	return result
}

func scheduledResultReviewInput(input agentcore.AgentInput, result agentcore.AgentResult) resultreview.Input {
	reviewInput := resultreview.Input{
		TaskID:            strings.TrimSpace(input.Task.ID),
		MissionID:         strings.TrimSpace(input.Task.MissionID),
		RunID:             strings.TrimSpace(input.RunID.String()),
		Goal:              boundedReviewText(input.Task.Goal, 1000),
		ExecutionContract: scheduledReviewContractText(firstNonBlank(input.Task.WorkerPersonalGoal, input.ContextPack.WorkerPersonalGoal)),
		Summary:           boundedReviewText(result.Summary.Text, 1000),
		Blocker:           blockerSummary(result.Blocker),
	}
	for _, item := range input.Task.ExpectedDeliverables {
		reviewInput.ExpectedDeliverables = append(reviewInput.ExpectedDeliverables, resultreview.ContractItem{
			ID:          strings.TrimSpace(item.ID),
			Description: boundedReviewText(item.Title, 300),
			Required:    true,
		})
	}
	for _, item := range input.Task.EvidenceRequirements {
		reviewInput.EvidenceRequirements = append(reviewInput.EvidenceRequirements, resultreview.ContractItem{
			ID:          strings.TrimSpace(item.ID),
			Description: boundedReviewText(item.Description, 500),
			Required:    item.Required,
		})
	}
	for _, item := range input.Task.AcceptanceCriteria {
		reviewInput.AcceptanceCriteria = append(reviewInput.AcceptanceCriteria, resultreview.ContractItem{
			ID:          strings.TrimSpace(item.ID),
			Description: boundedReviewText(item.Description, 500),
			Required:    true,
		})
	}
	for _, item := range result.Deliverables {
		reviewInput.Deliverables = append(reviewInput.Deliverables, resultreview.Deliverable{
			ID:            strings.TrimSpace(string(item.ID)),
			ExpectedID:    strings.TrimSpace(item.ExpectedDeliverableID),
			Title:         boundedReviewText(item.Title, 300),
			SourcePresent: !item.ArtifactRef.IsZero(),
		})
	}
	for _, item := range result.Evidence {
		reviewInput.Evidence = append(reviewInput.Evidence, resultreview.Evidence{
			ID:            strings.TrimSpace(string(item.ID)),
			RequirementID: strings.TrimSpace(item.EvidenceRequirementID),
			CriterionID:   strings.TrimSpace(item.AcceptanceCriterionID),
			Kind:          strings.TrimSpace(string(item.Kind)),
			SourcePresent: item.HasTraceableSource(),
		})
	}
	return reviewInput
}

var scheduledReviewLocations = regexp.MustCompile(`https?://[^\s"'<>]+|(?:[A-Za-z]:[\\/]|/(?:data|home|tmp|var|etc|opt|usr)/)[^\s"'<>]+`)

func scheduledReviewContractText(value string) string {
	sanitized, ok := agentcore.SanitizeTraceValue(value, agentcore.TraceSanitizerOptions{}).(string)
	if !ok {
		return ""
	}
	return boundedReviewText(scheduledReviewLocations.ReplaceAllString(sanitized, "[location omitted]"), 8000)
}

func blockerSummary(blocker *agentcore.Blocker) string {
	if blocker == nil {
		return ""
	}
	return boundedReviewText(blocker.Summary, 800)
}

func boundedReviewText(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if limit <= 0 || len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}

func shortReviewDigest(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 16 {
		return value[:16]
	}
	if value == "" {
		return "unknown"
	}
	return value
}

var _ resultreview.Service = productionScheduledResultReviewer{}
