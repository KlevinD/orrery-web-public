package discussionsession

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

const (
	discussionOutputLanguagePromptBlockID = "user_selected_output_language"
	discussionOutputLanguageProviderKey   = "discussion_output_language"
)

func normalizeDiscussionOutputLanguage(value string) string {
	tag := strings.ToLower(strings.TrimSpace(strings.SplitN(value, ",", 2)[0]))
	tag = strings.TrimSpace(strings.SplitN(tag, ";", 2)[0])
	switch {
	case strings.HasPrefix(tag, "zh"):
		return "zh-CN"
	case strings.HasPrefix(tag, "en"):
		return "en-US"
	default:
		return ""
	}
}

func discussionOutputLanguagePrompt(language string) string {
	language = normalizeDiscussionOutputLanguage(language)
	if language == "" {
		return ""
	}
	languageName := "English"
	if language == "zh-CN" {
		languageName = "Simplified Chinese"
	}
	return strings.Join([]string{
		"<user_selected_output_language>",
		"The user selected " + languageName + " (" + language + ") as the output language.",
		"Use this language for every user-visible response and every user-visible string written through tools, including replies, progress and status text, Mission titles, final_goal, root_goal, task goals, scope, worker_personal_goal, plan summaries, acceptance criteria, evidence descriptions, approval text, and deliverable titles.",
		"Write from the intended meaning. Mission and task titles must be concise, natural summaries of the desired outcome. Do not translate word for word, transliterate ordinary phrases, concatenate keywords, or preserve awkward source grammar.",
		"Preserve proper nouns, product names, code identifiers, tool names, file paths, URLs, and established technical terms when translating them would reduce accuracy.",
		"Quoted source text may remain in its original language, but explanations, labels, and summaries must use the selected language.",
		"When delegating work, make the selected output language explicit in the worker's execution objective so downstream results use the same language.",
		"</user_selected_output_language>",
	}, "\n")
}

func appendDiscussionOutputLanguageBlock(blocks []agentcore.RuntimePromptBlock, language string) []agentcore.RuntimePromptBlock {
	content := discussionOutputLanguagePrompt(language)
	if content == "" {
		return blocks
	}
	filtered := make([]agentcore.RuntimePromptBlock, 0, len(blocks))
	for _, existing := range blocks {
		if strings.EqualFold(strings.TrimSpace(existing.BlockID), discussionOutputLanguagePromptBlockID) {
			continue
		}
		filtered = append(filtered, existing)
	}
	blocks = filtered
	if len(blocks) == 0 {
		blocks = agentcore.DefaultRuntimePromptBlocks()
	}
	block := agentcore.RuntimePromptBlock{
		BlockID: discussionOutputLanguagePromptBlockID,
		Code:    discussionOutputLanguagePromptBlockID,
		Role:    agentcore.RuntimePromptRoleSystem,
		Content: content,
		Enabled: true,
	}
	insertAt := len(blocks)
	for i := range blocks {
		if !strings.EqualFold(strings.TrimSpace(blocks[i].Role), agentcore.RuntimePromptRoleSystem) {
			insertAt = i
			break
		}
	}
	out := make([]agentcore.RuntimePromptBlock, 0, len(blocks)+1)
	out = append(out, blocks[:insertAt]...)
	out = append(out, block)
	out = append(out, blocks[insertAt:]...)
	return out
}
