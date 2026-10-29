package discussionsession

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const discussionMentionScheme = "mention://"

type discussionMentionLink struct {
	Kind  string
	ID    string
	Label string
	Start int
	End   int
}

// parseDiscussionMentionLinks accepts the canonical markdown link shape and
// the bare URI shape occasionally emitted by model providers. Routing callers
// still validate parsed IDs against persisted conversation membership.
func parseDiscussionMentionLinks(content string) []discussionMentionLink {
	links := make([]discussionMentionLink, 0)
	for cursor := 0; cursor < len(content); {
		openOffset := strings.IndexByte(content[cursor:], '[')
		if openOffset < 0 {
			break
		}
		open := cursor + openOffset
		labelEndOffset := strings.Index(content[open+1:], "](")
		if labelEndOffset < 0 {
			break
		}
		labelEnd := open + 1 + labelEndOffset
		if nestedOpen := strings.LastIndexByte(content[open+1:labelEnd], '['); nestedOpen >= 0 {
			open = open + 1 + nestedOpen
		}
		targetStart := labelEnd + 2
		if !strings.HasPrefix(content[targetStart:], discussionMentionScheme) {
			cursor = labelEnd + 1
			continue
		}
		targetEndOffset := strings.IndexByte(content[targetStart:], ')')
		if targetEndOffset < 0 {
			break
		}
		targetEnd := targetStart + targetEndOffset
		target := content[targetStart+len(discussionMentionScheme) : targetEnd]
		slash := strings.IndexByte(target, '/')
		kind := ""
		id := ""
		if slash > 0 {
			kind = target[:slash]
			id = strings.TrimSpace(target[slash+1:])
		}
		label := strings.TrimSpace(content[open+1 : labelEnd])
		label = strings.TrimPrefix(label, "@")
		if validDiscussionMentionKind(kind) && validDiscussionMentionID(id) && label != "" {
			links = append(links, discussionMentionLink{Kind: kind, ID: id, Label: label, Start: open, End: targetEnd + 1})
		}
		cursor = targetEnd + 1
	}
	links = append(links, parseBareDiscussionMentionLinks(content, links)...)
	sort.SliceStable(links, func(i, j int) bool { return links[i].Start < links[j].Start })
	return links
}

func parseBareDiscussionMentionLinks(content string, markdownLinks []discussionMentionLink) []discussionMentionLink {
	links := make([]discussionMentionLink, 0)
	for cursor := 0; cursor < len(content); {
		offset := strings.Index(content[cursor:], discussionMentionScheme)
		if offset < 0 {
			break
		}
		start := cursor + offset
		if discussionMentionOffsetInLinks(start, markdownLinks) || strings.HasSuffix(content[:start], "](") {
			cursor = start + len(discussionMentionScheme)
			continue
		}
		targetStart := start + len(discussionMentionScheme)
		slashOffset := strings.IndexByte(content[targetStart:], '/')
		if slashOffset <= 0 {
			cursor = targetStart
			continue
		}
		slash := targetStart + slashOffset
		kind := content[targetStart:slash]
		idStart := slash + 1
		idEnd := idStart
		for idEnd < len(content) {
			r, size := utf8.DecodeRuneInString(content[idEnd:])
			if bareDiscussionMentionTerminator(r) {
				break
			}
			idEnd += size
		}
		id := strings.TrimSpace(content[idStart:idEnd])
		if validDiscussionMentionKind(kind) && validDiscussionMentionID(id) {
			links = append(links, discussionMentionLink{Kind: kind, ID: id, Label: id, Start: start, End: idEnd})
		}
		cursor = idEnd
		if cursor <= start {
			cursor = start + len(discussionMentionScheme)
		}
	}
	return links
}

func discussionMentionOffsetInLinks(offset int, links []discussionMentionLink) bool {
	for _, link := range links {
		if offset >= link.Start && offset < link.End {
			return true
		}
	}
	return false
}

func bareDiscussionMentionTerminator(r rune) bool {
	if unicode.IsSpace(r) || unicode.IsControl(r) {
		return true
	}
	return strings.ContainsRune(",.;!?)]}>\"'，。；！？）】》", r)
}

func validDiscussionMentionKind(kind string) bool {
	if kind == "" {
		return false
	}
	for _, r := range kind {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && r != '_' {
			return false
		}
	}
	return true
}

func validDiscussionMentionID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func hasDiscussionMentionLink(content string) bool {
	return len(parseDiscussionMentionLinks(content)) > 0
}

func hasDiscussionAgentMentionLink(content string) bool {
	for _, link := range parseDiscussionMentionLinks(content) {
		if link.Kind == "agent" {
			return true
		}
	}
	return false
}

func discussionAgentMentionIDs(content string) []string {
	ids := make([]string, 0)
	for _, link := range parseDiscussionMentionLinks(content) {
		if link.Kind == "agent" {
			ids = append(ids, link.ID)
		}
	}
	return ids
}

func hasPlainDiscussionMention(content string) bool {
	runes := []rune(content)
	for i, r := range runes {
		if r != '@' || (i > 0 && !discussionMentionBoundary(runes[i-1])) {
			continue
		}
		if i+1 < len(runes) && discussionMentionNameRune(runes[i+1]) {
			return true
		}
	}
	return false
}

func discussionMentionBoundary(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune("，。！？；：,.!?;:", r)
}

func discussionMentionNameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-'
}
