# Local Feishu SDK patches

This directory is based on `github.com/larksuite/oapi-sdk-go/v3` v3.9.9.
Orrery replaces the upstream module because the upstream WebSocket client does
not return from `Start`, restarts its ping goroutine after shutdown, and sleeps
through cancellation during reconnects. Repeated Gateway reloads therefore
leak goroutines and retain old credentials.

The local patch in `ws/client.go` makes lifecycle loops context-aware, fences
concurrent starts, uses cancellable WebSocket dialing and synchronized reconnect
state, and stops reconnect/ping work on shutdown. It also prevents connection
URLs and unexpected WebSocket payloads from entering production logs because
those values can contain provider credentials or user message content. Message
callbacks are handled synchronously per chat, handler failures propagate to
the WebSocket acknowledgement, and failed dedupe reservations are released so
Feishu can redeliver an event after an infrastructure or reply failure. Raw
handler failures are omitted from SDK logs because they can contain database or
downstream-provider details. Successful send responses without a provider
message ID are rejected instead of being treated as delivered or panicking. Data
frame callbacks use bounded concurrency with backpressure while control frames
remain responsive. Fragment reassembly is synchronized and rejects malformed
fragment indexes or excessive fragment counts before allocating memory. Keep
`ws/lifecycle_patch_test.go` when upgrading the upstream SDK.

The local `scene/registration` package also implements application QR
registration. Its begin-response decoder accepts both the legacy `expire_in`
field and the provider's current `expires_in` field. Keep the registration
contract tests when upgrading so provider-issued launcher URLs and expiry
values continue to reach the Gateway registration flow.
