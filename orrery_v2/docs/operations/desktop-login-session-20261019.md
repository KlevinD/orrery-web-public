# Desktop OAuth session compatibility

The desktop signs in through Hydra, but `/api/auth/session` previously accepted only application session tokens. A successful browser login was therefore followed by HTTP 401 in the desktop, which discarded its OAuth credential.

The session endpoint now verifies a desktop bearer through the existing introspection and subject/account mapping services. Only Hydra's verified, top-level `client_id=orrery-desktop` and an existing source identity binding qualify. The response contains current account permissions and token expiry, without creating a Web session, setting a cookie, or returning OAuth credentials.

For first-party tokens containing only `openid profile` and optional `offline_access`, business authorization follows the same live account permissions as Web. Legacy tokens containing business scopes retain their credential scope ceiling. Other OAuth clients retain their existing authorization behavior. Profile claims cannot override the introspected client identity.

This implements the identity, desktop session, and authorization contracts in the production design baseline (chapters 03 and 08). It preserves subject/account binding, disabled account rejection, tenant authorization, desktop local grants, approval requirements, and bearer precedence over unrelated cookies. It does not change browser session issuance or scheduled business tasks.

Regression coverage includes verified desktop login, live permission changes, legacy scope limits, other clients, forged client claims, inactive/expired tokens, wrong issuer/audience, profile binding mismatch, disabled subjects/accounts, and existing Web session behavior. Validation and deployment receipts are stored separately under `D:/Orrery/artifacts/desktop-login-session-20261019`; a real user completing browser login remains a separate end-to-end acceptance check.
