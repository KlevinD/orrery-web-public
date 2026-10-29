package core

type AgentPersona struct {
	Backstory              string
	ResponsibilityBoundary string
	BehaviorBoundary       string
}

func (p AgentPersona) HasRequiredBoundary() bool {
	return p.Backstory != "" || p.ResponsibilityBoundary != ""
}
