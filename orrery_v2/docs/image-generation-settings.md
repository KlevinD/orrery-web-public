# Native Image Generation Configuration

Platform settings now contain an independent Image Generation tab. A user with
`settings.manage` can save the gateway API prefix, protocol, model and managed
credential reference, enable or disable the configuration, and check the saved
gateway's model catalog. `settings.read` permits read-only access.

The initial protocol is `openai-compatible`. Model names are not hard-coded; an
Image2 or other model may be configured when the chosen gateway supports it.
The gateway URL must be public HTTPS, such as `https://gateway.example/v1`.
Credentials use the existing `SettingsSecretResolver` and `secret://` reference
mechanism. This page does not create credentials or accept plaintext API keys.

## Persistence And Compatibility

Orrery can supply a platform-managed default service. Deployment operators set
`bootstrap.image_generation` once; users do not supply their own gateway or API
key. The built-in model default is `gpt-image-2` and the managed credential reference
defaults to `secret://image/default`. Neither the gateway URL nor a real API key
is hard-coded or borrowed from a chat profile or Marketing MCP.

```yaml
bootstrap:
  image_generation:
    enabled: true
    base_url: https://your-platform-gateway.example/v1
    model: gpt-image-2
    api_key_ref: secret://image/default
```

Provision the corresponding `image_default` credential through the existing
secret provider, replace the example URL with the platform gateway, then enable
the deployment default. The production sample remains disabled until provisioned.
It is not a working public image service and does not imply a free usage quota.

New settings records and legacy records without `image_generation` (or with
`null`) inherit these defaults on read. Existing objects, including `{}` or an
explicit `enabled: false`, always take precedence. Default reads do not write to
the database; saving settings makes the effective values explicit. Subsequent
deployment-default changes do not overwrite saved values. The deployment default
uses the same URL and credential validation as administrator-entered settings.

The configuration is stored as `SettingsDTO.image_generation` in the existing
production settings JSON record. No database migration is needed. The dedicated
PUT endpoint changes only this field; legacy `/api/settings` clients that omit
it retain the stored configuration. Chat LLM profiles and Marketing MCP grants
are unchanged. This is platform configuration, not a personal assistant profile.

## Connection Check

`POST /api/settings/image-generation/test` accepts an empty body or object and
checks only saved settings. It resolves the credential server-side and performs
`GET {base_url}/models`. Private addresses, proxy routing and redirects are
blocked; DNS results are validated and the connection pins a verified IP. The
request has a 10-second deadline and a 1 MiB response limit. Provider bodies and
credentials are never included in test results.

A listed model returns `model_available: true`, but `generation_verified` remains
`false`. A missing catalog entry is a warning, not proof that generation cannot
work. The check makes no image-generation request and does not enable the service.

## Personal Assistant Runtime

Managed personal assistants receive the native `image_generate` tool. The server
requires the current user's private assistant conversation, matching tenant,
owner principal, live run and `image.generate` tool binding. Shared employees,
read-only review turns and unbound calls cannot use it. Only the prompt and one
of three bounded image sizes are accepted; credentials and routing are never
model-controlled. Existing Marketing generation continues through its own MCP.

Generation posts once to `/images/generations`, with a three-minute deadline,
one PNG result, bounded response size and actual image decoding. URL-only image
responses are rejected instead of following arbitrary download URLs. Images
are private conversation attachments protected by the existing download access
checks. The tool returns authenticated image markdown, not a public URL.

An exclusive claim under `data/image-generation` limits each turn to one provider
submission, across retries and restarts on the shared ECS filesystem. Successful
replays return the stored attachment. Failed or uncertain claims are not removed
automatically. This is charge protection, not a complete billing or quota system.
Multi-host deployments must share this claim directory or use a database claim.

Weixin personal-assistant replies upload the stored PNG with the channel's bound
account, recipient and context token using the official encrypted CDN protocol.
Image markdown is removed from the Weixin text reply when a native image is
present. Delivery retries reuse the channel client ID and do not regenerate the
image. Other channels retain their existing delivery behavior.

## Verification

- `go test ./internal/transport/http -count=1`
- `npm run check` in `web/`
- `npm run test -- src/lib/components/settings src/lib/mappers/settings.test.ts src/lib/types/api-generated.test.ts` in `web/`
- `npx playwright test e2e/image-generation-settings.spec.ts --workers=1` in `web/`

Browser tests use intercepted fixture responses, never live credentials or image
inference. They cover 1440px desktop, 390px mobile and read-only permissions.

The historical desktop/mobile PNG captures were retired on 2026-10-18 because
their embedded artwork lacked complete distribution evidence. The fixture spec
remains the reproducible source for the following checks; a screenshot is not
proof that a live provider or real generation succeeded.

| View | Fixture checks |
| --- | --- |
| 1440px desktop / 390px mobile | Image Generation tab, saved URL, dedicated PUT endpoint, model-catalog result, no horizontal overflow |
| Read-only account | Configuration inputs and connection check disabled; no PUT request |

Run from `web/`, saving test output outside the source tree. For this workspace:

```powershell
$env:TEMP = 'D:/Orrery/.tmp'
$env:TMP = 'D:/Orrery/.tmp'
$env:npm_config_cache = 'D:/Orrery/.cache/npm'
npx playwright test e2e/image-generation-settings.spec.ts --workers=1 --output=D:/Orrery/artifacts/image-generation-settings
```

The original captures and unresolved provenance remain in private audit storage
and the root `compliance/web-asset-rights.json` retired records. Do not reuse
them as cleared release assets.
