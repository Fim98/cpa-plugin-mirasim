# ADR 0020: Bind state-less OAuth callbacks to the loopback session

## Status

Accepted — 2026-09-03

Extends the OAuth flow in [ADR 0005](0005-implement-mirasim-oauth-login.md) and the remote bridge in [ADR 0019](0019-bridge-remote-oauth-through-loopback.md).

## Context

Mirasim 0.0.272 accepts a `state` parameter on `/auth/oauth/{provider}/login` but returns only `access_token` and `refresh_token` to the callback. CPA's generic manual callback endpoint and the plugin's browser and CLI handlers correctly rejected that response because no state could be associated with a pending login.

The official desktop client uses one random loopback listener and a random callback path for each login. Its channel binding therefore does not depend on the authentication service echoing state. A public fixed callback path cannot safely make the same assumption: accepting whichever state-less callback arrives while one login happens to be pending would permit login CSRF and account substitution.

## Decision

1. Never accept a state-less callback directly on the plugin's public fixed resource route.
2. Have the loopback bridge remember only the valid state observed on its `/oauth/start` request. Keep one state for at most the plugin's three-minute login lifetime, replace it when a new login starts, and clear it after the callback is forwarded.
3. When the matching loopback callback omits state, inject that pending state before forwarding to CPA. Reject a state-less callback locally when no unexpired start was observed. Continue passing an explicit state through for CPA's constant-time validation.
4. For local CLI login, bind a missing state to the active invocation only after the request reaches its loopback-only, 144-bit random callback path. Apply the same rule to a callback URL explicitly pasted into that invocation. Preserve rejection when an explicit but incorrect state is supplied.
5. Store and log no access token, refresh token, callback URL, or query string in either path.

## Consequences

Current Mirasim OAuth works while retaining CPA's state validation. The bridge holds one non-secret state briefly in memory but remains unable to persist credentials or proxy non-OAuth routes. Concurrent login attempts through one bridge instance intentionally replace the earlier state; operators complete one login at a time.

An explicitly allowlisted public callback is still insufficient with the current authentication service unless a trusted intermediary restores the original state. Implementations that correctly echo state continue to work without fallback.

## Alternatives considered

- Accept a missing state whenever CPA has exactly one pending session: rejected because the fixed public callback is reachable by an attacker and would permit account substitution.
- Remove state validation: rejected because it discards the OAuth request-to-callback binding.
- Put state into the callback query before sending `redirect_uri`: rejected because the authentication service's query-merging behavior is undocumented and the observed callback replaces the query.
- Add a dynamic callback path through the plugin API: unavailable because CLIProxyAPI registers plugin resource routes as exact paths at startup.
