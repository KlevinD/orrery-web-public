# Security reporting

Do not post live credentials, customer data or private exploit data in a public
issue. Use GitHub private vulnerability reporting when enabled for this repo.
If unavailable, request a private channel from a maintainer without publishing
sensitive details. The public release is limited to the sanitized Web/API
source profile; no response-time guarantee is provided.

Include the affected commit/component, synthetic reproduction, expected
authorization boundary and impact. Redact secrets. Boundaries include tenant
authorization, local Workspace Grants, explicit approvals, OS-vault credentials
and typed WorkOrders. Loopback and permission controls are not an OS sandbox.

Before exposing a private repository, scan the proposed tree and its reachable
history. A clean checkout cannot prove deleted credentials are absent from
history. Triage findings and rotate or revoke real credentials before publishing.
