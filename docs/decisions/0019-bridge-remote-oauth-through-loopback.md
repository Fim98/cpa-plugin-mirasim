# ADR 0019: Bridge remote OAuth through loopback

## Status

Accepted — 2026-09-03

Extends the browser OAuth design in [ADR 0005](0005-implement-mirasim-oauth-login.md).

## Context

Mirasim 0.0.272 opens a random HTTP listener on `127.0.0.1` and sends that loopback callback as `redirect_uri`. The current `auth.mirasim.ai` deployment likewise accepts loopback callbacks and `*.mirofish.ai` callbacks by default, but rejects an unregistered public CPA callback with `redirect_uri 不在白名单` unless its operator adds the exact URI to `OAUTH_REDIRECT_ALLOWLIST`.

The plugin's public-base option remains valid for an explicitly allowlisted domain, but a normal remote CPA operator cannot change the official Mirasim authentication service configuration. A remote browser also cannot reach the CPA process through its own loopback address without a local forwarding component.

The callback query contains bearer credentials. Any forwarding path must avoid request logging, expose no general-purpose proxy, bind only to loopback, and encrypt the hop to remote CPA.

## Decision

1. Keep direct HTTPS callbacks supported for domains explicitly registered by the Mirasim authentication service.
2. Ship `mirasim-oauth-bridge` for remote installations that do not have an allowlisted callback domain. It listens only on a loopback address and forwards only the plugin's exact `/oauth/start` and `/oauth/callback` resource paths to a configured CPA HTTPS origin.
3. Strip authorization, API-key, cookie, proxy-authorization, and referrer headers before forwarding. Do not log requests, query strings, response bodies, or forwarding errors that could contain callback material.
4. Configure the remote plugin's `oauth-public-base-url` to the bridge's local HTTP origin while the bridge is running on the same machine as the browser. Mirasim then sees the same class of loopback callback used by its official client, and the bridge carries that request to the original CPA process over HTTPS.
5. Treat the bridge as a login-time helper only. CPA remains the OAuth session owner and persists the completed credential in its normal `auth-dir`; the bridge stores nothing.

## Consequences

Remote Docker installations can complete OAuth without importing official-client credentials or asking Mirasim to whitelist a personal domain. The operator must run one small helper on the browser machine during login and select a free loopback port. Inference traffic never traverses the helper.

An explicitly allowlisted public callback remains the simplest unattended option. The loopback bridge is intentionally not a general reverse proxy and cannot access Management API or inference routes.

## Alternatives considered

- Continue using an arbitrary public HTTPS callback: rejected as the default because the official authentication service refuses it before Google or GitHub authorization begins.
- Import tokens from the desktop client: rejected because it creates a second credential source and contradicts CPA-owned OAuth persistence.
- Forward remote CPA TLS directly through SSH to an HTTPS loopback URL: rejected because the public certificate does not normally authenticate `127.0.0.1` or `localhost`.
- Ask the browser page to receive the callback itself: rejected because a web page cannot open a loopback HTTP listener, and URL fragments or cross-window token messaging would require an allowlisted service controlled by Mirasim.
