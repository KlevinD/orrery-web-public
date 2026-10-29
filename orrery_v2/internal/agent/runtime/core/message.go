package core

import agentcore "orrery_v2/internal/agent/core"

type MessageType = agentcore.RuntimeMessageType

const (
	MessageTypeText       = agentcore.RuntimeMessageText
	MessageTypeThinking   = agentcore.RuntimeMessageThinking
	MessageTypeToolUse    = agentcore.RuntimeMessageToolUse
	MessageTypeToolResult = agentcore.RuntimeMessageToolResult
	MessageTypeStatus     = agentcore.RuntimeMessageStatus
	MessageTypeError      = agentcore.RuntimeMessageError
	MessageTypeLog        = agentcore.RuntimeMessageLog
	MessageTypePlan       = agentcore.RuntimeMessagePlan
	MessageStatusFinal    = agentcore.RuntimeMessageStatusFinal
)

type RuntimeMessage = agentcore.RuntimeMessage
type ToolCall = agentcore.RuntimeToolCall
