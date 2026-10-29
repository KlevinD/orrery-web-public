package discussionsession

import (
	"strings"
	"unicode"
)

const personalAssistantLightweightTurnProviderKey = "personal_assistant_lightweight_turn"

// PersonalAssistantReadOnlyReviewProviderKey marks a trusted server-owned
// personal-assistant turn that may interpret supplied material but must not
// receive tools or skills.
const PersonalAssistantReadOnlyReviewProviderKey = "personal_assistant_read_only_review"

var personalAssistantLightweightUtterances = map[string]struct{}{
	"hi": {}, "hello": {}, "hey": {}, "yo": {},
	"who are you": {}, "what is your name": {}, "what's your name": {},
	"thanks": {}, "thank you": {}, "ok": {}, "okay": {},
	"bye": {}, "goodbye": {},
	"你好": {}, "嗨": {}, "哈喽": {}, "哈啰": {}, "在吗": {},
	"早": {}, "早上好": {}, "下午好": {}, "晚上好": {},
	"你是谁": {}, "你叫什么": {}, "你叫什么名字": {},
	"谢谢": {}, "感谢": {}, "好的": {}, "收到": {}, "明白": {},
	"再见": {}, "拜拜": {},
}

func isPersonalAssistantLightweightTurn(request DiscussionAgentRequest) bool {
	if !isPersonalAssistantRuntimeRequest(request) {
		return false
	}
	utterance := normalizePersonalAssistantLightweightUtterance(discussionCurrentTriggerMessage(request).Content)
	_, ok := personalAssistantLightweightUtterances[utterance]
	return ok
}

func isPersonalAssistantResourceRestrictedTurn(request DiscussionAgentRequest) bool {
	if isPersonalAssistantLightweightTurn(request) {
		return true
	}
	if !isPersonalAssistantRuntimeRequest(request) {
		return false
	}
	marker := strings.TrimSpace(request.ProviderSpecific[PersonalAssistantReadOnlyReviewProviderKey])
	return strings.EqualFold(marker, "true") || marker == "1"
}

func normalizePersonalAssistantLightweightUtterance(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	})
	return strings.Join(strings.Fields(value), " ")
}
