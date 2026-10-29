package outbound

import (
	"regexp"
	"strings"
)

var (
	fenceRegex   = regexp.MustCompile(`^` + "```" + `(\w*)$`)
	headingRegex = regexp.MustCompile(`^#{1,6}\s`)
)

const syntheticFenceClose = "\n```"

// SplitWithCodeFences splits markdown into rune-bounded chunks. When a chunk
// ends inside a fenced code block, the fence is closed and reopened in the
// next chunk so every chunk remains valid markdown on its own.
func SplitWithCodeFences(text string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	if runeCount(text) <= limit {
		return []string{text}
	}

	lines := strings.Split(text, "\n")
	// Very small limits cannot contain an opening fence, a content rune and a
	// synthetic closing fence. In that case bounded plain splitting is the
	// only representation that can satisfy the caller's hard size limit.
	for _, line := range lines {
		if match := fenceRegex.FindStringSubmatch(line); len(match) > 0 && runeCount(line)+runeCount(syntheticFenceClose)+2 > limit {
			return splitRunes(text, limit)
		}
	}

	var chunks []string
	var current []rune
	fenceOpen := false
	fenceLang := ""
	syntheticPrefixLen := 0

	startReopenedChunk := func() {
		if !fenceOpen {
			return
		}
		current = []rune("```" + fenceLang)
		syntheticPrefixLen = len(current)
	}
	hasContent := func() bool {
		return len(current) > syntheticPrefixLen
	}
	flush := func() {
		if len(current) == 0 || !hasContent() {
			return
		}
		chunk := string(current)
		if fenceOpen {
			chunk += syntheticFenceClose
		}
		chunks = append(chunks, chunk)
		current = nil
		syntheticPrefixLen = 0
		startReopenedChunk()
	}

	for lineIndex, line := range lines {
		match := fenceRegex.FindStringSubmatch(line)
		isFence := len(match) > 0

		if isFence {
			opening := !fenceOpen
			postFenceOpen := opening
			for {
				separatorLen := 0
				if lineIndex > 0 || syntheticPrefixLen > 0 {
					separatorLen = 1
				}
				reserve := 0
				if postFenceOpen {
					reserve = runeCount(syntheticFenceClose)
				}
				if len(current)+separatorLen+runeCount(line)+reserve <= limit {
					if separatorLen > 0 {
						current = append(current, '\n')
					}
					current = append(current, []rune(line)...)
					fenceOpen = postFenceOpen
					if opening {
						fenceLang = match[1]
					} else {
						fenceLang = ""
					}
					break
				}
				flush()
			}
			continue
		}

		separatorBeforeLine := lineIndex > 0
		if !fenceOpen && headingRegex.MatchString(line) && hasContent() && len(current)*4 > limit*3 {
			flush()
			separatorBeforeLine = false
		}

		remaining := []rune(line)
		firstPart := true
		for firstPart || len(remaining) > 0 {
			separatorLen := 0
			if (firstPart && separatorBeforeLine) || syntheticPrefixLen > 0 {
				separatorLen = 1
			}
			reserve := 0
			if fenceOpen {
				reserve = runeCount(syntheticFenceClose)
			}
			capacity := limit - len(current) - separatorLen - reserve
			if capacity <= 0 {
				if separatorLen > 0 && len(current)+separatorLen+reserve <= limit {
					current = append(current, '\n')
					firstPart = false
					if len(remaining) == 0 {
						break
					}
					flush()
					continue
				}
				flush()
				continue
			}
			if separatorLen > 0 {
				current = append(current, '\n')
			}
			if len(remaining) <= capacity {
				current = append(current, remaining...)
				remaining = nil
				firstPart = false
				break
			}
			current = append(current, remaining[:capacity]...)
			remaining = remaining[capacity:]
			firstPart = false
			flush()
		}
	}
	flush()
	return chunks
}

func splitRunes(text string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	runes := []rune(text)
	if len(runes) == 0 {
		return []string{""}
	}
	chunks := make([]string, 0, (len(runes)+limit-1)/limit)
	for len(runes) > 0 {
		size := limit
		if len(runes) < size {
			size = len(runes)
		}
		chunks = append(chunks, string(runes[:size]))
		runes = runes[size:]
	}
	return chunks
}

func runeCount(value string) int {
	return len([]rune(value))
}
