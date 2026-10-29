package core

type AgentID string
type AgentProfileID string
type AgentInstanceID string
type AgentResultID string
type AgentRunID string
type ContextPackID string

func (id AgentID) String() string         { return string(id) }
func (id AgentProfileID) String() string  { return string(id) }
func (id AgentInstanceID) String() string { return string(id) }
func (id AgentResultID) String() string   { return string(id) }
func (id AgentRunID) String() string      { return string(id) }
func (id ContextPackID) String() string   { return string(id) }

func (id AgentID) IsZero() bool         { return id == "" }
func (id AgentProfileID) IsZero() bool  { return id == "" }
func (id AgentInstanceID) IsZero() bool { return id == "" }
func (id AgentResultID) IsZero() bool   { return id == "" }
func (id AgentRunID) IsZero() bool      { return id == "" }
func (id ContextPackID) IsZero() bool   { return id == "" }
