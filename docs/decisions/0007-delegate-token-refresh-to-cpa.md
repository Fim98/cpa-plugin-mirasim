# ADR 0007: Delegate token refresh coordination to CPA

## Status

Accepted — 2026-09-03

Supersedes the request-time emergency-refresh consequence in [ADR 0006](0006-store-credentials-in-cpa-auth-json.md). Its credential ownership and private refresh-transport decisions remain in force.

## Context

The Mirasim client previously refreshed an expiring access token while preparing a provider request. A rotated refresh token updated the pooled client immediately, but CPA did not receive new `StorageJSON` during that request. A process failure before the next scheduled refresh could therefore leave the auth file with an obsolete refresh token.

CPA already serializes refreshes per auth record, calls the plugin `RefreshAuth` capability, persists the returned storage atomically, and retries one request after HTTP 401. That request-time path is enabled only when runtime auth metadata contains a refresh credential.

## Decision

1. Publish the current access and refresh tokens in `AuthData.Metadata`, as well as in provider-owned `StorageJSON`, so CPA can detect and coordinate refreshable credentials.
2. Do not call `/auth/refresh` while preparing a relay request or minting a device ticket. Return HTTP 401 when a known-expiring access token enters its refresh lead or when the device-session endpoint rejects it.
3. Continue retrying one relay 401 with a newly minted device ticket. If that also fails, return the 401 to CPA.
4. Perform token refresh only through the plugin `RefreshAuth` capability. Keep its private, proxy-aware HTTP transport so the refresh-token request body does not enter CPA's request recorder.

## Consequences

Scheduled refreshes and request-triggered refreshes now share CPA's locking, persistence, and retry lifecycle. A rotated refresh token is returned to the host before inference is retried, closing the stale-on-disk window described by ADR 0006.

Runtime auth metadata contains bearer credentials because CPA's refresh coordinator uses those conventional keys. It remains in the same trusted host process and is merged into the same protected auth JSON; it is not exposed through plugin management responses.

Opaque access tokens without a known expiry remain usable until the device-session endpoint returns 401. JWT access tokens trigger CPA refresh at the configured lead time.

## Alternatives considered

- Save auth JSON directly from the executor: rejected because it would duplicate CPA's refresh locking and persistence lifecycle.
- Keep request-local refresh and shorten the scheduling interval: rejected because no timing interval closes the crash window after refresh-token rotation.
- Send `/auth/refresh` through the host HTTP bridge: rejected until the bridge can mark sensitive request bodies as non-recordable.
