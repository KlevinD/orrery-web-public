package markdown

import "strings"

type heading struct {
	level  int
	title  string
	offset int
}

func parseHeadings(text string) []heading {
	lines := strings.SplitAfter(text, "\n")
	headings := make([]heading, 0)
	offset := 0
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			offset += len([]rune(line))
			continue
		}
		if !inFence {
			if level, title, ok := parseATXHeading(trimmed); ok {
				headings = append(headings, heading{level: level, title: title, offset: offset})
			}
		}
		offset += len([]rune(line))
	}
	return headings
}

func parseATXHeading(line string) (int, string, bool) {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(line) || line[level] != ' ' {
		return 0, "", false
	}
	title := strings.TrimSpace(line[level+1:])
	title = strings.TrimSpace(strings.TrimRight(title, "#"))
	if title == "" {
		return 0, "", false
	}
	return level, title, true
}

func buildSections(text string, headings []heading) []sectionDraft {
	if len(headings) == 0 {
		return []sectionDraft{{start: 0, end: len([]rune(text))}}
	}
	sections := make([]sectionDraft, 0, len(headings))
	stack := make([]string, 0, 6)
	for i, current := range headings {
		if current.level <= len(stack) {
			stack = stack[:current.level-1]
		}
		for len(stack) < current.level-1 {
			stack = append(stack, "")
		}
		stack = append(stack, current.title)
		end := len([]rune(text))
		if i+1 < len(headings) {
			end = headings[i+1].offset
		}
		sections = append(sections, sectionDraft{
			headingPath: cloneStrings(stack),
			start:       current.offset,
			end:         end,
		})
	}
	return sections
}

type sectionDraft struct {
	headingPath []string
	start       int
	end         int
}

func runeSlice(text string, start, end int) string {
	runes := []rune(text)
	if start < 0 {
		start = 0
	}
	if end > len(runes) {
		end = len(runes)
	}
	if start > end {
		start = end
	}
	return string(runes[start:end])
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
