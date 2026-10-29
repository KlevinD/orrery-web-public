package actioncontract

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// ActionSemanticInterpreter turns a user utterance into a business action
// frame. The frame is context for the agent; execution still goes through the
// normal ActionRun authorization and audit path.
type ActionSemanticInterpreter interface {
	InterpretAction(context.Context, string, string) (ActionSemanticFrame, error)
}

type SemanticInterpreter struct {
	repository Repository
}

type actionTypeSemanticPageReader interface {
	ListActionTypePage(context.Context, string, string, int) (ActionTypePage, error)
}

func NewSemanticInterpreter(repository Repository) *SemanticInterpreter {
	return &SemanticInterpreter{repository: repository}
}

func (i *SemanticInterpreter) InterpretAction(ctx context.Context, tenantID, utterance string) (ActionSemanticFrame, error) {
	if err := ctx.Err(); err != nil {
		return ActionSemanticFrame{}, err
	}
	if i == nil || i.repository == nil {
		return ActionSemanticFrame{}, fmt.Errorf("%w: action repository is required", ErrInvalidRequest)
	}
	actionTypes, err := listSemanticActionTypes(ctx, i.repository, tenantID)
	if err != nil {
		return ActionSemanticFrame{}, err
	}
	return InterpretActionTypes(actionTypes, tenantID, utterance), nil
}

func listSemanticActionTypes(ctx context.Context, repository Repository, tenantID string) ([]ActionType, error) {
	pageReader, ok := repository.(actionTypeSemanticPageReader)
	if !ok || strings.TrimSpace(tenantID) == "" {
		return repository.ListActionTypes(ctx)
	}
	const pageSize = 256
	afterID := ""
	var out []ActionType
	for {
		page, err := pageReader.ListActionTypePage(ctx, tenantID, afterID, pageSize)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
		if !page.HasMore || len(page.Items) == 0 {
			return out, nil
		}
		afterID = string(page.Items[len(page.Items)-1].ID)
	}
}

// InterpretDefaultAction recognizes the managed system action vocabulary. It
// is used as a fail-open fallback when a runtime has no action repository.
func InterpretDefaultAction(tenantID, utterance string) ActionSemanticFrame {
	return InterpretActionTypes(DefaultActionTypes(), tenantID, utterance)
}

func InterpretActionTypes(actionTypes []ActionType, tenantID, utterance string) ActionSemanticFrame {
	normalized := normalizeSemanticText(utterance)
	if normalized == "" {
		return ActionSemanticFrame{}
	}
	tenantID = strings.TrimSpace(tenantID)
	type candidate struct {
		action ActionType
		verb   string
		score  float64
	}
	var candidates []candidate
	for _, actionType := range actionTypes {
		if !actionType.Enabled || !actionTypeVisibleToTenant(actionType, tenantID) {
			continue
		}
		for _, verb := range semanticVerbs(actionType) {
			verbNormalized := normalizeSemanticText(verb)
			if verbNormalized == "" || !strings.Contains(normalized, verbNormalized) {
				continue
			}
			score := 0.90 + minFloat(float64(len([]rune(verbNormalized)))/100, 0.08)
			if normalized == verbNormalized {
				score = 0.99
			}
			candidates = append(candidates, candidate{action: actionType, verb: strings.TrimSpace(verb), score: score})
		}
	}
	if len(candidates) == 0 {
		return ActionSemanticFrame{}
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		if candidates[left].score != candidates[right].score {
			return candidates[left].score > candidates[right].score
		}
		leftLength := len([]rune(normalizeSemanticText(candidates[left].verb)))
		rightLength := len([]rune(normalizeSemanticText(candidates[right].verb)))
		if leftLength != rightLength {
			return leftLength > rightLength
		}
		return candidates[left].action.ID < candidates[right].action.ID
	})
	selected := candidates[0]
	speechAct, state, actor, retrieval := classifySpeechAct(utterance, selected.verb)
	negated := containsSemanticMarker(utterance, semanticNegationMarkers)
	return ActionSemanticFrame{
		Recognized:         true,
		ActionTypeID:       selected.action.ID,
		ActionName:         selected.action.Name,
		SpeechAct:          speechAct,
		State:              state,
		Negated:            negated,
		Actor:              actor,
		MatchedVerb:        selected.verb,
		Confidence:         selected.score,
		ExecutionRequested: speechAct == SpeechActCommand && !negated,
		RetrievalMode:      retrieval,
		Roles:              append([]SemanticRole(nil), selected.action.Semantics.Roles...),
		Preconditions:      append([]string(nil), selected.action.Semantics.Preconditions...),
		Effects:            append([]string(nil), selected.action.Semantics.Effects...),
		DecomposesInto:     append([]ActionTypeID(nil), selected.action.Semantics.DecomposesInto...),
		KnowledgeTerms:     append([]string(nil), selected.action.Semantics.KnowledgeTerms...),
		MemoryTerms:        append([]string(nil), selected.action.Semantics.MemoryTerms...),
	}
}

