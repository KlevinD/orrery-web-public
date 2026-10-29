package slidingwindow

func chooseEnd(runes []rune, start, maxEnd, minEnd int) int {
	if maxEnd >= len(runes) {
		return len(runes)
	}
	if minEnd < start {
		minEnd = start
	}
	candidates := []rune{'\n', '.', '!', '?', ';', ',', ' '}
	for _, candidate := range candidates {
		for i := maxEnd - 1; i >= minEnd; i-- {
			if runes[i] == candidate {
				return i + 1
			}
		}
	}
	return maxEnd
}

func nextStart(start, end, overlap int) int {
	if overlap <= 0 {
		return end
	}
	next := end - overlap
	if next <= start {
		return end
	}
	return next
}
