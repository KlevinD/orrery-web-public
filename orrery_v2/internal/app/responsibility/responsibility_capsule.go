package responsibility

import (
	"bufio"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const DefaultResponsibilityCapsuleRunes = 3200

var responsibilitySectionOrder = []string{
	"Mission", "Summary", "Accountable outputs", "Core duties", "Decision rights", "Service boundaries",
	"Forbidden actions", "Allowed tool scopes", "Allowed data scopes", "Human-in-the-loop triggers",
	"Evidence expectations", "Escalation policy",
}

// BuildResponsibilityPulsePromptWithLimit is retained for explicitly approved interactive planning.
// Periodic responsibility evaluation must inspect structured Assignments and must not execute this prompt.
func BuildResponsibilityPulsePromptWithLimit(portfolio PeopleResponsibilityPortfolio, maxRunes int) (string, string) {
	if maxRunes <= 0 {
		maxRunes = DefaultResponsibilityCapsuleRunes
	}
	goal := fmt.Sprintf("Review People responsibility portfolio %s and advance the highest-value due obligation with traceable evidence.", portfolio.ID)
	var capsule strings.Builder
	capsule.WriteString("People responsibility capsule\n")
	capsule.WriteString("subject: " + portfolio.SubjectID + "\n")
	capsule.WriteString("source revision: " + portfolio.SourceRevision + "\n")
	for _, item := range portfolio.Responsibilities {
		capsule.WriteString("\n[" + strings.TrimSpace(item.Code) + "] " + strings.TrimSpace(item.Title) + "\n")
		sections := parseResponsibilitySections(item.Description)
		for _, section := range responsibilitySectionOrder {
			values := sections[section]
			if len(values) == 0 {
				continue
			}
			capsule.WriteString(section + ":\n")
			for _, value := range values {
				capsule.WriteString("- " + value + "\n")
			}
		}
	}
	instructions := `Run policy:
1. Inspect current goals, open Missions/Tasks, recent signals, and this capsule.
2. Advance one due obligation or the minimum follow-up; do not attempt every duty.
3. Schedule recurring work only when justified and authorized. Never duplicate an equivalent schedule.
4. Stay inside tool/data boundaries; escalate only for stated triggers, missing authority, or material risk.
5. Return the obligation, action, result, evidence, blockers, and next review point.
6. Use the portfolio and revision as audit references; fetch more organization data only when needed.`
	policyRunes := utf8.RuneCountInString(instructions)
	if maxRunes <= policyRunes+2 {
		return goal, truncateRunes(instructions, maxRunes)
	}
	capsuleBudget := maxRunes - policyRunes - 2
	prompt := strings.TrimSpace(truncateRunes(capsule.String(), capsuleBudget)) + "\n\n" + instructions
	return goal, prompt
}

func parseResponsibilitySections(description string) map[string][]string {
	allowed := make(map[string]string, len(responsibilitySectionOrder))
	for _, section := range responsibilitySectionOrder {
		allowed[strings.ToLower(section)] = section
	}
	result := make(map[string][]string)
	current := ""
	scanner := bufio.NewScanner(strings.NewReader(description))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		headingLine := strings.TrimSpace(strings.TrimLeft(line, "#"))
		if before, after, ok := strings.Cut(headingLine, ":"); ok {
			if canonical, exists := allowed[strings.ToLower(strings.TrimSpace(before))]; exists {
				current = canonical
				appendCapsuleValue(result, current, after)
				continue
			}
			if strings.TrimSpace(after) == "" {
				current = ""
				continue
			}
		}
		if canonical, exists := allowed[strings.ToLower(strings.TrimSuffix(headingLine, ":"))]; exists {
			current = canonical
			continue
		}
		if current == "" || (!strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "* ")) {
			continue
		}
		appendCapsuleValue(result, current, strings.TrimSpace(strings.TrimLeft(line, "-*")))
	}
	return result
}

func appendCapsuleValue(result map[string][]string, section, value string) {
	value = strings.TrimSpace(value)
	if value == "" || len(result[section]) >= 6 {
		return
	}
	value = truncateRunes(value, 240)
	for _, existing := range result[section] {
		if existing == value {
			return
		}
	}
	result[section] = append(result[section], value)
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	if limit <= 3 {
		return string(runes[:limit])
	}
	return strings.TrimSpace(string(runes[:limit-3])) + "..."
}

func ResponsibilityPortfolioSourceRefs(portfolio PeopleResponsibilityPortfolio) []string {
	refs := []string{"people:subject:" + portfolio.SubjectID, "people:source-revision:" + portfolio.SourceRevision}
	for _, item := range portfolio.Responsibilities {
		refs = append(refs, "people:responsibility:"+item.ID+":v"+fmt.Sprint(item.Revision))
		for _, source := range item.Sources {
			bindingID := strings.TrimSpace(source.BindingID)
			if bindingID == "" {
				bindingID = strings.TrimSpace(source.ID)
			}
			if bindingID != "" {
				refs = append(refs, "people:responsibility-binding:"+bindingID+":v"+fmt.Sprint(source.Revision))
			}
		}
	}
	sort.Strings(refs)
	return refs
}
