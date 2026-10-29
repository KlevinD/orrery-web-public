# Runtime Discovery

Orrery exposes local runtime discovery through `GET /api/settings/runtime/discovery`. Runtime-aware UI flows or API clients can call this endpoint when they need to inspect locally available CLI runtimes. Settings can register more than one returned candidate at once; each selected candidate becomes a `runtime_profiles[]` entry.

This follows the Multica daemon discovery pattern: `multica` is not a runtime type. It is the older daemon behavior where the local process probes all supported CLI providers and registers every provider it can actually run.

Discovery order for each provider:

1. Explicit executable env path.
2. The executable on `PATH`.
3. On Unix-like systems, a login shell fallback using `command -v`.

Optional extra argument env vars are also imported into `runtime_config.extra_args` using shell-like parsing, so quoted values such as `"multi word"` stay intact.

Supported providers and env overrides:

| Provider | Command | Path overrides | Args overrides |
| --- | --- | --- | --- |
| Codex | `codex` | `ORRERY_CODEX_PATH`, `MULTICA_CODEX_PATH` | `ORRERY_CODEX_ARGS`, `MULTICA_CODEX_ARGS` |
| Claude Code | `claude` | `ORRERY_CLAUDE_PATH`, `MULTICA_CLAUDE_PATH` | `ORRERY_CLAUDE_ARGS`, `MULTICA_CLAUDE_ARGS` |
| Cursor | `cursor-agent` | `ORRERY_CURSOR_PATH`, `MULTICA_CURSOR_PATH` | `ORRERY_CURSOR_ARGS`, `MULTICA_CURSOR_ARGS` |
| Gemini | `gemini` | `ORRERY_GEMINI_PATH`, `MULTICA_GEMINI_PATH` | `ORRERY_GEMINI_ARGS`, `MULTICA_GEMINI_ARGS` |
| PI | `pi` | `ORRERY_PI_PATH`, `MULTICA_PI_PATH` | `ORRERY_PI_ARGS`, `MULTICA_PI_ARGS` |
| OpenClaw | `openclaw` | `ORRERY_OPENCLAW_PATH`, `MULTICA_OPENCLAW_PATH` | `ORRERY_OPENCLAW_ARGS`, `MULTICA_OPENCLAW_ARGS` |
| Hermes | `hermes` | `ORRERY_HERMES_PATH`, `MULTICA_HERMES_PATH` | `ORRERY_HERMES_ARGS`, `MULTICA_HERMES_ARGS` |

Each discovered candidate includes `kind`, `executable`, `config_ref`, `version`, `status`, `source`, and `runtime_config`.

OpenClaw gets one extra pass after the executable is found:

- `openclaw config file` records the active config path when available.
- `openclaw config get agents.list --json` reads the first agent id and fills `runtime_config.agent_id`.

Minimum environment setup by host environment:

- Windows terminal/server: install the runtime CLI and make it visible on the process `PATH`, or set the matching absolute `ORRERY_*_PATH` / `MULTICA_*_PATH`.
- Windows desktop app: prefer explicit `ORRERY_*_PATH` / `MULTICA_*_PATH` when the desktop process is launched without your terminal PATH.
- macOS/Linux terminal/server: keep the CLI on `PATH`; if it is installed by `nvm`, `fnm`, `volta`, Homebrew, or a per-user npm prefix, the login-shell fallback can still find it.
- macOS/Linux desktop app: put the CLI in login-shell startup files that `command -v` can see, or set the matching explicit path override in the app launch environment.
- Optional provider args: set `ORRERY_*_ARGS` or `MULTICA_*_ARGS` for provider-wide flags that should be registered as `runtime_config.extra_args`.
- For OpenClaw auto agent fill, make sure the active config is readable and `agents.list` contains at least one agent id.

## PI Runtime Debug

PI runtime profiles can enable per-run debug artifacts through `runtime_config.debug`:

```json
{
  "debug": {
    "capture_runtime_input": true,
    "capture_provider_request": true
  }
}
```

`capture_runtime_input` writes `runtime-input.json` under the run PI home debug directory and records the path in run `provider_metadata.pi_debug_runtime_input_path`. The file contains the final PI args, prompt, system prompt, skills, tools, workspace policy, and redacted environment.

`capture_provider_request` installs an Orrery PI extension that listens to PI's `before_provider_request` hook and writes each provider payload to `provider-requests.jsonl`. The path is exposed as `provider_metadata.pi_debug_provider_requests_path` and is also returned from discussion run debug as `provider_metadata`.

Environment overrides are available for ad hoc debugging:

- `ORRERY_PI_DEBUG_CAPTURE_RUNTIME_INPUT=true`
- `ORRERY_PI_DEBUG_CAPTURE_PROVIDER_REQUEST=true`
- `ORRERY_PI_DEBUG_DIR=/absolute/debug/dir`

Provider request payloads can contain private prompt context and tool schemas. Enable this only for targeted debugging and treat generated files as sensitive artifacts.

Current v2 execution support is limited to the provider kinds in the table. Providers that existed in Multica but are not listed here should not be registered until their v2 runtime adapter exists.

## Codex Governed Memory

Orrery Pensieve is the memory authority for Codex runs. The runtime adapter passes the structured `ContextPack` through to Codex app-server and maps only permission-filtered personal and collective digest summaries to `turn/start.additionalContext`. Every entry uses Codex's `untrusted` context kind and an opaque stable source key. Raw memory and raw discussion history are never sent through this path.

Codex native memory is intentionally disabled. Local run-scoped `CODEX_HOME/config.toml` files set the `memories` feature off and disable both native memory generation and use; `thread/start` and `thread/resume` carry the same overrides for remote app-server execution. This prevents a second ungoverned memory store from bypassing Orrery tenant filtering, review, lineage, and audit.

Bound Orrery tools are exposed through Codex app-server `dynamicTools` and dispatched through the shared server-side runtime tool dispatcher. Terminal diary maintenance starts a restricted Codex thread with only diary read/append tools and a bounded, explicitly untrusted snapshot of terminal context. It keeps the same Agent identity and model but does not resume the ordinary Codex thread because app-server cannot replace a resumed thread's dynamic-tool surface. Diary paths, tenant identity, append semantics, and idempotency remain locked and enforced server-side.
