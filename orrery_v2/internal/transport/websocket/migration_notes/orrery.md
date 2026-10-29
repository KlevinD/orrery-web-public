# Orrery WebSocket Migration Notes

Legacy Orrery did not expose a Mission Control WebSocket contract. Stage 10 introduces `WS /api/missions/{mission_id}/stream` with SSE fallback as a transport-only subscription endpoint backed by `internal/app/missioncontrol` DTOs and patches.

| Frame | Disposition | Contract evidence |
| --- | --- | --- |
| `subscribe` | Creates a mission subscription and sends a compact `snapshot` first. | `mission_subscription_test.go` |
| `patch` | Encodes `MissionControlPatchEnvelope`; raw `MissionEvent` never appears on the wire. | `patch_encoder_test.go` |
| `ack` | Updates client lag and drives compaction decisions. | `hub_test.go`, `backpressure_test.go` |
| `heartbeat` / `complete` | Uses timeout close semantics defined by the Orrery daemon contract. | `hub_test.go` |
