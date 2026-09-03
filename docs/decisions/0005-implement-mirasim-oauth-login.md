# ADR 0005: Implement Mirasim OAuth login

## Status

Accepted — 2026-09-02. The credential-persistence portions are superseded by [ADR 0006](0006-store-credentials-in-cpa-auth-json.md) as of 2026-09-03.

## Context

Mirasim 0.0.272 supports GitHub and Google browser sign-in through `/auth/oauth/{provider}/login`. Unlike OAuth authorization-code flows used by CPA's built-in Codex, Claude, and Gemini integrations, Mirasim redirects the browser with `state`, `access_token` (or `token`), and `refresh_token` directly in the callback query. There is no code-exchange or PKCE step in the current client.

CPA's generic plugin login lifecycle is still useful: Management Center requests `<provider>-auth-url`, polls `get-auth-status`, and persists the `AuthData` returned by `PollLogin`. Its generic `/v0/management/oauth-callback` intentionally records only `code`, `state`, and `error`, however, so it cannot carry Mirasim's direct token result.

The plugin's existing credential contract keeps bearer tokens and the device private key in a configured plaintext directory. CPA auth JSON contains only that directory and public Mirasim endpoint/version settings. Any login implementation must preserve this boundary and must not route a refresh-token request body through CPA's host request recorder.

## Decision

1. Register browser resources at `/oauth/start` and `/oauth/callback` under CPA's host-assigned `/v0/resource/plugins/<plugin-id>` prefix. Use the existing `StartLogin` and `PollLogin` interfaces around those resources, without changing CLIProxyAPI core.
2. Generate a 256-bit random state for each login, expire sessions after three minutes, cap pending/completed sessions at 64, compare callback state in constant time, and accept each token-bearing callback once.
3. Show a plugin-owned GitHub/Google chooser, then redirect to `/auth/oauth/{provider}/login` with the exact plugin callback URL and state. Email verification is not part of this OAuth implementation.
4. For remote deployments, accept `oauth-public-base-url` (or `MIRASIM_OAUTH_PUBLIC_BASE_URL`) as the externally reachable CPA origin. Require HTTPS except for loopback hosts. Allow an optional reverse-proxy path prefix.
5. Keep callback tokens only in process memory until the next CPA poll. Never place them in polling metadata, response bodies, errors, or auth JSON. Redirect the browser immediately to a token-free callback URL after accepting them.
6. On successful polling, atomically write `access-token.txt` and `refresh-token.txt`, preserve a valid existing Ed25519 PKCS#8 device key or generate a new one, then invalidate the cached Mirasim client before validation.
7. Also expose `--mirasim-login` with a random loopback callback and pasted-callback fallback, following CPA's Gemini CLI plugin convention. Keep `--mirasim-import` as a separate, mutually exclusive migration path.
8. Refresh tokens through a private HTTP client that honors CPA's configured proxy. Do not send refresh bodies through the host HTTP bridge, which may record request bodies.
9. Parse the plugin-specific YAML document that CPA actually passes through the native ABI, while retaining compatibility with the earlier full `plugins.configs.mirasim` test/embedding shape. This is required for the credential directory and public callback base to take effect in a loaded `.so`/`.dll`.

## Consequences

Management Center can complete Mirasim OAuth using CPA's normal provider login and polling UX, while the saved auth record remains path-only. No Mirasim token is written to the host's OAuth callback file or plugin polling metadata.

The direct-token protocol necessarily places credentials in the incoming callback query before plugin code runs. CLIProxyAPI masks token-named query parameters in its own request logs, but operators must configure any reverse proxy or external access log to redact or omit query strings. HTTPS is mandatory for non-loopback callbacks.

The in-memory handoff does not survive a plugin reload or CPA restart. A login interrupted that way must be started again. Cancelled or abandoned callbacks expire without writing credentials because installation happens only during an active poll.

The OAuth browser can reach the authentication service independently of the CPA host, but later token refresh still requires the CPA container to reach `auth.mirasim.ai` directly or through its configured proxy.

## Alternatives considered

- Reuse CPA's generic `/v0/management/oauth-callback`: rejected because it drops `access_token` and `refresh_token` by design.
- Put tokens into `AuthLoginStartResponse.Metadata`: rejected because metadata is registered and polled by the host and would broaden secret persistence.
- Store tokens directly in CPA auth JSON: rejected because it breaks the established path-only credential boundary and duplicates secrets.
- Patch CLIProxyAPI core to accept provider-specific callback fields: rejected because browser resource routes already provide the required extension point.
- Implement email-code login in the same change: deferred because it is a separate `/auth/code` plus `/auth/verify` interaction, not the requested OAuth provider flow.
