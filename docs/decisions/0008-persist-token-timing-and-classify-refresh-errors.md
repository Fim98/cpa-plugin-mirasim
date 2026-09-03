# ADR 0008: Persist token timing and classify refresh errors

## Status

Accepted — 2026-09-03

## Context

Mirasim auth previously derived access-token expiry only from a JWT `exp` claim. An opaque access token had no known expiry and was scheduled for immediate refresh on every parse. Refresh failures were plain strings, so callers could not reliably distinguish permanent credential rejection from rate limiting or transient upstream failure.

CPA's built-in OAuth providers persist an expiry and last-refresh timestamp, and their refresh paths preserve HTTP status semantics. CPA currently serializes plugin error status across the native ABI, but not a retry duration.

## Decision

1. Persist RFC 3339 `expired` and `last_refresh` fields in Mirasim auth JSON and runtime metadata.
2. Resolve expiry in this order: a positive refresh-response `expires_in`, a JWT `exp` claim, then a conservative 30-minute lifetime for opaque tokens.
3. Run CPA-coordinated refresh on a cancellation-independent context with a 60-second bound. CPA's per-auth refresh lock and the pooled client's mutex serialize concurrent refreshes.
4. Return a typed refresh error with HTTP status, retryability, and parsed `Retry-After`. Only allowlisted identifier-shaped error codes may be retained; never retain or reflect the upstream response message.
5. Treat HTTP 400/401/403 as permanent, HTTP 429 and 5xx as retryable, and transport or malformed-success responses as transient failures.

## Consequences

Older JWT-backed auth records acquire explicit timing when parsed. Older opaque-token records receive a conservative schedule rather than an immediate refresh loop, and the next successful refresh persists authoritative timing when the endpoint supplies it.

The current CPA native plugin error envelope carries `StatusCode` but not `RetryAfter`. The plugin still exposes `RetryAfter()` for direct callers and future ABI support; current hosts apply their normal status-based backoff after crossing the ABI boundary.

Refresh response bodies remain unavailable for diagnostics by design. Safe HTTP status and identifier-shaped error codes provide enough classification without risking reflected credentials or personal data.

## Alternatives considered

- Require every access token to be a JWT: rejected because OAuth access tokens may be opaque.
- Store a boolean expired flag: rejected because CPA scheduling needs an absolute time.
- Return the complete refresh error body: rejected because the private refresh path exists specifically to keep long-lived credentials out of host logs.
