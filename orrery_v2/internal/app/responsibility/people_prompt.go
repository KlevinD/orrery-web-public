package responsibility

func BuildResponsibilityPulsePrompt(portfolio PeopleResponsibilityPortfolio) string {
	_, prompt := BuildResponsibilityPulsePromptWithLimit(portfolio, DefaultResponsibilityCapsuleRunes)
	return prompt
}
