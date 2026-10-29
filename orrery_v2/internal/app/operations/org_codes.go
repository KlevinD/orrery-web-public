package operations

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func validOrganizationCode(code string) bool {
	if len(code) == 0 || len(code)%2 != 0 {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func displayOrganizationCode(code string) string {
	if !validOrganizationCode(code) {
		return code
	}
	parts := make([]string, 0, len(code)/2)
	for i := 0; i < len(code); i += 2 {
		parts = append(parts, code[i:i+2])
	}
	return strings.Join(parts, ".")
}

func organizationCodeLevel(code string) int {
	if !validOrganizationCode(code) {
		return 0
	}
	return len(code) / 2
}

func nextOrganizationCode(parentCode string, existingSiblingCodes []string) (string, error) {
	if parentCode != "" && !validOrganizationCode(parentCode) {
		return "", fmt.Errorf("%w: parent organization code is invalid", ErrInvalidRequest)
	}
	used := map[int]bool{}
	prefix := parentCode
	for _, code := range existingSiblingCodes {
		if !strings.HasPrefix(code, prefix) || len(code) != len(prefix)+2 {
			continue
		}
		suffix, err := strconv.Atoi(code[len(code)-2:])
		if err == nil {
			used[suffix] = true
		}
	}
	for i := 1; i <= 99; i++ {
		if !used[i] {
			return prefix + twoDigitSuffix(i), nil
		}
	}
	return "", fmt.Errorf("%w: organization code sibling range is full", ErrInvalidRequest)
}

func twoDigitSuffix(value int) string {
	return fmt.Sprintf("%02d", value)
}

func sortOrganizationCodes(codes []string) {
	sort.SliceStable(codes, func(i, j int) bool {
		return codes[i] < codes[j]
	})
}
