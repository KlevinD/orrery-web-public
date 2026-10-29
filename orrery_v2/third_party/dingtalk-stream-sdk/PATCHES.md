# Local DingTalk SDK patches

This directory is based on
`github.com/open-dingtalk/dingtalk-stream-sdk-go` v0.9.1. Orrery replaces the
upstream module to make stream shutdown and reconnect behavior usable during
Gateway hot reloads.
Handler failure acknowledgements use a stable public message instead of
returning internal database, runtime, or provider errors to DingTalk.

The local client patch adds cancellable dialing and reconnect delays, a bounded
`Shutdown` lifecycle, synchronized connection writes, tracked callback
goroutines, and explicit connection-state notifications. Keep the lifecycle
tests when upgrading the upstream SDK. Connection tickets and failed inbound
payloads are omitted from logs so credentials and message content are not
written to production logging sinks. Callback execution is bounded with
backpressure instead of creating an unbounded goroutine for every inbound
frame, and replacement of the SDK's process-wide logger is synchronized during
Gateway hot reloads.
