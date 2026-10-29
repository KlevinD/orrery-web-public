package outputpolicy

import "strings"

const (
	thinkOpenTag  = "<think>"
	thinkCloseTag = "</think>"
)

// FilterResult is public text plus non-sensitive audit information about
// private reasoning removed from the source.
type FilterResult struct {
	Text            string
	Suppressed      bool
	SuppressedBytes int64
}

// PrivateReasoningFilter removes <think> blocks without assuming that tags or
// content arrive in a single provider chunk.
type PrivateReasoningFilter struct {
	inPrivate       bool
	pending         string
	suppressed      bool
	suppressedBytes int64
}

func (f *PrivateReasoningFilter) Write(value string) string {
	if f == nil || value == "" {
		return value
	}
	f.pending += value
	var output strings.Builder
	for f.pending != "" {
		lower := strings.ToLower(f.pending)
		if f.inPrivate {
			if index := strings.Index(lower, thinkCloseTag); index >= 0 {
				f.suppress(index + len(thinkCloseTag))
				f.pending = f.pending[index+len(thinkCloseTag):]
				f.inPrivate = false
				continue
			}
			keep := matchingTagPrefixSuffix(lower, thinkCloseTag)
			f.suppress(len(f.pending) - keep)
			f.pending = f.pending[len(f.pending)-keep:]
			return output.String()
		}

		index, tag := nextReasoningTag(lower)
		if index >= 0 {
			output.WriteString(f.pending[:index])
			f.suppress(len(tag))
			f.pending = f.pending[index+len(tag):]
			if tag == thinkOpenTag {
				f.inPrivate = true
			}
			continue
		}

		keep := max(
			matchingTagPrefixSuffix(lower, thinkOpenTag),
			matchingTagPrefixSuffix(lower, thinkCloseTag),
		)
		output.WriteString(f.pending[:len(f.pending)-keep])
		f.pending = f.pending[len(f.pending)-keep:]
		return output.String()
	}
	return output.String()
}

// Finish closes the stream. A trailing partial tag is suppressed because it
// may be the beginning of a private reasoning delimiter.
func (f *PrivateReasoningFilter) Finish() string {
	if f == nil || f.pending == "" {
		return ""
	}
	if f.inPrivate || isReasoningTagPrefix(strings.ToLower(f.pending)) {
		f.suppress(len(f.pending))
		f.pending = ""
		return ""
	}
	value := f.pending
	f.pending = ""
	return value
}

func (f *PrivateReasoningFilter) Result(text string) FilterResult {
	if f == nil {
		return FilterResult{Text: text}
	}
	return FilterResult{Text: text, Suppressed: f.suppressed, SuppressedBytes: f.suppressedBytes}
}

func FilterPrivateReasoning(value string) FilterResult {
	filter := &PrivateReasoningFilter{}
	text := filter.Write(value) + filter.Finish()
	return filter.Result(text)
}

func (f *PrivateReasoningFilter) suppress(size int) {
	if size <= 0 {
		return
	}
	f.suppressed = true
	f.suppressedBytes += int64(size)
}

func nextReasoningTag(value string) (int, string) {
	open := strings.Index(value, thinkOpenTag)
	close := strings.Index(value, thinkCloseTag)
	switch {
	case open < 0:
		return close, thinkCloseTag
	case close < 0 || open <= close:
		return open, thinkOpenTag
	default:
		return close, thinkCloseTag
	}
}

func matchingTagPrefixSuffix(value, tag string) int {
	limit := min(len(value), len(tag)-1)
	for size := limit; size > 0; size-- {
		if strings.HasSuffix(value, tag[:size]) {
			return size
		}
	}
	return 0
}

func isReasoningTagPrefix(value string) bool {
	return value != "" && (strings.HasPrefix(thinkOpenTag, value) || strings.HasPrefix(thinkCloseTag, value))
}
