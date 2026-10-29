package resourceadmin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"orrery_v2/internal/decision"
	"orrery_v2/internal/platform/db"
)

const rsiRunIndexTable = "resource_rsi_learning_run_index"

// RSILearningAdvice is a review aid, never authorization to retry or publish.
// It contains no tool inputs, raw errors, private evidence, or skill source.
type RSILearningAdvice struct {
	ObservationID    string    `json:"observation_id"`
	EvidenceRef      string    `json:"evidence_ref"`
	Classification   string    `json:"classification"`
	AssessmentState  string    `json:"assessment_state"`
	NextStep         string    `json:"next_step"`
	Summary          string    `json:"summary"`
	ModelRevision    string    `json:"model_revision,omitempty"`
	ConfidenceKind   string    `json:"confidence_kind,omitempty"`
	AssessmentSource string    `json:"assessment_source,omitempty"`
	DiagnosisState   string    `json:"diagnosis_state,omitempty"`
	At               time.Time `json:"observed_at"`
}

func LearningAdvice(observation RSILearningObservation) RSILearningAdvice {
	advice := RSILearningAdvice{ObservationID: observation.ID, EvidenceRef: observation.Sample.EvidenceRef, Classification: "insufficient", AssessmentState: "pending", NextStep: "collect_evidence", Summary: "建议补充失败现场、预期结果和可复现步骤，再决定是否改进技能。", At: observation.At}
	if record := observation.Assessment; record != nil {
		advice.AssessmentState = "uncertain"
		advice.ModelRevision, advice.ConfidenceKind = record.ModelRevision, record.ConfidenceKind
		if validateRSIAssessment(observation, *record) == nil && !record.Conflicting && !record.InputTruncated && record.Fallback == "" && decision.ValidateDecisions(record.Decisions, 1, BuildDecisionRSISampleJob(observation.Sample).Question) == nil && record.Decisions[0].AnswerConfidence >= .85 {
			advice.Classification = record.Decisions[0].Choice
			advice.AssessmentState = "suggested"
			switch advice.Classification {
			case "skill_defect":
				advice.NextStep = "reproduce_skill"
				advice.Summary = "疑似可复用的技能缺陷：先复现并核对完整证据，再生成候选改进，进行基线与候选回放比较。"
			case "tool_usage":
				advice.NextStep = "review_arguments"
				advice.Summary = "疑似工具或参数使用问题：对照工具契约和原始输入核查，验证后再决定是否需要修改技能。"
			case "environment":
				advice.NextStep = "check_dependency"
				advice.Summary = "疑似环境或依赖异常：先检查服务状态和调用结果是否落地，再由现有执行流程决定是否重试；保留此样本。"
			default:
				advice.AssessmentState = "uncertain"
			}
		}
	}
	if diagnosis := observation.Diagnosis; diagnosis != nil && validateRSIFallback(observation, *diagnosis) == nil {
		advice.DiagnosisState = diagnosis.State
		if diagnosis.State == "completed" {
			// A later high-confidence primary assessment that disagrees with the
			// diagnosis needs evidence review, rather than silently choosing one.
			current := observation.Assessment
			laterDisagreement := current != nil && current.At.After(diagnosis.Primary.At) &&
				validateRSIAssessment(observation, *current) == nil && !current.InputTruncated && current.Fallback == "" &&
				decision.ValidateDecisions(current.Decisions, 1, BuildDecisionRSISampleJob(observation.Sample).Question) == nil &&
				current.Decisions[0].AnswerConfidence >= .85 && current.Decisions[0].Choice != "insufficient" &&
				current.Decisions[0].Choice != diagnosis.Result.Classification
			if laterDisagreement {
				advice.Classification, advice.AssessmentState, advice.NextStep = "insufficient", "uncertain", "collect_evidence"
				advice.Summary = "小模型与大模型的判断不一致，建议核对完整现场和复现证据后再决定改进。"
			} else if observation.Assessment != nil && RSIFallbackReason(*observation.Assessment) != "" {
				advice.AssessmentSource, advice.ModelRevision, advice.ConfidenceKind = "llm_fallback", diagnosis.Model.ModelRevision, diagnosis.Model.ConfidenceKind
				if diagnosis.Primary.SelectionSource == "rsi_llm_primary" {
					advice.AssessmentSource = "llm_primary"
				}
				advice.Classification, advice.AssessmentState = diagnosis.Result.Classification, "suggested"
				switch advice.Classification {
				case "skill_defect":
					advice.NextStep, advice.Summary = "reproduce_skill", "大模型建议核查可复用技能或业务逻辑缺陷：先复现，再生成候选改进并比较基线与候选测试结果。"
				case "tool_usage":
					advice.NextStep, advice.Summary = "review_arguments", "大模型建议核查工具契约与实际参数，验证后再决定是否修改技能。"
				case "environment":
					advice.NextStep, advice.Summary = "check_dependency", "大模型建议核查环境和依赖服务；由现有执行流程核对结果后决定后续动作。"
				default:
					advice.AssessmentState, advice.NextStep, advice.Summary = "uncertain", "collect_evidence", "大模型分析后仍缺少依据，建议补充完整现场、预期结果和可复现步骤。"
				}
			}
		}
	}
	// Recorded uncertainty about side effects always overrides a model label.
	if observation.Sample.ErrorCode == "tool_outcome_unrecorded" {
		advice.NextStep = "reconcile_outcome"
		advice.Summary = "工具可能已经执行：先核对下游状态并对账，不能自动重复调用。"
	}
	return advice
}

