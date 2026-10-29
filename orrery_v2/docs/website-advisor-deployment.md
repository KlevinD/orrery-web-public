# Shared production website advisor

The website BFF forwards the bounded `/api/advisor/` contract to the main Orrery
API. The receptionist profiles use native PI with `bound_tools_only: true`.
The retired `managed` runtime is unavailable in the native session registry and
cannot serve the conversation after human verification.

Install `deploy/systemd/orrery-api-website-advisor.conf` as a drop-in for the
active API unit. Production supports the system unit `orrery-api.service` or
the user unit `orrery-api-production.service`. Confirm the listener's owning
unit before installing the drop-in, and keep one API owner. The referenced environment file contains
`ORRERY_WEBSITE_IDENTITY_ASSERTION_KEY`, synchronized with the website BFF's
`WEBSITE_ORRERY_ASSERTION_KEY`. Keep this file readable only by the service owner.
The production API also needs its existing owner HMAC key and website origin
allowlist. Do not load the retired isolated database configuration into the main
API.

The production ingress `/api/advisor/` upstream is `127.0.0.1:18080`, shared with
the main API. Preserve the origin header handling and disable response buffering
for SSE. The website BFF continues to own CAPTCHA validation and strip browser
authority headers. Website login availability remains a separate product setting.

The public and customer PageIndex tools keep separate credentials and fixed
document scopes. Native PI disables built-in file and shell tools; the existing
profile tool bindings and session-only memory policy still apply. Do not enable
built-in tools to make the receptionist runtime start.

Each website turn creates a private temporary workspace for the native provider
and removes it after execution. Internal discussion workspaces, history,
attachments, and memory remain excluded from the receptionist request.

For `minvia.cn` and `www.minvia.cn`, serve the automatically renewed certificate
from `/etc/letsencrypt/live/minvia.cn/`. Keep the HTTP ACME challenge location
outside the HTTPS redirect. The Certbot renewal deploy hook reloads Nginx.

Before activation, back up both Nginx configurations and the API service drop-in,
run the website profile boundary tests, and build from the current production
source snapshot so other deployed fixes are preserved. Switch the API release
only if the active release still matches the build's recorded parent.

Validate the deployed public domain with normal certificate verification:

- HTTPS page and FAQ requests succeed.
- Anonymous session creation returns 201 and normal questions complete over SSE.
- A knowledge question returns a real approved public source and revision.
- A real CAPTCHA accepted through the BFF returns 204, and the subsequent native
  agent turn retrieves approved knowledge and returns a completed answer.
- Forged identity assertions and cross-scope document reads remain rejected.

Rollback restores the recorded API parent release, previous drop-in, and Nginx
configuration, followed by configuration validation and service reload. The
website authentication database and knowledge documents are not replaced.
