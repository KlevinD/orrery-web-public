package memorypreference

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

var durablePreference = regexp.MustCompile(`(?i)\b(i (prefer|like)|my preference|from now on|by default|always (use|reply|respond)|please always|remember (that|to))\b|我(?:更|最)?(?:喜欢|偏好|习惯)|以后|今后|默认|请记住|每次|始终`)
var privateIdentifier = regexp.MustCompile(`(?i)[\w.+-]+@[\w.-]+\.[a-z]{2,}|\b(?:\d[ -]?){7,}\b|\b(?:sk-|ghp_|github_pat_)[a-z0-9_-]+|api[ _-]?key|password|secret|密码|密钥|身份证|银行卡|手机号|住址`)

func HasPreferenceSignal(text string) bool {
	return durablePreference.MatchString(text) && !privateIdentifier.MatchString(text)
}

func validateFacts(facts []Fact, userText string) error {
	if len(facts) > MaxFacts {
		return fmt.Errorf("preference extraction exceeds %d facts", MaxFacts)
	}
	seen := map[string]bool{}
	for _, fact := range facts {
		if !slices.Contains(preferenceKeys, fact.Key) || seen[fact.Key] {
			return fmt.Errorf("preference extraction has an unknown or duplicate topic")
		}
		seen[fact.Key] = true
		if strings.TrimSpace(fact.Value) == "" || utf8.RuneCountInString(fact.Value) > 200 ||
			strings.TrimSpace(fact.SourceQuote) == "" || utf8.RuneCountInString(fact.SourceQuote) > 500 ||
			!strings.Contains(userText, fact.SourceQuote) {
			return fmt.Errorf("preference must have a bounded value and verbatim user evidence")
		}
		if !durablePreference.MatchString(fact.SourceQuote) || unsafePreferenceText(fact.Value) || unsafePreferenceText(fact.SourceQuote) || !safeEvidenceContext(userText, fact.SourceQuote) {
			return fmt.Errorf("preference evidence is not an explicit durable interaction preference")
		}
	}
	return nil
}

// A model must not turn "客户说以后用中文" or a question into the user's
// preference by quoting only "以后用中文". Inspect the enclosing sentence too.
func safeEvidenceContext(text, quote string) bool {
	start := strings.Index(text, quote)
	if start < 0 {
		return false
	}
	end := start + len(quote)
	boundary := strings.LastIndexAny(text[:start], "。！？!?；;\n")
	if boundary >= 0 {
		_, width := utf8.DecodeRuneInString(text[boundary:])
		start = boundary + width
	} else {
		start = 0
	}
	boundary = strings.IndexAny(text[end:], "。！？!?；;\n")
	if boundary >= 0 {
		_, width := utf8.DecodeRuneInString(text[end+boundary:])
		end += boundary + width
	} else {
		end = len(text)
	}
	context := strings.TrimSpace(text[start:end])
	return !unsafePreferenceText(context) && !strings.ContainsAny(context, "\"“”‘’")
}

func unsafePreferenceText(value string) bool {
	if !utf8.ValidString(value) || privateIdentifier.MatchString(value) || strings.ContainsAny(value, "?？<>`") {
		return true
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	value = strings.ToLower(value)
	for _, phrase := range []string{
		"这次", "本次", "今天", "临时", "这份", "这封", "假设", "假如", "如果", "例如", "比如", "示例", "客户说", "他说", "她说",
		"this time", "for this", "today", "hypothetically", "for example", "if ", " said ", " says ", "suppose ",
		"ignore", "override", "system:", "assistant:", "忽略", "绕过",
	} {
		if strings.Contains(value, phrase) {
			return true
		}
	}
	return false
}