func actionTypeVisibleToTenant(actionType ActionType, tenantID string) bool {
	owner := strings.TrimSpace(actionType.TenantID)
	return owner == "" || strings.EqualFold(owner, "system") || strings.EqualFold(owner, tenantID)
}

func semanticVerbs(actionType ActionType) []string {
	verbs := append([]string(nil), actionType.Semantics.Verbs...)
	verbs = append(verbs, actionType.Name)
	parts := strings.Split(string(actionType.ID), ".")
	if len(parts) > 0 {
		verbs = append(verbs, strings.ReplaceAll(parts[len(parts)-1], "_", " "))
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(verbs))
	for _, verb := range verbs {
		key := normalizeSemanticText(verb)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, verb)
	}
	return out
}

func classifySpeechAct(utterance, matchedVerb string) (SpeechAct, ActionState, string, RetrievalMode) {
	if containsSemanticMarker(utterance, semanticFailureMarkers) {
		return SpeechActFailureReport, ActionStateFailed, "user", RetrievalModeTroubleshoot
	}
	if containsSemanticMarker(utterance, semanticCommandMarkers) {
		return SpeechActCommand, ActionStateRequested, "agent", RetrievalModeNone
	}
	if containsSemanticMarker(utterance, semanticIntentionMarkers) {
		return SpeechActIntention, ActionStatePlanned, "user", RetrievalModeNone
	}
	if containsSemanticMarker(utterance, semanticQuestionMarkers) || strings.ContainsAny(utterance, "?？") {
		return SpeechActQuestion, ActionStateUnknown, "user", RetrievalModeExplain
	}
	if containsSemanticMarker(utterance, semanticCompletionMarkers) {
		return SpeechActAssertion, ActionStateCompleted, "user", RetrievalModeNone
	}
	normalized := normalizeSemanticText(utterance)
	verb := normalizeSemanticText(matchedVerb)
	if normalized == verb || (verb != "" && strings.HasPrefix(normalized, verb)) {
		return SpeechActCommand, ActionStateRequested, "agent", RetrievalModeNone
	}
	return SpeechActAssertion, ActionStateObserved, "user", RetrievalModeNone
}

func containsSemanticMarker(value string, markers []string) bool {
	normalized := normalizeSemanticText(value)
	for _, marker := range markers {
		if strings.Contains(normalized, normalizeSemanticText(marker)) {
			return true
		}
	}
	return false
}

func normalizeSemanticText(value string) string {
	var b strings.Builder
	for _, char := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(char) || unicode.IsNumber(char) {
			b.WriteRune(char)
		}
	}
	return b.String()
}

func minFloat(left, right float64) float64 {
	if left < right {
		return left
	}
	return right
}

var semanticFailureMarkers = []string{
	"失败", "报错", "错误", "异常", "没成功", "未成功", "超时", "被拒绝", "failed", "failure", "error", "timed out", "denied",
}

var semanticCommandMarkers = []string{
	"帮我", "请你", "请帮", "替我", "给我", "帮忙", "麻烦", "立即", "现在就", "能不能", "可以帮", "please", "can you", "could you", "would you",
}

var semanticIntentionMarkers = []string{
	"我要", "我想", "我来", "准备", "计划", "打算", "接下来", "want to", "plan to", "going to", "will",
}

var semanticQuestionMarkers = []string{
	"怎么", "如何", "为什么", "为何", "是否", "什么", "哪", "吗", "么", "how", "why", "what", "when", "where", "which",
}

var semanticCompletionMarkers = []string{
	"已经", "已完成", "刚刚", "刚才", "完成了", "成功了", "提交了", "推送了", "合并了", "done", "completed", "already", "have committed", "has committed",
}

var semanticNegationMarkers = []string{
	"不要", "别", "禁止", "无需", "不用", "没有", "没", "未", "not", "don't", "do not", "never",
}