type RSILearningPlanItem struct {
	GroupID           string              `json:"group_id"`
	Tool              string              `json:"tool"`
	ErrorCode         string              `json:"error_code"`
	OccurrencesInPage int                 `json:"occurrences_in_page"`
	Priority          string              `json:"priority"`
	Advice            []RSILearningAdvice `json:"advice"`
}

type RSILearningPlan struct {
	Items              []RSILearningPlanItem `json:"items"`
	ObservationsInPage int                   `json:"observations_in_page"`
	NextCursor         string                `json:"next_cursor,omitempty"`
	Scope              string                `json:"ranking_scope"`
	AdvisoryOnly       bool                  `json:"advisory_only"`
}

// LearningPlan ranks a bounded page; it never drops environmental, pending,
// truncated or uncertain observations. Cursor pagination preserves access to
// the remaining history and does not imply global frequency estimates.
func (s *PlatformRSILearningSampleStore) LearningPlan(ctx context.Context, scope, after string, limit int) (RSILearningPlan, error) {
	page, err := s.List(ctx, scope, after, limit)
	if err != nil {
		return RSILearningPlan{}, err
	}
	plan := RSILearningPlan{Items: []RSILearningPlanItem{}, ObservationsInPage: len(page.Items), NextCursor: page.NextCursor, Scope: "current_page", AdvisoryOnly: true}
	groups := map[string]int{}
	for _, observation := range page.Items {
		sample := observation.Sample
		raw, _ := json.Marshal([]string{scope, sample.Tool, sample.ErrorCode})
		id := decision.Hash(string(raw))
		i, ok := groups[id]
		if !ok {
			i = len(plan.Items)
			groups[id] = i
			plan.Items = append(plan.Items, RSILearningPlanItem{GroupID: id, Tool: sample.Tool, ErrorCode: sample.ErrorCode, Priority: "normal", Advice: []RSILearningAdvice{}})
		}
		item := &plan.Items[i]
		item.OccurrencesInPage++
		advice := LearningAdvice(observation)
		item.Advice = append(item.Advice, advice)
		if item.OccurrencesInPage >= 2 || advice.NextStep == "reproduce_skill" || advice.NextStep == "reconcile_outcome" {
			item.Priority = "high"
		}
	}
	sort.SliceStable(plan.Items, func(i, j int) bool {
		a, b := plan.Items[i], plan.Items[j]
		if a.Priority != b.Priority {
			return a.Priority == "high"
		}
		if a.OccurrencesInPage != b.OccurrencesInPage {
			return a.OccurrencesInPage > b.OccurrencesInPage
		}
		return a.GroupID < b.GroupID
	})
	return plan, nil
}

func runtimeSampleRun(sample RSILearningSample) string {
	var ref []string
	if json.Unmarshal([]byte(sample.EvidenceRef), &ref) != nil || len(ref) != 3 || ref[0] != "runtime_tool" || ref[1] == "" || ref[2] == "" || len(ref[1]) > 256 {
		return ""
	}
	return ref[1]
}

func (s *PlatformRSILearningSampleStore) indexRun(ctx context.Context, observation RSILearningObservation) error {
	run := runtimeSampleRun(observation.Sample)
	if run == "" {
		return nil
	}
	key := decision.Hash(observation.Sample.Scope) + "/" + decision.Hash(run) + "/" + observation.ID
	_, err := s.store.CreateRecord(ctx, rsiRunIndexTable, key, db.Record{"observation_id": observation.ID})
	return err
}

// RunAdvice reads only the exact authorized tenant/agent/run index. It never
// scans another agent's samples or includes private evidence in the response.
func (s *PlatformRSILearningSampleStore) RunAdvice(ctx context.Context, scope, run string, limit int) ([]RSILearningAdvice, error) {
	if s == nil || s.store == nil {
		return nil, ErrServiceDependency
	}
	if err := validateRSIScope(scope); err != nil {
		return nil, err
	}
	if run == "" || len(run) > 256 || limit < 1 || limit > 6 {
		return nil, ErrInvalidRequest
	}
	prefix := decision.Hash(scope) + "/" + decision.Hash(run) + "/"
	page, err := s.store.ListRecords(ctx, rsiRunIndexTable, prefix, limit)
	if err != nil {
		return nil, err
	}
	out := make([]RSILearningAdvice, 0, limit)
	for _, row := range page.Items {
		if len(row.Key) < len(prefix) || row.Key[:len(prefix)] != prefix {
			break
		}
		id, ok := row.Record["observation_id"].(string)
		if !ok || row.Key != prefix+id {
			return nil, fmt.Errorf("%w: invalid RSI run index", ErrInvalidRequest)
		}
		observation, err := s.Get(ctx, scope, id)
		if err != nil {
			return nil, err
		}
		if runtimeSampleRun(observation.Sample) != run {
			return nil, ErrConflict
		}
		out = append(out, LearningAdvice(observation))
	}
	return out, nil
}
