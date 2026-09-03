# ADR 0018: Back off tickets and track account plans

## Status

Accepted — 2026-09-03

Extends the CPA-owned refresh lifecycle in [ADR 0007](0007-delegate-token-refresh-to-cpa.md) and the persisted token timing in [ADR 0008](0008-persist-token-timing-and-classify-refresh-errors.md).

## Context

Mirasim 0.0.272 refreshes a device ticket two minutes before expiry, accepts either `expiresIn` or `expiresAt`, and uses a ten-minute expiry when neither value is present. Its ticket manager backs off failed mint attempts exponentially from one second to a thirty-second ceiling and prevents repeated relay refusals from causing an unbounded re-mint loop. A still-valid ticket remains usable while a proactive replacement attempt is backing off.

The official client also reads `plan` and `plan_exp` from the access-token JWT, periodically checks the authoritative `GET /auth/me` profile, and refreshes the access token when a newly observed profile plan differs from its claims. The same unchanged mismatch is suppressed so an upstream token that has not caught up does not cause a refresh loop.

CPA must remain the sole coordinator and persister of access-token refreshes. Request-time plugin code cannot rotate a refresh token safely because it cannot atomically return the new credential record to the host.

## Decision

1. Refresh device tickets two minutes before expiry. Resolve their expiry from a positive finite `expiresIn`, then a positive finite future `expiresAt`, then a ten-minute default. Reject values that cannot be represented safely.
2. On transport, 429, or 5xx mint failures, retry after 1, 2, 4, 8, 16, and at most 30 seconds. Use a valid upstream `Retry-After` value when present, bounded to fifteen minutes. Give non-retryable or malformed responses a fixed thirty-second delay.
3. Continue using an existing ticket until its actual expiry when proactive renewal fails. Preserve a newly established mint backoff when a relay rejects that stale ticket, and apply a thirty-second refusal floor to prevent repeated successful-but-rejected tickets from creating a re-mint storm.
4. Mark a rejected device session or twice-rejected relay request as requiring access-token refresh. Return HTTP 401 semantics to CPA and perform the refresh only through the plugin `RefreshAuth` capability.
5. Persist additive `plan`, `plan_exp`, and `profile_checked_at` fields in the existing version-1 OAuth JSON. Seed them from string and numeric JWT claims, then treat a successful `GET /auth/me` result as authoritative.
6. Publish `refresh_interval_seconds: 300` in runtime metadata so CPA invokes its normal per-auth refresh lifecycle for a best-effort profile check every five minutes. A newly changed profile/JWT mismatch requests one CPA-owned token refresh; an identical already-recorded mismatch does not.
7. Use the same private, proxy-aware transport for `/auth/me` and `/auth/refresh`, keeping bearer credentials outside CPA's host request recorder. Profile failures do not make an otherwise usable credential unavailable.

## Consequences

Transient ticket-mint failures no longer create one attempt per inference request, and a proactive failure does not discard a ticket that the relay can still accept. Relay refusal remains bounded while preserving the existing one-ticket-retry behavior.

CPA auth records now carry non-secret account plan state alongside their OAuth credentials. Plan changes can rotate stale access claims without bypassing CPA's lock, atomic persistence, failure classification, or request retry. The five-minute interval adds one lightweight profile request per active auth per interval; failed checks are retried on a later CPA cycle.

Mirasim's official client can fall back to a plain issuer token when device tickets are unsupported. The plugin deliberately does not copy that behavior because its verified relay contract requires signed ticket authentication; HTTP 404 or 501 therefore enters bounded failure handling instead of silently changing the authentication envelope.

## Alternatives considered

- Mint a ticket on every request after a failure: rejected because concurrent traffic would amplify an upstream outage.
- Discard a still-valid ticket at the proactive refresh boundary: rejected because the official behavior continues using it until actual expiry.
- Poll `/auth/me` on every inference request: rejected because plan state changes slowly and CPA already provides a scheduled refresh lifecycle.
- Refresh directly when a profile mismatch is observed: rejected because a rotated refresh token could be lost before CPA persists it.
- Store plan state in a plugin-owned sidecar: rejected because it would duplicate auth identity, locking, and persistence outside CPA.
