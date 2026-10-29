package core

type MissionID string
type TaskID string

func (id MissionID) String() string {
	return string(id)
}

func (id TaskID) String() string {
	return string(id)
}

func (id MissionID) IsZero() bool {
	return id == ""
}

func (id TaskID) IsZero() bool {
	return id == ""
}

type RequesterKind string

const (
	RequesterKindHuman  RequesterKind = "human"
	RequesterKindAgent  RequesterKind = "agent"
	RequesterKindSystem RequesterKind = "system"
)

type RequesterRef struct {
	ID   string
	Kind RequesterKind
}
