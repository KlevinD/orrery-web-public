package core

import (
	"context"
	"errors"
	"strings"
	"time"

	missioncore "orrery_v2/internal/mission/core"
)

type RuntimePipelineError struct {
	Status           RuntimeStatus
	Message          string
	ProviderMetadata map[string]string
}

type PipelineDependencyError struct {
	Dependency string
	Message    string
}

// PostCommitPipelineError reports a failed projection or notification after
// the Agent result has already been durably committed. Queue workers must not
// replay the Runtime or its tool side effects when this error is returned.
type PostCommitPipelineError struct {
	Stage string
	Err   error
}

func NewPostCommitPipelineError(stage string, err error) PostCommitPipelineError {
	return PostCommitPipelineError{Stage: strings.TrimSpace(stage), Err: err}
}

func (e PostCommitPipelineError) Error() string {
	if e.Err == nil {
		return "post-commit Agent pipeline stage failed: " + e.Stage
	}
	if e.Stage == "" {
		return "post-commit Agent pipeline failed: " + e.Err.Error()
	}
	return "post-commit Agent pipeline stage " + e.Stage + " failed: " + e.Err.Error()
}

func (e PostCommitPipelineError) Unwrap() error { return e.Err }

func (e PipelineDependencyError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "missing pipeline dependency: " + e.Dependency
}

func (e RuntimePipelineError) Error() string {
	if e.Message == "" {
		return "runtime finished with status " + string(e.Status)
	}
	return e.Message
}

type ResultNormalizer interface {
	Normalize(input AgentInput, response RuntimeResponse, now time.Time) (AgentResult, error)
}

// ContextResultNormalizer is implemented by production normalizers that must
// persist or verify result artifacts before policy validation.
type ContextResultNormalizer interface {
	NormalizeContext(ctx context.Context, input AgentInput, response RuntimeResponse, now time.Time) (AgentResult, error)
}

type ResultNormalizerFunc func(input AgentInput, response RuntimeResponse, now time.Time) (AgentResult, error)

func (f ResultNormalizerFunc) Normalize(input AgentInput, response RuntimeResponse, now time.Time) (AgentResult, error) {
	return f(input, response, now)
}

type DefaultResultNormalizer struct{}

func (DefaultResultNormalizer) Normalize(input AgentInput, response RuntimeResponse, now time.Time) (AgentResult, error) {
	return normalizeRuntimeResponse(input, response, now)
}

func defaultNormalizer(normalizer ResultNormalizer) ResultNormalizer {
	if normalizer != nil {
		return normalizer
	}
	return ResultNormalizerFunc(normalizeRuntimeResponse)
}

func normalizeAgentResult(ctx context.Context, normalizer ResultNormalizer, input AgentInput, response RuntimeResponse, now time.Time) (AgentResult, error) {
	if contextual, ok := normalizer.(ContextResultNormalizer); ok {
		return contextual.NormalizeContext(ctx, input, response, now)
	}
	return normalizer.Normalize(input, response, now)
}

func defaultSummaryGenerator(generator SummaryGenerator) SummaryGenerator {
	if generator != nil {
		return generator
	}
	return DefaultSummaryGenerator{}
}

func defaultTraceRecorder(recorder TraceRecorder) TraceRecorder {
	return recorder
}

func defaultNotifier(notifier Notifier) Notifier {
	return notifier
}

func normalizeRuntimeResponse(input AgentInput, response RuntimeResponse, now time.Time) (AgentResult, error) {
	summary := firstTextMessage(response.Messages)
	result := AgentResult{
		ID:               AgentResultID("result-" + input.RunID.String()),
		MissionID:        missionIDFromInput(input),
		TaskID:           taskIDFromInput(input),
		AgentID:          input.ContextPack.AgentID,
		RunID:            input.RunID,
		Summary:          Summary{Text: summary},
		InternalTraceRef: response.RawOutputRef,
		CreatedAt:        now,
	}
	for i, expected := range input.Task.ExpectedDeliverables {
		if i >= len(response.Artifacts) {
			break
		}
		result.Deliverables = append(result.Deliverables, Deliverable{
			ID:                    DeliverableID("deliverable-" + expected.ID),
			ExpectedDeliverableID: expected.ID,
			Title:                 expected.Title,
			ArtifactRef:           response.Artifacts[i],
		})
	}
	for _, call := range response.ToolCalls {
		requirementID := ""
		kind := EvidenceKindToolLog
		for _, requirement := range input.Task.EvidenceRequirements {
			if requirement.ID == call.ID {
				requirementID = requirement.ID
				if requirement.Kind != "" {
					kind = EvidenceKind(requirement.Kind)
				}
				break
			}
		}
		if requirementID == "" {
			continue
		}
		result.Evidence = append(result.Evidence, Evidence{
			ID:                    EvidenceID("tool-call-" + call.ID),
			EvidenceRequirementID: requirementID,
			Kind:                  kind,
			ToolLogRef:            ToolLogRef{ID: ToolLogID(call.ID)},
			AcceptanceCriterionID: firstAcceptanceID(input),
		})
	}
	return result, nil
}

