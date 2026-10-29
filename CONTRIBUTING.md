# Contributing

This source release covers the Go API and SvelteKit web under `orrery_v2`,
including the shared source they need. Read the applicable `AGENTS.md` before
changing code. The Electron client and maintenance Tauri tree are outside this
release; working on those products requires their separate source and guidance.

Use a feature branch and pull request. Explain the user-visible change and
validation. Preserve upstream copyright notices. Never include credentials,
customer records, databases, exported workspaces, logs or private keys.

Required tools: Go from `orrery_v2/go.mod` and Node >=22.19. The release was
validated with Go 1.25.0 and Node 22.23.3; build inputs are recorded in
`orrery_v2/config/build.env`. Run `go test ./...` from `orrery_v2` on Linux for
server changes. From `orrery_v2/web`, run `npm ci`, `npm run check` and
`npm run test` for web changes. Client tooling and Bun are not required for
this Web/API source release.

Permission, credential, workspace and cloud WorkOrder changes need failure-path
tests. Cloud requests cannot create local grants or carry arbitrary commands,
argv, environment variables, credentials or absolute local paths. Permission
controls are not an OS sandbox. New Core APIs must be implemented and tested
against the actual Go process; unsupported functionality fails explicitly.

See `docs/opensource-preparation.md` for dependency and release boundaries.
