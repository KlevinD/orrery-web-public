// Historical source: AionCore aionui-session, Copyright 2025 AionUi
// (aionui.com), Apache-2.0. Go adaptation and modifications: Minvia, 2026.
// See docs/runtime/runtime-source-provenance.md for scope and the full license.
package session

import agentcore "orrery_v2/internal/agent/core"

// Rehydrate restores only the durable session anchor. Mid-turn state is not
// durable, so every cold reconstruction starts idle and waits for fresh events.
// Reference: aionui-session/backend/rehydrate.rs@v0.1.52.
func Rehydrate(_ *agentcore.Tier2Checkpoint) State {
	return Idle()
}
