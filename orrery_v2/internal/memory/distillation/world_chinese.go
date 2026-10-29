package distillation

import (
	"regexp"
	"strings"
	"unicode"
)

var chineseRequirementMarker = regexp.MustCompile(`(?:必须|务必|应该|应当)?(?:不允许|不应该|不得|禁止|不应|不能|不要|不用|不再|无需|不必)|必须|务必|应该|应当|需要|要求`)

type chineseRequirement struct {
	subject  string
	action   string
	negative bool
}

// Compare opposite requirements within the same clause subject and action.
// A negation elsewhere in a paragraph does not negate all of its requirements.
func chineseRequirementsContradict(left, right string) bool {
	for _, a := range chineseRequirements(left) {
		for _, b := range chineseRequirements(right) {
			if a.negative != b.negative && a.subject == b.subject && a.action == b.action {
				return true
			}
		}
	}
	return false
}

func chineseRequirements(text string) []chineseRequirement {
	sentences := strings.FieldsFunc(text, func(r rune) bool {
		return strings.ContainsRune("。！!；;\n", r)
	})
	var result []chineseRequirement
	for _, sentence := range sentences {
		if strings.ContainsAny(sentence, "?？\"“”‘’") || strings.Contains(sentence, "是否") {
			continue
		}
		conditional := false
		for _, marker := range []string{"如果", "假如", "假设", "例如", "比如"} {
			conditional = conditional || strings.Contains(sentence, marker)
		}
		if conditional {
			continue
		}
		for _, clause := range strings.FieldsFunc(sentence, func(r rune) bool { return r == '，' || r == ',' }) {
			if requirement, ok := parseChineseRequirement(strings.TrimSpace(clause)); ok {
				result = append(result, requirement)
			}
		}
	}
	return result
}

func parseChineseRequirement(clause string) (chineseRequirement, bool) {
	matches := chineseRequirementMarker.FindAllStringIndex(clause, -1)
	if len(matches) != 1 {
		return chineseRequirement{}, false
	}
	match := matches[0]
	marker := clause[match[0]:match[1]]
	action := normalizeChineseRequirementAction(clause[match[1]:])
	if len([]rune(action)) < 2 || strings.HasPrefix(action, "不") || strings.HasPrefix(action, "无") {
		return chineseRequirement{}, false
	}
	subject := strings.Join(strings.Fields(clause[:match[0]]), "")
	for _, prefix := range []string{"今后", "以后", "默认", "请"} {
		subject = strings.TrimPrefix(subject, prefix)
	}
	return chineseRequirement{
		subject: subject, action: action,
		negative: strings.ContainsAny(marker, "不无禁"),
	}, true
}

func normalizeChineseRequirementAction(value string) string {
	value = strings.Trim(strings.Join(strings.Fields(value), ""), "。.!！?？；;，,")
	for _, prefix := range []string{"使用", "采用", "用"} {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimPrefix(value, prefix)
		}
	}
	return value
}

// Character bigrams allow Chinese source text to participate in similarity
// ranking without relying on whitespace or adding an external tokenizer.
func addHanKeywords(out map[string]struct{}, text string) {
	var previous rune
	for _, current := range text {
		if !unicode.Is(unicode.Han, current) {
			previous = 0
			continue
		}
		if previous != 0 {
			out[string([]rune{previous, current})] = struct{}{}
		}
		previous = current
	}
}
