package discussion

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type MentionParseResult struct {
	Targets  []MentionTarget
	Warnings []MentionParseWarning
}

type MentionParseWarning struct {
	Target MentionTarget
	Reason string
}

// MentionParser reads Orrery's persisted mention links. It owns no runtime or
// authorization state; the discussion service resolves and authorizes targets.
type MentionParser struct{}

func NewMentionParser() MentionParser { return MentionParser{} }

func (MentionParser) Parse(content string) MentionParseResult {
	var result MentionParseResult
	seen := make(map[MentionTarget]struct{})
	collect := func(kind, ref string) {
		target := MentionTarget{Ref: ref}
		switch kind {
		case "agent":
			target.Kind = MentionTargetAgent
		case "role":
			target.Kind, target.Role = MentionTargetRole, ref
		case "user":
			target.Kind = MentionTargetUser
		case "team":
			target.Kind = MentionTargetTeam
		case "all":
			target.Kind = MentionTargetAll
		default:
			result.Warnings = append(result.Warnings, MentionParseWarning{
				Target: MentionTarget{Kind: MentionTargetKind(kind), Ref: ref},
				Reason: "unsupported mention target",
			})
			return
		}
		if _, exists := seen[target]; !exists {
			seen[target] = struct{}{}
			result.Targets = append(result.Targets, target)
		}
	}

	// Preserve the historical ordering: formatted links precede bare links.
	opening := -1
formatted:
	for offset := 0; offset < len(content); offset++ {
		switch content[offset] {
		case '\n':
			opening = -1
		case '[':
			if opening < 0 {
				opening = offset
			}
		case ']':
			if opening < 0 {
				continue
			}
			if offset == opening+1 || !strings.HasPrefix(content[offset+1:], "(mention://") {
				continue
			}
			start := offset + len("](mention://")
			end := strings.IndexByte(content[start:], ')')
			if end < 0 {
				// No later formatted link can close either. Stop here so a
				// message with many unfinished links is scanned only once.
				break formatted
			}
			if kind, ref, ok := splitMentionAddress(content[start : start+end]); ok {
				collect(kind, ref)
			}
			offset = start + end
			opening = -1
		}
	}

	const scheme = "mention://"
	for cursor := 0; cursor < len(content); {
		found := strings.Index(content[cursor:], scheme)
		if found < 0 {
			break
		}
		start := cursor + found
		cursor = start + len(scheme)
		if start > 0 {
			previous, _ := utf8.DecodeLastRuneInString(content[:start])
			if !unicode.IsSpace(previous) && !strings.ContainsRune("，。！？；：,.!?;:", previous) {
				continue
			}
		}
		end := cursor
		for end < len(content) && mentionKindByte(content[end]) {
			end++
		}
		if end == cursor || end >= len(content) || content[end] != '/' {
			continue
		}
		kind, refStart := content[cursor:end], end+1
		end = refStart
		for end < len(content) && (mentionKindByte(content[end]) || content[end] >= '0' && content[end] <= '9' || content[end] == '-') {
			end++
		}
		if end > refStart {
			collect(kind, content[refStart:end])
		}
		cursor = end
	}
	return result
}

func splitMentionAddress(address string) (string, string, bool) {
	kind, ref, found := strings.Cut(address, "/")
	if !found || kind == "" || ref == "" {
		return "", "", false
	}
	for i := range kind {
		if !mentionKindByte(kind[i]) {
			return "", "", false
		}
	}
	return kind, ref, true
}

func mentionKindByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '_'
}