func missionIDFromInput(input AgentInput) missioncore.MissionID {
	if input.ContextPack.MissionID != "" {
		return input.ContextPack.MissionID
	}
	return missioncore.MissionID(input.Task.MissionID)
}

func taskIDFromInput(input AgentInput) missioncore.TaskID {
	if input.ContextPack.TaskID != "" {
		return input.ContextPack.TaskID
	}
	return missioncore.TaskID(input.Task.ID)
}

func firstTextMessage(messages []RuntimeMessage) string {
	if text := finalRuntimeMessageText(messages); text != "" {
		return text
	}
	var parts []string
	for _, msg := range messages {
		if msg.Type == RuntimeMessageText && strings.TrimSpace(msg.Content) != "" {
			parts = append(parts, msg.Content)
		}
	}
	return mergedRuntimeText(parts)
}

func finalRuntimeMessageText(messages []RuntimeMessage) string {
	var final string
	for _, msg := range messages {
		if msg.Type == RuntimeMessageText && msg.Status == RuntimeMessageStatusFinal && strings.TrimSpace(msg.Content) != "" {
			final = strings.TrimSpace(msg.Content)
		}
	}
	return final
}

func mergedRuntimeText(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	nonEmpty := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			nonEmpty = append(nonEmpty, part)
		}
	}
	if len(nonEmpty) == 0 {
		return ""
	}
	last := strings.TrimSpace(nonEmpty[len(nonEmpty)-1])
	if len(nonEmpty) > 1 && collapseRuntimeText(strings.Join(nonEmpty[:len(nonEmpty)-1], "")) == collapseRuntimeText(last) {
		return last
	}
	return strings.TrimSpace(strings.Join(nonEmpty, ""))
}

func collapseRuntimeText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func firstAcceptanceID(input AgentInput) string {
	if len(input.Task.AcceptanceCriteria) == 0 {
		return ""
	}
	return input.Task.AcceptanceCriteria[0].ID
}

type ResultValidator interface {
	Validate(input AgentInput, result AgentResult) error
}

type ResultValidatorFunc func(input AgentInput, result AgentResult) error

func (f ResultValidatorFunc) Validate(input AgentInput, result AgentResult) error {
	return f(input, result)
}

func defaultValidator(validator ResultValidator) ResultValidator {
	if validator != nil {
		return validator
	}
	return ResultValidatorFunc(validatePipelineResult)
}

func validatePipelineResult(input AgentInput, result AgentResult) error {
	var errs []ValidationError
	errs = append(errs, ValidateAgentResult(result)...)
	// A blocker records incomplete work; only completed results must cover
	// the full task contract. Supplied evidence must still be traceable.
	if result.Blocker == nil {
		for _, expected := range input.Task.ExpectedDeliverables {
			if !hasDeliverable(result.Deliverables, expected.ID) {
				errs = append(errs, validationError(CodeMissingDeliverable, "deliverables", "missing expected deliverable "+expected.ID))
			}
		}
		for _, required := range input.Task.EvidenceRequirements {
			if !hasEvidence(result.Evidence, required.ID) {
				errs = append(errs, validationError(CodeMissingRequiredEvidence, "evidence", "missing required evidence "+required.ID))
			}
		}
		for _, criterion := range input.Task.AcceptanceCriteria {
			if !hasAcceptanceEvidence(result.Evidence, criterion.ID) {
				errs = append(errs, validationError(CodeMissingAcceptanceMapping, "evidence", "missing acceptance evidence "+criterion.ID))
			}
		}
	}
	for _, evidence := range result.Evidence {
		if !evidence.HasTraceableSource() {
			errs = append(errs, validationError(CodeInvalidRef, "evidence", "evidence must be traceable"))
		}
	}
	if len(errs) > 0 {
		return ResultValidationError{Errors: errs}
	}
	return nil
}

func hasDeliverable(deliverables []Deliverable, id string) bool {
	for _, deliverable := range deliverables {
		if deliverable.ExpectedDeliverableID == id {
			return true
		}
	}
	return false
}

func hasEvidence(evidence []Evidence, id string) bool {
	for _, item := range evidence {
		if item.EvidenceRequirementID == id {
			return true
		}
	}
	return false
}

func hasAcceptanceEvidence(evidence []Evidence, id string) bool {
	for _, item := range evidence {
		if item.AcceptanceCriterionID == id {
			return true
		}
	}
	return false
}

type ResultValidationError struct {
	Errors []ValidationError
}

func (e ResultValidationError) Error() string {
	return "agent result validation failed"
}

func (e ResultValidationError) Is(target error) bool {
	_, ok := target.(ResultValidationError)
	return ok
}

func IsResultValidationError(err error) bool {
	var validationErr ResultValidationError
	return errors.As(err, &validationErr)
}
