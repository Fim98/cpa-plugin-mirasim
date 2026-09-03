# ADR 0009: Validate OAuth and name auths by account

## Status

Accepted — 2026-09-03

## Context

OAuth completion previously validated only the local shape of token strings and the generated Ed25519 private key. CPA then saved every login as `mirasim.json`, so logging in with another account silently replaced the existing credential. CPA's built-in Claude and Codex providers and the Gemini CLI plugin use account-specific identity and filenames.

Mirasim does not currently expose a dedicated account-profile contract to this plugin. Its access token may contain stable account or email claims, and the relay can validate the token plus generated device identity by minting a device ticket and serving the authenticated model catalog.

## Decision

1. Do not return OAuth success until the credential can mint a device ticket and complete `GET /v1/models`.
2. Extract only `account_id`, `user_id`, `sub`, and `email` identity claims from the access token. Use them for naming only after remote validation succeeds.
3. Name auth files `mirasim-<account-id>.json` when a stable account claim is present. Fall back to email, then to the generated device public-key fingerprint.
4. Show email in the CPA auth label when available, otherwise an abbreviated account ID or device fingerprint.
5. Preserve legacy filenames while parsing existing files; account-specific naming applies to new OAuth results.
6. Use CPA's host HTTP client for Management Center validation. CLI command execution has no host HTTP callback, so use a short-lived proxy-aware client implementing the same request interface for that one validation.

## Consequences

Different identified accounts coexist in one `auth-dir`, while repeating login for the same stable account replaces its prior credential. Tokens without usable identity claims remain multi-account safe by receiving device-specific filenames, although repeating login for the same opaque-token account creates another device-specific auth.

OAuth failure is reported before CPA persists unusable credentials. Validation failure messages retain only the operation and HTTP status; upstream bodies are not reflected into the browser or command output.

The plugin treats identity claims as display and file-selection data, not authorization data. Authorization remains the successful Mirasim relay validation.

## Alternatives considered

- Continue using `mirasim.json`: rejected because it prevents multiple accounts and silently overwrites credentials.
- Trust token claims without an upstream request: rejected because callback contents alone have not proved possession of a usable Mirasim session.
- Require a profile endpoint: rejected because no stable profile contract is currently available; remote relay validation plus device fallback works with the deployed protocol.
