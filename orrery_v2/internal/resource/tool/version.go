package tool

import (
	"fmt"
	"strconv"
	"strings"
)

func versionMatches(version, constraint string) bool {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" || constraint == "*" {
		return true
	}
	switch {
	case strings.HasPrefix(constraint, ">="):
		return compareVersion(version, strings.TrimSpace(strings.TrimPrefix(constraint, ">="))) >= 0
	case strings.HasPrefix(constraint, "^"):
		base := strings.TrimSpace(strings.TrimPrefix(constraint, "^"))
		return caretMatches(version, base)
	case strings.HasSuffix(constraint, ".x"):
		prefix := strings.TrimSuffix(constraint, ".x")
		return strings.HasPrefix(version, prefix+".")
	default:
		return version == constraint
	}
}

func caretMatches(version, base string) bool {
	v := parseVersion(version)
	b := parseVersion(base)
	if len(v) == 0 || len(b) == 0 || v[0] != b[0] {
		return false
	}
	return compareVersion(version, base) >= 0
}

func compareVersion(a, b string) int {
	av := parseVersion(a)
	bv := parseVersion(b)
	maxLen := len(av)
	if len(bv) > maxLen {
		maxLen = len(bv)
	}
	for i := 0; i < maxLen; i++ {
		var ai, bi int
		if i < len(av) {
			ai = av[i]
		}
		if i < len(bv) {
			bi = bv[i]
		}
		if ai > bi {
			return 1
		}
		if ai < bi {
			return -1
		}
	}
	return 0
}

func parseVersion(version string) []int {
	parts := strings.Split(version, ".")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		value, err := strconv.Atoi(part)
		if err != nil {
			return nil
		}
		out = append(out, value)
	}
	return out
}

func versionMismatch(version, constraint string) error {
	return fmt.Errorf("%w: %s does not satisfy %s", ErrVersionMismatch, version, constraint)
}
